// In-process integration tests: every test drives cli.Run exactly as
// main() does, against a temp registry, a fixed clock, a fabricated
// procfs, and a scriptable live probe. No real /proc, no real config
// dir, no wall-clock dependence.
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/portberth/internal/assign"
	"github.com/JaydenCJ/portberth/internal/portspec"
)

// harness bundles the deterministic Env plus captured output.
type harness struct {
	registry string
	procRoot string
	inUse    map[int]bool // ports the fake live probe reports busy
	envVars  map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	procRoot := filepath.Join(dir, "proc")
	os.MkdirAll(filepath.Join(procRoot, "net"), 0o755)
	return &harness{
		registry: filepath.Join(dir, "registry.json"),
		procRoot: procRoot,
		inUse:    map[int]bool{},
		envVars:  map[string]string{},
	}
}

// run executes one portberth invocation in-process.
func (h *harness) run(args ...string) (code int, stdout, stderr string) {
	var out, errBuf bytes.Buffer
	code = Run(args, Env{
		Stdout:        &out,
		Stderr:        &errBuf,
		Getenv:        func(k string) string { return h.envVars[k] },
		Now:           func() time.Time { return time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC) },
		ProcRoot:      h.procRoot,
		PortInUse:     func(p int) bool { return h.inUse[p] },
		UserConfigDir: func() (string, error) { return filepath.Dir(h.registry), nil },
	})
	return code, out.String(), errBuf.String()
}

// reg prepends --registry so commands hit the harness registry.
func (h *harness) reg(args ...string) (int, string, string) {
	return h.run(append([]string{args[0], "--registry", h.registry}, args[1:]...)...)
}

// addListener fabricates a LISTEN socket in the fake procfs, owned by
// pid/process.
func (h *harness) addListener(t *testing.T, port int, pid, process string) {
	t.Helper()
	inode := strconv.Itoa(400000 + port)
	row := "   0: " + kernelHexAddr(port) + " 00000000:0000 0A" +
		" 00000000:00000000 00:00000000 00000000  1000        0 " + inode + " 1\n"
	path := filepath.Join(h.procRoot, "net", "tcp")
	existing, _ := os.ReadFile(path)
	if len(existing) == 0 {
		existing = []byte("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n")
	}
	if err := os.WriteFile(path, append(existing, row...), 0o644); err != nil {
		t.Fatal(err)
	}
	fdDir := filepath.Join(h.procRoot, pid, "fd")
	os.MkdirAll(fdDir, 0o755)
	os.WriteFile(filepath.Join(h.procRoot, pid, "comm"), []byte(process+"\n"), 0o644)
	if err := os.Symlink("socket:["+inode+"]", filepath.Join(fdDir, "3")); err != nil {
		t.Fatal(err)
	}
}

// kernelHexAddr renders 127.0.0.1:port the way /proc/net/tcp does.
func kernelHexAddr(port int) string {
	return "0100007F:" + strings.ToUpper(strconv.FormatInt(int64(port), 16))
}

// preferred computes the deterministic port claim will pick for a spec
// under the default range, so tests assert exact values without
// hardcoding hash outputs.
func preferred(t *testing.T, spec string) int {
	t.Helper()
	s, err := portspec.ParseSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	rng, err := portspec.ParseRange(DefaultRange)
	if err != nil {
		t.Fatal(err)
	}
	return assign.Preferred(s, rng)
}

func TestClaimAssignsDeterministicStablePort(t *testing.T) {
	h1, h2 := newHarness(t), newHarness(t)
	_, out1, _ := h1.reg("claim", "shop/web")
	_, out2, _ := h2.reg("claim", "shop/web")
	if out1 != out2 {
		t.Fatalf("same spec on two machines diverged:\n%q\n%q", out1, out2)
	}
	if !strings.Contains(out1, "reserved shop/web -> ") {
		t.Fatalf("unexpected claim output: %q", out1)
	}
}

