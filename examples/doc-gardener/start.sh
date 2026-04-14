#!/bin/bash
# start.sh — launch an isolated synapbus instance and build the
# docgardener demo driver. Mirrors cold-topic-explainer layout.
#
# Exit codes:
#   0  everything came up
#   1  synapbus failed to start
#   2  admin socket never appeared
#   3  preflight failed

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

PORT="${SYNAPBUS_PORT:-18089}"
DATA_DIR="$SCRIPT_DIR/data"
BIN_DIR="$SCRIPT_DIR/bin"
BIN="$BIN_DIR/synapbus"
DOCGARDENER="$BIN_DIR/docgardener"
SOCKET="$DATA_DIR/synapbus.sock"
PID_FILE="$SCRIPT_DIR/.synapbus.pid"
LOG_FILE="$SCRIPT_DIR/synapbus.log"

cd "$SCRIPT_DIR"

say() { printf '\033[1;36m[start]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[start][FAIL]\033[0m %s\n' "$*" >&2; exit "${2:-1}"; }

# --- preflight ---------------------------------------------------------
for cmd in go sqlite3 curl; do
    command -v "$cmd" >/dev/null || die "missing required CLI: $cmd" 3
done

if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    die "synapbus already running (pid $(cat "$PID_FILE")); run ./stop.sh first"
fi

# --- build -------------------------------------------------------------
say "building synapbus + docgardener binaries..."
mkdir -p "$BIN_DIR"
(cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o "$BIN" ./cmd/synapbus)
(cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o "$DOCGARDENER" ./cmd/docgardener)

# --- fresh data dir ----------------------------------------------------
say "wiping data dir $DATA_DIR"
rm -rf "$DATA_DIR"
mkdir -p "$DATA_DIR"

# --- launch synapbus ---------------------------------------------------
say "starting synapbus on port $PORT"
nohup "$BIN" serve \
    --port "$PORT" \
    --data "$DATA_DIR" \
    > "$LOG_FILE" 2>&1 &
echo $! > "$PID_FILE"
say "pid $(cat "$PID_FILE") → $LOG_FILE"

# Wait for the admin socket + HTTP to appear.
for i in $(seq 1 100); do
    if [ -S "$SOCKET" ]; then break; fi
    if ! kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
        die "synapbus crashed during boot — see $LOG_FILE" 1
    fi
    sleep 0.1
done
if [ ! -S "$SOCKET" ]; then
    die "admin socket $SOCKET never appeared after 10s" 2
fi
for i in $(seq 1 100); do
    if curl -fsS "http://localhost:$PORT/health" >/dev/null 2>&1; then break; fi
    sleep 0.1
done

say "synapbus is up"
echo
echo "  Web UI:        http://localhost:$PORT   (login: algis / algis-demo-pw)"
echo "  Log:           tail -f $LOG_FILE"
echo "  Admin socket:  $SOCKET"
echo
echo "Next: ./run_task.sh"
