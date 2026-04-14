#!/bin/bash
# report.sh — render the HTML report for the most recent doc-gardener run.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$SCRIPT_DIR/bin/docgardener"
DB="$SCRIPT_DIR/data/synapbus.db"
OUT="$SCRIPT_DIR/report.html"

cd "$SCRIPT_DIR"

say() { printf '\033[1;36m[report]\033[0m %s\n' "$*"; }

if [ ! -x "$BIN" ]; then
    printf '\033[1;31m[FAIL]\033[0m docgardener binary not found at %s — run ./start.sh first\n' "$BIN" >&2
    exit 1
fi

say "rendering $OUT"
"$BIN" report --db "$DB" --out "$OUT"

say "opening in browser..."
if command -v open >/dev/null 2>&1; then
    open "$OUT"
elif command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$OUT"
else
    say "(no opener found — browse to file://$OUT)"
fi