func TestClaimIsIdempotent(t *testing.T) {
	h := newHarness(t)
	code, first, _ := h.reg("claim", "shop/web")
	if code != 0 {
		t.Fatalf("first claim failed: %d", code)
	}
	code, second, _ := h.reg("claim", "shop/web")
	if code != 0 {
		t.Fatalf("second claim failed: %d", code)
	}
	if !strings.Contains(second, "existing reservation since 2026-07-13") {
		t.Fatalf("second claim should say existing: %q", second)
	}
	// Both mention the same port.
	port := strings.TrimSpace(strings.TrimPrefix(first, "reserved shop/web -> "))
	if !strings.Contains(second, " -> "+port+" ") {
		t.Fatalf("port moved between claims: %q then %q", first, second)
	}
}

func TestClaimExplicitPort(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.reg("claim", "shop/db", "--port", "4321")
	if code != 0 || !strings.Contains(out, "reserved shop/db -> 4321") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	code, out, _ = h.reg("get", "shop/db")
	if code != 0 || strings.TrimSpace(out) != "4321" {
		t.Fatalf("get after explicit claim: code=%d out=%q", code, out)
	}
}

func TestClaimExplicitPortConflictExplainsProvenance(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/db", "--port", "4321", "--note", "primary db")
	code, _, errOut := h.reg("claim", "blog/db", "--port", "4321")
	if code != 1 {
		t.Fatalf("conflicting explicit claim should exit 1, got %d", code)
	}
	for _, want := range []string{"port 4321 is not available for blog/db", "reserved by shop/db since 2026-07-13", "portberth explain 4321"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOut)
		}
	}
}

func TestClaimExplicitWellKnownPortWarnsButAllows(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.reg("claim", "shop/db", "--port", "5432")
	if code != 0 || !strings.Contains(out, "reserved shop/db -> 5432") {
		t.Fatalf("explicit well-known claim should succeed: code=%d out=%q", code, out)
	}
	if !strings.Contains(errOut, "well-known port (postgresql") {
		t.Fatalf("expected a well-known warning on stderr, got: %q", errOut)
	}
}

func TestClaimRefusesToMoveExistingReservation(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/web", "--port", "4100")
	code, _, errOut := h.reg("claim", "shop/web", "--port", "4200")
	if code != 1 {
		t.Fatalf("moving a reservation via claim should exit 1, got %d", code)
	}
	if !strings.Contains(errOut, "already reserved at port 4100") || !strings.Contains(errOut, "portberth release shop/web") {
		t.Fatalf("stderr should explain and hint: %q", errOut)
	}
}

func TestClaimAvoidsWellKnownPortsByDefault(t *testing.T) {
	h := newHarness(t)
	// A range that is a single well-known port: nothing assignable.
	code, _, errOut := h.reg("claim", "shop/web", "--range", "5432-5432")
	if code != 1 || !strings.Contains(errOut, "no free port in range") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	// The escape hatch.
	code, out, _ := h.reg("claim", "shop/web", "--range", "5432-5432", "--allow-well-known")
	if code != 0 || !strings.Contains(out, "-> 5432") {
		t.Fatalf("--allow-well-known should permit it: code=%d out=%q", code, out)
	}
}

func TestClaimProbeSkipsLiveOccupiedPorts(t *testing.T) {
	h := newHarness(t)
	want := preferred(t, "shop/web")
	h.inUse[want] = true // someone squats the hash slot right now
	code, out, _ := h.reg("claim", "shop/web", "--probe")
	if code != 0 {
		t.Fatalf("claim --probe failed: %d", code)
	}
	if strings.Contains(out, "-> "+strconv.Itoa(want)) {
		t.Fatalf("claim picked the live-occupied port %d: %q", want, out)
	}
	if !strings.Contains(out, "-> "+strconv.Itoa(want+1)) {
		t.Fatalf("expected next port %d: %q", want+1, out)
	}
	// An explicit --port that is live-occupied is refused outright.
	h.inUse[4500] = true
	code, _, errOut := h.reg("claim", "other/web", "--port", "4500", "--probe")
	if code != 1 || !strings.Contains(errOut, "live-occupied") {
		t.Fatalf("explicit probe conflict: code=%d stderr=%q", code, errOut)
	}
}

