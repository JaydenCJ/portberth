package cli

import (
	"fmt"

	"github.com/JaydenCJ/portberth/internal/probe"
	"github.com/JaydenCJ/portberth/internal/registry"
	"github.com/JaydenCJ/portberth/internal/render"
	"github.com/JaydenCJ/portberth/internal/wellknown"
)

// listResult is the JSON payload for `list`.
type listResult struct {
	Registry string           `json:"registry"`
	Entries  []registry.Entry `json:"entries"`
}

// cmdList prints every reservation as an aligned table or JSON.
func cmdList(args []string, env *Env) int {
	fs, registryPath, format := newFlagSet("list", env)
	project := fs.String("project", "", "show only this project's reservations")
	pos, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return usageErr(env, "list: takes no positional arguments")
	}
	if !checkFormat(env, *format) {
		return exitUsage
	}
	path, err := resolveRegistryPath(env, *registryPath)
	if err != nil {
		return runtimeErr(env, err)
	}
	reg, err := registry.Load(path)
	if err != nil {
		return runtimeErr(env, err)
	}
	entries := reg.Entries
	if *project != "" {
		entries = reg.ByProject(*project)
	}
	if *format == "json" {
		if entries == nil {
			entries = []registry.Entry{}
		}
		if err := render.JSON(env.Stdout, listResult{Registry: path, Entries: entries}); err != nil {
			return runtimeErr(env, err)
		}
		return exitOK
	}
	if len(entries) == 0 {
		fmt.Fprintf(env.Stdout, "no reservations in %s\n", path)
		return exitOK
	}
	rows := [][]string{{"PROJECT", "SERVICE", "PORT", "SINCE", "NOTE"}}
	for _, e := range entries {
		rows = append(rows, []string{e.Project, e.Service, fmt.Sprintf("%d", e.Port), sinceDate(e.ClaimedAt), e.Note})
	}
	render.Table(env.Stdout, rows, map[int]bool{2: true})
	return exitOK
}

// doctorResult is the JSON payload for `doctor`.
type doctorResult struct {
	Registry string   `json:"registry"`
	Entries  int      `json:"entries"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
	OK       bool     `json:"ok"`
}

// cmdDoctor audits the registry against reality:
//
//	errors   — internal integrity breaks (duplicate ports or specs,
//	           invalid names/ports), typically from hand edits.
//	warnings — friction with the outside world: reservations sitting on
//	           well-known ports, and reservations whose port some process
//	           is live-occupying right now.
//
// Exit 1 on errors; --strict escalates warnings to failures too.
func cmdDoctor(args []string, env *Env) int {
	fs, registryPath, format := newFlagSet("doctor", env)
	strict := fs.Bool("strict", false, "treat warnings as failures")
	probeLive := fs.Bool("probe", false, "also bind-probe each reserved port on loopback")
	pos, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return usageErr(env, "doctor: takes no positional arguments")
	}
	if !checkFormat(env, *format) {
		return exitUsage
	}
	path, err := resolveRegistryPath(env, *registryPath)
	if err != nil {
		return runtimeErr(env, err)
	}
	reg, err := registry.Load(path)
	if err != nil {
		return runtimeErr(env, err)
	}

	res := doctorResult{Registry: path, Entries: len(reg.Entries)}
	res.Errors = reg.Validate()
	res.Warnings = doctorWarnings(env, reg, *probeLive)
	if res.Errors == nil {
		res.Errors = []string{}
	}
	if res.Warnings == nil {
		res.Warnings = []string{}
	}
	res.OK = len(res.Errors) == 0 && (!*strict || len(res.Warnings) == 0)

	if *format == "json" {
		if err := render.JSON(env.Stdout, res); err != nil {
			return runtimeErr(env, err)
		}
	} else {
		printDoctor(env, res)
	}
	if !res.OK {
		return exitConflict
	}
	return exitOK
}

// doctorWarnings collects the non-fatal findings for every reservation,
// in registry order so output is deterministic.
func doctorWarnings(env *Env, reg *registry.Registry, probeLive bool) []string {
	var warnings []string
	listeners := probe.Snapshot(env.ProcRoot)
	for _, e := range reg.Entries {
		if svc, known := wellknown.Lookup(e.Port); known {
			warnings = append(warnings,
				fmt.Sprintf("%s reserves well-known port %d (%s — %s)", e.Spec(), e.Port, svc.Name, svc.Desc))
		}
		live := probe.OnPort(listeners, e.Port)
		switch {
		case len(live) > 0:
			l := live[0]
			if l.PID > 0 {
				warnings = append(warnings,
					fmt.Sprintf("%s port %d is in use by pid %d (%s) on %s", e.Spec(), e.Port, l.PID, l.Process, l.Addr))
			} else {
				warnings = append(warnings,
					fmt.Sprintf("%s port %d is in use on %s (process not visible)", e.Spec(), e.Port, l.Addr))
			}
		case probeLive && env.PortInUse(e.Port):
			warnings = append(warnings,
				fmt.Sprintf("%s port %d is in use on loopback (process unknown)", e.Spec(), e.Port))
		}
	}
	return warnings
}

func printDoctor(env *Env, res doctorResult) {
	fmt.Fprintf(env.Stdout, "registry: %s (%s)\n", res.Registry, plural(res.Entries, "entry", "entries"))
	if len(res.Errors) > 0 {
		fmt.Fprintf(env.Stdout, "\nerrors\n")
		for _, e := range res.Errors {
			fmt.Fprintf(env.Stdout, "  %s\n", e)
		}
	}
	if len(res.Warnings) > 0 {
		fmt.Fprintf(env.Stdout, "\nwarnings\n")
		for _, w := range res.Warnings {
			fmt.Fprintf(env.Stdout, "  %s\n", w)
		}
	}
	status := "OK"
	if !res.OK {
		status = "FAIL"
	}
	fmt.Fprintf(env.Stdout, "\ndoctor: %s — %s, %s\n", status,
		plural(len(res.Errors), "error", "errors"), plural(len(res.Warnings), "warning", "warnings"))
}

// plural renders "1 error" but "0 errors" / "2 entries" — small, but a
// diagnostic tool that misgrammars its own summary erodes trust fast.
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", n, pluralForm)
}
