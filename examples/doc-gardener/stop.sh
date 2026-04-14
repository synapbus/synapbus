#!/bin/bash
# stop.sh — shut down the synapbus instance started by ./start.sh.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PID_FILE="$SCRIPT_DIR/.synapbus.pid"

say() { printf '\033[1;36m[stop]\033[0m %s\n' "$*"; }

if [ ! -f "$PID_FILE" ]; then
    say "no pid file — nothing to stop"
    exit 0
fi
PID=$(cat "$PID_FILE")
if ! kill -0 "$PID" 2>/dev/null; then
    say "process $PID already gone"
    rm -f "$PID_FILE"
    exit 0
fi

say "signaling synapbus (pid $PID)"
kill "$PID"
for i in $(seq 1 50); do
    if ! kill -0 "$PID" 2>/dev/null; then break; fi
    sleep 0.1
done
if kill -0 "$PID" 2>/dev/null; then
    say "process did not exit gracefully — sending SIGKILL"
    kill -9 "$PID" 2>/dev/null || true
fi
rm -f "$PID_FILE"
say "stopped"
