#!/bin/sh
# synapbus-agent-wrapper.sh — canonical entry script for every agent
# running inside the synapbus-agent container. Baked into the image at
# /usr/local/bin/synapbus-agent-wrapper.sh; the Dockerfile sets it as
# the default CMD.
#
# Run by tini as the container's PID 1 child. Reads the per-run state
# the SynapBus docker harness materialized into /workspace, hands it to
# the agent CLI selected by $AGENT_CLI (default: gemini), and exits.
# Every side effect — send_message, create_goal, propose_task_tree —
# happens through MCP tool calls inside the CLI session, NOT through
# the SynapBus admin socket (which the container can't reach).
#
# Required env vars (set by the harness via harness_config_json):
#   AGENT_NAME    — human-readable role name, used in log lines
#   GEMINI_MODEL  — model id passed to the gemini CLI
#
# Optional env vars:
#   AGENT_CLI     — "gemini" (default) or "claude". Selects which
#                   binary to invoke and which system-instructions
#                   file to load (GEMINI.md vs CLAUDE.md).

set -eu

log() { printf '[wrapper %s] %s\n' "${AGENT_NAME:-?}" "$*" >&2; }

CLI="${AGENT_CLI:-gemini}"

[ -f /workspace/message.json ] || { log "no message.json"; exit 2; }

BODY=$(jq -r '.body' < /workspace/message.json)
FROM=$(jq -r '.from_agent' < /workspace/message.json)

log "cli=$CLI from=$FROM body_bytes=$(printf '%s' "$BODY" | wc -c)"

case "$CLI" in
gemini)
    PROMPT_FILE=/workspace/GEMINI.md
    ;;
claude)
    PROMPT_FILE=/workspace/CLAUDE.md
    ;;
*)
    log "unknown AGENT_CLI=$CLI"
    exit 3
    ;;
esac

[ -f "$PROMPT_FILE" ] || { log "no $PROMPT_FILE"; exit 4; }

PROMPT="$(cat "$PROMPT_FILE")

Incoming DM from @${FROM}:
${BODY}"

printf '%s' "$PROMPT" > /workspace/prompt.txt

set +e
case "$CLI" in
gemini)
    gemini -m "${GEMINI_MODEL:-gemini-2.5-flash}" --approval-mode yolo -p "$PROMPT" \
        > /workspace/gemini.stdout.log 2> /workspace/gemini.stderr.log
    EXIT=$?
    ;;
claude)
    # Claude Code's --print mode writes to stdout. We don't pipe stdin
    # because the prompt is already in -p / via the @-include.
    claude --print "$PROMPT" \
        > /workspace/claude.stdout.log 2> /workspace/claude.stderr.log
    EXIT=$?
    ;;
esac
set -e

log "$CLI exited=$EXIT"
if [ "$EXIT" -ne 0 ]; then
    log "tail of stderr:"
    tail -20 /workspace/${CLI}.stderr.log >&2 || true
fi

# We never propagate the CLI's exit code. The actual outcome lives in
# whatever MCP send_message calls the agent made; the harness captures
# them via traces. wrapper.sh succeeds as long as the CLI ran at all,
# so the reactive run is marked "succeeded" and the next coalesced
# trigger is allowed to fire.
exit 0
