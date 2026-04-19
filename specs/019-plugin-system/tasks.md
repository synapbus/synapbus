# Tasks — Plugin System for SynapBus Core

**Feature**: 019-plugin-system
**Branch**: `019-plugin-system`
**Worktree**: `/Users/user/repos/synapbus-plugin-system`

Tasks are organized by the five user stories in `spec.md`. Story priorities:
- **US1** (P1): Operator toggles an optional feature without rebuilding
- **US2** (P1): Plugin author builds a new feature without touching core
- **US3** (P1): Wiki survives the refactor as a fully-functional pilot plugin
- **US4** (P1): Backup-and-squash preserves operator data
- **US5** (P2): Failed plugin does not crash the bus

Scope reminder (FR-031): only the `wiki` plugin is extracted in this feature. The framework must be general enough for the nine follow-ups but we don't build them here.

## Phase 1 — Setup

- [ ] T001 Confirm worktree `/Users/user/repos/synapbus-plugin-system` is on branch `019-plugin-system` and clean; abort if not.
- [ ] T002 Add dev dependencies to `go.mod`: `cloudflare/tableflip`, `gopkg.in/yaml.v3`, `xeipuuv/gojsonschema`; run `go mod tidy` and commit.
- [ ] T003 Create directory skeletons `internal/plugin/`, `internal/plugin/plugintest/`, `internal/plugins/standard/`, `internal/plugins/wiki/schema/`, `internal/plugins/wiki/ui/`, `scripts/`, `docs/plugins/`, `test/integration/`.

## Phase 2 — Foundational (must complete before any user story)

- [ ] T010 Write core interface file `internal/plugin/plugin.go` exposing `Plugin`, `HasMigrations`, `HasMCPTools`, `HasActions`, `HasHTTPRoutes`, `HasWebPanels`, `HasCLICommands`, `HasChannelType`, `HasEventHook`, `HasLifecycle`, `HasConfigSchema`, `HasStability`, `Migration`, `ActionRegistration`, `MCPTool`, `Event`, `PanelManifest`, `ChannelTypeDef`, `Scope` per `contracts/plugin.md`.
- [ ] T011 Write `internal/plugin/host.go` exposing the `Host` struct per `contracts/host.md` including `BaseURL()` and `ExecTx()`.
- [ ] T012 [P] Write `internal/plugin/status.go` with `Status` type (registered|migrated|initialized|started|failed|disabled), `StatusEntry` struct, and thread-safe `StatusStore` that collects transitions and exposes JSON for `/api/plugins/status`.
- [ ] T013 [P] Write `internal/plugin/config.go` that loads `synapbus.yaml`, resolves `$SYNAPBUS_CONFIG_PATH` env var, extracts per-plugin `json.RawMessage`, and rejects unknown plugin names with a pointing error.
- [ ] T014 Write `internal/plugin/registry.go` that constructs a `Registry` from a `[]Plugin` + config, enforces name uniqueness (panic on dup), assigns enabled state, and exposes `Enabled()`, `Get(name)`, `All()`.
- [ ] T015 Write `internal/plugin/migrator.go` that: ensures `plugin_migrations (plugin, version, name, applied_at, checksum)` core table exists; for each enabled plugin implementing `HasMigrations`, applies its unapplied migrations inside one transaction per plugin; records rows with SHA-256 checksum; refuses to apply a previously-applied migration whose checksum differs.
- [ ] T016 Write `internal/plugin/lifecycle.go` that drives the three-phase boot: (1) migrate all enabled plugins, (2) build a per-plugin `Host` with scoped logger/tracer/metrics/secrets/datadir, call `Init`, register all capabilities, on error mark failed and notify owner, (3) for plugins with `HasLifecycle` call `Start` in registration order and record `startedAt`; on `Shutdown` reverse order.
- [ ] T017 Write `internal/plugin/restart.go` using `cloudflare/tableflip`: install SIGHUP handler that calls `upg.Upgrade()`, waits for new process readiness, drains HTTP+SSE, exits. Expose `TriggerRestart()` helper invoked by enable/disable endpoints.
- [ ] T018 [P] Write `internal/plugin/plugintest/nop_host.go`: constructs an in-memory `*sqlx.DB` via `modernc.org/sqlite`, temp `DataDir`, no-op messaging/channels stubs, discard logger, `NopMetrics`, returns `*plugin.Host`.
- [ ] T019 [P] Write `internal/plugin/plugintest/run.go`: `Run(t, plugin)` applies migrations, calls `Init`, if `HasLifecycle` calls `Start` + `Shutdown`, asserts that every declared capability is observable on the host-side registries.
- [ ] T020 [P] Write `internal/plugin/plugintest/assertions.go`: `HasTool(t, reg, "name")`, `HasAction(t, reg, "name")`, `HasRoute(t, reg, "/api/plugins/<plugin>/…")`, `HasMigration(t, host, "plugin", version)`, `HasPanel(t, reg, id)`.
- [ ] T021 Write unit tests `internal/plugin/plugin_test.go`, `registry_test.go`, `migrator_test.go`, `lifecycle_test.go`, `config_test.go`, `status_test.go` covering happy paths, duplicate names, unknown plugin names, migration checksum mismatch, Init failure isolation, plugin without capabilities, config decode error.
- [ ] T022 Add go-analysis boundary-lint tool `tools/arch-lint/main.go`: rejects (a) imports from `internal/plugins/*` in files outside that subtree that are not in `internal/plugin` or `cmd/synapbus`, (b) imports of core packages (other than `internal/plugin`) from within `internal/plugins/*`. Add `make arch-lint` target.
- [ ] T023 Wire `internal/plugin.Registry` into existing core bootstrap in `cmd/synapbus/main.go`: replace hard-coded feature wiring with `registry.InitAll(ctx, host...)` call. Create `cmd/synapbus/plugins.go` with `defaultPlugins()` returning an empty slice for now (wiki added later).
- [ ] T024 Add `/api/plugins/status` handler mounted on the existing internal API router per `contracts/rest.md`. Returns JSON from `StatusStore`. Add `POST /api/plugins/{name}/enable` and `.../disable` (admin-only) that edit `synapbus.yaml` atomically and call `TriggerRestart()`.

