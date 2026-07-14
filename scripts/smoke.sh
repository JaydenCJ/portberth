#!/usr/bin/env bash
# End-to-end smoke test for portberth: builds the binary, drives every
# subcommand against a temp registry, verifies conflict provenance and
# stability, and checks live-listener detection with a local loopback
# listener. No network beyond 127.0.0.1, idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
LISTENER_PID=""
cleanup() {
  [ -n "$LISTENER_PID" ] && kill "$LISTENER_PID" 2>/dev/null || true
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/portberth"
export PORTBERTH_REGISTRY="$WORKDIR/registry.json"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/portberth) || fail "go build failed"

echo "2. version matches manifest"
[ "$("$BIN" version)" = "portberth 0.1.0" ] || fail "version mismatch"

echo "3. claim is deterministic and idempotent"
FIRST="$("$BIN" claim shop/web --note "storefront dev server")"
echo "$FIRST" | grep -q "reserved shop/web -> " || fail "claim output wrong: $FIRST"
PORT="${FIRST##* }"
SECOND="$("$BIN" claim shop/web)"
echo "$SECOND" | grep -q "shop/web -> $PORT (existing reservation" \
  || fail "second claim should be idempotent at $PORT: $SECOND"

echo "4. get prints the bare port for scripts"
[ "$("$BIN" get shop/web)" = "$PORT" ] || fail "get returned wrong port"

echo "5. list renders the sorted table"
"$BIN" claim shop/api >/dev/null
"$BIN" claim blog >/dev/null
LIST="$("$BIN" list)"
echo "$LIST" | grep -q "^PROJECT" || fail "list header missing"
echo "$LIST" | grep -q "storefront dev server" || fail "note missing from list"
[ "$(echo "$LIST" | sed -n '2p' | awk '{print $1}')" = "blog" ] || fail "list not sorted"

echo "6. env emits shell-ready variables"
ENVOUT="$("$BIN" env shop --export)"
echo "$ENVOUT" | grep -q "^export SHOP_WEB_PORT=$PORT$" || fail "env output wrong: $ENVOUT"

echo "7. explicit-port conflict is refused with provenance"
set +e
ERR="$("$BIN" claim otherapp --port "$PORT" 2>&1 >/dev/null)"
CODE=$?
set -e
[ $CODE -eq 1 ] || fail "conflicting claim should exit 1, got $CODE"
echo "$ERR" | grep -q "reserved by shop/web since" || fail "provenance missing: $ERR"

echo "8. explain reports reservation and well-known knowledge"
set +e
OUT="$("$BIN" explain "$PORT")"
[ $? -eq 1 ] || fail "explain of a reserved port should exit 1"
set -e
echo "$OUT" | grep -q "reserved by shop/web since" || fail "explain registry line missing"
echo "$OUT" | grep -q "verdict: reserved, not in use" || fail "explain verdict wrong"
set +e
WK="$("$BIN" explain 5432)"
set -e
echo "$WK" | grep -q "postgresql" || fail "well-known knowledge missing: $WK"

echo "9. live listener detection on loopback"
mkdir -p "$WORKDIR/listener"
cat > "$WORKDIR/listener/main.go" <<'EOF'
// Throwaway loopback listener used only by smoke.sh.
package main

import (
	"fmt"
	"net"
	"os"
)

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:"+os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("ready") // signal the harness via the fifo
	select {}            // hold the port until killed
	_ = ln
}
EOF
(cd "$WORKDIR/listener" && go mod init smoke-listener >/dev/null 2>&1 && go build -o listener .) \
  || fail "listener build failed"
mkfifo "$WORKDIR/ready"
"$WORKDIR/listener/listener" "$PORT" > "$WORKDIR/ready" &
LISTENER_PID=$!
read -r -t 10 READY < "$WORKDIR/ready" || fail "listener never became ready"
[ "$READY" = "ready" ] || fail "unexpected listener handshake: $READY"
set +e
OUT="$("$BIN" explain "$PORT")"
set -e
echo "$OUT" | grep -Eq "LISTENING on 127.0.0.1|in use on loopback" \
  || fail "live listener not detected: $OUT"
echo "$OUT" | grep -q "verdict: reserved and in use" || fail "combined verdict wrong"
"$BIN" doctor > "$WORKDIR/doctor.txt" || fail "doctor should pass (warnings only)"
grep -q "port $PORT is in use" "$WORKDIR/doctor.txt" || fail "doctor missed the squatter"
kill "$LISTENER_PID" && wait "$LISTENER_PID" 2>/dev/null || true
LISTENER_PID=""

echo "10. doctor flags hand-edit damage and --strict escalates"
"$BIN" doctor > "$WORKDIR/doctor.txt" || fail "clean registry should pass doctor"
grep -q "doctor: OK" "$WORKDIR/doctor.txt" || fail "doctor OK line missing"
"$BIN" claim pgadmin --port 5432 >/dev/null 2>&1 || fail "well-known explicit claim failed"
"$BIN" doctor > "$WORKDIR/doctor.txt" || fail "doctor should still pass with warnings"
grep -q "reserves well-known port 5432" "$WORKDIR/doctor.txt" || fail "well-known warning missing"
set +e
"$BIN" doctor --strict >/dev/null
[ $? -eq 1 ] || fail "--strict should fail on warnings"
set -e
cp "$PORTBERTH_REGISTRY" "$WORKDIR/backup.json"
sed "s/\"port\": $PORT/\"port\": 5432/" "$WORKDIR/backup.json" > "$PORTBERTH_REGISTRY"
set +e
OUT="$("$BIN" doctor)"
[ $? -eq 1 ] || fail "duplicate ports should fail doctor"
set -e
echo "$OUT" | grep -q "duplicate port 5432" || fail "duplicate not named: $OUT"
cp "$WORKDIR/backup.json" "$PORTBERTH_REGISTRY"

echo "11. JSON envelope is machine-readable"
JSON="$("$BIN" list --format json)"
echo "$JSON" | grep -q '"tool": "portberth"' || fail "json envelope missing"
set +e
JSON="$("$BIN" explain 5432 --format json)"
[ $? -eq 1 ] || fail "explain of the pgadmin reservation should exit 1"
set -e
echo "$JSON" | grep -q '"verdict": "reserved, not in use"' || fail "explain json wrong: $JSON"

echo "12. release drops reservations"
REL="$("$BIN" release shop/api)"
echo "$REL" | grep -q "released shop/api" || fail "release failed: $REL"
"$BIN" release shop --all >/dev/null || fail "release --all failed"
set +e
"$BIN" get shop/web >/dev/null 2>&1
[ $? -eq 1 ] || fail "released reservation still resolvable"
set -e

echo "13. usage errors exit 2"
set +e
"$BIN" claim >/dev/null 2>&1
[ $? -eq 2 ] || fail "missing spec should exit 2"
"$BIN" list --format yaml >/dev/null 2>&1
[ $? -eq 2 ] || fail "bad --format should exit 2"
set -e

echo "SMOKE OK"
