# Phase 0 Research — Plugin System for SynapBus

**Status**: All unknowns resolved. Decisions below derived from prior brainstorming session + three parallel research agents (HashiCorp, Caddy v2, broader Go plugin landscape — OpenTelemetry Collector, Prometheus SD, Coraza, Cosmos-SDK, etc.).

---

## Decision 1 — Plugin interface shape

**Decision**: Tiny `Plugin` interface (Name, Version, Init) + optional `HasX` capability sub-interfaces detected by type assertion at registration.

**Rationale**:
- Terraform framework's growth from monolithic provider interface → optional sub-interfaces is a cautionary tale in reverse: the decomposition is inevitable.
- OpenTelemetry Collector `component.Component` is this shape and is the gold standard for compile-in Go plugins.
- Plugins pay only for the capabilities they need; a 15-line plugin is a valid plugin.

**Alternatives considered**:
- Monolithic `Plugin` with all methods — rejected: Cosmos-SDK's `AppModule` is living proof this ossifies.
- `Host.RegisterXxx()` imperative registration during `Init` — viable but harder to reason about ordering; capability interfaces make the contract declarative.

---

## Decision 2 — Registration mechanism

**Decision**: Explicit list in `defaultPlugins()` (cmd/synapbus/plugins.go), not `init()` side effects.

**Rationale**:
- OpenTelemetry Collector moved away from `init()` registration because test builds, OSS vs. enterprise distributions, and CI need to supply alternate plugin sets; `init()` makes this painful.
- One line per plugin is trivial ceremony.
- `go test` order-independence is easier to guarantee.

**Alternatives considered**:
- `init()` + blank imports (Caddy style) — rejected for the above reasons; the ergonomic win doesn't outweigh the control loss.
- Hybrid (init-registers + config allow-list) — rejected as unnecessarily two-layered.

---

## Decision 3 — Host API shape

**Decision**: Single `Host` struct passed to `Plugin.Init(ctx, host) error`. Fields are typed accessors for core services. Not globals, not a service locator with string keys.

**Rationale**:
- Vault's `BackendConfig` pattern + Kong's PDK struct both land here.
- Trivially mockable in tests — one `mockHost{}` covers 100% of plugin-side testing.
- Adding a new service means adding a field; no breaking signature change to `Init`.

**Alternatives considered**:
- `Host` as an interface — rejected: interfaces force one-at-a-time mocking and obscure which fields exist. A struct with exported typed fields is more Go-idiomatic for a closed set.
- Global singletons — rejected outright; violates DI and hurts testability.

---

## Decision 4 — Dynamic enable/disable approach

**Decision**: Compile-in all plugins. Enable/disable via YAML config. Runtime toggle triggers SIGHUP-based graceful restart using `cloudflare/tableflip`.

**Rationale**:
- Go's `plugin` package is effectively broken (Linux-only, no unload, deps must match exactly).
- HashiCorp go-plugin (subprocess + gRPC) would require a wire protocol for every extension point, including Web UI panels, which is impractical.
- Wasm (wazero) would constrain plugin authors significantly and adds a whole toolchain.
- Graceful restart with tableflip = ~200–800 ms visible downtime on dev; MCP clients reconnect automatically. This is 99% indistinguishable from real hot-load for the user.

**Alternatives considered**:
- Real hot-reload via Wasm — rejected for toolchain burden.
- Full restart (kill -TERM + re-spawn by systemd) — rejected: > 1 s downtime, in-flight HTTP/SSE requests lost.

---

## Decision 5 — SQL migration ownership

**Decision**: Each plugin owns its own numbered migration chain under `internal/plugins/<name>/schema/`. Core records applied migrations in a new `plugin_migrations (plugin, version)` table. Plugin tables namespaced `plugin_<name>_*`.

**Rationale**:
- Isolation: a plugin's schema lives with its code.
- Re-enable-after-disable works: tables and data survive.
- Namespace prefix prevents collisions and makes a renegade `SELECT * FROM plugin_wiki_articles` visible in reviews.

**Alternatives considered**:
- Single monolithic numbered chain — rejected: couples plugin code to a global numbering, makes alt distributions impossible.
- Per-plugin separate SQLite file — rejected: loses the simplicity advantage of one database handle.

---

## Decision 6 — Web UI panel integration

**Decision**: Each plugin serves its own HTML/JS from `/ui/plugins/<name>/*` backed by its own `embed.FS`. Core shell renders panels as iframes in the existing content pane, driven by the plugin-supplied `PanelManifest`.

