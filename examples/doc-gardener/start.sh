#!/bin/bash
# start.sh — doc-gardener example, MCP-native + docker-isolated.
#
# Provisions 3 agents that all run inside the synapbus-agent container
# image (built locally on first run):
#
#   doc-coordinator    — triage + delegation, smart model
#   docs-inspector     — fetches docs, runs CLI commands inside the
#                        sandbox, reports findings
#   docs-critic        — independent reviewer with its own MCP API key
#
# Every agent talks to the SynapBus MCP server (host) from inside its
# container via host.docker.internal:<port>. The harness rewrites
# .gemini/settings.json URLs automatically.
#
# Exit codes:
#   0  everything came up
#   1  synapbus failed to start
#   2  admin socket never appeared
#   3  preflight failed (missing CLI, GEMINI_API_KEY, etc.)
#   4  failed to mint API key

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

PORT="${SYNAPBUS_PORT:-18089}"
DATA_DIR="$SCRIPT_DIR/data"
BIN_DIR="$SCRIPT_DIR/bin"
BIN="$BIN_DIR/synapbus"
SOCKET="$DATA_DIR/synapbus.sock"
PID_FILE="$SCRIPT_DIR/.synapbus.pid"
LOG_FILE="$SCRIPT_DIR/synapbus.log"

# Two-tier model hierarchy: smart for triage, fast for workers.
COORDINATOR_MODEL="${SYNAPBUS_COORDINATOR_MODEL:-gemini-3.1-pro-preview}"
WORKER_MODEL="${SYNAPBUS_WORKER_MODEL:-gemini-2.5-flash}"

# Container image agents run inside.
AGENT_IMAGE="${SYNAPBUS_AGENT_IMAGE:-synapbus-agent:latest}"

cd "$SCRIPT_DIR"

say() { printf '\033[1;36m[start]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[start][FAIL]\033[0m %s\n' "$*" >&2; exit "${2:-1}"; }

# --- preflight ---------------------------------------------------------
for cmd in go jq sqlite3 curl docker; do
    command -v "$cmd" >/dev/null || die "missing required CLI: $cmd" 3
done

if ! docker version --format '{{.Server.Version}}' >/dev/null 2>&1; then
    die "docker daemon unreachable — start Docker Desktop / dockerd first" 3
fi

