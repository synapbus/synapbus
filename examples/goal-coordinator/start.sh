#!/bin/bash
# start.sh — universal goal-coordinator example.
#
# Provisions 3 agents:
#   goal-coordinator    — triage + delegation, high-reasoning model
#   generic-inspector   — worker that does scan/verify/report in one pass
#   critic-auditor      — independent reviewer with its own config_hash
#
# Harness-agnostic: all three agents go through the subprocess harness
# calling examples/goal-coordinator/wrapper.sh, which today invokes
# `gemini` but can be swapped to any CLI (claude, codex, etc.) by
# changing the call block in wrapper.sh — nothing in SynapBus itself
# is tied to a specific LLM.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

PORT="${SYNAPBUS_PORT:-18090}"
DATA_DIR="$SCRIPT_DIR/data"
BIN_DIR="$SCRIPT_DIR/bin"
BIN="$BIN_DIR/synapbus"
SOCKET="$DATA_DIR/synapbus.sock"
PID_FILE="$SCRIPT_DIR/.synapbus.pid"
LOG_FILE="$SCRIPT_DIR/synapbus.log"

# Two-tier model hierarchy: coordinator gets the smart model, workers
# get the fast model. Override either via env.
COORDINATOR_MODEL="${SYNAPBUS_COORDINATOR_MODEL:-gemini-3.1-pro-preview}"
WORKER_MODEL="${SYNAPBUS_WORKER_MODEL:-gemini-2.5-flash}"

cd "$SCRIPT_DIR"

say() { printf '\033[1;36m[start]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[start][FAIL]\033[0m %s\n' "$*" >&2; exit "${2:-1}"; }

# --- preflight ---------------------------------------------------------
for cmd in go gemini jq sqlite3 curl; do
    command -v "$cmd" >/dev/null || die "missing required CLI: $cmd" 3
done

if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    die "synapbus already running (pid $(cat "$PID_FILE")); run ./stop.sh first"
fi

# --- build web + binary -----------------------------------------------
DIST_DIR="$REPO_ROOT/internal/web/dist"
WEB_SRC="$REPO_ROOT/web/build"
if [ ! -d "$DIST_DIR/_app" ]; then
    say "embedded web dist missing — building SPA"
    if [ -d "$REPO_ROOT/web/node_modules" ]; then
        (cd "$REPO_ROOT/web" && npm run build >/dev/null 2>&1) || true
    fi
    if [ -d "$WEB_SRC/_app" ]; then
        rm -rf "$DIST_DIR"; mkdir -p "$DIST_DIR"
        cp -r "$WEB_SRC/"* "$DIST_DIR/"
    fi
fi

say "building synapbus binary"
mkdir -p "$BIN_DIR"
(cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o "$BIN" ./cmd/synapbus)

# --- fresh data dir ----------------------------------------------------
say "wiping $DATA_DIR"
rm -rf "$DATA_DIR"
mkdir -p "$DATA_DIR"

# --- launch synapbus ---------------------------------------------------
say "starting synapbus on port $PORT"
export SYNAPBUS_DISABLE_EXPIRY_WORKER=1
export SYNAPBUS_DISABLE_RETENTION_WORKER=1
export SYNAPBUS_DISABLE_STALEMATE_WORKER=1
# Keep per-run workdirs so you can inspect GEMINI.md, .gemini/settings.json,
# MCP traces, and gemini stdout/stderr under data/harness/subprocess/.
export SYNAPBUS_KEEP_WORKDIR=1
nohup "$BIN" serve --port "$PORT" --data "$DATA_DIR" \
    > "$LOG_FILE" 2>&1 &
echo $! > "$PID_FILE"
say "pid $(cat "$PID_FILE") → $LOG_FILE"

for i in $(seq 1 100); do
    [ -S "$SOCKET" ] && break
    if ! kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
        die "synapbus crashed — see $LOG_FILE" 1
    fi
    sleep 0.1
