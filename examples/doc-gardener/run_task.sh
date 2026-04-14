#!/bin/bash
# run_task.sh — drive the docgardener demo against a running synapbus
# instance. This is the demo's "coordinator run + specialist loop"
# shortcut: it invokes ./bin/docgardener run, which creates a goal,
# builds a task tree, spawns specialists (with real config_hash +
# delegation cap checks + reputation seeding), walks tasks through
# the state machine, and records reputation evidence.
#
# The demo does NOT launch real LLM subprocesses in v1 — it produces
# synthetic artifacts so we can demonstrate the data primitives
# end-to-end. Real subprocess execution comes via the reactor
# integration in a follow-up PR (see specs/018/tasks.md, Phase 9+).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$SCRIPT_DIR/bin/docgardener"
DB="$SCRIPT_DIR/data/synapbus.db"

cd "$SCRIPT_DIR"

say() { printf '\033[1;36m[run]\033[0m %s\n' "$*"; }

if [ ! -x "$BIN" ]; then
    printf '\033[1;31m[FAIL]\033[0m docgardener binary not found at %s — run ./start.sh first\n' "$BIN" >&2
    exit 1
fi
if [ ! -f "$DB" ]; then
    printf '\033[1;31m[FAIL]\033[0m DB not found at %s — is synapbus running?\n' "$DB" >&2
    exit 1
fi

say "executing docgardener run..."
"$BIN" run --db "$DB"

say "done. View the run in the Web UI or render a report:"
echo "  ./report.sh"
echo "  open report.html"
