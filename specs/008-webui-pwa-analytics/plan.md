# Implementation Plan: SynapBus v0.7.0 — WebUI Analytics, PWA, UX Fixes, Website, MCP Prompts

**Branch**: `008-webui-pwa-analytics` | **Date**: 2026-03-17 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/008-webui-pwa-analytics/spec.md`

## Summary

Add analytics dashboard with time-series message graph and top-5 leaderboards, convert Web UI to a PWA with push notifications, fix 6 UX issues (auto-resize textarea, editable names, smart mentions, font size, version footer), add MCP prompts for common operator workflows, and update the synapbus.dev website messaging.

## Technical Context

**Language/Version**: Go 1.25+ (backend), SvelteKit 2 + Svelte 5 (frontend), SvelteKit (website)
**Primary Dependencies**: go-chi/chi (HTTP), mark3labs/mcp-go (MCP), modernc.org/sqlite (storage), SherClockHolmes/webpush-go (push notifications — NEW)
**Storage**: SQLite (existing DB, 1 new migration for push_subscriptions), localStorage (font size)
**Testing**: `go test ./...` (Go), manual browser testing (Svelte), curl (API)
**Target Platform**: linux/amd64, darwin/arm64 (binary); Chrome/Edge/Safari (PWA)
**Project Type**: Web service with embedded SPA
**Performance Goals**: Analytics queries < 500ms for 100K messages, dashboard render < 2s
**Constraints**: Zero CGO, single binary, offline-capable PWA shell
**Scale/Scope**: Single-user/small-team (1-10 users), ~50K messages, ~20 agents, ~30 channels

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Local-First, Single Binary | PASS | All features embedded in single binary. VAPID keys stored in data dir. Push sent directly (no external service). |
| II. MCP-Native | PASS | New MCP prompts added. REST endpoints are for Web UI only. |
| III. Pure Go, Zero CGO | PASS | webpush-go is pure Go. No CGO dependencies added. |
| IV. Multi-Tenant with Ownership | PASS | Analytics scoped to owner's data. Push subscriptions per-user. |
| V. Embedded OAuth 2.1 | PASS | No auth changes. Existing session auth used for new endpoints. |
| VI. Semantic-Ready Storage | PASS | No changes to vector/search layer. |
| VII. Swarm Intelligence Patterns | N/A | No changes to swarm patterns. |
| VIII. Observable by Default | PASS | Analytics enhance observability. Push notification sends could be traced. |
| IX. Progressive Complexity | PASS | Analytics/PWA are additive — basic messaging unchanged. Push notifications optional. |
| X. Web UI as First-Class Citizen | PASS | Major UI enhancements. PWA upgrades Web UI to installable app. |

**Gate result**: ALL PASS. No violations to justify.

## Project Structure

### Documentation (this feature)

```text
specs/008-webui-pwa-analytics/
├── plan.md              # This file
├── spec.md              # Feature specification
├── research.md          # Research decisions
├── data-model.md        # Data model (push_subscriptions, analytics queries)
├── quickstart.md        # Verification guide
├── contracts/
│   └── api.md           # API endpoint contracts
└── checklists/
    └── requirements.md  # Spec quality checklist
```

### Source Code (repository root)

```text
# Backend (Go)
internal/
├── api/
│   ├── analytics_handler.go    # NEW: analytics REST endpoints
│   ├── push_handler.go         # NEW: push subscription endpoints
│   ├── version_handler.go      # NEW: version endpoint
│   └── router.go               # MODIFIED: register new routes
├── push/                       # NEW: Web Push service
│   ├── service.go              # VAPID key management, send push
│   ├── store.go                # SQLite push subscription CRUD
│   └── service_test.go         # Tests
├── mcp/
│   ├── prompts.go              # NEW: MCP prompt resources
│   └── server.go               # MODIFIED: register prompts

# Frontend (Svelte)
web/src/
├── routes/
│   ├── +page.svelte            # MODIFIED: analytics dashboard
│   └── settings/+page.svelte   # MODIFIED: display name, font size, push toggle
├── lib/
│   ├── components/
│   │   ├── ComposeForm.svelte  # MODIFIED: auto-resize textarea
│   │   ├── MessageBody.svelte  # MODIFIED: smart mention/channel highlighting
│   │   ├── AnalyticsChart.svelte # NEW: SVG bar chart component
│   │   └── TopList.svelte      # NEW: ranked list component
│   ├── stores/
│   │   ├── fontSize.ts         # NEW: font size store
│   │   └── entities.ts         # NEW: cached agents/channels for mention validation
│   └── api/
│       └── client.ts           # MODIFIED: add analytics, push, version, profile endpoints
├── static/
│   ├── manifest.json           # NEW: PWA manifest
│   ├── sw.js                   # NEW: service worker
│   └── icons/                  # NEW: PWA icons (192x192, 512x512)

# Database
schema/
└── 012_push_subscriptions.sql  # NEW: push subscriptions table

