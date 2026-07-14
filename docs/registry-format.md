# Registry file format

portberth stores reservations in one JSON file. By default it lives at
`<user-config>/portberth/registry.json` (`~/.config/portberth/registry.json`
on Linux, `~/Library/Application Support/portberth/registry.json` on
macOS); `--registry PATH` or `PORTBERTH_REGISTRY` override it.

The file is meant to be read by humans, checked into dotfiles, and — if
you must — edited by hand. `portberth doctor` exists precisely because
hand edits happen.

## Shape

```json
{
  "schema_version": 1,
  "entries": [
    {
      "project": "shop",
      "service": "web",
      "port": 3708,
      "claimed_at": "2026-07-13T09:00:00Z",
      "note": "storefront dev server"
    }
  ]
}
```

| Field | Type | Rules |
|---|---|---|
| `schema_version` | int | `1`. Files with a newer version are refused with an upgrade hint, never guessed at. |
| `entries[].project` | string | lowercase; letters, digits, `.`, `_`, `-`; starts with a letter or digit; ≤64 chars |
| `entries[].service` | string | same rules; `"default"` is what a bare `claim myapp` writes |
| `entries[].port` | int | 1–65535; unique across the file (doctor flags duplicates) |
| `entries[].claimed_at` | string | RFC 3339 UTC, written at claim time |
| `entries[].note` | string | optional free text, shown by `list` and `explain` |

## Guarantees

- **Atomic writes.** Saves go to a temp file in the same directory and
  are renamed over the target; a crash mid-save can never leave a
  half-written registry.
- **Stable ordering.** Entries are sorted by project, then service, so
  the file diffs cleanly under version control.
- **Owner-only.** The file is written with mode `0600`.
- **No silent repair.** A corrupt file is reported with its path and the
  JSON error; portberth never discards data it cannot parse.

## Sharing a registry across a team

The registry is a plain file, so a team that wants org-wide stable ports
can commit one to a dotfiles repo and point `PORTBERTH_REGISTRY` at the
checkout. Because assignment is a pure function of project/service name
and range, two machines that claim the same names independently converge
on the same ports even without sharing the file — sharing only matters
when explicit `--port` pins or notes should travel too.
