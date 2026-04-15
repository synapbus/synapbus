#!/bin/sh
# wrapper.sh — harness-agnostic entry point for every agent in the
# goal-coordinator example. The subprocess harness execs this with
# cwd = per-run workdir containing GEMINI.md, .gemini/settings.json
# (MCP config), and message.json.
#
# Dispatches by $AGENT_ROLE:
#   coordinator → pass-through: run gemini with MCP tools, let the
#                 model call send_message/create_goal/propose_task_tree
#                 directly via the synapbus MCP server.
#   inspector   → parse task JSON, run, forward result JSON to critic
#   critic      → audit, DM owner on FINAL or re-brief inspector on REVISE
#
# The inspector and critic still use the old "emit JSON, wrapper
# dispatches" pattern because they're workers with a fixed contract.
# Only the coordinator owns real decision-making, and only it needs
# MCP-native tool calls.

set -eu

log() { printf '[wrapper %s] %s\n' "${AGENT_NAME:-?}" "$*" >&2; }

[ -f message.json ] || { log "no message.json"; exit 2; }

BODY=$(jq -r '.body' < message.json)
FROM=$(jq -r '.from_agent' < message.json)

log "role=$AGENT_ROLE from=$FROM body_bytes=$(printf '%s' "$BODY" | wc -c)"

# --- build prompt -----------------------------------------------------
PROMPT="$(cat GEMINI.md)

Incoming DM from @${FROM}:
${BODY}"

printf '%s' "$PROMPT" > prompt.txt

# --- coordinator: MCP pass-through -----------------------------------
# The harness materializes .gemini/settings.json from the agent's
# mcp_servers config, so Gemini picks up the synapbus MCP server on
# its own. We just run it and let the model drive — every side-effect
# (send_message, create_goal, propose_task_tree) is an MCP tool call.
if [ "$AGENT_ROLE" = "coordinator" ]; then
    log "coordinator pass-through: invoking gemini with MCP tools"
    set +e
    gemini -m "$GEMINI_MODEL" --approval-mode yolo -p "$PROMPT" \
        >gemini.stdout.log 2>gemini.stderr.log
    CLI_EXIT=$?
    set -e
    log "coordinator gemini exited=$CLI_EXIT stdout=$(wc -c < gemini.stdout.log 2>/dev/null || echo 0)B"
    if [ "$CLI_EXIT" -ne 0 ]; then
        tail -20 gemini.stderr.log >&2 || true
    fi
    exit 0
fi

# --- inspector + critic: legacy JSON-plan pattern --------------------
set +e
RAW=$(gemini -m "$GEMINI_MODEL" --approval-mode yolo -p "$PROMPT" 2>gemini.stderr.log)
CLI_EXIT=$?
set -e

# Strip the MCP-warning preamble Gemini prepends when its MCP config
# can't reach a server. (Inspector + critic don't use MCP from inside
# gemini; their orchestration happens in this wrapper.)
RAW=$(printf '%s' "$RAW" | sed 's|^MCP issues detected\. Run /mcp list for status\.||')
printf '%s' "$RAW" > gemini.stdout.raw

# --- extract the first JSON object from the response -----------------
# Models wrap JSON in ```json fences sometimes; strip them.
RESPONSE=$(printf '%s' "$RAW" \
    | sed -E 's/^```(json)?//' \
    | sed -E 's/```$//' \
    | awk 'BEGIN{d=0;c=0} { for(i=1;i<=length($0);i++){ch=substr($0,i,1); if(c==0 && ch=="{") c=1; if(c){printf "%s",ch; if(ch=="{")d++; else if(ch=="}"){d--; if(d==0){print ""; exit}}}} if(c&&d>0) print ""}')

if [ -z "$RESPONSE" ]; then
    log "empty response from $GEMINI_MODEL (exit=$CLI_EXIT); tail of stderr:"
    tail -10 gemini.stderr.log >&2 || true
    exit 3
fi

printf '%s' "$RESPONSE" > response.txt
log "response bytes=$(printf '%s' "$RESPONSE" | wc -c)"

# --- shortcut helper --------------------------------------------------
send_dm() {
    # $1 = to, $2 = body (stdin)
    "$SYNAPBUS_BIN" --socket "$SYNAPBUS_SOCKET" messages send \
        --from "$AGENT_NAME" \
        --to "$1" \
        --priority 5 >&2 || {
        log "admin socket send failed (to=$1)"
        return 4
    }
}

# --- dispatch by role -------------------------------------------------
case "$AGENT_ROLE" in
inspector)
    # Pass the full JSON response forward to the critic — the critic's
    # GEMINI.md is set up to parse it. Also carry the critic_brief
    # from the original task through unchanged.
    CRITIC_BRIEF=$(printf '%s' "$BODY" | jq -r '.critic_brief // empty')
    PAYLOAD=$(printf '%s' "$RESPONSE" | jq -c --arg cb "$CRITIC_BRIEF" '. + {critic_brief:$cb, from_inspector:"generic-inspector"}')
    log "forwarding inspector result to $NEXT_AGENT"
    printf '%s' "$PAYLOAD" | send_dm "$NEXT_AGENT"
    ;;

critic)
    VERDICT=$(printf '%s' "$RESPONSE" | jq -r '.verdict // "UNKNOWN"')
    case "$VERDICT" in
    FINAL|Final|final)
        FINAL_SUMMARY=$(printf '%s' "$RESPONSE" | jq -r '.final_summary // .reason // "approved"')
        log "verdict=FINAL → $OWNER_AGENT"
        printf 'FINAL: %s' "$FINAL_SUMMARY" | send_dm "$OWNER_AGENT"
        ;;
    REVISE|Revise|revise)
        PATCH=$(printf '%s' "$RESPONSE" | jq -r '.patch // .reason // "please revise"')
        TASK_ID=$(printf '%s' "$RESPONSE" | jq -r '.task_id // 0')
        log "verdict=REVISE → $INSPECTOR_AGENT"
        # Re-brief the inspector with the patch.
        REVISE_MSG=$(jq -nc \
            --arg t "$TASK_ID" \
            --arg brief "Revision requested by critic: $PATCH" \
            '{task_id:($t|tonumber), goal_title:"revision", brief:$brief, acceptance_criteria:"address the critic patch"}')
        printf '%s' "$REVISE_MSG" | send_dm "$INSPECTOR_AGENT"
        # Also tell the owner we're iterating.
        printf 'REVISING: %s' "$PATCH" | send_dm "$OWNER_AGENT"
        ;;
    *)
        log "critic emitted unknown verdict: $VERDICT"
        printf 'CRITIC_ERROR: %s' "$RESPONSE" | send_dm "$OWNER_AGENT"
        exit 6
        ;;
    esac
    ;;

*)
    log "unknown AGENT_ROLE: $AGENT_ROLE"
    exit 7
    ;;
esac

log "done"
