// Package portspec parses and validates the small grammar portberth
// exposes on the command line: project/service specs, port numbers, and
// port ranges. Everything here is pure and unit-testable.
package portspec

import (
	"fmt"
	"strconv"
	"strings"
)

// DefaultService is the service name used when a spec names only a
// project ("myapp" means "myapp/default").
const DefaultService = "default"

// MaxNameLen bounds project and service names so registry files and
// rendered tables stay sane.
const MaxNameLen = 64

// Spec identifies one reservation slot: a project plus a service within
// it. A project can hold many services (web, api, db, ...), each with its
// own stable port.
type Spec struct {
	Project string
	Service string
}

// String renders the canonical form: "project/service", with the default
// service left implicit.
func (s Spec) String() string {
	if s.Service == DefaultService {
		return s.Project
	}
	return s.Project + "/" + s.Service
}

// ParseSpec parses "project" or "project/service". Names are normalized
// to lowercase; validation is strict so registry keys stay canonical.
func ParseSpec(raw string) (Spec, error) {
	if raw == "" {
		return Spec{}, fmt.Errorf("empty spec: expected project or project/service")
	}
	project, service := raw, DefaultService
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		project, service = raw[:i], raw[i+1:]
		if strings.Contains(service, "/") {
			return Spec{}, fmt.Errorf("invalid spec %q: at most one '/' allowed", raw)
		}
	}
	project = strings.ToLower(project)
	service = strings.ToLower(service)
	if err := ValidateName(project); err != nil {
		return Spec{}, fmt.Errorf("invalid project in %q: %w", raw, err)
	}
	if err := ValidateName(service); err != nil {
		return Spec{}, fmt.Errorf("invalid service in %q: %w", raw, err)
	}
	return Spec{Project: project, Service: service}, nil
}

// ValidateName enforces the naming rules for projects and services:
// lowercase letters, digits, '.', '_' and '-'; must start with a letter
// or digit; length 1..MaxNameLen.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is empty")
	}
	if len(name) > MaxNameLen {
		return fmt.Errorf("name %q exceeds %d characters", name, MaxNameLen)
	}
	for i, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-'
		if !ok {
			return fmt.Errorf("name %q contains invalid character %q", name, r)
		}
		if i == 0 && (r == '.' || r == '_' || r == '-') {
			return fmt.Errorf("name %q must start with a letter or digit", name)
		}
	}
	return nil
}

// Range is an inclusive TCP port range.
type Range struct {
	Lo int
	Hi int
}

// Size returns how many ports the range contains.
func (r Range) Size() int { return r.Hi - r.Lo + 1 }

// Contains reports whether p falls inside the range.
func (r Range) Contains(p int) bool { return p >= r.Lo && p <= r.Hi }

// String renders "lo-hi".
func (r Range) String() string { return fmt.Sprintf("%d-%d", r.Lo, r.Hi) }

// ParseRange parses "lo-hi" (e.g. "3000-3999"). Both ends must be valid
// ports and lo must not exceed hi.
func ParseRange(raw string) (Range, error) {
	lo, hi, ok := strings.Cut(raw, "-")
	if !ok {
		return Range{}, fmt.Errorf("invalid range %q: expected LO-HI, e.g. 3000-3999", raw)
	}
	l, err := ParsePort(strings.TrimSpace(lo))
	if err != nil {
		return Range{}, fmt.Errorf("invalid range %q: %w", raw, err)
	}
	h, err := ParsePort(strings.TrimSpace(hi))
	if err != nil {
		return Range{}, fmt.Errorf("invalid range %q: %w", raw, err)
	}
	if l > h {
		return Range{}, fmt.Errorf("invalid range %q: low end exceeds high end", raw)
	}
	return Range{Lo: l, Hi: h}, nil
}

// ParsePort parses a decimal TCP port in 1..65535.
func ParsePort(raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("port %q is not a number", raw)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port %d is outside 1-65535", n)
	}
	return n, nil
}

// EnvName derives the shell variable name portberth emits for a
// reservation: MYAPP_PORT for the default service, MYAPP_API_PORT for
// service "api". Characters outside [A-Z0-9] become underscores.
func EnvName(project, service string) string {
	if service == DefaultService {
		return sanitizeEnv(project) + "_PORT"
	}
	return sanitizeEnv(project) + "_" + sanitizeEnv(service) + "_PORT"
}

func sanitizeEnv(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
