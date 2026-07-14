# Contributing to portberth

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else. There are no runtime dependencies.

```bash
git clone https://github.com/JaydenCJ/portberth && cd portberth
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, drives every subcommand against a
temp registry, checks conflict provenance, and verifies live-listener
detection with a loopback-only helper; it must finish by printing
`SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (90 deterministic tests, no network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (assignment and parsing never touch the filesystem — only
   `registry` and `probe` do, and both take injectable roots/paths).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in
  the PR.
- No network calls, ever — the loopback bind probe is the only socket
  operation, and it never sends a packet. No telemetry.
- Determinism first: the same spec must map to the same port everywhere;
  anything that could break existing assignments is a breaking change.
- Well-known ports are data: new entries go into the table in
  `internal/wellknown/wellknown.go` with a short honest description, and
  only if a developer plausibly collides with them on a laptop.
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `portberth version`, the full command you ran,
your registry file (`portberth list --format json` redacts nothing
sensitive — it is just names and ports), and for detection bugs the
relevant `/proc/net/tcp` line, since that is exactly what the scanner
sees.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