# Auth: prefer GEMINI_API_KEY (passed as -e to each container). When
# absent the docker harness auto-mounts ~/.gemini/ read-only at
# /home/agent/.gemini and sets GEMINI_DEFAULT_AUTH_TYPE=oauth-personal,
# so the in-container Gemini CLI reuses the host's OAuth session.
GEMINI_API_KEY="${GEMINI_API_KEY:-}"
if [ -z "$GEMINI_API_KEY" ]; then
    if [ -f "$HOME/.gemini/oauth_creds.json" ]; then
        say "no GEMINI_API_KEY — harness will auto-mount host OAuth creds (MountHostCredentials)"
    else
        die "no Gemini auth available.
       Either:
         export GEMINI_API_KEY=...   (get one at https://aistudio.google.com/apikey)
       OR run \`gemini\` once on the host to set up OAuth, then re-run ./start.sh." 3
    fi
fi

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

# --- ensure the agent image is built ----------------------------------
if ! docker image inspect "$AGENT_IMAGE" >/dev/null 2>&1; then
    say "building $AGENT_IMAGE (first run, ~2-5 minutes)..."
    (cd "$REPO_ROOT" && docker build -t "$AGENT_IMAGE" image-build/synapbus-agent) \
        || die "failed to build $AGENT_IMAGE — see docker output above" 1
fi
say "agent image: $AGENT_IMAGE"

# --- fresh data dir ----------------------------------------------------
say "wiping $DATA_DIR"
rm -rf "$DATA_DIR"
mkdir -p "$DATA_DIR"

# --- launch synapbus ---------------------------------------------------
say "starting synapbus on port $PORT"
export SYNAPBUS_DISABLE_EXPIRY_WORKER=1
export SYNAPBUS_DISABLE_RETENTION_WORKER=1
export SYNAPBUS_DISABLE_STALEMATE_WORKER=1
# Keep per-run docker workdirs around so you can inspect what each
# container saw (GEMINI.md, .gemini/settings.json, gemini.stdout.log,
# message.json) under data/harness/docker/.
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

for name in doc-coordinator docs-inspector docs-critic; do
    say "creating agent $name"
    admin agent create --name "$name" --display-name "$name" --type ai --owner "$OWNER_ID" >/dev/null 2>&1 || true
done

say "configuring reactive trigger mode"
# max_trigger_depth = 4 caps the conversation loop at about two
# inspector↔critic round-trips (each REVISE costs two hops). Prevents
# the agents from spinning forever on a badly-formed report when the
# critic doesn't converge; see configs/critic.json for the structural
# half of the cap.
sqlite3 "$DATA_DIR/synapbus.db" <<SQL
UPDATE agents SET
    trigger_mode = 'reactive',
    cooldown_seconds = 0,
    daily_trigger_budget = 50,
    max_trigger_depth = 4
WHERE name IN ('doc-coordinator','docs-inspector','docs-critic');
SQL

# --- mint fresh API keys for each agent (MCP auth from inside container)
say "minting API keys for each agent (one per role)"
mint_key() {
    local name="$1"
    local key
    key=$(admin agent revoke-key --name "$name" | jq -r '.new_api_key')
    if [ -z "$key" ] || [ "$key" = "null" ]; then
        die "failed to mint API key for $name" 4
    fi
    printf '%s' "$key"
}
COORDINATOR_APIKEY=$(mint_key doc-coordinator)
INSPECTOR_APIKEY=$(mint_key docs-inspector)
CRITIC_APIKEY=$(mint_key docs-critic)

# Credential mounting is handled automatically by the docker harness
# (MountHostCredentials=true). It mounts ~/.gemini and ~/.claude RO
# at /home/agent/ and sets HOME=/home/agent + GEMINI_DEFAULT_AUTH_TYPE.
# No manual HOME seeding needed.
EXTRA_MOUNTS_JSON='[]'

# --- apply per-agent harness config -----------------------------------
apply_config() {
    local agent="$1"
    local config_path="$2"
    local tmp
    tmp=$(mktemp)
    sed \
        -e "s|__PORT__|${PORT}|g" \
        -e "s|__COORDINATOR_APIKEY__|${COORDINATOR_APIKEY}|g" \
        -e "s|__INSPECTOR_APIKEY__|${INSPECTOR_APIKEY}|g" \
        -e "s|__CRITIC_APIKEY__|${CRITIC_APIKEY}|g" \
        -e "s|__COORDINATOR_MODEL__|${COORDINATOR_MODEL}|g" \
        -e "s|__WORKER_MODEL__|${WORKER_MODEL}|g" \
        -e "s|__GEMINI_API_KEY__|${GEMINI_API_KEY}|g" \
        -e "s|__EXTRA_MOUNTS__|${EXTRA_MOUNTS_JSON}|g" \
        "$config_path" > "$tmp"
    # Strip empty GEMINI_API_KEY so it doesn't shadow OAuth auth.
    if [ -z "$GEMINI_API_KEY" ]; then
        jq 'del(.env.GEMINI_API_KEY)' "$tmp" > "${tmp}.clean" && mv "${tmp}.clean" "$tmp"
    fi
    # Set harness_name explicitly so the resolver picks docker even
    # though local_command is empty. The docker block also satisfies
    # auto-detection but explicit is safer.
    admin harness config set \
        --agent "$agent" \
        --harness-name docker \
        --file "$tmp" >/dev/null
    rm -f "$tmp"
}

say "applying docker harness configs (image=$AGENT_IMAGE coordinator=$COORDINATOR_MODEL workers=$WORKER_MODEL)"
apply_config doc-coordinator "$SCRIPT_DIR/configs/coordinator.json"
apply_config docs-inspector  "$SCRIPT_DIR/configs/inspector.json"
apply_config docs-critic     "$SCRIPT_DIR/configs/critic.json"

# The synapbus-agent image already bakes /usr/local/bin/synapbus-agent-wrapper.sh
# as its CMD, so we don't need to mount a wrapper into the container.
# Examples that need custom dispatch logic can still override
# docker.command in their config.

echo
echo "  Web UI:    http://localhost:$PORT   (login: algis / algis-demo-pw)"
echo "  Log:       tail -f $LOG_FILE"
echo "  Agents:    http://localhost:$PORT/agents"
echo "  Runs:      http://localhost:$PORT/runs"
echo "  Goals:     http://localhost:$PORT/goals"
echo
echo "Next: ./run_task.sh"
echo "Try:  ./run_task.sh \"Verify the CLI commands on https://docs.mcpproxy.app/cli/command-reference\""
echo "      ./run_task.sh \"what does this demo do?\"   (TRIVIAL path)"
