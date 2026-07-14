package cli

import (
	"fmt"

	"github.com/JaydenCJ/portberth/internal/portspec"
	"github.com/JaydenCJ/portberth/internal/probe"
	"github.com/JaydenCJ/portberth/internal/registry"
	"github.com/JaydenCJ/portberth/internal/render"
	"github.com/JaydenCJ/portberth/internal/wellknown"
)

// explainResult is the JSON payload for `explain`.
type explainResult struct {
	Port         int                `json:"port"`
	Reservations []registry.Entry   `json:"reservations"`
	WellKnown    *wellknown.Service `json:"well_known"`
	Listeners    []probe.Listener   `json:"listeners"`
	InUse        bool               `json:"in_use"`
	Reserved     bool               `json:"reserved"`
	Verdict      string             `json:"verdict"`
}

// cmdExplain reports every provenance signal portberth has for one port:
// registry reservations, the well-known table, and live listeners. Exit
// code 0 means the port is entirely free; 1 means something — a
// reservation or a listener — holds it.
func cmdExplain(args []string, env *Env) int {
	fs, registryPath, format := newFlagSet("explain", env)
	pos, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return usageErr(env, "explain: expected exactly one <port> argument")
	}
	if !checkFormat(env, *format) {
		return exitUsage
	}
	port, err := portspec.ParsePort(pos[0])
	if err != nil {
		return usageErr(env, "explain: %v", err)
	}
	path, err := resolveRegistryPath(env, *registryPath)
	if err != nil {
		return runtimeErr(env, err)
	}
	reg, err := registry.Load(path)
	if err != nil {
		return runtimeErr(env, err)
	}

	res := explainResult{
		Port:         port,
		Reservations: reg.ByPort(port),
		Listeners:    probe.OnPort(probe.Snapshot(env.ProcRoot), port),
	}
	if svc, known := wellknown.Lookup(port); known {
		res.WellKnown = &svc
	}
	res.Reserved = len(res.Reservations) > 0
	res.InUse = len(res.Listeners) > 0
	if !res.InUse && env.PortInUse(port) {
		// procfs saw nothing (non-Linux, or a foreign network namespace)
		// but the bind probe disagrees; trust the probe.
		res.InUse = true
	}
	res.Verdict = verdict(res.Reserved, res.InUse)

	if *format == "json" {
		if err := render.JSON(env.Stdout, res); err != nil {
			return runtimeErr(env, err)
		}
	} else {
		printExplain(env, res)
	}
	if res.Reserved || res.InUse {
		return exitConflict
	}
	return exitOK
}

func verdict(reserved, inUse bool) string {
	switch {
	case reserved && inUse:
		return "reserved and in use"
	case reserved:
		return "reserved, not in use"
	case inUse:
		return "in use, not reserved"
	default:
		return "free"
	}
}

func printExplain(env *Env, res explainResult) {
	fmt.Fprintf(env.Stdout, "port %d\n\n", res.Port)

	if len(res.Reservations) == 0 {
		fmt.Fprintf(env.Stdout, "  registry    no reservation\n")
	}
	for _, e := range res.Reservations {
		line := fmt.Sprintf("  registry    reserved by %s since %s", e.Spec(), sinceDate(e.ClaimedAt))
		if e.Note != "" {
			line += fmt.Sprintf(" (note: %s)", e.Note)
		}
		fmt.Fprintln(env.Stdout, line)
	}

	if res.WellKnown != nil {
		fmt.Fprintf(env.Stdout, "  well-known  %s — %s\n", res.WellKnown.Name, res.WellKnown.Desc)
	} else {
		fmt.Fprintf(env.Stdout, "  well-known  no\n")
	}

	switch {
	case len(res.Listeners) > 0:
		for _, l := range res.Listeners {
			if l.PID > 0 {
				fmt.Fprintf(env.Stdout, "  live        LISTENING on %s by pid %d (%s)\n", l.Addr, l.PID, l.Process)
			} else {
				fmt.Fprintf(env.Stdout, "  live        LISTENING on %s (process not visible)\n", l.Addr)
			}
		}
	case res.InUse:
		fmt.Fprintf(env.Stdout, "  live        in use on loopback (process unknown)\n")
	default:
		fmt.Fprintf(env.Stdout, "  live        no listener detected\n")
	}

	fmt.Fprintf(env.Stdout, "\nverdict: %s\n", res.Verdict)
}
