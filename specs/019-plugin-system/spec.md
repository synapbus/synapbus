# Feature Specification: Plugin System for SynapBus Core

**Feature Branch**: `019-plugin-system`
**Created**: 2026-04-19
**Status**: Draft
**Input**: User description: "Plugin system for SynapBus core: compile-in Plugin interface with HasX capability sub-interfaces, typed Host struct, explicit registration via defaultPlugins() in main.go, enable/disable via synapbus.yaml with SIGHUP graceful restart, three-phase boot (Migrate/Init/Start), per-plugin failure isolation, plugintest helpers. Scope: Phase 0 backup+squash, Phase 1 plumbing, Phase 2 extract wiki as pilot."

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Operator toggles an optional feature without rebuilding (Priority: P1)

A SynapBus operator decides their deployment does not need the wiki feature. They edit `synapbus.yaml`, set `plugins.wiki.enabled: false`, and either save via the Web UI settings page or run `synapbus plugin disable wiki`. The server performs a graceful restart (under one second of visible downtime). After restart, the wiki's MCP actions, REST routes, and Web UI panel are no longer visible anywhere; agents that attempt to call wiki actions receive a clean "action not found" diagnostic. When the operator re-enables the plugin, the wiki returns with all existing data intact.

**Why this priority**: This is the core promise of the plugin system to operators. Without it there is no observable difference from the current monolithic build.

**Independent Test**: Start the server with wiki enabled; verify wiki panel, tools, and routes are present. Disable via config and restart. Verify they disappear. Re-enable and verify everything comes back with state preserved.

**Acceptance Scenarios**:

1. **Given** the server is running with wiki enabled and has 3 articles stored, **When** the operator sets `plugins.wiki.enabled: false` and triggers graceful restart, **Then** within 2 seconds the wiki panel is gone from the Web UI, `/api/plugins/wiki/*` returns 404, and wiki MCP actions return "action not found"; the 3 articles remain in the database for later re-enable.
2. **Given** wiki is disabled, **When** the operator re-enables it and restarts, **Then** the panel and all actions return and the 3 articles are accessible.
3. **Given** a plugin's `Init` panics, **When** the server boots, **Then** core stays up, the failed plugin is marked failed in `/api/plugins/status`, and a direct message is sent to the agent owner's inbox naming the failing plugin and the error.

---

### User Story 2 — Plugin author builds a new feature without touching core (Priority: P1)

A contributor wants to add a new feature (for example, a "linear-issues" channel type). They create a new directory under `internal/plugins/<name>/`, implement the `Plugin` interface plus whichever `HasX` capability interfaces they need, and add one line to `defaultPlugins()` in the main entry point. They run the shipped `plugintest` harness, which provides a `NopHost` backed by an in-memory database. They write a smoke test that exercises their plugin's lifecycle end-to-end without needing any real infrastructure. No core code is modified.

**Why this priority**: This is the scope-creep-prevention promise to maintainers. Without it the plugin system exists only on paper.

**Independent Test**: Create a minimal trivial plugin under `internal/plugins/demo/`, add it to `defaultPlugins()`, run `go test ./internal/plugins/demo/...`, verify it passes. Boot the binary and verify the demo plugin's MCP action is callable.

**Acceptance Scenarios**:

1. **Given** the plugin skeleton is in place, **When** a contributor writes a plugin that returns one MCP tool, one REST route, and one migration, **Then** the standard `plugintest.Run(t, plugin)` helper verifies all three are registered without boot ceremony.
2. **Given** a contributor writes a plugin that does not need any capability, **When** they implement only `Plugin.Name()`, `Plugin.Version()`, and `Plugin.Init()`, **Then** the code compiles and runs; unused `HasX` interfaces are not required.

---

### User Story 3 — Wiki survives the refactor as a fully-functional pilot plugin (Priority: P1)

The operator's existing wiki feature (17 articles in production-like use) continues to work identically after it has been extracted into a plugin. MCP actions (`create_article`, `get_article`, `list_articles`, `update_article`, `get_backlinks`) behave identically. The Web UI panel is served from the plugin's own route and the shell displays it in the navigation. SQL tables are renamed `plugin_wiki_articles` with no data loss. Agents that currently use the wiki see no change in behavior.

**Why this priority**: This is the canonical proof that the plugin framework accommodates a real feature with storage, MCP, UI, and REST surface. If the wiki cannot be extracted, none of the remaining nine extractions can be either.

**Independent Test**: Restore the live kubic backup snapshot into a scratch database, run the squash migration, run the wiki plugin's migration, verify all 17 articles are readable via `mcp__synapbus__execute call('list_articles', {})` and `mcp__synapbus__execute call('get_article', { slug: 'synapbus-architecture' })`.

**Acceptance Scenarios**:

