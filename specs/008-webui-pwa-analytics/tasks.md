# Tasks: SynapBus v0.7.0 — WebUI Analytics, PWA, UX Fixes, Website, MCP Prompts

**Branch**: `008-webui-pwa-analytics`
**Plan**: [plan.md](plan.md)
**Generated**: 2026-03-17

## Phase 1: Backend Analytics + Version API

- [x] **T-001**: Create `internal/api/analytics_handler.go` with analytics endpoints (timeline, top-agents, top-channels, summary)
- [x] **T-002**: Create `internal/api/version_handler.go` with GET /api/version endpoint
- [x] **T-003**: Register analytics and version routes in `internal/api/router.go`
- [x] **T-004**: Wire version string from `cmd/synapbus/main.go` to version handler
- [x] **T-005**: Write Go tests for analytics handler (table-driven, various spans, empty data)
- [x] **T-006**: Write Go tests for version handler

## Phase 2: Frontend Analytics Dashboard

- [x] **T-007**: Add analytics and version API client methods to `web/src/lib/api/client.ts`
- [x] **T-008**: Create `web/src/lib/components/AnalyticsChart.svelte` — SVG bar chart with time spans
- [x] **T-009**: Create `web/src/lib/components/TopList.svelte` — ranked list component
- [x] **T-010**: Redesign `web/src/routes/+page.svelte` dashboard with analytics, stat cards, span selector
- [x] **T-011**: Add version footer to `web/src/routes/+layout.svelte` with GitHub link

## Phase 3: UX Fixes — Textarea, Names, Font Size

- [x] **T-012**: Modify `web/src/lib/components/ComposeForm.svelte` — auto-resize textarea (min 3 lines, max 12 lines)
- [x] **T-013**: Add inline display_name editing to `web/src/routes/agents/[name]/+page.svelte`
- [x] **T-014**: Create `web/src/lib/stores/fontSize.ts` — font size store synced with localStorage
- [x] **T-015**: Add display name edit + font size -/+ controls to `web/src/routes/settings/+page.svelte`
- [x] **T-016**: Create `PUT /api/auth/profile` endpoint in Go for human display name editing
- [x] **T-017**: Apply font size CSS custom property in `web/src/routes/+layout.svelte`

## Phase 4: Smart Mention/Channel Highlighting

- [x] **T-018**: Create `web/src/lib/stores/entities.ts` — cached agent/channel lists for mention validation
- [x] **T-019**: Modify `web/src/lib/components/MessageBody.svelte` — smart mention/channel highlighting with inactive badges

## Phase 5: PWA — Manifest, Service Worker, Push Notifications

- [x] **T-020**: Create `schema/012_push_subscriptions.sql` migration
- [x] **T-021**: Create `internal/push/service.go` — VAPID key management, Web Push sending
- [x] **T-022**: Create `internal/push/store.go` — SQLite push subscription CRUD
- [x] **T-023**: Create `internal/api/push_handler.go` — subscribe, unsubscribe, VAPID key endpoints
- [x] **T-024**: Register push routes in `internal/api/router.go` and wire in `cmd/synapbus/main.go`
- [x] **T-025**: Create `web/static/manifest.json` PWA manifest with icons
- [x] **T-026**: Create `web/static/sw.js` service worker (cache-first static, network-only API)
- [x] **T-027**: Register service worker in layout + add push notification toggle to Settings
- [x] **T-028**: Integrate push sending into message delivery for priority >= 7 DMs and @mentions
- [x] **T-029**: Write Go tests for push service and store
- [x] **T-030**: Generate PWA icons (192x192, 512x512)

## Phase 6: MCP Prompts

- [x] **T-031**: Create `internal/mcp/prompts.go` with 4 prompts (daily-digest, agent-health-check, channel-overview, debug-agent)
- [x] **T-032**: Register prompts in `internal/mcp/server.go`
- [x] **T-033**: Write Go tests for MCP prompts

## Phase 7: Website Update

- [ ] **T-034**: Update `~/repos/synapbus-website/` hero and feature messaging for individual/small-team positioning
- [ ] **T-035**: Add/update screenshots showing analytics dashboard and mobile views

## Phase 8: Integration Testing & Polish

- [x] **T-036**: Run full test suite (`make test`), fix any failures
- [x] **T-037**: Build and verify end-to-end (`make build && ./bin/synapbus serve`)
- [ ] **T-038**: Manual verification of all features via browser and curl
- [ ] **T-039**: Create git tag v0.7.0
