#!/bin/sh
# Subprocess-harness wrapper for cold-topic-explainer Gemini agents.
#
# The subprocess harness execs this with cwd = per-run workdir. The
# workdir already contains GEMINI.md and message.json, written by
# MaterialiseAgentConfig and the harness itself. Required env vars are
# supplied by the agent's harness_config_json.env block (see
# configs/*.json):
#
#   AGENT_ROLE      decomposer | writer | critic
#   AGENT_NAME      this agent's synapbus name
#   GEMINI_MODEL    e.g. gemini-2.5-pro
#   NEXT_AGENT      the agent to DM on the happy path
#   REVISE_AGENT    (critic only) the agent to DM when asking for fixes
#   OWNER_AGENT     (critic only) the agent to DM with FINAL: results
#   SYNAPBUS_SOCKET full path to the synapbus admin unix socket
#   SYNAPBUS_BIN    path to the synapbus CLI (used to send messages)
#
# All agents then: read the DM body, call gemini headless with GEMINI.md
# + the body, post-process, and hand off via `synapbus messages send`
# over the admin socket.

set -eu

log() {
    printf '[wrapper %s] %s\n' "${AGENT_NAME:-?}" "$*" >&2
}

# --- read the triggering DM -------------------------------------------
if [ ! -f message.json ]; then
    log "no message.json in workdir; refusing to fabricate a task"
    exit 2
fi
BODY=$(jq -r '.body' < message.json)
FROM=$(jq -r '.from_agent' < message.json)

log "received from=$FROM bytes=$(printf '%s' "$BODY" | wc -c)"

# --- call gemini ------------------------------------------------------
# -y / --approval-mode yolo means "don't prompt" — safe because we're
# not giving gemini any tools to call in this workflow.
PROMPT="$(cat GEMINI.md)

Incoming DM from @${FROM}:
${BODY}"

# Preserve the exact prompt and any stderr noise for forensics.
printf '%s' "$PROMPT" > gemini.prompt.txt
set +e
RAW=$(gemini -m "$GEMINI_MODEL" --approval-mode yolo -p "$PROMPT" 2>gemini.stderr.log)
GEMINI_EXIT=$?
set -e

# Gemini prepends "MCP issues detected. Run /mcp list for status." to
# stdout when its MCP config can't reach a server. Strip it.
RESPONSE=$(printf '%s' "$RAW" | sed 's|^MCP issues detected\. Run /mcp list for status\.||')

# Save the cleaned response for forensics before we decide next steps.
printf '%s' "$RESPONSE" > gemini.stdout.txt

if [ -z "$RESPONSE" ]; then
    log "empty gemini response (exit=$GEMINI_EXIT); last stderr:"
    tail -20 gemini.stderr.log >&2 || true
    exit 3
fi

log "gemini response bytes=$(printf '%s' "$RESPONSE" | wc -c)"

# Save full response for forensics.
printf '%s' "$RESPONSE" > result.md
printf '%s\n' "$RESPONSE"

# --- decide who to DM next --------------------------------------------
TO="$NEXT_AGENT"
if [ "$AGENT_ROLE" = "critic" ]; then
    # Critic's prompt tells gemini to prefix FINAL: or REVISE:.
    case "$RESPONSE" in
        FINAL:*|*"FINAL:"*|Final:*|*"Final:"*)
            TO="$OWNER_AGENT"
            log "verdict=FINAL → $TO"
            ;;
        *)
            TO="$REVISE_AGENT"
            log "verdict=REVISE → $TO"
            ;;
    esac
fi

# --- hand off ----------------------------------------------------------
printf '%s' "$RESPONSE" | "$SYNAPBUS_BIN" --socket "$SYNAPBUS_SOCKET" messages send \
    --from "$AGENT_NAME" \
    --to "$TO" \
    --priority 5 >&2 || {
    log "admin socket send failed — check $SYNAPBUS_SOCKET"
    exit 4
}

log "handed off to $TO"