1. **Given** the kubic backup snapshot is restored, **When** the new binary is started, **Then** the 17 articles are accessible via the wiki MCP actions and the Web UI panel.
2. **Given** the server is running, **When** an agent creates a new article via `call('create_article', …)`, reads it back, updates it, and fetches its backlinks, **Then** each operation returns correct results.
3. **Given** wiki is enabled, **When** an operator visits `/ui/plugins/wiki` in a browser, **Then** the shell renders the wiki panel with the article list.

---

### User Story 4 — Backup-and-squash preserves operator data (Priority: P1)

Before any destructive refactor touches SynapBus deployments, the operator backs up their kubic instance (database, attachments, secrets key, vector index). The squashed `000_initial.sql` is generated from the current schema dump and verified to load the backup cleanly into a scratch database. The refactor does not start until the backup is verified.

**Why this priority**: The operator has personal data and agent experiments in the live instance. Data loss is unrecoverable.

**Independent Test**: Run the shipped backup script against a reference SQLite database; verify the resulting archive is reloadable and produces an identical schema to the live DB.

**Acceptance Scenarios**:

1. **Given** the backup script is run against a live instance, **When** the script completes, **Then** an archive exists containing the SQLite file, attachments directory, secrets key, and vector index with a manifest listing SHA-256 of each.
2. **Given** the squashed `000_initial.sql` is generated, **When** it is applied to an empty database, **Then** the resulting schema matches the live schema and the backup data loads without error.

---

### User Story 5 — Failed plugin does not crash the bus (Priority: P2)

When a buggy or misconfigured plugin fails during its initialization phase, the core messaging bus continues to operate. Other enabled plugins still start. The failing plugin is marked failed in a status endpoint and the agent owner is notified via an in-band direct message so the problem is visible in the normal operator workflow.

**Why this priority**: Failure isolation is the justification for the plugin boundary. Without it, plugins are merely a code-organization choice with no operational value.

**Independent Test**: Install a deliberately-broken test plugin alongside the wiki plugin; start the server; verify wiki still works and the broken plugin is reported as failed with an explanatory message.

**Acceptance Scenarios**:

1. **Given** two plugins are enabled and one throws during `Init`, **When** the server boots, **Then** the healthy plugin's surfaces are registered and usable, the failing plugin's surfaces are absent, and `/api/plugins/status` lists the failing plugin with its error.

---

### Edge Cases

- A plugin declares a migration with the same filename as another plugin's migration. The boot process MUST detect this at registration and refuse to start with a named collision error.
- A plugin is disabled mid-operation while an agent is holding a valid MCP tool call for one of its tools. The graceful restart drains in-flight calls before the new process takes over; the draining agent receives a normal response, the next call to that tool after restart returns a clean "not registered" diagnostic.
- The configuration file is invalid (unparseable YAML or references a non-existent plugin name). The server refuses to start and prints a pointing error; it does not silently ignore unknown keys.
- An operator enables a plugin whose stability level is `experimental`. The server emits a visible warning in the log and in `/api/plugins/status` but proceeds.
- The Web UI panel of a disabled plugin is requested directly by URL. The core shell returns 404 with a user-friendly "plugin not enabled" page.
- A plugin's background goroutine panics during `Start`. The recovery logs the panic, marks the plugin failed, and does not kill the host process.
- A plugin attempts to read another plugin's tables directly by name. Code review and tests catch this; core SQL schema policy namespaces all plugin tables as `plugin_<name>_*` and provides no cross-plugin table access.
- Graceful restart is triggered during a long-running harness job. The job's state in SQLite survives; the poller resumes tracking it after the new process takes over.

## Requirements *(mandatory)*

### Functional Requirements

**Registration and discovery**

- **FR-001**: System MUST provide a minimal `Plugin` interface requiring only a stable name, a version, and an initialization hook. Plugins MUST NOT be required to implement any other method.
- **FR-002**: System MUST offer a set of optional capability interfaces (`HasMigrations`, `HasMCPTools`, `HasActions`, `HasHTTPRoutes`, `HasWebPanels`, `HasCLICommands`, `HasChannelType`, `HasEventHook`, `HasLifecycle`, `HasConfigSchema`) that plugins implement only if they need the corresponding extension point.
- **FR-003**: The set of compiled-in plugins MUST be declared explicitly in the program entry point. The system MUST NOT rely on package-init side effects for plugin registration.
- **FR-004**: Registering two plugins with the same name MUST fail fast with a clear error before any plugin is initialized.

**Host capabilities exposed to plugins**

