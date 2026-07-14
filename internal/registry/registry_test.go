// Tests for the on-disk registry: load/save round trips, atomicity,
// corruption handling, mutation helpers, and the integrity validation
// that backs `portberth doctor`.
package registry

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/JaydenCJ/portberth/internal/portspec"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "registry.json")
}

func entry(project, service string, port int) Entry {
	return Entry{Project: project, Service: service, Port: port, ClaimedAt: "2026-07-13T09:00:00Z"}
}

func TestLoadMissingFileYieldsEmptyRegistry(t *testing.T) {
	// First run must need no setup ceremony.
	r, err := Load(filepath.Join(t.TempDir(), "does", "not", "exist.json"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(r.Entries) != 0 {
		t.Fatalf("expected empty registry, got %d entries", len(r.Entries))
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := tempPath(t)
	r, _ := Load(path)
	e := entry("shop", "api", 3456)
	e.Note = "checkout service"
	if err := r.Add(e); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := r.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	r2, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := r2.Lookup(portspec.Spec{Project: "shop", Service: "api"})
	if !ok || got != e {
		t.Fatalf("round trip lost data: got %+v ok=%v, want %+v", got, ok, e)
	}
	if r2.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", r2.SchemaVersion, SchemaVersion)
	}
}

func TestSaveIsAtomicOwnerOnlyAndNewlineTerminated(t *testing.T) {
	path := tempPath(t)
	r, _ := Load(path)
	r.Add(entry("a", "default", 3001))
	if err := r.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Atomic: the temp file must be gone after the rename.
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != "registry.json" {
		var names []string
		for _, f := range files {
			names = append(names, f.Name())
		}
		t.Fatalf("expected only registry.json, found %v", names)
	}
	// Private: reservations are the user's own business.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("registry perm = %o, want 600", perm)
		}
	}
	// POSIX-friendly: files diff and cat cleanly.
	data, _ := os.ReadFile(path)
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatal("registry file must end with a newline")
	}
}

func TestSaveCreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "registry.json")
	r, _ := Load(path)
	r.Add(entry("a", "default", 3001))
	if err := r.Save(); err != nil {
		t.Fatalf("save should create parents: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("registry not written: %v", err)
	}
}

func TestLoadRejectsCorruptJSON(t *testing.T) {
	path := tempPath(t)
	os.WriteFile(path, []byte("{not json"), 0o600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("corrupt JSON must be an error, never silently discarded")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("error should explain the problem, got: %v", err)
	}
}

func TestLoadRejectsNewerSchema(t *testing.T) {
	path := tempPath(t)
	os.WriteFile(path, []byte(`{"schema_version": 99, "entries": []}`), 0o600)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "schema 99") {
		t.Fatalf("future schema should be refused with an explanation, got: %v", err)
	}
}

func TestAddRejectsDuplicateSpec(t *testing.T) {
	r, _ := Load(tempPath(t))
	if err := r.Add(entry("shop", "web", 3100)); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(entry("shop", "web", 3200)); err == nil {
		t.Fatal("adding the same spec twice must fail")
	}
}

func TestByPortReturnsAllDuplicates(t *testing.T) {
	// Hand-edited registries can pin two specs to one port; ByPort must
	// surface both so conflicts are explained, not hidden.
	r, _ := Load(tempPath(t))
	r.Entries = []Entry{entry("a", "default", 3100), entry("b", "default", 3100), entry("c", "default", 3200)}
	if got := len(r.ByPort(3100)); got != 2 {
		t.Fatalf("ByPort(3100) = %d entries, want 2", got)
	}
	if got := len(r.ByPort(9999)); got != 0 {
		t.Fatalf("ByPort(9999) = %d entries, want 0", got)
	}
	if _, ok := r.Lookup(portspec.Spec{Project: "ghost", Service: "default"}); ok {
		t.Fatal("lookup of unknown spec should miss")
	}
}

func TestByProjectFiltersAndTakenPortsCollects(t *testing.T) {
	r, _ := Load(tempPath(t))
	r.Add(entry("shop", "web", 3100))
	r.Add(entry("shop", "api", 3200))
	r.Add(entry("blog", "default", 3300))
	if got := len(r.ByProject("shop")); got != 2 {
		t.Fatalf("ByProject(shop) = %d, want 2", got)
	}
	taken := r.TakenPorts()
	if !taken[3100] || !taken[3200] || !taken[3300] || taken[3400] {
		t.Fatalf("TakenPorts wrong: %v", taken)
	}
}

func TestRemoveReportsExistence(t *testing.T) {
	r, _ := Load(tempPath(t))
	r.Add(entry("shop", "web", 3100))
	if !r.Remove(portspec.Spec{Project: "shop", Service: "web"}) {
		t.Fatal("removing an existing spec should report true")
	}
	if r.Remove(portspec.Spec{Project: "shop", Service: "web"}) {
		t.Fatal("removing a missing spec should report false")
	}
}

func TestRemoveProjectCountsRemovals(t *testing.T) {
	r, _ := Load(tempPath(t))
	r.Add(entry("shop", "web", 3100))
	r.Add(entry("shop", "api", 3200))
	r.Add(entry("blog", "default", 3300))
	if n := r.RemoveProject("shop"); n != 2 {
		t.Fatalf("RemoveProject = %d, want 2", n)
	}
	if len(r.Entries) != 1 || r.Entries[0].Project != "blog" {
		t.Fatalf("unexpected survivors: %+v", r.Entries)
	}
}

func TestEntriesAreSortedDeterministically(t *testing.T) {
	r, _ := Load(tempPath(t))
	r.Add(entry("zeta", "default", 3900))
	r.Add(entry("alpha", "web", 3100))
	r.Add(entry("alpha", "api", 3200))
	want := []string{"alpha/api", "alpha/web", "zeta"}
	for i, e := range r.Entries {
		if e.Spec().String() != want[i] {
			t.Fatalf("entry %d = %s, want %s", i, e.Spec(), want[i])
		}
	}
}

func TestValidateFlagsDuplicatePorts(t *testing.T) {
	r, _ := Load(tempPath(t))
	r.Entries = []Entry{entry("a", "default", 3100), entry("b", "default", 3100)}
	problems := r.Validate()
	if len(problems) != 1 || !strings.Contains(problems[0], "duplicate port 3100") {
		t.Fatalf("expected one duplicate-port problem, got %v", problems)
	}
}

func TestValidateFlagsDuplicateSpecsAndBadEntries(t *testing.T) {
	r, _ := Load(tempPath(t))
	r.Entries = []Entry{
		entry("a", "default", 3100),
		entry("a", "default", 3200),      // duplicate spec
		entry("bad name!", "web", 70000), // invalid name and port
	}
	problems := strings.Join(r.Validate(), "\n")
	for _, want := range []string{"duplicate entry for a", "invalid character", "outside 1-65535"} {
		if !strings.Contains(problems, want) {
			t.Errorf("problems missing %q:\n%s", want, problems)
		}
	}
}

func TestValidateCleanRegistryHasNoProblems(t *testing.T) {
	r, _ := Load(tempPath(t))
	r.Add(entry("a", "default", 3100))
	r.Add(entry("b", "default", 3200))
	if problems := r.Validate(); len(problems) != 0 {
		t.Fatalf("clean registry should validate, got %v", problems)
	}
}
