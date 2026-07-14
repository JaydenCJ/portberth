// Package cli wires portberth's subcommands together. Everything the
// commands touch that belongs to the outside world — clock, environment
// variables, procfs root, live port probe, config directory — enters
// through Env, so integration tests run the real command paths fully
// in-process and fully deterministically.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/JaydenCJ/portberth/internal/probe"
	"github.com/JaydenCJ/portberth/internal/render"
	"github.com/JaydenCJ/portberth/internal/version"
)

// Exit codes, documented in the README:
//
//	0 success (for explain: the port is free)
//	1 conflict, breach, or not found
//	2 usage error
//	3 runtime error (unreadable registry, I/O failure)
const (
	exitOK       = 0
	exitConflict = 1
	exitUsage    = 2
	exitRuntime  = 3
)

// DefaultRange is where auto-assigned ports live unless --range or
// PORTBERTH_RANGE says otherwise.
const DefaultRange = "3000-3999"

// Env is the command environment. Zero fields are filled with real
// implementations by Run; tests override them.
type Env struct {
	Stdout        io.Writer
	Stderr        io.Writer
	Getenv        func(string) string
	Now           func() time.Time
	ProcRoot      string         // procfs root for listener snapshots
	PortInUse     func(int) bool // live loopback probe
	UserConfigDir func() (string, error)
}

func (e *Env) fillDefaults() {
	if e.Stdout == nil {
		e.Stdout = os.Stdout
	}
	if e.Stderr == nil {
		e.Stderr = os.Stderr
	}
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.ProcRoot == "" {
		e.ProcRoot = probe.DefaultProcRoot
	}
	if e.PortInUse == nil {
		e.PortInUse = probe.PortInUse
	}
	if e.UserConfigDir == nil {
		e.UserConfigDir = os.UserConfigDir
	}
}

// Run executes one portberth invocation and returns its exit code.
func Run(args []string, env Env) int {
	env.fillDefaults()
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "claim":
		return cmdClaim(args[1:], &env)
	case "get":
		return cmdGet(args[1:], &env)
	case "release":
		return cmdRelease(args[1:], &env)
	case "list", "ls":
		return cmdList(args[1:], &env)
	case "env":
		return cmdEnv(args[1:], &env)
	case "explain":
		return cmdExplain(args[1:], &env)
	case "doctor":
		return cmdDoctor(args[1:], &env)
	case "version", "--version", "-V":
		return cmdVersion(args[1:], &env)
	case "help", "--help", "-h":
		fmt.Fprint(env.Stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(env.Stderr, "portberth: unknown command %q\n\n", args[0])
		fmt.Fprint(env.Stderr, usage)
		return exitUsage
	}
}

const usage = `portberth — a local port registry: stable ports per project, honest answers about conflicts

usage: portberth <command> [flags] [args]

commands:
  claim <project>[/<service>]    reserve a stable port (idempotent)
  get <project>[/<service>]      print the reserved port number
  release <project>[/<service>]  drop a reservation (--all drops the whole project)
  list                           show every reservation (alias: ls)
  env <project>                  print PORT environment variables for a project
  explain <port>                 full provenance for one port: registry, well-known, live
  doctor                         audit the registry against reality
  version                        print the version

global flags (accepted by every command):
  --registry PATH   registry file (default: $PORTBERTH_REGISTRY, else <user-config>/portberth/registry.json)
  --format FMT      output format: text (default) or json

run 'portberth <command> -h' for command-specific flags
`

// versionResult is the JSON payload for `version`; the envelope already
// carries the tool identity, so the payload only restates the version for
// scripts that read `.data` uniformly across commands.
type versionResult struct {
	Version string `json:"version"`
}

// cmdVersion prints the version. Like every other command it accepts the
// global flags, so `portberth version --format json` yields the standard
// envelope.
func cmdVersion(args []string, env *Env) int {
	fs, _, format := newFlagSet("version", env)
	pos, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return usageErr(env, "version: takes no positional arguments")
	}
	if !checkFormat(env, *format) {
		return exitUsage
	}
	if *format == "json" {
		if err := render.JSON(env.Stdout, versionResult{Version: version.Version}); err != nil {
			return runtimeErr(env, err)
		}
		return exitOK
	}
	fmt.Fprintf(env.Stdout, "portberth %s\n", version.Version)
	return exitOK
}

// newFlagSet builds a silent FlagSet whose errors the caller renders, and
// registers the two global flags every command accepts.
func newFlagSet(name string, env *Env) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	registryPath := fs.String("registry", "", "registry file path")
	format := fs.String("format", "text", "output format: text or json")
	return fs, registryPath, format
}

// parseFlags runs fs over args, allowing flags and positional arguments
// to interleave (`portberth claim shop --port 4321` works), and maps flag
// package outcomes onto exit codes: -h prints help and exits 0, anything
// malformed exits 2. Returns the collected positional arguments.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, int, bool) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if err == flag.ErrHelp {
				return nil, exitOK, false
			}
			return nil, exitUsage, false
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, exitOK, true
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// checkFormat validates --format.
func checkFormat(env *Env, format string) bool {
	if format == "text" || format == "json" {
		return true
	}
	fmt.Fprintf(env.Stderr, "portberth: unknown --format %q (want text or json)\n", format)
	return false
}

// resolveRegistryPath applies precedence: --registry flag, then
// $PORTBERTH_REGISTRY, then the OS user config directory.
func resolveRegistryPath(env *Env, flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if p := env.Getenv("PORTBERTH_REGISTRY"); p != "" {
		return p, nil
	}
	dir, err := env.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine config directory: %w (set --registry or PORTBERTH_REGISTRY)", err)
	}
	return filepath.Join(dir, "portberth", "registry.json"), nil
}

// runtimeErr prints a runtime failure and returns exit code 3.
func runtimeErr(env *Env, err error) int {
	fmt.Fprintf(env.Stderr, "portberth: %v\n", err)
	return exitRuntime
}

// usageErr prints a usage failure and returns exit code 2.
func usageErr(env *Env, format string, a ...any) int {
	fmt.Fprintf(env.Stderr, "portberth: "+format+"\n", a...)
	return exitUsage
}

// sinceDate trims an RFC 3339 timestamp to its date for display.
func sinceDate(claimedAt string) string {
	if len(claimedAt) >= 10 {
		return claimedAt[:10]
	}
	return claimedAt
}
