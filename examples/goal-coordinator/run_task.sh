#!/bin/bash
# run_task.sh — send a goal DM from algis to goal-coordinator and
# wait for the coordinator's response.
#
# The coordinator will triage the goal into one of:
#   TRIVIAL     → direct reply from the coordinator
#   INFEASIBLE  → refusal with reason
#   SINGLE-STEP → delegates to inspector → critic → FINAL: reply
#   MULTI-STEP  → multi-phase delegation (rare)
#
# Usage: ./run_task.sh "your goal brief here"

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$SCRIPT_DIR/bin/synapbus"
SOCKET="$SCRIPT_DIR/data/synapbus.sock"

say() { printf '\033[1;36m[run]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[run][FAIL]\033[0m %s\n' "$*" >&2; exit 1; }

if [ "$#" -lt 1 ]; then
    die "usage: $0 \"<goal brief>\""
fi
GOAL="$1"

[ -x "$BIN" ] || die "synapbus binary not found at $BIN — run ./start.sh first"
[ -S "$SOCKET" ] || die "admin socket missing — is synapbus running?"

cd "$SCRIPT_DIR"

DB="$SCRIPT_DIR/data/synapbus.db"

# Snapshot the current max message id so we only pick up responses
# from THIS run, not stale replies left from previous invocations.
BASELINE=$(sqlite3 "$DB" "SELECT COALESCE(MAX(id), 0) FROM messages" 2>/dev/null || echo 0)

say "sending goal DM: algis → goal-coordinator (baseline msg_id=$BASELINE)"
printf '%s' "$GOAL" | "$BIN" --socket "$SOCKET" messages send \
    --from algis \
    --to goal-coordinator \
    --priority 8 >&2

say "waiting for coordinator's reply to algis (up to 180s)..."
deadline=$(( $(date +%s) + 180 ))
last_seen_id=$BASELINE

while [ "$(date +%s)" -lt "$deadline" ]; do
    # Query the messages table directly. Look for any DM to algis
    # (to_agent='algis') that's newer than the last one we saw and is
    # NOT from algis itself.
    NEW_LINES=$(sqlite3 -separator '|' "$DB" "
        SELECT id, from_agent, replace(substr(body, 1, 280), char(10), ' ')
        FROM messages
        WHERE to_agent = 'algis'
          AND from_agent != 'algis'
          AND id > $last_seen_id
        ORDER BY id ASC
    " 2>/dev/null || true)

    if [ -n "$NEW_LINES" ]; then
        while IFS='|' read -r id from body; do
            [ -z "$id" ] && continue
            say "← [$from #$id] $body"
            last_seen_id=$id
            # Terminal states:
            #   FINAL:      — critic approved, goal done
            #   CANNOT:     — coordinator refused as infeasible
            #   (direct)    — coordinator replied inline (TRIVIAL triage)
            # Non-terminal:
            #   DELEGATED:  — coordinator kicked off workers, keep waiting
            #   REVISING:   — critic asked for iteration
            case "$body" in
                DELEGATED:*|REVISING:*)
                    ;;  # informational, keep waiting
                *)
                    if [ "$from" = "goal-coordinator" ] || \
                       [ "${body#FINAL:}" != "$body" ] || \
                       [ "${body#CANNOT:}" != "$body" ]; then
                        say "terminal response received"
                        exit 0
                    fi
                    ;;
            esac
        done <<EOF
$NEW_LINES
EOF
    fi
    sleep 1
done

say "timed out waiting for terminal response (FINAL: or CANNOT:)"
say "check http://localhost:18090/runs and http://localhost:18090/dm/algis"