- **FR-005**: Each plugin MUST receive a typed host handle during initialization that exposes: a logger pre-tagged with the plugin name, a shared database handle, the messaging API, the channels API, the attachments store, the search index, a secrets accessor scoped to the calling plugin, an event bus, its own portion of configuration as an untyped blob to be decoded by the plugin, a guaranteed-to-exist per-plugin data directory under the main data directory, a tracer, and a metrics registerer.
- **FR-006**: Plugins MUST NOT reach into core internal packages directly. The host handle MUST be the only approved channel for core capabilities.
- **FR-007**: The secrets accessor given to a plugin MUST return only secrets scoped to that plugin. A plugin MUST NOT be able to read another plugin's secrets through the host.

**Lifecycle**

- **FR-008**: Server boot MUST proceed in three phases applied to all enabled plugins in order: first, migrations are collected from plugins implementing `HasMigrations` and applied within a single transaction per plugin; second, `Init(host)` is called on each plugin; third, plugins that implement `HasLifecycle` have their `Start(ctx)` called.
- **FR-009**: Shutdown MUST call `Shutdown` on plugins that implement `HasLifecycle` in the reverse order of `Start`.
- **FR-010**: A plugin's migrations MUST be namespaced by the plugin's name; each plugin's tables MUST have the prefix `plugin_<name>_`.
- **FR-011**: Migration tracking MUST record which migrations of which plugin have been applied, so re-runs are idempotent.

**Enable / disable**

- **FR-012**: A machine-readable configuration file (`synapbus.yaml`) MUST allow each plugin to be enabled or disabled and MUST carry each plugin's typed configuration.
- **FR-013**: A disabled plugin MUST NOT run migrations, MUST NOT receive `Init`, MUST NOT register any MCP tool, action, REST route, Web UI panel, CLI subcommand, channel type, or event subscription, and MUST NOT start background goroutines.
- **FR-014**: A disabled plugin's previously-created database tables MUST remain in place, so that re-enabling the plugin recovers its state.
- **FR-015**: Operators MUST be able to toggle enable/disable without recompiling the binary; the change takes effect after a graceful restart.

**Graceful restart**

- **FR-016**: The system MUST support SIGHUP-triggered graceful restart that preserves listening sockets. In-flight HTTP and SSE requests MUST be drained before the old process exits. Visible downtime on a developer-class machine MUST be under two seconds.
- **FR-017**: MCP clients that use the long-poll / streamable-HTTP transport MUST reconnect transparently across restart; the operator MUST NOT need to restart clients.
- **FR-018**: Long-running background jobs with durable state (harness runs) MUST survive graceful restart because their state lives in the database.

**Failure isolation**

- **FR-019**: A plugin's `Init` failure MUST NOT prevent the core or other plugins from starting.
- **FR-020**: A plugin's `Start`-goroutine panic MUST be recovered; the panic MUST be logged, the plugin MUST be marked failed, and the host process MUST continue running.
- **FR-021**: When a plugin enters the failed state the system MUST send a direct message to the human owner of the default agent naming the plugin and the error, and MUST expose the failure on a status endpoint (`/api/plugins/status`).

**Extension point semantics**

- **FR-022**: MCP tools contributed by a plugin MUST be namespaced to avoid accidental collisions; the system MUST reject duplicate tool names at registration.
- **FR-023**: Bridged actions contributed by a plugin MUST carry a required authorization scope (`read`, `write`, or `admin`) that the host checks against the caller's token before dispatch.
- **FR-024**: Web UI panels contributed by a plugin MUST be served from a plugin-owned route under `/ui/plugins/<name>`; the core shell MUST render them without requiring a shell rebuild.
- **FR-025**: REST routes contributed by a plugin MUST live under `/api/plugins/<name>` to keep the boundary visible.
- **FR-026**: Channel-type plugins MUST be able to hook `OnMessage` and `OnReaction` for channels of their declared type without modifying core channel handling.

**Testing**

- **FR-027**: The system MUST ship a `plugintest` helper package that provides a `NopHost` backed by an in-memory database and filesystem so plugins can be unit-tested in complete isolation.
- **FR-028**: The `plugintest` package MUST provide a one-line smoke-test helper that exercises a plugin's full lifecycle and asserts all capability registrations are visible.

**Data safety**

- **FR-029**: Before any destructive refactor runs against a live instance, the backup script MUST produce a verifiable archive of the SQLite database, the attachments directory, the secrets master key, and the vector index, with a manifest containing a SHA-256 hash of each.
- **FR-030**: The squashed initial migration MUST reproduce the live schema exactly when applied to an empty database; a verification step MUST load the backup into a scratch database and diff the resulting schemas.

**Scope boundary**

- **FR-031**: Within this feature's scope, the only plugin actually extracted from core is the wiki plugin, proving the framework. The other nine candidates (webhooks, push, trust, marketplace, runners subprocess/docker/k8s, goals, auction+blackboard channel types, reactive triggers) are follow-up work.

### Key Entities *(include if feature involves data)*

