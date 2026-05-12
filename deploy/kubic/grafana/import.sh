#!/bin/bash
# Imports the SynapBus dream worker dashboard into Grafana.
# Usage:
#   GRAFANA_PASS=... ./import.sh
#   GRAFANA_URL=http://grafana.example:3000 GRAFANA_USER=admin GRAFANA_PASS=... ./import.sh
set -euo pipefail

GRAFANA_URL="${GRAFANA_URL:-http://kubic.home.arpa:30083}"
GRAFANA_USER="${GRAFANA_USER:-admin}"
GRAFANA_PASS="${GRAFANA_PASS:?need GRAFANA_PASS}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DASH_FILE="${SCRIPT_DIR}/dream-dashboard.json"

[ -f "$DASH_FILE" ] || { echo "dashboard JSON not found: $DASH_FILE" >&2; exit 1; }

DS_UID=$(curl -fsS -u "$GRAFANA_USER:$GRAFANA_PASS" "$GRAFANA_URL/api/datasources" \
  | jq -r '.[] | select(.type=="prometheus") | .uid' | head -1)
[ -z "$DS_UID" ] && { echo "no prometheus datasource found in $GRAFANA_URL" >&2; exit 1; }
echo "Using Prometheus DS uid=$DS_UID" >&2

DASHBOARD=$(jq --arg uid "$DS_UID" '
  (.. | objects | select(.type? == "prometheus") | .uid) |= $uid
  | .id = null
  | . as $dash | { dashboard: $dash, overwrite: true, message: "feat(020): dream worker + memory injection dashboard" }
' "$DASH_FILE")

curl -fsS -u "$GRAFANA_USER:$GRAFANA_PASS" \
  -H "Content-Type: application/json" \
  -X POST "$GRAFANA_URL/api/dashboards/db" \
  -d "$DASHBOARD"
echo