func TestClaimJSONOutput(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.reg("claim", "shop/web", "--port", "4600", "--note", "storefront", "--format", "json")
	if code != 0 {
		t.Fatalf("claim failed: %d", code)
	}
	var env struct {
		Tool string `json:"tool"`
		Data struct {
			Project   string `json:"project"`
			Service   string `json:"service"`
			Port      int    `json:"port"`
			Created   bool   `json:"created"`
			ClaimedAt string `json:"claimed_at"`
			Note      string `json:"note"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	d := env.Data
	if env.Tool != "portberth" || d.Project != "shop" || d.Service != "web" ||
		d.Port != 4600 || !d.Created || d.ClaimedAt != "2026-07-13T09:00:00Z" || d.Note != "storefront" {
		t.Fatalf("payload wrong: %+v", env)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	h := newHarness(t)
	cases := [][]string{
		{"claim"},                           // missing spec
		{"claim", "a", "b"},                 // too many args
		{"claim", "bad name"},               // invalid spec
		{"claim", "ok", "--port", "70000"},  // port out of bounds
		{"claim", "ok", "--port", "0"},      // explicit 0 must not auto-assign silently
		{"claim", "ok", "--range", "9-1"},   // reversed range
		{"claim", "ok", "--format", "yaml"}, // unknown format
		{"claim", "ok", "--no-such-flag"},   // unknown flag
		{"explain", "0"},                    // port below range
		{"explain", "notaport"},             // not a number
		{"explain", "70000"},                // port above range
		{"list", "extra"},                   // stray positional
		{"doctor", "extra"},                 // stray positional
	}
	for _, args := range cases {
		if code, _, _ := h.reg(args...); code != 2 {
			t.Errorf("%v should exit 2, got %d", args, code)
		}
	}
}

func TestGetPrintsBarePortForScripts(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop", "--port", "4800")
	code, out, _ := h.reg("get", "shop")
	if code != 0 || out != "4800\n" {
		t.Fatalf("get must print the bare port: code=%d out=%q", code, out)
	}
	// A miss exits 1 with an actionable hint on stderr, stdout stays clean.
	code, out, errOut := h.reg("get", "ghost")
	if code != 1 || out != "" || !strings.Contains(errOut, "ghost is not reserved") || !strings.Contains(errOut, "portberth claim ghost") {
		t.Fatalf("miss: code=%d out=%q stderr=%q", code, out, errOut)
	}
}

func TestReleaseRemovesReservation(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/web", "--port", "4900")
	code, out, _ := h.reg("release", "shop/web")
	if code != 0 || !strings.Contains(out, "released shop/web (port 4900)") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if code, _, _ := h.reg("get", "shop/web"); code != 1 {
		t.Fatal("reservation still present after release")
	}
}

func TestReleaseAllRemovesWholeProject(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/web", "--port", "4901")
	h.reg("claim", "shop/api", "--port", "4902")
	h.reg("claim", "blog", "--port", "4903")
	code, out, _ := h.reg("release", "shop", "--all")
	if code != 0 || !strings.Contains(out, "shop/api") || !strings.Contains(out, "shop/web") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if code, _, _ := h.reg("get", "blog"); code != 0 {
		t.Fatal("release --all must not touch other projects")
	}
}

func TestReleaseMissingExitsOne(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.reg("release", "ghost"); code != 1 {
		t.Fatal("releasing an unknown spec should exit 1")
	}
	if code, _, _ := h.reg("release", "ghost", "--all"); code != 1 {
		t.Fatal("releasing an unknown project should exit 1")
	}
	if code, _, _ := h.reg("release", "shop/web", "--all"); code != 2 {
		t.Fatal("--all with a service spec is a usage error")
	}
}

func TestListRendersAlignedTable(t *testing.T) {
	h := newHarness(t)
	// Empty registry first: an explicit message, exit 0.
	code0, out0, _ := h.reg("list")
	if code0 != 0 || !strings.Contains(out0, "no reservations in") {
		t.Fatalf("empty list: code=%d out=%q", code0, out0)
	}
	h.reg("claim", "shop/web", "--port", "4901", "--note", "storefront")
	h.reg("claim", "blog", "--port", "4903")
	code, out, _ := h.reg("list")
	if code != 0 {
		t.Fatalf("list failed: %d", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "PROJECT") {
		t.Fatalf("unexpected table:\n%s", out)
	}
	// Sorted: blog before shop.
	if !strings.HasPrefix(lines[1], "blog") || !strings.HasPrefix(lines[2], "shop") {
		t.Fatalf("rows not sorted:\n%s", out)
	}
	if !strings.Contains(lines[2], "storefront") {
		t.Fatalf("note missing:\n%s", out)
	}
}

func TestListProjectFilterAndJSON(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/web", "--port", "4901")
	h.reg("claim", "blog", "--port", "4903")
	code, out, _ := h.reg("list", "--project", "shop", "--format", "json")
	if code != 0 {
		t.Fatalf("list failed: %d", code)
	}
	var env struct {
		Data struct {
			Registry string `json:"registry"`
			Entries  []struct {
				Project string `json:"project"`
				Port    int    `json:"port"`
			} `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(env.Data.Entries) != 1 || env.Data.Entries[0].Project != "shop" || env.Data.Entries[0].Port != 4901 {
		t.Fatalf("filter wrong: %+v", env.Data)
	}
	if env.Data.Registry != h.registry {
		t.Fatalf("registry path missing from payload")
	}
}

func TestEnvPrintsVariables(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "my-shop", "--port", "4901")
	h.reg("claim", "my-shop/api", "--port", "4902")
	code, out, _ := h.reg("env", "my-shop")
	if code != 0 {
		t.Fatalf("env failed: %d", code)
	}
	if out != "MY_SHOP_API_PORT=4902\nMY_SHOP_PORT=4901\n" {
		t.Fatalf("unexpected env output: %q", out)
	}
	_, out, _ = h.reg("env", "my-shop", "--export")
	if !strings.HasPrefix(out, "export MY_SHOP_API_PORT=4902\n") {
		t.Fatalf("--export prefix missing: %q", out)
	}
}

func TestEnvMissingProjectExitsOne(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.reg("env", "ghost"); code != 1 {
		t.Fatal("env for unknown project should exit 1")
	}
}

func TestExplainFreePortExitsZero(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.reg("explain", "4444")
	if code != 0 {
		t.Fatalf("free port should exit 0, got %d", code)
	}
	for _, want := range []string{"port 4444", "no reservation", "no listener detected", "verdict: free"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestExplainReservedPort(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/db", "--port", "4321", "--note", "primary db")
	code, out, _ := h.reg("explain", "4321")
	if code != 1 {
		t.Fatalf("reserved port should exit 1, got %d", code)
	}
	for _, want := range []string{"reserved by shop/db since 2026-07-13", "(note: primary db)", "verdict: reserved, not in use"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestExplainWellKnownPort(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.reg("explain", "5432")
	if code != 0 { // well-known alone does not make a port "held"
		t.Fatalf("unreserved, unused well-known port should exit 0, got %d", code)
	}
	if !strings.Contains(out, "postgresql — PostgreSQL database server") {
		t.Fatalf("well-known info missing:\n%s", out)
	}
}

func TestExplainLiveListenerFromProcSnapshot(t *testing.T) {
	h := newHarness(t)
	h.addListener(t, 4555, "1234", "node")
	code, out, _ := h.reg("explain", "4555")
	if code != 1 {
		t.Fatalf("live port should exit 1, got %d", code)
	}
	for _, want := range []string{"LISTENING on 127.0.0.1 by pid 1234 (node)", "verdict: in use, not reserved"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Reserving the same port upgrades the verdict.
	h.reg("claim", "shop/web", "--port", "4555")
	code, out, _ = h.reg("explain", "4555")
	if code != 1 || !strings.Contains(out, "verdict: reserved and in use") {
		t.Fatalf("code=%d out:\n%s", code, out)
	}
}

func TestExplainProbeFallbackWhenProcfsSilent(t *testing.T) {
	// Non-Linux: procfs empty, but the bind probe knows the truth.
	h := newHarness(t)
	h.inUse[4666] = true
	code, out, _ := h.reg("explain", "4666")
	if code != 1 || !strings.Contains(out, "in use on loopback (process unknown)") {
		t.Fatalf("code=%d out:\n%s", code, out)
	}
}

func TestExplainJSON(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/db", "--port", "5432")
	_, out, _ := h.reg("explain", "5432", "--format", "json")
	var env struct {
		Data struct {
			Port      int  `json:"port"`
			Reserved  bool `json:"reserved"`
			InUse     bool `json:"in_use"`
			WellKnown *struct {
				Name string `json:"Name"`
			} `json:"well_known"`
			Verdict string `json:"verdict"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	d := env.Data
	if d.Port != 5432 || !d.Reserved || d.InUse || d.WellKnown == nil || d.Verdict != "reserved, not in use" {
		t.Fatalf("payload wrong: %+v", d)
	}
}

func TestDoctorCleanRegistryIsOK(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/web", "--port", "4901")
	code, out, _ := h.reg("doctor")
	if code != 0 || !strings.Contains(out, "doctor: OK — 0 errors, 0 warnings") {
		t.Fatalf("code=%d out:\n%s", code, out)
	}
	// One reservation: the header must use the singular form, not "1 entries".
	if !strings.Contains(out, "(1 entry)") {
		t.Fatalf("singular entry count missing:\n%s", out)
	}
}

func TestDoctorFlagsDuplicatePortsFromHandEdit(t *testing.T) {
	h := newHarness(t)
	// Fabricate the classic hand-edit accident: two specs, one port.
	handEdited := `{"schema_version":1,"entries":[
	  {"project":"shop","service":"web","port":4901,"claimed_at":"2026-07-13T09:00:00Z"},
	  {"project":"blog","service":"default","port":4901,"claimed_at":"2026-07-13T09:00:00Z"}]}`
	os.WriteFile(h.registry, []byte(handEdited), 0o600)
	code, out, _ := h.reg("doctor")
	if code != 1 {
		t.Fatalf("duplicate ports should fail doctor, got %d", code)
	}
	// Exactly one error: the summary must use the singular form.
	if !strings.Contains(out, "duplicate port 4901") || !strings.Contains(out, "doctor: FAIL — 1 error, 0 warnings") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestDoctorWarnsAboutWellKnownReservations(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/db", "--port", "5432")
	code, out, _ := h.reg("doctor")
	if code != 0 {
		t.Fatalf("warnings alone should not fail doctor, got %d", code)
	}
	if !strings.Contains(out, "shop/db reserves well-known port 5432 (postgresql") {
		t.Fatalf("warning missing:\n%s", out)
	}
	// --strict escalates.
	if code, _, _ := h.reg("doctor", "--strict"); code != 1 {
		t.Fatal("--strict should fail on warnings")
	}
}

func TestDoctorWarnsAboutLiveSquatters(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/web", "--port", "4555")
	h.addListener(t, 4555, "888", "python3")
	code, out, _ := h.reg("doctor")
	if code != 0 {
		t.Fatalf("doctor: %d", code)
	}
	if !strings.Contains(out, "shop/web port 4555 is in use by pid 888 (python3) on 127.0.0.1") {
		t.Fatalf("squatter warning missing:\n%s", out)
	}
}

func TestDoctorProbeFlagUsesLiveProbe(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/web", "--port", "4555")
	h.inUse[4555] = true // invisible to procfs, visible to bind probe
	_, out, _ := h.reg("doctor")
	if strings.Contains(out, "in use") {
		t.Fatalf("without --probe the bind probe must not run:\n%s", out)
	}
	_, out, _ = h.reg("doctor", "--probe")
	if !strings.Contains(out, "shop/web port 4555 is in use on loopback") {
		t.Fatalf("--probe warning missing:\n%s", out)
	}
}

func TestDoctorJSON(t *testing.T) {
	h := newHarness(t)
	h.reg("claim", "shop/db", "--port", "5432")
	_, out, _ := h.reg("doctor", "--format", "json")
	var env struct {
		Data struct {
			Entries  int      `json:"entries"`
			Errors   []string `json:"errors"`
			Warnings []string `json:"warnings"`
			OK       bool     `json:"ok"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	d := env.Data
	if d.Entries != 1 || len(d.Errors) != 0 || len(d.Warnings) != 1 || !d.OK {
		t.Fatalf("payload wrong: %+v", d)
	}
}

func TestVersionHelpAndUnknownCommand(t *testing.T) {
	h := newHarness(t)
	for _, arg := range []string{"version", "--version"} {
		code, out, _ := h.run(arg)
		if code != 0 || out != "portberth 0.1.0\n" {
			t.Errorf("%s: code=%d out=%q", arg, code, out)
		}
	}
	// version honors the global --format flag like every other command.
	jcode, jout, _ := h.run("version", "--format", "json")
	if jcode != 0 || !strings.Contains(jout, `"schema_version": 1`) || !strings.Contains(jout, `"version": "0.1.0"`) {
		t.Errorf("version --format json: code=%d out=%q", jcode, jout)
	}
	code, out, _ := h.run("help")
	if code != 0 || !strings.Contains(out, "usage: portberth") {
		t.Fatalf("help: code=%d", code)
	}
	code, _, errOut := h.run("frobnicate")
	if code != 2 || !strings.Contains(errOut, `unknown command "frobnicate"`) {
		t.Fatalf("unknown command: code=%d stderr=%q", code, errOut)
	}
	if code, _, _ = h.run(); code != 2 {
		t.Fatal("no arguments should exit 2")
	}
}

func TestEnvironmentVariableConfiguration(t *testing.T) {
	h := newHarness(t)
	// PORTBERTH_RANGE steers auto-assignment.
	h.envVars["PORTBERTH_RANGE"] = "4700-4700"
	code0, out0, _ := h.reg("claim", "ranged/web")
	if code0 != 0 || !strings.Contains(out0, "-> 4700") {
		t.Fatalf("PORTBERTH_RANGE not honored: code=%d out=%q", code0, out0)
	}
	delete(h.envVars, "PORTBERTH_RANGE")
	alt := filepath.Join(t.TempDir(), "alt.json")
	h.envVars["PORTBERTH_REGISTRY"] = alt
	// No --registry flag: PORTBERTH_REGISTRY wins over the config dir.
	code, _, _ := h.run("claim", "shop", "--port", "4700")
	if code != 0 {
		t.Fatalf("claim failed: %d", code)
	}
	if _, err := os.Stat(alt); err != nil {
		t.Fatalf("registry not written to $PORTBERTH_REGISTRY path: %v", err)
	}
	// Explicit --registry beats the environment.
	code, _, _ = h.reg("claim", "other", "--port", "4701")
	if code != 0 {
		t.Fatalf("claim failed: %d", code)
	}
	if _, err := os.Stat(h.registry); err != nil {
		t.Fatalf("--registry ignored: %v", err)
	}
}

func TestCorruptRegistryExitsThreeEverywhere(t *testing.T) {
	h := newHarness(t)
	os.WriteFile(h.registry, []byte("{broken"), 0o600)
	for _, args := range [][]string{
		{"claim", "shop"}, {"get", "shop"}, {"release", "shop"},
		{"list"}, {"env", "shop"}, {"explain", "3000"}, {"doctor"},
	} {
		if code, _, errOut := h.reg(args...); code != 3 || !strings.Contains(errOut, "not valid JSON") {
			t.Errorf("%v: code=%d stderr=%q", args, code, errOut)
		}
	}
}
