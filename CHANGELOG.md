# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- `claim` subcommand assigning a stable port per project/service: an
  FNV-1a hash of the spec picks a deterministic starting point in the
  range, with forward probing past collisions — the same project gets the
  same port on any machine, and unrelated reservations never move it.
- Reservation registry as a human-readable JSON file (schema v1) with
  atomic temp-file-and-rename saves, 0600 permissions, tolerant loading
  of a missing file, and hard refusal of corrupt or newer-schema files.
- Conflict provenance: explicitly requested ports that are already held
  are refused with the owning reservation, its claim date, and any
  well-known-port identity quoted — never silently reassigned.
- `explain` subcommand reporting every signal for one port — registry
  reservation, curated well-known table (40 developer-relevant ports),
  live listeners — with a one-line verdict and scripting-friendly exit
  codes (0 free, 1 held).
- Live listener detection via /proc/net/tcp + tcp6 parsing with
  inode-to-PID/process attribution on Linux, degrading to a loopback
  bind probe elsewhere; nothing ever leaves 127.0.0.1.
- `doctor` subcommand auditing the registry: hand-edit damage (duplicate
  ports/specs, invalid entries) as errors, well-known collisions and
  live squatters as warnings, with `--strict` escalation.
- `get`, `list`, `release` (incl. `--all`), and `env` (shell-ready
  `MYAPP_PORT=…` lines, `--export`) subcommands; text tables and a
  stable JSON envelope (`schema_version: 1`) everywhere.
- Configuration via `--registry` / `PORTBERTH_REGISTRY` and `--range` /
  `PORTBERTH_RANGE`; `--probe` to require live-freeness on claim;
  `--allow-well-known` escape hatch.
- Runnable examples (`examples/dev-server.sh`, `examples/envrc.example`)
  and a registry format reference (`docs/registry-format.md`).
- 90 deterministic offline tests (unit + in-process CLI integration
  against fabricated procfs trees and temp registries) and
  `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/portberth/releases/tag/v0.1.0