## Phase 3 — User Story 4 (P1): Backup and squash before any destruction

**Story goal**: ensure data safety before touching anything else.

**Independent test**: run `scripts/backup-kubic.sh` against a reference SQLite database; load the resulting archive into a scratch dir; diff schemas.

- [ ] T030 [P] [US4] Write `scripts/backup-kubic.sh`: takes `--host`, `--remote-dir`, `--out` flags; performs `sqlite3 .backup`, tars attachments + secrets.key + hnsw.idx, writes `manifest.json` with SHA-256 per entry. Idempotent; refuses to overwrite unless `--force`.
- [ ] T031 [P] [US4] Write `scripts/verify-backup.sh`: takes `--archive` + `--scratch-dir`; extracts archive, opens the DB read-only, prints schema, hashes each manifest entry, diffs against manifest.
- [ ] T032 [P] [US4] Write `scripts/generate-squash.sh`: takes `--source-db` + `--output`; runs `sqlite3 .schema` minus `sqlite_*` tables minus the 26 legacy `schema_migrations` rows, normalizes whitespace, writes to `schema/000_initial.sql` (creating the file). Includes seed INSERTs for any "default admin" rows discovered in core.
- [ ] T033 [US4] Stage the squash: move existing `schema/*.sql` to `schema/legacy/` (keep as reference). Generate `schema/000_initial.sql` locally from `~/repos/synapbus/data/synapbus.db` (the developer's own data). Add an entry `('core', 0, '000_initial', NOW(), <sha256>)` into `plugin_migrations` at first boot so the squash is recorded as applied.
- [ ] T034 [US4] Document in `docs/plugins/migration-notes.md`: how to back up, how to verify, how the squash was generated, how to re-generate if core schema changes before the first real release.

## Phase 4 — User Story 1 (P1): Operator enables/disables plugins

**Story goal**: operator toggles `plugins.wiki.enabled` and sees the change after a graceful restart.

**Independent test**: boot with wiki enabled → wiki present; set enabled=false → SIGHUP → wiki absent; set enabled=true → wiki back with data.

*Depends on Phase 2 foundational work. This story runs in parallel with Phase 5 (wiki extraction) because wiki doesn't exist until Phase 5 — we validate the enable/disable mechanic with a synthetic test plugin first.*

- [ ] T040 [US1] Add `internal/plugin/plugintest/fake_plugin.go`: a fixture `FakePlugin` implementing `HasMigrations`, `HasActions`, `HasHTTPRoutes`, `HasWebPanels` with trivial implementations, used by lifecycle tests.
- [ ] T041 [US1] Write `internal/plugin/lifecycle_enable_test.go`: builds a registry from `[FakePlugin]` with `enabled=true`, asserts capabilities registered, rebuilds with `enabled=false`, asserts none registered, flips back, asserts capabilities return.
- [ ] T042 [US1] Wire `/api/plugins/{name}/enable` and `.../disable` end-to-end: update the YAML file on disk, re-read to verify the edit, trigger `TriggerRestart()`. Unit tests use a tempdir-backed YAML + a stubbed restarter.
- [ ] T043 [US1] Write `synapbus plugin enable <name>` and `synapbus plugin disable <name>` cobra subcommands under `cmd/synapbus/plugin_cli.go` hitting the same code path.
- [ ] T044 [US1] Integration test `test/integration/plugin_toggle_test.go`: builds the binary, spawns it with a fixture YAML containing `fake` enabled, curls `/api/plugins/status` and asserts status=started, curls `/api/plugins/fake/ping` expecting 200, writes YAML with fake disabled, sends SIGHUP, polls `/api/plugins/status` until status=disabled (≤ 2 s), curls `/api/plugins/fake/ping` expecting 404.

## Phase 5 — User Story 3 (P1): Extract wiki as canonical pilot plugin

**Story goal**: the existing wiki feature lives as `internal/plugins/wiki/` and behaves identically from the agent's and operator's point of view.

**Independent test**: restore the developer's existing wiki data (17 articles), bring up the new binary, call `list_articles` / `get_article` via MCP, assert identical contents.

*Depends on Phase 2 foundational work and (optionally) Phase 4 toggle mechanics.*

- [ ] T050 [US3] Read the current `internal/wiki/*.go` implementation; list every exported symbol and every SQL statement. Note the existing table names and FK/index dependencies.
- [ ] T051 [US3] Create `internal/plugins/wiki/schema/001_initial.sql` creating `plugin_wiki_articles` and `plugin_wiki_backlinks` matching the current wiki schema, with `plugin_wiki_` prefix and identical column definitions. Add the CTEs and indexes used by the existing queries.
- [ ] T052 [US3] Create `internal/plugins/wiki/store.go`: a thin layer over `plugin.Host.DB` with the same query set as `internal/wiki/store.go`, but against `plugin_wiki_*` tables. Port unit tests from `internal/wiki/store_test.go`.
- [ ] T053 [US3] Create `internal/plugins/wiki/plugin.go`: `WikiPlugin` struct, `Name/Version/Init`, `Migrations()` embedding the SQL, `Actions()` returning `create_article`, `get_article`, `list_articles`, `update_article`, `get_backlinks` with identical argument schemas and output shapes as the current bridged actions. Each action delegates to `store.go`.
- [ ] T054 [P] [US3] Create `internal/plugins/wiki/ui/index.html`: a minimal self-contained page that fetches `/api/plugins/wiki/articles` and renders a list + markdown preview. Uses vanilla HTML + fetch + [Markdown-it CDN or embedded]. No Svelte build step required.
- [ ] T055 [P] [US3] Create `internal/plugins/wiki/routes.go`: REST routes under `/api/plugins/wiki/` — `GET /articles` (list), `GET /articles/{slug}` (detail), to back the UI panel. Auth via existing session middleware.
- [ ] T056 [P] [US3] Create `internal/plugins/wiki/panel.go`: `WebPanels()` returns one `PanelManifest` (id=wiki, Route=/ui/plugins/wiki); `PanelHandler()` serves the embedded `index.html` and static assets via `http.FS`.
- [ ] T057 [US3] Write `internal/plugins/wiki/plugin_test.go`: `plugintest.Run(t, wiki.New())` smoke test; action-level tests for each of the five MCP actions against a `NopHost`; fixture-backed test that inserts 3 articles and verifies backlink computation.
- [ ] T058 [US3] Add `wiki.New()` to `cmd/synapbus/plugins.go`'s `defaultPlugins()` slice. Add `plugins: { wiki: { enabled: true } }` to the default generated `synapbus.yaml`.
- [ ] T059 [US3] Decommission `internal/wiki/`: delete the old package, the bridged action registrations in the legacy MCP bridge, and the `wiki_articles` table creation in the old migrations (already removed by squash). Update any core imports pointing to it (should be none once the arch-lint passes).
- [ ] T060 [US3] Import the developer's own existing wiki articles from `/Users/user/repos/synapbus/data/synapbus.db` into the refactored schema as part of the squash seed; on next boot the articles are visible via the new plugin. Scripted via `scripts/import-legacy-wiki.sh` (idempotent).
- [ ] T061 [US3] Integration test `test/integration/wiki_plugin_test.go`: boots binary with wiki enabled, curls `/api/plugins/wiki/articles` and expects the imported articles, calls MCP `list_articles`, `get_article`, `create_article`, `update_article`, `get_backlinks`, asserts correct responses.

## Phase 6 — User Story 5 (P2): Failure isolation

**Story goal**: a broken plugin does not break core.

**Independent test**: start with `[wiki, broken]`, observe wiki works and broken is listed as failed with the error, owner receives a DM.

- [ ] T070 [US5] Add `internal/plugin/plugintest/broken_plugin.go`: a fixture `BrokenPlugin` whose `Init` returns an explanatory error OR panics (two variants), used only in tests.
- [ ] T071 [US5] Extend `internal/plugin/lifecycle.go` to: `recover()` around each `Init` call, `recover()` around each `Start` goroutine, mark plugin failed with error message, post a DM to `Host.DefaultOwner` citing plugin name + error, emit `plugin.status.changed` event.
- [ ] T072 [US5] Unit test `internal/plugin/lifecycle_failure_test.go`: registers `[FakePlugin, BrokenPlugin]`, calls `InitAll`, asserts fake is started, broken is failed, status store carries both, a fake-messaging.Bus records exactly one DM containing "broken".
- [ ] T073 [US5] Integration test `test/integration/plugin_failure_test.go`: boots binary with fixture `broken` plugin enabled (reusing the BrokenPlugin fixture; conditional `//go:build integration`); curls `/api/plugins/status`, asserts broken is failed with explanatory error, curls wiki endpoints and asserts they still work.

## Phase 7 — User Story 2 (P1): Plugin author onboarding

**Story goal**: a contributor can write a new plugin in under 20 minutes following `quickstart.md`.

**Independent test**: create the `hello` plugin per `quickstart.md`, run `go test`, boot, curl.

- [ ] T080 [P] [US2] Verify `quickstart.md` against the real package by implementing the `hello` plugin exactly as written into a scratch directory (`_examples/hello/`), running `plugintest.Run`, confirming it passes. Check every code fence compiles without edits.
- [ ] T081 [P] [US2] Write `docs/plugins/authoring.md` — step-by-step narrative version of `quickstart.md` with rationale at each step. Link to `contracts/plugin.md` and `contracts/host.md`.
- [ ] T082 [P] [US2] Write `docs/plugins/lifecycle.md` — sequence diagram of migrate → init → start → shutdown with call-order guarantees.
- [ ] T083 [P] [US2] Write `docs/plugins/capabilities.md` — one section per `HasX` interface with a minimal example snippet.
- [ ] T084 [US2] Add template generator `scripts/new-plugin.sh <name>`: scaffolds `internal/plugins/<name>/{plugin.go, schema/001_initial.sql, ui/index.html, plugin_test.go}` from a heredoc template.
- [ ] T085 [US2] Verify once more: run the generator for "greeter", `go test ./internal/plugins/greeter/...`, passes without edits. Delete the scaffold afterward.

## Phase 8 — End-to-End Verification

- [ ] T090 Run `make build` — binary compiles cleanly.
- [ ] T091 Run `go test ./...` — all tests pass including new `internal/plugin/...`, `internal/plugins/wiki/...`, `test/integration/...` (with `go test -tags=integration ./test/integration/...`).
- [ ] T092 Run `make arch-lint` — boundary invariants hold.
- [ ] T093 Run `make lint` — no new lint issues.
- [ ] T094 Boot the binary with the default `synapbus.yaml` (wiki enabled); verify via browser:
    - `/ui/` loads the shell and shows the wiki panel in nav.
    - `/ui/plugins/wiki` renders the article list.
    - `/api/plugins/status` shows wiki=started.
- [ ] T095 Boot the binary, then `curl -X POST -H 'admin-token' /api/plugins/wiki/disable`; observe graceful restart in logs (≤ 2 s); `/ui/plugins/wiki` returns 404; `/api/plugins/status` shows wiki=disabled; `curl -X POST .../enable`; restart; wiki back; articles preserved.
- [ ] T096 Exercise MCP actions directly from Claude Code: call `mcp__synapbus__execute call("list_articles", {})`; call `get_article({slug: "synapbus-architecture"})`; assert expected shapes.
- [ ] T097 Chrome-in-Claude smoke test: navigate to `http://localhost:8080/ui/plugins/wiki`, assert the article list renders (at least one article visible), click on an article, assert body is shown.
- [ ] T098 SC measurement: time a graceful restart end-to-end (SIGHUP dispatch → new-process readiness); record in `autonomous_summary.md`; assert < 2 s.

## Phase 9 — Polish

- [ ] T099 Update `README.md` and `CLAUDE.md`: note the plugin directory (`internal/plugins/<name>`), the quickstart link, the arch-lint rule.
- [ ] T100 Update `docs/plugins/README.md` linking authoring / lifecycle / capabilities / migration-notes.
- [ ] T101 Archive `internal/wiki/` and the legacy schema directory under `docs/legacy/` as reference.
- [ ] T102 Write `autonomous_summary.md` in repo root with: shipped features, test results, SC measurements, open follow-ups (9 remaining plugin extractions).
- [ ] T103 Open a draft PR description summarizing the feature from operator, author, and maintainer perspectives.

## Dependencies

```
Setup (T001-T003)
  └── Foundational (T010-T024)
         ├── US4 Backup (T030-T034)               [parallel with others, but T033 blocks US3 import]
         ├── US1 Toggle (T040-T044)                [uses FakePlugin, independent of wiki code]
         ├── US3 Wiki   (T050-T061)                [needs squashed schema from US4 T033/T060]
         ├── US5 Failure (T070-T073)               [uses BrokenPlugin, independent]
         └── US2 Onboarding docs (T080-T085)       [independent]
                     └── Verification (T090-T098)
                             └── Polish (T099-T103)
```

## Parallel execution opportunities

Within each user story the `[P]` tasks can be worked on concurrently by subagents:
- US3: T054, T055, T056 (UI asset, routes, panel manifest) can go in parallel once T052 store layer lands.
- US4: T030, T031, T032 scripts are all independent.
- US2: T080, T081, T082, T083 docs are all independent.
- Foundational: T012, T013 (status, config) can land in parallel with T010/T011 (interfaces/host).

## MVP scope

**Minimum shippable increment**: Setup + Foundational + US3 (wiki extraction) + US1 enable/disable.
- US4 backup is P1 but runs against the operator's local data only; it MUST precede the schema squash but doesn't change the code that ships.
- US5 failure isolation is P2 — absence doesn't block merging; presence is what makes the framework production-safe.
- US2 onboarding is docs; can slip to a follow-up PR if time pressure demands.

## Format validation

All tasks above follow: `- [ ] T### [P]? [US#]? Description with path`. Setup and foundational have no story label. Story tasks carry [US1]..[US5]. Polish tasks have no story label. Every implementation task names a concrete file path.
