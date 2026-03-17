# Research: SynapBus v0.7.0

## Decision Log

### 1. Analytics API Design

**Decision**: Add 3 new REST endpoints under `/api/analytics/` — `timeline`, `top-agents`, `top-channels`. Reuse existing `/api/agents` count and `/api/channels` count for summary cards.

**Rationale**: Keeps analytics logic isolated from existing message endpoints. SQLite aggregation queries are fast enough for the expected data volume (thousands of messages, not millions). 60s server-side cache prevents repeated expensive queries.

**Alternatives considered**:
- Materialized views in SQLite: Rejected — adds migration complexity, overkill for single-user system
- Pre-computed stats table with background worker: Rejected — over-engineering for the expected scale
- Client-side aggregation: Rejected — would require fetching all messages to the browser

### 2. Chart Library

**Decision**: Use lightweight inline SVG rendering in Svelte (no external chart library). Bar chart for timeline, simple ranked list for top-5s.

**Rationale**: SynapBus embeds the web UI in the binary. Adding Chart.js or D3 would significantly increase bundle size. The required charts (bar chart + ranked lists) are simple enough to render with SVG elements in Svelte.

**Alternatives considered**:
- Chart.js via npm: Rejected — 70KB+ gzipped, overkill for 2 simple charts
- Lightweight libraries (uPlot, Frappe Charts): Rejected — still adds dependency weight for simple bar charts
- Canvas-based rendering: Rejected — SVG is more accessible and easier to style with Tailwind

### 3. PWA Service Worker Strategy

**Decision**: Use SvelteKit's static adapter output + a hand-written service worker (`sw.js`) that caches the app shell (HTML, CSS, JS) and provides offline fallback. API responses are NOT cached (always network-first).

**Rationale**: SynapBus data is real-time messaging — caching API responses would show stale data. The service worker should only cache static assets for offline app shell access.

**Alternatives considered**:
- Workbox: Rejected — heavy dependency for simple cache-first static + network-only API strategy
- SvelteKit service worker plugin: Does not exist for static adapter
- Cache API responses with short TTL: Rejected — messaging data must be fresh

### 4. Push Notification Architecture

**Decision**: Use Web Push API with VAPID keys. Server generates VAPID key pair on first run, stores in data directory. Push subscriptions stored in SQLite (new migration). Server sends push via standard Web Push protocol (no external service).

**Rationale**: Self-hosted, no external push service dependency (aligns with Constitution Principle I). VAPID is the standard for Web Push.

**Alternatives considered**:
- Firebase Cloud Messaging: Rejected — external dependency, violates local-first principle
- SSE-only notifications: Rejected — only works when tab is open, no background notifications
- WebSocket: Rejected — more complex, SSE already handles real-time updates

### 5. Web Push Go Library

**Decision**: Use `github.com/SherClockHolmes/webpush-go` — a pure Go Web Push library with VAPID support.

**Rationale**: Pure Go (no CGO), well-maintained, implements RFC 8291 (Message Encryption for Web Push) and RFC 8292 (VAPID). Aligns with Constitution Principle III.

**Alternatives considered**:
- Custom implementation: Rejected — Web Push encryption is complex, better to use tested library
- No push notifications: Rejected — core feature request

### 6. Mention/Channel Validation Strategy

**Decision**: At render time, the MessageBody component fetches agent list and channel list (cached in a Svelte store), then checks each @mention and #channel reference against the known entities. Agents/channels carry a `deleted` flag or are absent from the list.

**Rationale**: Validation at render time (not send time) means existing messages automatically update when entities are created/deleted. The agent and channel lists are small enough to cache in memory.

**Alternatives considered**:
- Server-side rendering of message HTML: Rejected — SynapBus uses client-side rendering
- Validate at send time and store resolved references: Rejected — wouldn't handle retroactive deletion
- API endpoint to validate mentions: Rejected — N+1 problem, better to batch-load entity lists

### 7. Font Size Persistence

**Decision**: localStorage with key `synapbus-font-size`. Applied via CSS custom property `--font-size` on `<html>` element. Svelte store syncs with localStorage.

**Rationale**: Simple, no server round-trip needed. CSS custom property allows global application without modifying every component.

**Alternatives considered**:
- Server-side user preference: Rejected — over-engineering for a UI preference
- Cookie: Rejected — localStorage is simpler for same-origin storage

### 8. Version Embedding

**Decision**: Use Go `-ldflags "-X main.version=..."` at build time. Already exists in codebase (`var version = "dev"` in main.go). Add `/api/version` endpoint exposing this value. Makefile already has LDFLAGS support.

**Rationale**: Standard Go pattern, already partially implemented. Just needs the API endpoint and UI footer.

### 9. MCP Prompts Design

**Decision**: Add 4 MCP prompts: `daily-digest`, `agent-health-check`, `channel-overview`, `debug-agent`. These are registered as MCP prompt resources and return formatted markdown.

**Rationale**: These cover the most common human-operator workflows: "What happened?", "Are my agents healthy?", "What's going on in channels?", and "Why isn't agent X working?".

**Alternatives considered**:
- More prompts (8-10): Rejected — start small, expand based on usage
- Prompts as tools: Rejected — MCP distinguishes prompts (templates) from tools (actions)

### 10. Website Stack

**Decision**: Website is SvelteKit (not Astro as assumed). Located at `~/repos/synapbus-website/` with SvelteKit + Tailwind + Cloudflare Pages.

**Rationale**: Direct observation of the repo structure. Corrects the assumption in spec.md.
