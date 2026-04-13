#!/bin/bash
# run_task.sh — kick off a cold-topic-explainer run and wait for the final.
#
# Usage: ./run_task.sh "topic describing what to explain"
#
# Sends the initial DM from algis → decomposer-pro via the admin
# socket, then polls for a DM to algis whose body starts with "FINAL:".

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_DIR="$SCRIPT_DIR/data"
BIN="$SCRIPT_DIR/bin/synapbus"
SOCKET="$DATA_DIR/synapbus.sock"

TOPIC="${1:-how does the SynapBus reactor coalesce bursts of DMs into one follow-up run via the pending_work flag?}"
TIMEOUT_SEC="${TIMEOUT:-240}"
POLL_INTERVAL_SEC=2

if [ ! -S "$SOCKET" ]; then
    echo "admin socket $SOCKET not found — run ./start.sh first" >&2
    exit 1
fi

say() { printf '\033[1;35m[task]\033[0m %s\n' "$*"; }

say "topic: $TOPIC"
say "kicking off via: algis → decomposer-pro"

printf '%s' "$TOPIC" | "$BIN" --socket "$SOCKET" messages send \
    --from algis \
    --to decomposer-pro \
    --priority 7 \
    --body-file /dev/stdin \
    >/dev/null

say "waiting up to ${TIMEOUT_SEC}s for FINAL: DM to algis ..."

deadline=$(( $(date +%s) + TIMEOUT_SEC ))
while [ $(date +%s) -lt "$deadline" ]; do
    # Query the DB directly — fast and avoids re-auth churn.
    final=$(sqlite3 -separator '|' "$DATA_DIR/synapbus.db" "
        SELECT id, body FROM messages
        WHERE to_agent='algis'
          AND from_agent='critic-lite'
          AND body LIKE 'FINAL:%'
        ORDER BY id DESC LIMIT 1;
    " 2>/dev/null || true)

    if [ -n "$final" ]; then
        id=$(printf '%s' "$final" | cut -d'|' -f1)
        body=$(printf '%s' "$final" | cut -d'|' -f2-)
        say "FINAL arrived (message #$id)"
        echo
        printf '%s\n' "$body"
        echo
        say "success"
        exit 0
    fi

    # Show a brief status line while we wait.
    running=$(sqlite3 "$DATA_DIR/synapbus.db" "
        SELECT agent_name FROM reactive_runs WHERE status='running';
    " 2>/dev/null | tr '\n' ',' | sed 's/,$//')
    done_count=$(sqlite3 "$DATA_DIR/synapbus.db" "
        SELECT COUNT(*) FROM reactive_runs
         WHERE status IN ('succeeded','failed');
    " 2>/dev/null || echo 0)
    printf '\r  running=[%s] done=%s ' "$running" "$done_count"

    sleep "$POLL_INTERVAL_SEC"
done

echo
say "timed out — dumping recent reactive_runs for debugging:"
sqlite3 -header -column "$DATA_DIR/synapbus.db" "
    SELECT id, agent_name, trigger_from, status, error_log
    FROM reactive_runs ORDER BY id DESC LIMIT 20;
"
exit 2
