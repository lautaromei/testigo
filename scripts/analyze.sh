#!/usr/bin/env bash
# analyze.sh — run checkedcov + edgecov on any Go package.
#
# Usage:
#   scripts/analyze.sh <package> [json]
#
#   <package>  a Go import path (strings, net/url, encoding/json, ...) OR a
#              filesystem directory. Import paths are resolved with `go list`,
#              so stdlib packages work out of the box (read-only GOROOT is fine:
#              the coverage profile is written to a temp dir, not the package).
#   json       optional. If given, emit machine-readable JSON to
#              out/<pkg>.checkedcov.json and out/<pkg>.edgecov.json.
#
# Examples:
#   scripts/analyze.sh strings
#   scripts/analyze.sh net/url json
#   scripts/analyze.sh ./internal/auctions
#
# Env overrides: CHECKEDCOV, EDGECOV (paths to prebuilt binaries; default: built
# under ./bin via `make build`, else `go run`).
set -euo pipefail

PKG="${1:?usage: analyze.sh <import-path-or-dir> [json]}"
MODE="${2:-text}"

cd "$(dirname "$0")/.."

# Resolve the package to a directory.
if [ -d "$PKG" ]; then
  DIR="$(cd "$PKG" && pwd)"
else
  DIR="$(go list -f '{{.Dir}}' "$PKG")"
fi
[ -n "$DIR" ] || { echo "could not resolve package: $PKG" >&2; exit 1; }

# Prefer prebuilt binaries; fall back to `go run`.
CHECKEDCOV="${CHECKEDCOV:-}"
EDGECOV="${EDGECOV:-}"
run_checked() { if [ -n "$CHECKEDCOV" ]; then "$CHECKEDCOV" "$@"; else go run ./cmd/checkedcov "$@"; fi; }
run_edge()    { if [ -n "$EDGECOV" ];    then "$EDGECOV" "$@";    else go run ./cmd/edgecov "$@"; fi; }

if [ "$MODE" = "json" ]; then
  SAFE="$(echo "$PKG" | tr '/.' '__')"
  mkdir -p out
  run_checked --format json "$DIR" > "out/${SAFE}.checkedcov.json"
  run_edge    --format json "$DIR" > "out/${SAFE}.edgecov.json"
  echo "wrote out/${SAFE}.checkedcov.json  out/${SAFE}.edgecov.json"
  exit 0
fi

echo "============================================================"
echo "PACKAGE: $PKG"
echo "DIR:     $DIR"
echo "============================================================"
echo
echo "######## checkedcov — covered-but-unchecked statement lines ########"
run_checked "$DIR"
echo
echo "######## edgecov — edges / branches / effects gaps ########"
run_edge "$DIR"
