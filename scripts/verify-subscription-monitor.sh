#!/bin/sh
set -eu

SUBMON_VERIFY_ROOT=${SUBMON_VERIFY_ROOT:-/Volumes/Arquivos/cliproxyapi-tests/v1-owner}
SUBMON_BUILD_ROOT=${SUBMON_BUILD_ROOT:-/Volumes/Arquivos/cliproxyapi-build}
export PATH="$SUBMON_BUILD_ROOT/go1.27.3/go/bin:$PATH"
export GOTOOLCHAIN=local
export GOCACHE="$SUBMON_BUILD_ROOT/gocache"
export GOPATH="$SUBMON_BUILD_ROOT/gopath"
export GOMODCACHE="$SUBMON_BUILD_ROOT/gomodcache"
export TMPDIR="$SUBMON_VERIFY_ROOT/tmp"
mkdir -p "$TMPDIR"
SUBMON_RUN_DIR=$(mktemp -d "$SUBMON_VERIFY_ROOT/verify-XXXXXXXX")
printf 'Evidence directory %s\n' "$SUBMON_RUN_DIR"

case "${1:-}" in
  --serve-fixtures)
    export SUBMON_FIXTURE_ROOT="$SUBMON_RUN_DIR/fixture"
    go test -v -count=1 -run '^TestUIFixtureServer$' -timeout 0 ./internal/submon
    ;;
  --live)
    export SUBMON_LIVE_DATA_DIR="$SUBMON_VERIFY_ROOT/live"
    go test -v -count=1 -run '^TestNativeLive$' -timeout 0 ./internal/submon > "$SUBMON_RUN_DIR/live.log" 2>&1
    cat "$SUBMON_RUN_DIR/live.log"
    ;;
  '')
    go test -count=1 ./internal/submon ./cmd/subscription-monitor > "$SUBMON_RUN_DIR/focused.log" 2>&1
    go test -race -count=1 ./internal/submon > "$SUBMON_RUN_DIR/race.log" 2>&1
    go build -o "$SUBMON_RUN_DIR/subscription-monitor" ./cmd/subscription-monitor
    go build -o "$SUBMON_RUN_DIR/server" ./cmd/server
    printf 'Focused tests, acceptance checks, race checks and both builds passed.\n'
    ;;
  *) printf 'Usage: %s [--serve-fixtures|--live]\n' "$0" >&2; exit 2 ;;
esac
