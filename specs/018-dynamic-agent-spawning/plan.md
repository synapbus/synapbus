# Implementation Plan: Dynamic Agent Spawning

**Branch**: `018-dynamic-agent-spawning` | **Date**: 2026-04-14 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/018-dynamic-agent-spawning/spec.md`

## Summary

A human owner types a free-text goal. A pre-built coordinator meta-agent decomposes it into a task tree, proposes spawning specialist sub-agents, and — on human approval — SynapBus materializes the new agents with a `config_hash`-rooted trust ledger, delegates cap-bounded autonomy, runs them on heartbeats, verifies their artifacts, lets them ask the human for missing secrets, and auto-pauses the goal on budget exhaustion. The doc-gardener example is the end-to-end acceptance test: a working `./start.sh && ./run_task.sh --auto-approve && ./report.sh` pipeline that produces a rich HTML report of the run.

Technical approach: extend the existing channels-first SynapBus kernel with a first-class `goals` + `tasks` data model backed by `#goal-<slug>` channels (tasks table is authoritative, channel is the event log). Reuse the migration-015 reactive trigger engine for heartbeats. Promote `system_prompt` out of `harness_config_json`, add `config_hash` / `parent_agent_id` / `spawn_depth` / `autonomy_tier` / `tool_scope` to `agents`, and reactivate the dormant `trust` table (migration 014) as a new `(config_hash, task_domain)`-keyed append-only reputation ledger with read-time rolling rollup. Add `agent_proposals`, `resource_requests`, `secrets` tables. Expose everything as new MCP tools. Ship a new `/goals` page in the Svelte 5 Web UI. Wire the doc-gardener example end-to-end.

## Technical Context

**Language/Version**: Go 1.25+ (per `go.mod`), no CGO, cross-compiled for `linux/amd64` + `darwin/arm64`
**Primary Dependencies**: `mark3labs/mcp-go` (MCP tools), `go-chi/chi` (HTTP), `spf13/cobra` (CLI), `modernc.org/sqlite` (storage), `golang.org/x/crypto/nacl/secretbox` (secret encryption — pure Go, already in ecosystem), existing `SherClockHolmes/webpush-go`, `TFMV/hnsw`, `ory/fosite`
**Storage**: SQLite via `modernc.org/sqlite` — five new migrations (`021_goals_tasks.sql`, `022_agent_proposals.sql`, `023_agent_trust_model.sql`, `024_secrets.sql`, `025_harness_runs_task_id.sql`); existing content-addressable attachment store reused for encrypted secret blobs
**Testing**: Standard `go test` with table-driven unit tests, real-SQLite integration tests (temp dirs), and a full end-to-end subprocess harness test that wires up coordinator + specialist and asserts artifacts. Doc-gardener example itself is a release-quality smoke test.
**Target Platform**: Linux server and macOS developer laptop (same binary)
**Project Type**: Single Go binary with embedded Svelte 5 Web UI — extends existing SynapBus (`cmd/synapbus/`, `internal/`, `web/`, `schema/`)
**Performance Goals**: `/goals` page loads under 1 s for 100 goals × 1000 tasks; atomic task claim survives 100 concurrent contenders; end-to-end doc-gardener demo completes under 5 min on a laptop
**Constraints**: Pure Go (no CGO), single `--data` directory, embedded Web UI via `go:embed`, existing MCP-native agent contract unchanged, backwards-compatible with pre-existing agents (system_prompt extraction migration must be idempotent)
**Scale/Scope**: Targeted at dozens of goals, hundreds of tasks, tens of agents per installation — not a hyperscale system. 5 migrations. 8 new MCP tools. 2 new Web UI pages. ~4 new internal packages (`internal/goals/`, `internal/tasks/`, `internal/trust/`, `internal/secrets/`). 1 new example (`examples/doc-gardener/`).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Evidence |
|---|---|---|
| **I. Local-First, Single Binary** | PASS | All new code lives inside the existing binary. New migrations ship via `go:embed` under `internal/storage/schema/`. Secret encryption uses a local master key file under the existing `--data` dir. No external services introduced. |
| **II. MCP-Native** | PASS | All new agent-facing operations (`create_goal`, `propose_task_tree`, `propose_agent`, `claim_task`, `verify_task`, `request_resource`, `list_resources`, `unquarantine_agent`, `resume_goal`) are MCP tools with JSON Schema. The new Web UI `/goals` page consumes the internal REST API (Principle II allows REST for the embedded UI). No external agent-facing HTTP endpoints are added. |
| **III. Pure Go, Zero CGO** | PASS | All new dependencies are pure Go: `golang.org/x/crypto/nacl/secretbox` for secret encryption (pure Go XSalsa20 + Poly1305), `crypto/sha256` for config hashing (stdlib). No new C bindings. Cross-compile for `linux/amd64` + `darwin/arm64` is a success-criterion (SC-010). |
| **IV. Multi-Tenant with Ownership** | PASS | Every new entity has an owner chain: `goals.owner_user_id`, `tasks` inherit from goal, spawned `agents.parent_agent_id` chain up to a human-owned root, `agent_proposals.proposer_agent_id`, `secrets.scope_type/scope_id`. Delegation cap rule means a child can never exceed its parent's grant. Reputation is keyed by `(owner, config_hash, domain)`. |
| **V. Embedded OAuth 2.1** | PASS | No changes to auth flow. New MCP tools are authenticated via the existing API-key / OAuth-access-token middleware. The new Web UI pages use existing session auth. |
| **VI. Semantic-Ready Storage** | PASS | All new tables live in SQLite via `modernc.org/sqlite`. No new HNSW indexes required in v1. The goal's backing channel inherits existing embedding behavior — messages posted to `#goal-<slug>` get embedded asynchronously if an embedding provider is configured. |
| **VII. Swarm Intelligence Patterns** | PASS | Goal channels are a new concrete use of the existing `blackboard` channel type: system messages are tagged `#task-proposed`, `#task-done`, `#artifact`. The `propose_agent` → `#approvals` → react-to-approve pattern is the existing task-auction pattern applied to agent creation. |
| **VIII. Observable by Default** | PASS | Every task state transition, spawn, proposal, approval, reputation delta, and resource request posts a structured message to the goal's backing channel AND writes a trace row via the existing `internal/trace/` system. OTel trace context is propagated through heartbeat-launched subprocesses (existing behavior from fee73e3, extended to include `task_id`). |
| **IX. Progressive Complexity** | PASS | All pre-existing features (basic messaging, agent registration, channels, triggers) continue to work with zero changes for users who do not create goals. Goals / tasks / spawning are opt-in: an installation with no goals has no new state. The `/goals` page is an additive route. Pre-existing agents are migrated idempotently (extract system_prompt; no data loss). |
| **X. Web UI as First-Class Citizen** | PASS | Two new Svelte 5 pages under `web/src/routes/goals/`: `goals/+page.svelte` (index) and `goals/[id]/+page.svelte` (detail with task tree). Both consume the internal REST API, support dark mode (existing Tailwind tokens), are responsive, and receive real-time updates via the existing SSE channel. The Web UI is rebuilt into `internal/web/dist/` and embedded via `go:embed` (existing pipeline from fee73e3). |

