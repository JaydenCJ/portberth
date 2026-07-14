package cli

import (
	"flag"
	"fmt"
	"time"

	"github.com/JaydenCJ/portberth/internal/assign"
	"github.com/JaydenCJ/portberth/internal/portspec"
	"github.com/JaydenCJ/portberth/internal/registry"
	"github.com/JaydenCJ/portberth/internal/render"
	"github.com/JaydenCJ/portberth/internal/wellknown"
)

// claimResult is the JSON payload for `claim`.
type claimResult struct {
	Project   string `json:"project"`
	Service   string `json:"service"`
	Port      int    `json:"port"`
	Created   bool   `json:"created"`
	ClaimedAt string `json:"claimed_at"`
	Note      string `json:"note,omitempty"`
}

// cmdClaim reserves a stable port for project[/service]. Idempotent:
// claiming an existing reservation returns the same port and changes
// nothing. Conflicts (an explicitly requested port that someone else
// holds) exit 1 with provenance, never silently reassign.
func cmdClaim(args []string, env *Env) int {
	fs, registryPath, format := newFlagSet("claim", env)
	port := fs.Int("port", 0, "request this exact port instead of auto-assigning")
	rangeStr := fs.String("range", "", "auto-assign range LO-HI (default $PORTBERTH_RANGE or "+DefaultRange+")")
	note := fs.String("note", "", "free-form note stored with the reservation")
	probeLive := fs.Bool("probe", false, "also verify the port is not live-occupied right now")
	allowWellKnown := fs.Bool("allow-well-known", false, "let auto-assignment use well-known ports")
	pos, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return usageErr(env, "claim: expected exactly one <project>[/<service>] argument")
	}
	if !checkFormat(env, *format) {
		return exitUsage
	}
	spec, err := portspec.ParseSpec(pos[0])
	if err != nil {
		return usageErr(env, "claim: %v", err)
	}
	// Validate --port whenever the flag was given at all, so an explicit
	// `--port 0` is refused rather than silently falling back to
	// auto-assignment (0 doubles as the "flag absent" sentinel below).
	portSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portSet = true
		}
	})
	if portSet && (*port < 1 || *port > 65535) {
		return usageErr(env, "claim: --port %d is outside 1-65535", *port)
	}

	path, err := resolveRegistryPath(env, *registryPath)
	if err != nil {
		return runtimeErr(env, err)
	}
	reg, err := registry.Load(path)
	if err != nil {
		return runtimeErr(env, err)
	}

	// Idempotent path: the spec is already reserved.
	if existing, ok := reg.Lookup(spec); ok {
		if *port != 0 && *port != existing.Port {
			fmt.Fprintf(env.Stderr,
				"claim: %s is already reserved at port %d; refusing to move it to %d\n"+
					"hint: `portberth release %s` first if the move is intentional\n",
				spec, existing.Port, *port, spec)
			return exitConflict
		}
		return emitClaim(env, *format, existing, false)
	}

	entry := registry.Entry{
		Project:   spec.Project,
		Service:   spec.Service,
		Port:      *port,
		ClaimedAt: env.Now().UTC().Format(time.RFC3339),
		Note:      *note,
	}

	if *port != 0 {
		// Explicit port: refuse with provenance if anything claims it.
		if code, ok := checkExplicitPort(env, reg, spec, *port, *probeLive); !ok {
			return code
		}
	} else {
		rng, code, ok := resolveRange(env, *rangeStr)
		if !ok {
			return code
		}
		avoid := func(p int) bool {
			if !*allowWellKnown {
				if _, known := wellknown.Lookup(p); known {
					return true
				}
			}
			return *probeLive && env.PortInUse(p)
		}
		picked, err := assign.Pick(spec, rng, reg.TakenPorts(), avoid)
		if err != nil {
			fmt.Fprintf(env.Stderr, "claim: %v\n", err)
			return exitConflict
		}
		entry.Port = picked
	}

	if err := reg.Add(entry); err != nil {
		return runtimeErr(env, err)
	}
	if err := reg.Save(); err != nil {
		return runtimeErr(env, err)
	}
	return emitClaim(env, *format, entry, true)
}

