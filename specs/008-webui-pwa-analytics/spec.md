# Feature Specification: SynapBus v0.7.0 — WebUI Analytics, PWA, UX Fixes, Website, MCP Prompts

**Feature Branch**: `008-webui-pwa-analytics`
**Created**: 2026-03-17
**Status**: Draft
**Input**: Analytics dashboard with time-series graphs, PWA conversion with push notifications, 6 UX fixes, website messaging update, and MCP prompt additions.

## Assumptions

1. **Chart rendering**: Use a lightweight SVG-based chart approach in Svelte to keep the bundle small and avoid heavy external dependencies.
2. **Push notifications**: Use the Web Push API with VAPID keys. The server generates VAPID keys on first run and stores them in the data directory. Push subscriptions are stored per-user in SQLite.
3. **PWA manifest**: Standard web app manifest with SynapBus branding. Service worker caches static assets and handles offline fallback.
4. **Font size range**: 12px to 24px in 2px increments, default 16px. Stored in localStorage per user.
5. **Version endpoint**: The Go binary embeds the git tag at build time via `-ldflags`. A `/api/version` endpoint exposes it.
6. **Analytics data source**: All analytics are derived from existing message data in SQLite. No new data collection. Aggregation queries run on-demand with reasonable caching (60s TTL).
7. **Agent name editability**: Only the `display_name` field is editable (the `name` field is immutable as it's used for routing).
8. **Website repo**: Located at `~/repos/synapbus-website/`, Astro + Tailwind stack, deployed to Cloudflare Pages.
9. **MCP prompts**: Added as MCP protocol prompt resources, discoverable via `prompts/list` and invocable via `prompts/get`.
10. **Mention/channel validation**: Uses existing API data (agent list, channel list) to validate references at render time, not at send time.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Analytics Dashboard (Priority: P1)

A human owner opens the SynapBus Web UI dashboard and sees an at-a-glance overview of messaging activity: a time-series graph of messages over time, the top 5 most active agents, the top 5 busiest channels, and summary cards showing total channels and agents. They can switch the time span to zoom in on the last hour or zoom out to the last month.

**Why this priority**: The dashboard is currently minimal (recent messages list). Analytics give the human owner visibility into agent activity patterns, helping them identify bottlenecks, inactive agents, and communication hotspots.

**Independent Test**: Navigate to dashboard, verify graph renders with data, switch time spans, confirm top-5 lists and counters update.

**Acceptance Scenarios**:

1. **Given** messages exist in the system, **When** the user navigates to the dashboard, **Then** a time-series bar/line chart displays message count over time with the default span (24h).
2. **Given** the dashboard is displayed, **When** the user clicks a time span button (1h, 4h, 24h, 7d, 1month), **Then** the chart and all statistics update to reflect only that time window.
3. **Given** 10 agents have sent messages, **When** viewing the dashboard, **Then** the "Top 5 Agents" section shows the 5 agents with the most messages in the selected time span, with message counts.
4. **Given** 20 channels have messages, **When** viewing the dashboard, **Then** the "Top 5 Channels" section shows the 5 channels with the most messages in the selected time span, with message counts.
5. **Given** the system has 8 channels and 12 agents, **When** viewing the dashboard, **Then** summary cards show "8 Channels" and "12 Agents".
6. **Given** no messages exist in the selected time span, **When** viewing the dashboard, **Then** the chart shows an empty state with "No messages in this period" and counters show zero.

---

### User Story 2 — PWA with Push Notifications (Priority: P1)

A human owner installs SynapBus as a PWA on their desktop or mobile device. They receive push notifications when high-priority DMs arrive or when agents mention them. The app works offline showing cached data and syncs when connectivity returns.

**Why this priority**: SynapBus is a messaging hub — users need instant awareness of agent activity without keeping a browser tab open. PWA installation provides native-app-like experience on all platforms.

**Independent Test**: Install the PWA on desktop Chrome, verify it launches as standalone app. Send a high-priority DM and verify push notification appears. Disconnect network, verify cached pages still load.

**Acceptance Scenarios**:

1. **Given** a user visits SynapBus in Chrome/Edge/Safari, **When** the browser detects the PWA manifest, **Then** an "Install" prompt is available (browser-native or custom banner).
2. **Given** the user has installed the PWA, **When** they launch it, **Then** it opens as a standalone window with SynapBus branding (no browser chrome).
3. **Given** the user enables push notifications, **When** a DM with priority >= 7 arrives, **Then** a push notification appears with sender name and message preview.
4. **Given** the user enables push notifications, **When** they are @mentioned in a channel, **Then** a push notification appears with channel name and mention context.
5. **Given** the user is offline, **When** they open the PWA, **Then** previously loaded pages render from cache with a "You are offline" indicator.
6. **Given** the PWA is open on a mobile device (< 768px viewport), **When** viewing any page, **Then** the layout adapts responsively (sidebar collapses to hamburger, content fills width).
7. **Given** the PWA is open on a tablet (768px-1024px), **When** viewing any page, **Then** the layout adapts with an appropriate intermediate layout.

---

### User Story 3 — Auto-Resizing Message Textarea (Priority: P1)

A user composes a long message in the ComposeForm. The textarea automatically grows to fit the content. When the text exceeds a maximum height, a scrollbar appears instead of the textarea continuing to grow infinitely.

**Why this priority**: The current fixed-height textarea is too small for multi-line messages, forcing users to scroll within a tiny box. This is the most common UX friction point.

**Independent Test**: Type progressively longer text in the compose form, verify the textarea grows. Paste a very long message, verify scrollbar appears at max height.

**Acceptance Scenarios**:

1. **Given** the compose form is empty, **When** the user starts typing, **Then** the textarea has a minimum height of 3 lines.
2. **Given** the user types multiple lines, **When** the content exceeds 3 lines, **Then** the textarea height grows to fit the content.
3. **Given** the textarea has grown, **When** the content exceeds 12 lines (approximately 240px), **Then** the textarea stops growing and a vertical scrollbar appears.
4. **Given** the user sends the message, **When** the textarea clears, **Then** it shrinks back to the minimum 3-line height.

---

### User Story 4 — Smart Mention/Channel Highlighting (Priority: P2)

When a message body contains @agentname or #channelname, the renderer checks if the referenced entity exists. Existing entities are highlighted as clickable links. Deleted entities show an "inactive" badge. Text that coincidentally contains @ or # symbols but doesn't reference any known entity is rendered as plain text.

**Why this priority**: Current highlighting blindly styles all @/# tokens, which creates confusing UI when entities are deleted or when text naturally contains these symbols (e.g., "issue #42" or "email@example.com").

**Independent Test**: Create a message with @existing-agent, @deleted-agent, @never-existed, #real-channel, #deleted-channel, and #random-text. Verify correct rendering for each case.

**Acceptance Scenarios**:

1. **Given** a message contains "@research-agent" and research-agent exists, **When** rendering, **Then** "@research-agent" is highlighted as a clickable link to the agent page.
2. **Given** a message contains "#bugs-synapbus" and the channel exists, **When** rendering, **Then** "#bugs-synapbus" is highlighted as a clickable link to the channel.
3. **Given** a message contains "@old-agent" and old-agent was deleted, **When** rendering, **Then** "@old-agent" shows with a small "inactive" badge/label.
4. **Given** a message contains "#archived-channel" and the channel was deleted, **When** rendering, **Then** "#archived-channel" shows with a small "inactive" badge/label.
5. **Given** a message contains "@nonexistent" and no agent by that name ever existed, **When** rendering, **Then** "@nonexistent" is rendered as plain text (no highlighting).
6. **Given** a message contains "email@example.com" or "issue #42", **When** rendering, **Then** these are rendered as plain text (not treated as mentions/channels).

---

### User Story 5 — Editable Agent Display Name (Priority: P2)

An owner views an agent's detail page and clicks on the agent's display name to edit it inline. The change is saved and reflected immediately across the UI.

**Why this priority**: Currently agent names can only be set at registration. Owners need to rename agents as their roles evolve.

**Independent Test**: Navigate to agent detail page, click display name, change it, verify it saves and updates in the sidebar/header.

**Acceptance Scenarios**:

1. **Given** a user is on the agent detail page, **When** they click the display name, **Then** it becomes an editable text input pre-filled with the current name.
2. **Given** the user has edited the name, **When** they press Enter or click away, **Then** the new name is saved via API and the UI updates.
3. **Given** the user edits the name to empty, **When** they try to save, **Then** validation prevents saving and shows an error.
4. **Given** the name was changed, **When** viewing the sidebar agent list, **Then** the updated name appears.

---

### User Story 6 — Editable Human Display Name in Settings (Priority: P2)

A human user opens Settings and edits their own display name. The change persists and is reflected in the header and anywhere the user's name appears.

**Why this priority**: Users should be able to personalize their identity in the system.

**Independent Test**: Go to Settings, change display name, verify it updates in the header.

**Acceptance Scenarios**:

1. **Given** the user navigates to Settings, **When** they see the account section, **Then** their current display name is shown in an editable field.
2. **Given** the user changes their display name and clicks Save, **When** the page reloads, **Then** the new name persists.
3. **Given** the user changes their name, **When** viewing the header, **Then** the updated name is shown.

---

### User Story 7 — Font Size Preference (Priority: P3)

A user opens Settings and adjusts the font size using -/+ controls. The change applies immediately across the entire UI and persists between sessions.

**Why this priority**: Accessibility feature — users with different vision needs or screen sizes benefit from adjustable text.

**Independent Test**: Go to Settings, click + to increase font size, verify all text across the app grows. Reload the page, verify the setting persists.

**Acceptance Scenarios**:

1. **Given** the user is on the Settings page, **When** they see the font size section, **Then** a -/+ control shows the current size (default: 16px).
2. **Given** the user clicks +, **When** the font size is less than 24px, **Then** the font size increases by 2px and the entire UI updates immediately.
3. **Given** the user clicks -, **When** the font size is greater than 12px, **Then** the font size decreases by 2px.
4. **Given** the user has set font size to 20px, **When** they close and reopen the app, **Then** the font size is still 20px.
5. **Given** the font size is at the minimum (12px), **When** the user clicks -, **Then** nothing happens (button appears disabled).

---

### User Story 8 — Version Display (Priority: P3)

A user sees the current SynapBus version (git tag) in the Web UI footer, along with a link to the GitHub repository. This helps with troubleshooting and identifying which version is deployed.

**Why this priority**: Essential for debugging but low user-facing value. Simple to implement.

**Independent Test**: Check the footer of any page, verify version string matches the deployed git tag and GitHub link is clickable.

**Acceptance Scenarios**:

1. **Given** SynapBus is built from git tag v0.7.0, **When** viewing any page, **Then** the footer shows "v0.7.0" with a link to the GitHub repository.
2. **Given** SynapBus is built from a commit without a tag, **When** viewing any page, **Then** the footer shows the short commit hash (e.g., "dev-abc1234").
3. **Given** the footer shows a version, **When** the user clicks the version text, **Then** a new tab opens to the GitHub repo releases page.

---

### User Story 9 — MCP Prompts (Priority: P2)

A developer using Claude Code or Gemini CLI with SynapBus MCP discovers pre-built prompts that help them accomplish common tasks: checking agent health, reviewing recent activity, summarizing channel conversations, and getting a daily digest.

**Why this priority**: MCP prompts improve developer UX by providing ready-made workflows for common agent management tasks.

**Independent Test**: Connect an MCP client, call `prompts/list`, verify prompts appear. Invoke a prompt, verify it returns a useful formatted response.

**Acceptance Scenarios**:

1. **Given** an MCP client connects, **When** it calls `prompts/list`, **Then** 3-5 prompts are listed with names and descriptions.
2. **Given** a user invokes the "daily-digest" prompt, **When** it executes, **Then** it returns a summary of recent activity including message counts, active agents, and notable events.
3. **Given** a user invokes the "agent-health-check" prompt, **When** it executes, **Then** it returns the status of all agents including last-seen times and pending message counts.
4. **Given** a user invokes the "channel-overview" prompt, **When** it executes, **Then** it returns a formatted list of channels with member counts and recent activity.

---

### User Story 10 — Website Messaging Update (Priority: P3)

The synapbus.dev website is updated to clearly communicate that SynapBus is a practical solution for individuals and small teams to build local agent networks. The site emphasizes human-agent collaboration through the Web UI on desktop and mobile.

**Why this priority**: Marketing/positioning update. Important for adoption but not a functional feature.

**Independent Test**: Visit synapbus.dev, verify messaging reflects individual/small team use case, agent collaboration narrative, and Web UI screenshots.

**Acceptance Scenarios**:

1. **Given** a visitor lands on synapbus.dev, **When** they read the hero section, **Then** the messaging emphasizes "practical local agent network for individuals and small teams".
2. **Given** the visitor scrolls, **When** they see feature sections, **Then** content highlights: agent collaboration, human-agent interaction via Web UI, desktop/mobile support.
3. **Given** the visitor views screenshots, **When** they see the Web UI images, **Then** screenshots show the analytics dashboard and mobile responsive views.

---

### Edge Cases

- What happens when the analytics time span has zero messages? → Empty state with informative message.
- What happens when push notification permission is denied? → Graceful degradation, no error. A small indicator in Settings shows notifications are disabled.
- What happens when an agent is deleted while viewing its detail page? → Redirect to agents list with a toast notification.
- What happens when the service worker cache is corrupted? → Force re-cache on next online visit.
- What happens when the user sets font size and clears browser storage? → Reverts to default 16px.
- What happens when @mention text contains special regex characters? → Escaping prevents rendering errors (e.g., "@agent++" renders as plain text).
- What happens when version info is not embedded at build time? → Footer shows "dev" as fallback.

## Requirements *(mandatory)*

### Functional Requirements

**Analytics Dashboard**
- **FR-001**: System MUST provide a REST endpoint returning message count aggregated by time bucket for a given time span.
- **FR-002**: System MUST provide a REST endpoint returning top N agents by message count for a given time span.
- **FR-003**: System MUST provide a REST endpoint returning top N channels by message count for a given time span.
- **FR-004**: System MUST provide a REST endpoint returning total agent count and total channel count.
- **FR-005**: Dashboard MUST display a time-series chart of messages with selectable spans: 1h, 4h, 24h, 7d, 1month.
- **FR-006**: Dashboard MUST display top 5 agents and top 5 channels for the selected time span.
- **FR-007**: Dashboard MUST display summary cards for total channels and total agents.

**PWA**
- **FR-008**: System MUST serve a valid web app manifest (manifest.json) with app name, icons, theme color, and display mode "standalone".
- **FR-009**: System MUST register a service worker that caches static assets for offline access.
- **FR-010**: System MUST support Web Push API for sending notifications to subscribed users.
- **FR-011**: System MUST provide endpoints for push subscription management (subscribe/unsubscribe).
- **FR-012**: System MUST send push notifications for DMs with priority >= 7 and @mentions.
- **FR-013**: PWA MUST be responsive across mobile (< 768px), tablet (768-1024px), and desktop (> 1024px) viewports.

**UX Fixes**
- **FR-014**: Compose textarea MUST auto-resize from a minimum of 3 lines to a maximum of 12 lines, then show scrollbar.
- **FR-015**: Agent display_name MUST be editable inline on the agent detail page.
- **FR-016**: Human user display_name MUST be editable on the Settings page.
- **FR-017**: Message renderer MUST validate @mentions and #channels against known entities: highlight existing, badge "inactive" for deleted, plain text for unknown.
- **FR-018**: Settings page MUST provide font size -/+ controls (12px to 24px, 2px steps) that apply globally and persist in localStorage.

**Version & Metadata**
- **FR-019**: System MUST expose a `/api/version` endpoint returning the build version (git tag or commit hash).
- **FR-020**: Web UI footer MUST display the version with a link to the GitHub repository.

**MCP Prompts**
- **FR-021**: MCP server MUST register 3-5 prompt resources discoverable via `prompts/list`.
- **FR-022**: Each prompt MUST return actionable, formatted text when invoked via `prompts/get`.

**Website**
- **FR-023**: Website hero and feature sections MUST communicate individual/small-team positioning and agent collaboration narrative.

### Key Entities

- **PushSubscription**: User ID, endpoint URL, auth key, p256dh key, created timestamp. One user can have multiple subscriptions (multi-device).
- **AnalyticsTimespan**: Enum of supported time windows (1h, 4h, 24h, 7d, 30d) used to parameterize analytics queries.
- **MCPPrompt**: Name, description, argument schema. Registered at server startup.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Dashboard loads and renders analytics within 2 seconds for time spans up to 1 month of data.
- **SC-002**: Users can install the PWA and receive push notifications on desktop Chrome, Edge, and Safari.
- **SC-003**: Compose textarea accommodates messages up to 500 lines without UX degradation.
- **SC-004**: Mention/channel highlighting correctly identifies 100% of existing, deleted, and non-existent entities in test scenarios.
- **SC-005**: Font size preference persists across browser sessions and applies to all text elements.
- **SC-006**: Version information is accurately displayed matching the deployed build.
- **SC-007**: MCP prompts are discoverable and return useful formatted responses for all registered prompts.
- **SC-008**: Website clearly communicates individual/small-team use case to new visitors.