done
[ -S "$SOCKET" ] || die "admin socket $SOCKET never appeared" 2
for i in $(seq 1 100); do
    curl -fsS "http://localhost:$PORT/health" >/dev/null 2>&1 && break
    sleep 0.1
done
say "synapbus is up"

# --- provision user + agents ------------------------------------------
admin() { "$BIN" --socket "$SOCKET" "$@"; }

say "creating user algis / algis-demo-pw"
admin user create --username algis --password 'algis-demo-pw' --display-name Algis >/dev/null 2>&1 || true

OWNER_ID=$(sqlite3 "$DATA_DIR/synapbus.db" "SELECT id FROM users WHERE username='algis'")
if [ -z "$OWNER_ID" ] || [ "$OWNER_ID" = "1" ]; then
    die "failed to resolve algis user id" 3
fi

admin agent create --name algis --display-name "Algis (human)" --type human --owner "$OWNER_ID" >/dev/null 2>&1 || true

for name in goal-coordinator generic-inspector critic-auditor; do
    say "creating agent $name"
    admin agent create --name "$name" --display-name "$name" --type ai --owner "$OWNER_ID" >/dev/null 2>&1 || true
done

say "configuring reactive trigger mode"
sqlite3 "$DATA_DIR/synapbus.db" <<SQL
UPDATE agents SET
    trigger_mode = 'reactive',
    cooldown_seconds = 0,
    daily_trigger_budget = 50,
    max_trigger_depth = 8
WHERE name IN ('goal-coordinator','generic-inspector','critic-auditor');
SQL

# --- mint fresh API key for coordinator so Gemini can call MCP -------
# The coordinator reaches SynapBus's MCP endpoint via the agent's own
# API key (Bearer auth). revoke-key always returns a fresh token; we
# parse the JSON and substitute it into configs/coordinator.json at
# apply_config time.
say "minting API key for goal-coordinator (MCP auth)"
COORDINATOR_APIKEY=$(admin agent revoke-key --name goal-coordinator | jq -r '.new_api_key')
if [ -z "$COORDINATOR_APIKEY" ] || [ "$COORDINATOR_APIKEY" = "null" ]; then
    die "failed to mint API key for goal-coordinator" 4
fi

# --- apply per-agent harness config -----------------------------------
apply_config() {
    local agent="$1"
    local config_path="$2"
    local tmp
    tmp=$(mktemp)
    sed \
        -e "s|__SOCKET__|${SOCKET//|/\\|}|g" \
        -e "s|__BIN__|${BIN//|/\\|}|g" \
        -e "s|__PORT__|${PORT}|g" \
        -e "s|__COORDINATOR_APIKEY__|${COORDINATOR_APIKEY}|g" \
        -e "s|__COORDINATOR_MODEL__|${COORDINATOR_MODEL}|g" \
        -e "s|__WORKER_MODEL__|${WORKER_MODEL}|g" \
        "$config_path" > "$tmp"
    admin harness config set \
        --agent "$agent" \
        --harness-name subprocess \
        --local-command "[\"$SCRIPT_DIR/wrapper.sh\"]" \
        --file "$tmp" >/dev/null
    rm -f "$tmp"
}

say "applying harness configs (coordinator=$COORDINATOR_MODEL workers=$WORKER_MODEL)"
apply_config goal-coordinator  "$SCRIPT_DIR/configs/coordinator.json"
apply_config generic-inspector "$SCRIPT_DIR/configs/inspector.json"
apply_config critic-auditor    "$SCRIPT_DIR/configs/critic.json"

echo
echo "  Web UI:    http://localhost:$PORT   (login: algis / algis-demo-pw)"
echo "  Log:       tail -f $LOG_FILE"
echo "  Agents:    http://localhost:$PORT/agents"
echo "  Runs:      http://localhost:$PORT/runs"
echo
echo "Next: ./run_task.sh \"<your goal brief here>\""
echo "Try:  ./run_task.sh \"what is 2+2?\"           (should triage TRIVIAL)"
echo "      ./run_task.sh \"check mcpproxy CLI drift\" (should triage SINGLE-STEP)"