// checkExplicitPort validates a --port request against the registry,
// the well-known table, and (optionally) live occupancy. Returns
// ok=false with the exit code to use when the port must be refused.
func checkExplicitPort(env *Env, reg *registry.Registry, spec portspec.Spec, port int, probeLive bool) (int, bool) {
	if owners := reg.ByPort(port); len(owners) > 0 {
		fmt.Fprintf(env.Stderr, "claim: port %d is not available for %s\n", port, spec)
		for _, o := range owners {
			fmt.Fprintf(env.Stderr, "  reserved by %s since %s\n", o.Spec(), sinceDate(o.ClaimedAt))
		}
		if svc, known := wellknown.Lookup(port); known {
			fmt.Fprintf(env.Stderr, "  well-known: %s — %s\n", svc.Name, svc.Desc)
		}
		fmt.Fprintf(env.Stderr, "hint: `portberth explain %d` shows full provenance; omit --port to auto-assign\n", port)
		return exitConflict, false
	}
	if svc, known := wellknown.Lookup(port); known {
		// Explicit requests may take well-known ports, but say so.
		fmt.Fprintf(env.Stderr, "warning: %d is a well-known port (%s — %s)\n", port, svc.Name, svc.Desc)
	}
	if probeLive && env.PortInUse(port) {
		fmt.Fprintf(env.Stderr, "claim: port %d is live-occupied right now (unreserved)\n", port)
		fmt.Fprintf(env.Stderr, "hint: `portberth explain %d` shows who is listening\n", port)
		return exitConflict, false
	}
	return exitOK, true
}

// resolveRange applies precedence for the auto-assign range: --range,
// then $PORTBERTH_RANGE, then DefaultRange.
func resolveRange(env *Env, flagValue string) (portspec.Range, int, bool) {
	raw := flagValue
	if raw == "" {
		raw = env.Getenv("PORTBERTH_RANGE")
	}
	if raw == "" {
		raw = DefaultRange
	}
	rng, err := portspec.ParseRange(raw)
	if err != nil {
		return portspec.Range{}, usageErr(env, "claim: %v", err), false
	}
	return rng, exitOK, true
}

func emitClaim(env *Env, format string, e registry.Entry, created bool) int {
	if format == "json" {
		if err := render.JSON(env.Stdout, claimResult{
			Project: e.Project, Service: e.Service, Port: e.Port,
			Created: created, ClaimedAt: e.ClaimedAt, Note: e.Note,
		}); err != nil {
			return runtimeErr(env, err)
		}
		return exitOK
	}
	if created {
		fmt.Fprintf(env.Stdout, "reserved %s -> %d\n", e.Spec(), e.Port)
	} else {
		fmt.Fprintf(env.Stdout, "%s -> %d (existing reservation since %s)\n",
			e.Spec(), e.Port, sinceDate(e.ClaimedAt))
	}
	return exitOK
}

// cmdGet prints the reserved port for a spec — nothing else, so shell
// substitution stays trivial: PORT=$(portberth get myapp).
func cmdGet(args []string, env *Env) int {
	fs, registryPath, format := newFlagSet("get", env)
	pos, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return usageErr(env, "get: expected exactly one <project>[/<service>] argument")
	}
	if !checkFormat(env, *format) {
		return exitUsage
	}
	spec, err := portspec.ParseSpec(pos[0])
	if err != nil {
		return usageErr(env, "get: %v", err)
	}
	path, err := resolveRegistryPath(env, *registryPath)
	if err != nil {
		return runtimeErr(env, err)
	}
	reg, err := registry.Load(path)
	if err != nil {
		return runtimeErr(env, err)
	}
	e, ok := reg.Lookup(spec)
	if !ok {
		fmt.Fprintf(env.Stderr, "get: %s is not reserved\nhint: `portberth claim %s` assigns a stable port\n", spec, spec)
		return exitConflict
	}
	if *format == "json" {
		if err := render.JSON(env.Stdout, claimResult{
			Project: e.Project, Service: e.Service, Port: e.Port,
			Created: false, ClaimedAt: e.ClaimedAt, Note: e.Note,
		}); err != nil {
			return runtimeErr(env, err)
		}
		return exitOK
	}
	fmt.Fprintf(env.Stdout, "%d\n", e.Port)
	return exitOK
}

