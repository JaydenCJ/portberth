# portberth examples

Small, runnable demonstrations. Both scripts assume `portberth` is built
(`go build -o portberth ./cmd/portberth` in the repo root) and on your
`PATH`, or pass the binary path as the first argument.

| File | What it shows |
|---|---|
| `dev-server.sh` | The everyday loop: claim a stable port for a project, start a dev server on it, let a second service claim its own port without stepping on the first. |
| `envrc.example` | A [direnv](https://direnv.net/)-style `.envrc` snippet that loads every port of a project into the environment on `cd`, so `$SHOP_WEB_PORT` is always set and always the same. |

Both examples use a throwaway registry under `mktemp -d` via
`PORTBERTH_REGISTRY`, so they never touch your real reservations.
