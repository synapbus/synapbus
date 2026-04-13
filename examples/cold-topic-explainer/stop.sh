#!/bin/bash
# stop.sh — stop the synapbus instance started by ./start.sh.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PID_FILE="$SCRIPT_DIR/.synapbus.pid"

if [ ! -f "$PID_FILE" ]; then
    echo "no pid file — nothing to stop"
    exit 0
fi

PID=$(cat "$PID_FILE")
if ! kill -0 "$PID" 2>/dev/null; then
    echo "pid $PID not alive — cleaning up pid file"
    rm -f "$PID_FILE"
    exit 0
fi

echo "stopping synapbus pid $PID"
kill "$PID" 2>/dev/null || true

# Wait up to 5s for graceful shutdown.
for i in $(seq 1 50); do
    if ! kill -0 "$PID" 2>/dev/null; then break; fi
    sleep 0.1
done

if kill -0 "$PID" 2>/dev/null; then
    echo "synapbus didn't exit in 5s; sending SIGKILL"
    kill -9 "$PID" 2>/dev/null || true
fi

rm -f "$PID_FILE"
echo "stopped"