// cmdRelease drops one reservation, or a whole project with --all.
func cmdRelease(args []string, env *Env) int {
	fs, registryPath, format := newFlagSet("release", env)
	all := fs.Bool("all", false, "release every service of the project")
	pos, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return usageErr(env, "release: expected exactly one <project>[/<service>] argument")
	}
	if !checkFormat(env, *format) {
		return exitUsage
	}
	spec, err := portspec.ParseSpec(pos[0])
	if err != nil {
		return usageErr(env, "release: %v", err)
	}
	if *all && spec.Service != portspec.DefaultService {
		return usageErr(env, "release: --all takes a bare project name, not %s", spec)
	}
	path, err := resolveRegistryPath(env, *registryPath)
	if err != nil {
		return runtimeErr(env, err)
	}
	reg, err := registry.Load(path)
	if err != nil {
		return runtimeErr(env, err)
	}

	type releaseResult struct {
		Released []registry.Entry `json:"released"`
	}
	var released []registry.Entry
	if *all {
		released = reg.ByProject(spec.Project)
		if len(released) == 0 {
			fmt.Fprintf(env.Stderr, "release: project %s has no reservations\n", spec.Project)
			return exitConflict
		}
		reg.RemoveProject(spec.Project)
	} else {
		e, ok := reg.Lookup(spec)
		if !ok {
			fmt.Fprintf(env.Stderr, "release: %s is not reserved\n", spec)
			return exitConflict
		}
		released = []registry.Entry{e}
		reg.Remove(spec)
	}
	if err := reg.Save(); err != nil {
		return runtimeErr(env, err)
	}
	if *format == "json" {
		if err := render.JSON(env.Stdout, releaseResult{Released: released}); err != nil {
			return runtimeErr(env, err)
		}
		return exitOK
	}
	for _, e := range released {
		fmt.Fprintf(env.Stdout, "released %s (port %d)\n", e.Spec(), e.Port)
	}
	return exitOK
}

// cmdEnv prints ready-to-eval PORT variables for every service of a
// project: MYAPP_PORT for the default service, MYAPP_API_PORT for "api".
func cmdEnv(args []string, env *Env) int {
	fs, registryPath, format := newFlagSet("env", env)
	export := fs.Bool("export", false, "prefix each line with 'export '")
	pos, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return usageErr(env, "env: expected exactly one <project> argument")
	}
	if !checkFormat(env, *format) {
		return exitUsage
	}
	spec, err := portspec.ParseSpec(pos[0])
	if err != nil {
		return usageErr(env, "env: %v", err)
	}
	if spec.Service != portspec.DefaultService {
		return usageErr(env, "env: takes a bare project name, not %s", spec)
	}
	path, err := resolveRegistryPath(env, *registryPath)
	if err != nil {
		return runtimeErr(env, err)
	}
	reg, err := registry.Load(path)
	if err != nil {
		return runtimeErr(env, err)
	}
	entries := reg.ByProject(spec.Project)
	if len(entries) == 0 {
		fmt.Fprintf(env.Stderr, "env: project %s has no reservations\n", spec.Project)
		return exitConflict
	}
	type envVar struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	}
	if *format == "json" {
		vars := make([]envVar, 0, len(entries))
		for _, e := range entries {
			vars = append(vars, envVar{Name: portspec.EnvName(e.Project, e.Service), Port: e.Port})
		}
		if err := render.JSON(env.Stdout, vars); err != nil {
			return runtimeErr(env, err)
		}
		return exitOK
	}
	prefix := ""
	if *export {
		prefix = "export "
	}
	for _, e := range entries {
		fmt.Fprintf(env.Stdout, "%s%s=%d\n", prefix, portspec.EnvName(e.Project, e.Service), e.Port)
	}
	return exitOK
}
