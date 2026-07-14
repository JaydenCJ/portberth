#!/usr/bin/env bash
# The everyday portberth loop: give each service of a project a stable
# port, and never guess "is 3000 free?" again.
#
# Usage: bash examples/dev-server.sh [path-to-portberth-binary]
set -euo pipefail

BIN="${1:-portberth}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

# A throwaway registry so this demo never touches your real one.
export PORTBERTH_REGISTRY="$WORKDIR/registry.json"

echo "# claim stable ports for two services of one project"
"$BIN" claim shop/web --note "storefront dev server"
"$BIN" claim shop/api

echo
echo "# the same claims are idempotent — run them in every start script"
"$BIN" claim shop/web

echo
echo "# wire the port into whatever you start (bare output = script-friendly)"
WEB_PORT="$("$BIN" get shop/web)"
echo "would run: npx serve -l $WEB_PORT   # or: python3 -m http.server $WEB_PORT"

echo
echo "# or export everything at once"
eval "$("$BIN" env shop --export)"
echo "SHOP_WEB_PORT=$SHOP_WEB_PORT SHOP_API_PORT=$SHOP_API_PORT"

echo
echo "# when a port is refused, portberth tells you exactly why"
"$BIN" claim blog --port "$WEB_PORT" || true

echo
echo "# audit everything against reality"
"$BIN" doctor