# Build
cmd/synapbus/main.go            # MODIFIED: wire push service, version endpoint
Makefile                         # MODIFIED: version ldflags (already exists)
```

**Structure Decision**: Follows existing repository layout. New Go packages only where warranted (push service has distinct responsibility). Frontend changes are additive to existing components.

## Implementation Phases

### Phase 1: Backend Analytics + Version API (~1h)

**Files**: `internal/api/analytics_handler.go`, `internal/api/version_handler.go`, `internal/api/router.go`, `cmd/synapbus/main.go`

1. Create `analytics_handler.go` with 4 endpoints:
   - `GET /api/analytics/timeline?span=24h` — time-bucketed message counts
   - `GET /api/analytics/top-agents?span=24h&limit=5` — top agents by messages
   - `GET /api/analytics/top-channels?span=24h&limit=5` — top channels by messages
   - `GET /api/analytics/summary` — total agents, channels, messages
2. Create `version_handler.go` with `GET /api/version` — returns version, commit, repo URL
3. Register routes in `router.go`
4. Wire version string from `main.go` to handler

**Tests**: Table-driven Go tests for each endpoint with various spans and edge cases (no data, single message, boundary timestamps).

### Phase 2: Frontend Analytics Dashboard (~1.5h)

**Files**: `web/src/routes/+page.svelte`, `web/src/lib/components/AnalyticsChart.svelte`, `web/src/lib/components/TopList.svelte`, `web/src/lib/api/client.ts`

1. Create `AnalyticsChart.svelte` — SVG bar chart with responsive width, hover tooltips, animated bars
2. Create `TopList.svelte` — ranked list with agent/channel name, message count, bar indicator
3. Add API client methods for analytics endpoints
4. Redesign dashboard page: stat cards (agents, channels) + time span selector + chart + top-5 panels
5. Add version display to layout footer with GitHub link

**Tests**: Manual browser testing. Verify chart renders, span switching, empty states.

### Phase 3: UX Fixes — Textarea, Names, Font Size (~1.5h)

**Files**: `web/src/lib/components/ComposeForm.svelte`, `web/src/routes/agents/[name]/+page.svelte`, `web/src/routes/settings/+page.svelte`, `web/src/lib/stores/fontSize.ts`, `web/src/routes/+layout.svelte`

1. **ComposeForm**: Replace fixed textarea with auto-resize (min 3 lines, max 12 lines, overflow-y scroll)
2. **Agent detail**: Add inline edit for display_name (click to edit, Enter to save, Escape to cancel)
3. **Settings**: Add display name edit field + save button, add font size -/+ controls
4. **Font size store**: Create Svelte store synced with localStorage, apply via CSS custom property on `<html>`
5. **Layout**: Apply font size CSS custom property from store on mount

**Tests**: Go test for `PUT /api/auth/profile` endpoint. Manual browser testing for UI interactions.

### Phase 4: Smart Mention/Channel Highlighting (~1h)

**Files**: `web/src/lib/components/MessageBody.svelte`, `web/src/lib/stores/entities.ts`, `web/src/lib/api/client.ts`

1. Create `entities.ts` store — fetches and caches agent list + channel list, refreshes on SSE events
2. Modify `MessageBody.svelte` mention/channel regex processing:
   - Check @name against entity store: exists → link, deleted → badge "inactive", unknown → plain text
   - Check #channel against entity store: same logic
   - Handle edge cases: email addresses, issue numbers, special characters
3. Add `deleted` status detection (agent/channel not in list = never existed; agent/channel with status=inactive = deleted)

**Tests**: Manual testing with various message contents. Verify all 6 acceptance scenarios.

### Phase 5: PWA — Manifest, Service Worker, Push (~2h)

**Files**: `web/static/manifest.json`, `web/static/sw.js`, `web/src/routes/+layout.svelte`, `internal/push/service.go`, `internal/push/store.go`, `internal/api/push_handler.go`, `schema/012_push_subscriptions.sql`, `cmd/synapbus/main.go`

1. Create PWA manifest with icons, theme color, display standalone
2. Create service worker — cache-first for static assets, network-only for API
3. Register service worker in layout
4. Create Go push service: VAPID key management, subscription CRUD, send push
5. Create SQLite migration for push_subscriptions table
6. Create push API endpoints: subscribe, unsubscribe, get VAPID key
7. Integrate push sending into message delivery flow (DMs with priority >= 7, @mentions)
8. Add push notification toggle in Settings page
9. Wire push service in main.go

**Tests**: Go tests for push service and store. Manual testing for PWA install and push delivery.

### Phase 6: MCP Prompts (~1h)

**Files**: `internal/mcp/prompts.go`, `internal/mcp/server.go`

1. Create `prompts.go` with 4 prompt handlers:
   - `daily-digest`: Message stats, active agents, channel activity for last 24h
   - `agent-health-check`: All agents with last-seen, pending messages, error count
   - `channel-overview`: All channels with member count, message count, last activity
   - `debug-agent`: Specific agent status, pending DMs, recent traces, error patterns
2. Register prompts in MCP server setup
3. Each prompt queries services and returns formatted markdown

**Tests**: Go tests for each prompt handler. Manual testing with MCP client.

### Phase 7: Website Update (~1h)

**Files**: `~/repos/synapbus-website/src/routes/+page.svelte`, related components

1. Update hero section: "Your local agent network" messaging
2. Update feature sections: agent collaboration, human-agent interaction, desktop/mobile
3. Update or add screenshots showing analytics dashboard
4. Ensure responsive design for all viewports

**Tests**: Manual browser testing at multiple viewports.

### Phase 8: Integration Testing & Polish (~1h)

1. Run full Go test suite: `make test`
2. Build and verify: `make build && ./bin/synapbus serve`
3. Test all features end-to-end via browser
4. Test analytics API via curl
5. Verify PWA installation and push notifications
6. Test MCP prompts via MCP client
7. Fix any issues found
8. Create git tag v0.7.0

## Complexity Tracking

No constitution violations to justify. All changes align with existing architecture.
