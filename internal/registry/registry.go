// Package registry owns the on-disk reservation file: a small, humanly
// readable JSON document that maps project/service pairs to ports. Loads
// tolerate a missing file, saves are atomic (temp file + rename), and
// Validate surfaces integrity problems introduced by hand edits.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/JaydenCJ/portberth/internal/portspec"
)

// SchemaVersion is the registry file schema this build reads and writes.
const SchemaVersion = 1

// Entry is one reservation: a project/service pair pinned to a port.
type Entry struct {
	Project   string `json:"project"`
	Service   string `json:"service"`
	Port      int    `json:"port"`
	ClaimedAt string `json:"claimed_at"` // RFC 3339, UTC
	Note      string `json:"note,omitempty"`
}

// Spec returns the entry's project/service identity.
func (e Entry) Spec() portspec.Spec {
	return portspec.Spec{Project: e.Project, Service: e.Service}
}

// Registry is the in-memory form of the reservation file.
type Registry struct {
	SchemaVersion int     `json:"schema_version"`
	Entries       []Entry `json:"entries"`

	path string
}

// file mirrors Registry for encoding, without the private path field.
type file struct {
	SchemaVersion int     `json:"schema_version"`
	Entries       []Entry `json:"entries"`
}

// Load reads the registry at path. A missing file yields an empty
// registry (first run needs no setup); malformed JSON or a
// newer-than-supported schema is an error, never silently discarded.
func Load(path string) (*Registry, error) {
	r := &Registry{SchemaVersion: SchemaVersion, path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read registry %s: %w", path, err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("registry %s is not valid JSON: %w (fix or remove the file)", path, err)
	}
	if f.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("registry %s uses schema %d, this portberth understands up to %d (upgrade portberth)",
			path, f.SchemaVersion, SchemaVersion)
	}
	r.Entries = f.Entries
	r.sort()
	return r, nil
}

// Path returns where this registry loads from and saves to.
func (r *Registry) Path() string { return r.path }

// Save writes the registry atomically: marshal to a temp file in the
// destination directory, fsync-free rename over the target. Readers never
// observe a half-written file.
func (r *Registry) Save() error {
	r.sort()
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("create registry directory: %w", err)
	}
	data, err := json.MarshalIndent(file{SchemaVersion: SchemaVersion, Entries: r.Entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".portberth-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp registry: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp registry: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp registry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp registry: %w", err)
	}
	if err := os.Rename(tmpName, r.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace registry: %w", err)
	}
	return nil
}

// Lookup returns the entry for a spec, if reserved.
func (r *Registry) Lookup(s portspec.Spec) (Entry, bool) {
	for _, e := range r.Entries {
		if e.Project == s.Project && e.Service == s.Service {
			return e, true
		}
	}
	return Entry{}, false
}

// ByPort returns every entry pinned to the given port. A healthy registry
// has at most one, but hand edits can introduce duplicates — callers must
// cope, and doctor reports them.
func (r *Registry) ByPort(port int) []Entry {
	var out []Entry
	for _, e := range r.Entries {
		if e.Port == port {
			out = append(out, e)
		}
	}
	return out
}

// ByProject returns every entry belonging to a project, sorted.
func (r *Registry) ByProject(project string) []Entry {
	var out []Entry
	for _, e := range r.Entries {
		if e.Project == project {
			out = append(out, e)
		}
	}
	return out
}

// TakenPorts returns the set of every reserved port.
func (r *Registry) TakenPorts() map[int]bool {
	taken := make(map[int]bool, len(r.Entries))
	for _, e := range r.Entries {
		taken[e.Port] = true
	}
	return taken
}

// Add inserts a new reservation. The spec must not already be present;
// callers decide idempotency policy before calling.
func (r *Registry) Add(e Entry) error {
	if _, ok := r.Lookup(e.Spec()); ok {
		return fmt.Errorf("%s is already reserved", e.Spec())
	}
	r.Entries = append(r.Entries, e)
	r.sort()
	return nil
}

// Remove deletes the reservation for a spec, reporting whether it existed.
func (r *Registry) Remove(s portspec.Spec) bool {
	for i, e := range r.Entries {
		if e.Project == s.Project && e.Service == s.Service {
			r.Entries = append(r.Entries[:i], r.Entries[i+1:]...)
			return true
		}
	}
	return false
}

// RemoveProject deletes every reservation of a project and returns how
// many were removed.
func (r *Registry) RemoveProject(project string) int {
	kept := r.Entries[:0]
	removed := 0
	for _, e := range r.Entries {
		if e.Project == project {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	r.Entries = kept
	return removed
}

// Validate checks registry integrity and returns one message per
// problem. It guards against the failure modes of hand-edited files:
// duplicate ports, duplicate specs, invalid ports, and invalid names.
func (r *Registry) Validate() []string {
	var problems []string
	seenSpec := map[string]bool{}
	portOwner := map[int]string{}
	for _, e := range r.Entries {
		spec := e.Spec().String()
		if err := portspec.ValidateName(e.Project); err != nil {
			problems = append(problems, fmt.Sprintf("entry %s: %v", spec, err))
		}
		if err := portspec.ValidateName(e.Service); err != nil {
			problems = append(problems, fmt.Sprintf("entry %s: %v", spec, err))
		}
		if e.Port < 1 || e.Port > 65535 {
			problems = append(problems, fmt.Sprintf("entry %s: port %d is outside 1-65535", spec, e.Port))
		}
		if seenSpec[spec] {
			problems = append(problems, fmt.Sprintf("duplicate entry for %s", spec))
		}
		seenSpec[spec] = true
		if owner, dup := portOwner[e.Port]; dup {
			problems = append(problems, fmt.Sprintf("duplicate port %d: reserved by both %s and %s", e.Port, owner, spec))
		} else {
			portOwner[e.Port] = spec
		}
	}
	return problems
}

// sort keeps entries in a stable, human-friendly order: by project, then
// service. All rendered output inherits this determinism.
func (r *Registry) sort() {
	sort.Slice(r.Entries, func(i, j int) bool {
		if r.Entries[i].Project != r.Entries[j].Project {
			return r.Entries[i].Project < r.Entries[j].Project
		}
		return r.Entries[i].Service < r.Entries[j].Service
	})
}