- **Plugin**: A self-contained optional feature identified by a unique name and version. Has a set of declared capabilities, a configuration struct, a set of migrations, and a lifecycle.
- **Plugin Registry**: The core-owned catalog of all compiled-in plugins. Records each plugin's factory function, enabled state, stability level, and runtime status (registered, migrated, initialized, started, failed).
- **Host**: The bundle of core services handed to a plugin during initialization. Mediates every interaction between a plugin and core.
- **Plugin Migration**: A named, numbered, idempotent schema change owned by one plugin. Records in `plugin_migrations` after application.
- **Plugin Status Entry**: A record of a plugin's health (running, disabled, or failed with an error message) exposed via a read-only API and used by the failure-notification path.
- **Backup Archive**: A timestamped file tree containing database file, attachments directory, secrets key, vector index, and a manifest with hashes, created by the pre-refactor backup script.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can toggle the wiki plugin's enabled state by editing one line of configuration and triggering a graceful restart; the change is visible in the Web UI and MCP surface within 2 seconds of the restart signal.
- **SC-002**: A plugin author can add a new plugin by creating a single directory with a handful of Go files and one line in `defaultPlugins()`; no change to core code is required, and a basic plugin template compiles and passes `plugintest.Run` within 20 minutes of starting.
- **SC-003**: All MCP actions exposed by the wiki (`create_article`, `get_article`, `list_articles`, `update_article`, `get_backlinks`) produce identical results before and after the extraction when run against the same restored backup.
- **SC-004**: When one of two enabled plugins deliberately throws during initialization, the healthy plugin remains fully functional and a notification about the failure appears in the owner's inbox within 5 seconds of boot completion.
- **SC-005**: The backup archive taken from a reference database can be reloaded into an empty instance and produces an identical row count across all core tables and 100% of wiki articles.
- **SC-006**: No plugin can read another plugin's secrets through the host API — a test that attempts the cross-plugin access returns a permission error.
- **SC-007**: The core of the system (as defined by files outside `internal/plugins/`) does not import anything from `internal/plugins/` — a compile-time lint enforces this.
- **SC-008**: Graceful restart completes in under 2 seconds on the developer machine used for testing, measured from SIGHUP dispatch to readiness of the new process.
- **SC-009**: A plugin's `Init` is called at most once per process lifetime; a lifecycle trace of a 60-second run shows exactly one `Init` and one matching `Shutdown` for every enabled plugin.
- **SC-010**: Running the full test suite (`go test ./...`) completes without regressions; all plugin package tests pass; the wiki's integration test exercises the full stack using `plugintest`.

## Assumptions

These decisions were made without user clarification and are documented here so the planning phase does not re-litigate them:

1. **Plugin authorship is first-party only.** No third-party plugin loader, no runtime plugin discovery from disk, no `.so` loading, no Wasm. Plugins live in the repo and are compiled in.
2. **Core boundary is "Pragmatic core"** — messaging, channels, reactions, auth, storage, MCP transport, search, attachments, and the Web UI shell stay in core. Everything else is a candidate for extraction. Only the wiki is extracted in this feature.
3. **Enable/disable requires graceful restart.** True hot-load of plugins in a running process is not required; the ~1-second SIGHUP restart is adequate for operator UX.
4. **Explicit list over `init()` registration.** The program entry point declares the plugin set directly, so custom distributions, test builds, and CI can construct alternate plugin lists without build tags.
5. **Configuration format is YAML** at a single known path (`synapbus.yaml` next to the binary or under the data directory). Operators may override the location via an environment variable.
6. **Plugin status is exposed on a read-only REST route** (`/api/plugins/status`) and visible in the Web UI's existing settings page. No dedicated admin console is built as part of this feature.
7. **The 26 existing migrations are squashed to a single `000_initial.sql`** based on a current schema dump from the live instance. The squash is verified by restoring the backup into a scratch database and diffing the schemas.
8. **The only extracted plugin in this feature is `wiki`.** The remaining nine candidates are mechanical follow-ups that reuse the framework without further design.
9. **Web UI panels are route-owned.** Each plugin serves its own HTML and embedded assets from its own route; the core Svelte shell renders them in a content pane via an iframe or dynamic load. No build-time coupling between plugin and shell.
10. **Cross-plugin dependencies are not supported in this feature.** If a future plugin needs another plugin's data, it must go through the host APIs, not through direct tables.
11. **Failure-notification owner is the default agent's owner user.** The system does not attempt richer routing (on-call rotations, webhooks) for failure notifications in this feature.
12. **Stability level metadata** (stable / beta / experimental) is a field on the plugin but has no functional gating beyond a log warning; in this feature every plugin defaults to `stable` except any deliberately-broken test plugins used for failure-isolation tests.
