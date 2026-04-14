#!/bin/sh
# wrapper.sh — harness-agnostic entry point for every agent in the
# goal-coordinator example. The subprocess harness execs this with
# cwd = per-run workdir containing GEMINI.md + message.json, and with
# the env block from the agent's harness_config_json.
#
# Dispatches by $AGENT_ROLE:
#   coordinator → triage (reply | refuse | delegate)
#   inspector   → do the work, DM critic
#   critic      → audit, DM owner on FINAL or re-brief inspector on REVISE
#
# All agents call the same gemini CLI; swapping in claude / codex is
# a 3-line change in the call block below. Nothing in this wrapper is
# gemini-specific except the exec invocation.

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

# --- call the model ---------------------------------------------------
# Swap this block to use a different CLI; the rest of wrapper.sh is
# model-agnostic. Expected output: a single JSON object on stdout.
set +e
RAW=$(gemini -m "$GEMINI_MODEL" --approval-mode yolo -p "$PROMPT" 2>gemini.stderr.log)
CLI_EXIT=$?
set -e

# Strip the MCP-warning preamble Gemini prepends when its MCP config
# can't reach a server. (We don't use MCP from inside gemini; the
# orchestration happens in this wrapper.)
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
coordinator)
    ACTION=$(printf '%s' "$RESPONSE" | jq -r '.action // empty')
    case "$ACTION" in
    reply)
        BODY_OUT=$(printf '%s' "$RESPONSE" | jq -r '.body // "no body"')
        log "triage=reply → $FROM"
        printf '%s' "$BODY_OUT" | send_dm "$FROM"
        ;;
    refuse)
        REASON=$(printf '%s' "$RESPONSE" | jq -r '.reason // "no reason"')
        log "triage=refuse → $FROM"
        printf 'CANNOT: %s' "$REASON" | send_dm "$FROM"
        ;;
    delegate)
        PATTERN=$(printf '%s' "$RESPONSE" | jq -r '.pattern // "inspector-critic"')
        GOAL_TITLE=$(printf '%s' "$RESPONSE" | jq -r '.goal.title // "untitled"')
        GOAL_DESC=$(printf '%s' "$RESPONSE" | jq -r '.goal.description // ""')
        GOAL_AC=$(printf '%s' "$RESPONSE" | jq -r '.goal.acceptance_criteria // ""')
        INSPECTOR_BRIEF=$(printf '%s' "$RESPONSE" | jq -r '.inspector_brief // ""')
        CRITIC_BRIEF=$(printf '%s' "$RESPONSE" | jq -r '.critic_brief // ""')

        log "triage=delegate pattern=$PATTERN title=$GOAL_TITLE"

        # Generate a pseudo task id for the thread. We don't write to
        # the goal_tasks table from bash; the inspector's and critic's
        # artifacts are captured as regular messages and the web UI
        # renders them via the /runs flow. A real MCP-tool-calling
        # coordinator would call create_goal / propose_task_tree here.
        TASK_ID=$(date +%s)

        # Compose the inspector's TASK JSON.
        INSPECT_MSG=$(jq -nc \
            --arg t "$TASK_ID" \
            --arg title "$GOAL_TITLE" \
            --arg brief "$INSPECTOR_BRIEF" \
            --arg ac "$GOAL_AC" \
            --arg origin "$FROM" \
            --arg critic_brief "$CRITIC_BRIEF" \
            '{task_id:($t|tonumber), goal_title:$title, brief:$brief, acceptance_criteria:$ac, owner:$origin, critic_brief:$critic_brief}')

        log "dispatching task $TASK_ID to $INSPECTOR_AGENT"
        printf '%s' "$INSPECT_MSG" | send_dm "$INSPECTOR_AGENT"

        # Let the owner know we delegated (transparency).
        printf 'DELEGATED: %s → %s → %s (task=%s)' \
            "$GOAL_TITLE" "$INSPECTOR_AGENT" "$CRITIC_AGENT" "$TASK_ID" \
            | send_dm "$FROM"
        ;;
    *)
        log "coordinator emitted unknown action: $ACTION"
        printf 'COORDINATOR_ERROR: could not parse response as JSON action' | send_dm "$FROM"
        exit 5
        ;;
    esac
    ;;

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
