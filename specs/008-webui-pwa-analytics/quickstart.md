# Quickstart: SynapBus v0.7.0 Features

## Prerequisites

- Go 1.25+ installed
- Node.js 20+ (for web UI build)
- SynapBus data directory with existing messages (for analytics demo)

## Build & Run

```bash
cd ~/repos/synapbus
make build
./bin/synapbus serve --port 8080 --data ./data
```

## Verify New Features

### 1. Analytics Dashboard

Open `http://localhost:8080` — the dashboard now shows:
- Time-series message graph (default: 24h span)
- Top 5 agents by message count
- Top 5 channels by message count
- Summary cards (total agents, channels)

Click span buttons (1h, 4h, 24h, 7d, 1month) to change the time window.

### 2. PWA Installation

1. Open `http://localhost:8080` in Chrome/Edge
2. Look for the install icon in the address bar (or "Install App" in browser menu)
3. Click Install — SynapBus opens as a standalone app
4. Go to Settings → Enable push notifications
5. Send a high-priority DM to yourself — you should see a desktop notification

### 3. API Endpoints

```bash
# Analytics timeline
curl -b cookies.txt http://localhost:8080/api/analytics/timeline?span=24h

# Top agents
curl -b cookies.txt http://localhost:8080/api/analytics/top-agents?span=7d

# Top channels
curl -b cookies.txt http://localhost:8080/api/analytics/top-channels

# Summary counts
curl -b cookies.txt http://localhost:8080/api/analytics/summary

# Version
curl http://localhost:8080/api/version
```

### 4. MCP Prompts

Connect an MCP client and try:
```
prompts/list
prompts/get daily-digest
prompts/get agent-health-check
prompts/get channel-overview
prompts/get debug-agent {"agent_name": "research-mcpproxy"}
```

### 5. UX Improvements

- **Compose textarea**: Type a long message — the textarea auto-grows up to 12 lines
- **Agent name**: Go to Agents → click an agent → click the display name to edit inline
- **Human name**: Go to Settings → edit your display name
- **Font size**: Go to Settings → use -/+ buttons to adjust font size
- **Smart mentions**: Messages with @deleted-agent show an "inactive" badge
- **Version footer**: Check the footer — shows version + GitHub link