**Rationale**:
- Zero build-time coupling between plugin releases and the core Svelte shell — upgrading the wiki doesn't rebuild the shell.
- Security isolation via iframe origin is a free bonus.
- Simple to implement — `http.FS(embed.FS)` with a Chi subrouter.

**Alternatives considered**:
- Dynamic ES module imports of Svelte components from the shell — rejected: requires a manifest merge at build time and couples versions.
- Server-rendered panels into the shell via fetch — rejected: still couples layout/style.

---

## Decision 7 — Config format

**Decision**: YAML (`synapbus.yaml`) at a known path (next to binary or `$SYNAPBUS_DATA_DIR/synapbus.yaml`). Each plugin's sub-tree is passed into its `Host.Config` as `json.RawMessage`; the plugin unmarshals into its typed struct. Optional `HasConfigSchema` lets the Web UI generate a form.

**Rationale**:
- YAML is the prevailing agent-tooling config format (LangChain, AutoGen, Helm, OTel all use YAML).
- `json.RawMessage` is used internally because that's what `yaml.v3` hands back after first-pass decode and Go's JSON-struct-tags are widely known.
- `HasConfigSchema` being optional means "zero-config" plugins still work.

**Alternatives considered**:
- HCL — rejected: adds HashiCorp dep, less familiar to Python-first audience.
- TOML — rejected: less familiar than YAML in agent ecosystem.
- Environment variables only — rejected: typed per-plugin blobs don't fit env-var flat keyspace.

---

## Decision 8 — Testing approach

**Decision**: Ship `internal/plugin/plugintest` package with `NopHost()` (in-memory SQLite + tmpdir) and `Run(t, plugin)` smoke helper.

**Rationale**:
- Copied from OTel Collector `componenttest.NewNopHost`.
- Every plugin gets a ≤ 20-line smoke test for free.
- Mocking the Host struct is the primary testing surface anyway; this formalizes it.

**Alternatives considered**:
- No helper (each plugin invents its own scaffolding) — rejected: produces inconsistent test quality and discourages testing.

---

## Decision 9 — Boundary enforcement

**Decision**: A custom `go/analysis` analyzer runs in CI that rejects imports from `internal/plugins/*` by files outside that subtree (and rejects imports into `internal/plugins/*` from core `internal/*` packages that aren't `internal/plugin*`).

**Rationale**:
- Compile-time enforcement of the architectural invariant.
- Without it, the `internal/` flat layout makes it trivially easy to accidentally cross the line.

**Alternatives considered**:
- `go-arch-lint` — rejected: another dep; a ~50-line custom analyzer is simpler.
- Honor system — rejected: precisely what we're escaping.

---

## Decision 10 — Squash of existing 26 migrations

**Decision**: Generate `000_initial.sql` from the live `.schema` output plus curated seed data for reference rows, after the kubic backup has been verified restorable. Archive legacy migration files under `schema/legacy/` for historical reference; they are not applied.

**Rationale**:
- No external users exist; backward-compat across the 26-step chain is unneeded.
- A single file is ~50× faster to boot and is the new baseline.
- Legacy chain is preserved for human forensic use.

**Alternatives considered**:
- Keep all 26 migrations forever — rejected: user explicitly authorized squash.
- Truncate legacy and delete — rejected: losing schema history is a small-but-real audit cost.

---

## Decision 11 — Failure-notification channel

**Decision**: On plugin `Init` error or `Start` panic: (a) mark the plugin failed in the registry, (b) log the error with `slog`, (c) send a direct message to the owner of the default system agent naming the plugin and the error.

**Rationale**:
- DM integration uses existing messaging; no new ops channel to manage.
- Surfaces plugin problems in the operator's normal workflow (the same inbox they read every day).
- Owner-of-default-agent is always defined and the simplest routing decision.

**Alternatives considered**:
- Dedicated `#plugin-alerts` channel — rejected: over-designed for a single-operator deployment.
- Webhook to external destination — rejected: violates local-first principle and adds config burden.

---

## Decision 12 — Integration test strategy

**Decision**: One `test/integration/plugin_system_test.go` boots the binary with a fixture YAML, waits for readiness, exercises the wiki plugin via `curl` + MCP JSON-RPC, then flips the config, sends SIGHUP, waits for restart, and re-checks. Uses `testcontainers`-free approach — just spawns `./synapbus serve` as a subprocess.

**Rationale**:
- No need for Docker in unit CI.
- End-to-end guarantees the three-phase boot works for real, not just in mocks.

**Alternatives considered**:
- Containerized e2e — rejected: slow, adds infra; this binary is single-file.

---

All Phase 0 unknowns resolved. Proceeding to Phase 1.