**Result: PASS on all 10 principles. No violations. No complexity-tracking entries needed.**

## Project Structure

### Documentation (this feature)

```text
specs/018-dynamic-agent-spawning/
├── plan.md              # This file
├── spec.md              # Feature specification (already written)
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output — MCP tool JSON Schemas + SQL DDL
│   ├── mcp-tools.md     # One file with all new tool contracts
│   └── sql-ddl.md       # Consolidated DDL for review
├── checklists/
│   └── requirements.md  # Spec quality checklist (already written)
└── tasks.md             # Phase 2 output (/speckit.tasks)
```

### Source Code (repository root)

```text
synapbus/
├── cmd/synapbus/
│   └── cmd_secrets.go            # NEW: `synapbus secrets set/get/list/revoke` cobra subcommands
├── internal/
│   ├── goals/                    # NEW package
│   │   ├── types.go              #   Goal, GoalStatus
│   │   ├── service.go            #   CreateGoal, GetGoal, ListGoals, PauseGoal, ResumeGoal, MarkStuck
│   │   ├── service_test.go       #   Table-driven unit tests
│   │   └── integration_test.go   #   Real-SQLite goal lifecycle + channel auto-create
│   ├── tasks/                    # NEW package
│   │   ├── types.go              #   Task, TaskStatus, VerifierConfig, HeartbeatConfig
│   │   ├── service.go            #   CreateTaskTree, ClaimTask (atomic), TransitionTo, RollupCosts
│   │   ├── service_test.go       #   State machine transitions, ancestry snapshot correctness
│   │   ├── claim_test.go         #   Concurrent-claim integration test (goroutines + real SQLite)
│   │   └── rollup_test.go        #   Recursive-CTE cost rollup test
│   ├── trust/                    # NEW package
│   │   ├── types.go              #   ReputationEvidence, RollingScore, Domain
│   │   ├── hash.go               #   ConfigHash(agent) → sha256 hex
│   │   ├── ledger.go             #   AppendEvidence, RollingScore (with time-decay half-life), Quarantine checks
│   │   ├── delegation.go         #   DelegationCap(parent, proposed) → effective grant + violations
│   │   └── trust_test.go         #   Hash stability, rolling decay, cap enforcement tests
│   ├── secrets/                  # NEW package
│   │   ├── types.go              #   Secret, Scope (user/agent/task)
│   │   ├── store.go              #   NaCl-secretbox encrypt/decrypt, SQLite CRUD, master-key file bootstrap
│   │   ├── injector.go           #   BuildEnv(agent, task) → sanitized env map for harness
│   │   └── store_test.go         #   Encrypt-decrypt roundtrip, scope resolution, env sanitization
│   ├── agents/
│   │   ├── types.go              # EXTEND: ConfigHash, ParentAgentID, SpawnDepth, SystemPrompt, AutonomyTier, ToolScope fields
│   │   ├── service.go            # EXTEND: MaterializeFromProposal, RecomputeHash, Quarantine/Unquarantine
│   │   ├── proposals.go          # NEW: AgentProposalService (propose, approve, reject, materialize)
│   │   └── proposals_test.go     # NEW: Proposal lifecycle + delegation-cap integration tests
│   ├── harness/
│   │   ├── subprocess/
│   │   │   └── runner.go         # EXTEND: inject scoped secrets via secrets.BuildEnv(); write task_id to harness_runs
│   │   └── reactor/
│   │       └── reactor.go        # EXTEND: new wake sources (task_assignment, task_timer, verification_requested); verifier dispatch; budget-gated run launches; post-run cost rollup + soft-alert + auto-pause
│   ├── mcp/
│   │   ├── tools_goals.go        # NEW: create_goal, resume_goal, unquarantine_agent
│   │   ├── tools_tasks.go        # NEW: propose_task_tree, claim_task, verify_task
│   │   ├── tools_spawn.go        # NEW: propose_agent
│   │   ├── tools_resources.go    # NEW: request_resource, list_resources
│   │   └── tools_*_test.go       # NEW: per-tool end-to-end tests using the existing in-process MCP test harness
│   ├── api/
│   │   ├── handlers_goals.go     # NEW: GET /api/goals, GET /api/goals/:id (returns tree + events)
│   │   └── handlers_goals_test.go
│   ├── storage/
│   │   └── schema/
│   │       ├── 021_goals_tasks.sql              # NEW
│   │       ├── 022_agent_proposals.sql          # NEW
│   │       ├── 023_agent_trust_model.sql        # NEW (drops + recreates the dormant trust table)
│   │       ├── 024_secrets.sql                  # NEW
│   │       └── 025_harness_runs_task_id.sql     # NEW
│   └── web/                                     # Re-embedded after Svelte build
├── web/                                         # Svelte 5 sources
│   └── src/routes/goals/
│       ├── +page.svelte                         # NEW: goal index
│       └── [id]/+page.svelte                    # NEW: goal detail + task tree + timeline
└── examples/
    └── doc-gardener/                            # NEW (mirrors examples/cold-topic-explainer)
        ├── start.sh
        ├── stop.sh
        ├── run_task.sh
        ├── report.sh
        ├── auto_approve.sh                      # Helper: polls #approvals, reacts `approve`
        ├── configs/
        │   ├── coordinator.json                 # Coordinator agent config (system_prompt, tool scope, tier)
        │   ├── docs-scanner.json                # Template for the docs-scanner specialist
        │   ├── cli-verifier.json                # Template for the CLI verifier specialist
        │   └── commit-watcher.json              # Template for the commit watcher (defers, stub)
        ├── report.html.tmpl                     # Go text/template for the rich HTML report
        ├── report.go                            # Queries DB, renders template, writes report.html
        ├── README.md
        └── wrapper.sh                           # Subprocess harness entrypoint, per cold-topic-explainer pattern
```

**Structure Decision**: Single Go project with embedded Web UI, as established by the existing SynapBus layout. Four new internal packages (`goals`, `tasks`, `trust`, `secrets`) keep concerns isolated and testable. Existing packages are extended, not replaced. The example lives under `examples/` following the `cold-topic-explainer` pattern exactly.

## Complexity Tracking

> No constitution violations. This section is empty by design.
