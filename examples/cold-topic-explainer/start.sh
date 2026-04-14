#!/bin/bash
# start.sh — launch an isolated synapbus instance and configure the
# cold-topic-explainer 3-agent chain end-to-end.
#
# Idempotent where possible: wipes ./data, rebuilds the binary,
# creates a fresh user + agents + channel + harness configs.
#
# Exit codes:
#   0   everything came up
#   1   synapbus failed to start
#   2   admin socket never appeared
#   3   CLI preflight failed

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

PORT="${SYNAPBUS_PORT:-18088}"
DATA_DIR="$SCRIPT_DIR/data"
BIN_DIR="$SCRIPT_DIR/bin"
BIN="$BIN_DIR/synapbus"
SOCKET="$DATA_DIR/synapbus.sock"
PID_FILE="$SCRIPT_DIR/.synapbus.pid"
LOG_FILE="$SCRIPT_DIR/synapbus.log"

cd "$SCRIPT_DIR"

say() { printf '\033[1;36m[start]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[start][FAIL]\033[0m %s\n' "$*" >&2; exit "${2:-1}"; }

# --- preflight ---------------------------------------------------------
for cmd in go gemini jq sqlite3 curl; do
    command -v "$cmd" >/dev/null || die "missing required CLI: $cmd" 3
done

# Refuse to run on top of an existing pid that's still alive.
if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    die "synapbus already running (pid $(cat "$PID_FILE")); run ./stop.sh first"
fi

# --- build -------------------------------------------------------------
# Rebuild the embedded Svelte SPA when web sources are newer than the
# baked dist. Without this, a stale internal/web/dist gets compiled
# into the binary and the Web UI loads a blank page.
if [ -d "$REPO_ROOT/web/node_modules" ]; then
    need_web_build=0
    if [ ! -d "$REPO_ROOT/internal/web/dist" ]; then
        need_web_build=1
    else
        # Any .svelte/.ts source newer than the embedded index.html?
        newest_src=$(find "$REPO_ROOT/web/src" -type f \( -name '*.svelte' -o -name '*.ts' -o -name '*.css' \) -print0 2>/dev/null | xargs -0 ls -t 2>/dev/null | head -1)
        embedded_index="$REPO_ROOT/internal/web/dist/index.html"
        if [ -n "$newest_src" ] && [ "$newest_src" -nt "$embedded_index" ]; then
            need_web_build=1
        fi
    fi
    if [ "$need_web_build" = 1 ]; then
        say "rebuilding Svelte SPA (sources newer than embedded dist)"
        (cd "$REPO_ROOT/web" && ./node_modules/.bin/vite build)
        rm -rf "$REPO_ROOT/internal/web/dist"
        cp -r "$REPO_ROOT/web/build" "$REPO_ROOT/internal/web/dist"
    fi
else
    say "note: web/node_modules missing — using whatever internal/web/dist is embedded"
    say "      (run 'make web' once from the repo root to bootstrap)"
fi

say "building synapbus binary..."
mkdir -p "$BIN_DIR"
(cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/synapbus)

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

# Wait for the admin socket to appear.
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

# Wait for HTTP to be ready too.
for i in $(seq 1 100); do
    if curl -fsS "http://localhost:$PORT/health" >/dev/null 2>&1; then break; fi
    sleep 0.1
done

say "synapbus is up"

# --- shorthand for admin calls -----------------------------------------
admin() { "$BIN" --socket "$SOCKET" "$@"; }

# --- user + human agent ------------------------------------------------
say "creating user algis / algis-demo-pw"
admin user create --username algis --password 'algis-demo-pw' --display-name Algis >/dev/null

say "creating type=human agent for algis"
admin agent create --name algis --display-name "Algis (human)" --type human --owner 1 >/dev/null

# --- three AI agents ---------------------------------------------------
for name in decomposer-pro writer-flash critic-lite; do
    say "creating agent $name"
    admin agent create --name "$name" --display-name "$name" --type ai --owner 1 >/dev/null
done

# --- reactive config ---------------------------------------------------
# No CLI command for trigger_mode yet; use sqlite3 directly. This also
# lets us set harness_name / local_command / harness_config_json for all
# three agents in one batch.
say "configuring reactive trigger mode via sqlite"
sqlite3 "$DATA_DIR/synapbus.db" <<SQL
UPDATE agents SET
    trigger_mode = 'reactive',
    cooldown_seconds = 0,
    daily_trigger_budget = 30,
    max_trigger_depth = 8
WHERE name IN ('decomposer-pro','writer-flash','critic-lite');
SQL

# --- per-agent harness config -----------------------------------------
# Each agent's harness_config_json carries GEMINI.md, an empty
# mcp_servers block (explicitly clearing any home-level config so the
# gemini CLI doesn't warn), and the role env map the wrapper reads.
apply_config() {
    local agent="$1"
    local config_path="$2"
    # Template replacement: the configs reference the literal strings
    # __SOCKET__, __BIN__, and __SYNAPBUS_URL__ so the same files work
    # regardless of where the user clones the repo.
    local tmp
    tmp=$(mktemp)
    sed \
        -e "s|__SOCKET__|${SOCKET//|/\\|}|g" \
        -e "s|__BIN__|${BIN//|/\\|}|g" \
        -e "s|__SYNAPBUS_URL__|http://localhost:$PORT|g" \
        "$config_path" > "$tmp"
    admin harness config set \
        --agent "$agent" \
        --harness-name subprocess \
        --local-command "[\"$SCRIPT_DIR/wrapper.sh\"]" \
        --file "$tmp" >/dev/null
    rm -f "$tmp"
}

say "applying harness configs"
apply_config decomposer-pro "$SCRIPT_DIR/configs/decomposer-pro.json"
apply_config writer-flash  "$SCRIPT_DIR/configs/writer-flash.json"
apply_config critic-lite    "$SCRIPT_DIR/configs/critic-lite.json"

say "ready"
echo
echo "  Web UI:   http://localhost:$PORT   (login: algis / algis-demo-pw)"
echo "  Log:      tail -f $LOG_FILE"
echo "  Messages: $BIN --socket $SOCKET messages list --limit 20"
echo
echo "Next: ./run_task.sh \"your topic here\""
