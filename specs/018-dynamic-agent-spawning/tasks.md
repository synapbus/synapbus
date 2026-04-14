---
description: "Task list for Dynamic Agent Spawning"
---

# Tasks: Dynamic Agent Spawning

**Input**: Design documents from `/specs/018-dynamic-agent-spawning/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: Included — the spec mandates table-driven unit tests, service-layer integration tests, and end-to-end MCP tests (success criteria SC-004, SC-005, SC-006, SC-007, SC-008, SC-009, SC-012).

**Organization**: Tasks are grouped by user story. Setup and Foundational phases run first; US1 is the MVP slice.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1–US9 from spec.md)
- File paths are absolute-from-repo-root

## Path Conventions

- **Backend code**: `internal/<package>/` and `cmd/synapbus/`
- **Schema**: `internal/storage/schema/`
- **Web UI source**: `web/src/`
- **Tests**: co-located `_test.go` files in the same package
- **Example**: `examples/doc-gardener/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create new packages and migration files; wire through the build. No behavior yet.

- [ ] T001 Create empty Go packages with `doc.go` files at `internal/goals/doc.go`, `internal/tasks/doc.go`, `internal/trust/doc.go`, `internal/secrets/doc.go`
- [ ] T002 [P] Create migration files (empty DDL, just table shells per data-model.md) at `internal/storage/schema/021_goals_tasks.sql`, `internal/storage/schema/022_agent_proposals.sql`, `internal/storage/schema/023_agent_trust_model.sql`, `internal/storage/schema/024_secrets.sql`, `internal/storage/schema/025_harness_runs_task_id.sql`
- [ ] T003 [P] Create example scaffolding at `examples/doc-gardener/` mirroring `examples/cold-topic-explainer/` — copy `start.sh`, `stop.sh`, `run_task.sh`, `wrapper.sh`, `README.md` with doc-gardener placeholders
- [ ] T004 [P] Add `golang.org/x/crypto/nacl/secretbox` to `go.mod` via `go get` and verify the pure-Go build still works (`CGO_ENABLED=0 go build ./...`)
- [ ] T005 Create `specs/018-dynamic-agent-spawning/BUILD_NOTES.md` with the developer runbook (build commands, test commands, example commands) for the whole feature

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Migrations, base types, and the trust primitive that every user story depends on.

- [ ] T010 Fill migration 021 DDL (goals + tasks tables, indexes, state CHECK constraints) in `internal/storage/schema/021_goals_tasks.sql` per data-model.md
- [ ] T011 Fill migration 022 DDL (agent_proposals + resource_requests tables) in `internal/storage/schema/022_agent_proposals.sql` per data-model.md
- [ ] T012 Fill migration 023 DDL (drop+recreate trust table; ALTER agents with config_hash/parent_agent_id/spawn_depth/system_prompt/autonomy_tier/tool_scope_json/quarantined_at; reputation_evidence table) in `internal/storage/schema/023_agent_trust_model.sql`
- [ ] T013 Fill migration 024 DDL (secrets table with nonce||ciphertext BLOB, unique-index-where-not-revoked) in `internal/storage/schema/024_secrets.sql`
- [ ] T014 Fill migration 025 DDL (ALTER harness_runs ADD task_id + index) in `internal/storage/schema/025_harness_runs_task_id.sql`
- [ ] T015 [P] Write data-migration Go code that extracts `system_prompt` from existing `harness_config_json` for pre-existing agents, idempotent, in `internal/storage/migrations_data.go`
- [ ] T016 [P] Write data-migration Go code that computes `config_hash` for every existing agent and backfills the column in `internal/storage/migrations_data.go`
- [ ] T017 Implement `trust.ConfigHash(agent)` canonical SHA-256 hashing (sorted-keys JSON) in `internal/trust/hash.go` with table-driven unit tests in `internal/trust/hash_test.go` verifying stability across shuffled input arrays
- [ ] T018 Implement `trust.AppendEvidence()`, `trust.RollingScore()` (with exponential decay, clamp [0,1], neutral 0.5 default), `trust.IsQuarantined()` in `internal/trust/ledger.go` with unit tests in `internal/trust/ledger_test.go`
- [ ] T019 [P] Implement `trust.DelegationCap(parent, proposed) (Grant, violations)` enforcing the child ≤ parent rule across tool_scope, autonomy_tier, and budget dimensions in `internal/trust/delegation.go` with a full tier-matrix test in `internal/trust/delegation_test.go` — **covers SC-005**
- [ ] T020 Extend `internal/agents/types.go` with the new columns (`ConfigHash`, `ParentAgentID`, `SpawnDepth`, `SystemPrompt`, `AutonomyTier`, `ToolScope`, `QuarantinedAt`)
- [ ] T021 Extend `internal/agents/service.go` with `RecomputeConfigHash(ctx, agentID)` that recomputes on any config mutation and re-seeds reputation at 70 % of prior value — includes unit test verifying the 70 % invariant in `internal/agents/service_test.go` — **covers SC-007**
- [ ] T022 Implement `secrets.Store` in `internal/secrets/store.go` with NaCl secretbox encrypt/decrypt, master key file bootstrap (0600 perms, auto-generate on first use), CRUD, scope-filter, env sanitization; unit tests in `internal/secrets/store_test.go`
- [ ] T023 Implement `secrets.BuildEnv(ctx, agentID, taskID) map[string]string` in `internal/secrets/injector.go` with precedence user < agent < task; unit test in `internal/secrets/injector_test.go`

---

## Phase 3: User Story 1 — Create a goal from free text (Priority: P1) 🎯 MVP

**Goal**: A human can run `create_goal` and get back a goal id, a `#goal-<slug>` channel, and a coordinator agent wired up.

**Independent Test**: Call `create_goal` via the MCP transport with title + description; assert `goals` row exists, channel exists, owner is a member, and a coordinator agent is referenced.

**Maps to**: Spec US1, FR-001, FR-002, SC-001.

- [ ] T030 [US1] Implement `Goal` struct, `GoalStatus` enum, and repository CRUD in `internal/goals/types.go` and `internal/goals/repo.go`
- [ ] T031 [US1] Implement `goals.Service.CreateGoal(ctx, title, description, ownerID, opts)` that atomically inserts the goal, generates a unique slug with collision-dedup suffixing, creates the `#goal-<slug>` channel via existing `channels.Service`, adds the owner as a member, and assigns the coordinator — in `internal/goals/service.go`
- [ ] T032 [US1] Implement `goals.Service.GetGoal`, `ListGoals`, `TransitionTo` with state-machine guarding in `internal/goals/service.go`
- [ ] T033 [P] [US1] Write table-driven unit tests for `goals.Service` covering happy path, slug collision, invalid status transitions in `internal/goals/service_test.go`
- [ ] T034 [P] [US1] Write real-SQLite integration test `TestCreateGoal_EndToEnd` verifying DB row, channel creation, member add, coordinator assignment in `internal/goals/integration_test.go`
- [ ] T035 [US1] Implement MCP tool `create_goal` handler in `internal/mcp/tools_goals.go` with JSON Schema matching `contracts/mcp-tools.md` §1
- [ ] T036 [P] [US1] Write MCP tool test `TestCreateGoalMCP` using the existing in-process MCP test harness in `internal/mcp/tools_goals_test.go`
- [ ] T037 [US1] Register `create_goal` in the MCP tool registry in `internal/mcp/server.go`
- [ ] T038 [P] [US1] Add REST endpoint `GET /api/goals` and `GET /api/goals/:id` in `internal/api/handlers_goals.go`, returning the JSON snapshot defined in `contracts/mcp-tools.md` §REST
- [ ] T039 [P] [US1] Write REST handler tests in `internal/api/handlers_goals_test.go`
- [ ] T040 [P] [US1] Create Svelte 5 route `web/src/routes/goals/+page.svelte` listing goals (title, status, owner, total spend, last activity) consuming `GET /api/goals`
- [ ] T041 [P] [US1] Create Svelte 5 route `web/src/routes/goals/[id]/+page.svelte` with placeholder goal header (task tree will come in US2)
- [ ] T042 [US1] Rebuild embedded Web UI via `make web` and verify `internal/web/dist/` is regenerated
- [ ] T043 [US1] Wire a `create_goal` helper into the example script `examples/doc-gardener/run_task.sh` that calls the admin socket to create the doc-gardener goal

**Checkpoint**: `./start.sh && synapbus goals create --title "Test" --desc "Test goal"` works; Web UI `/goals` page lists it.

---

## Phase 4: User Story 2 — Coordinator decomposes a goal (Priority: P1)

**Goal**: The coordinator agent posts a task-tree proposal; the human approves; tasks are materialized atomically with denormalized ancestry.

**Independent Test**: Create a goal, wait for coordinator heartbeat, assert a `task_proposal` message in `#approvals`. React `approve`. Assert `tasks` rows with correct parent/child links, depth, and ancestry JSON.

**Maps to**: Spec US2, FR-003, FR-004, FR-006, FR-008, FR-033.

- [ ] T050 [US2] Implement `Task` struct, `TaskStatus` enum, `AncestryNode`, `VerifierConfig`, `HeartbeatConfig` types in `internal/tasks/types.go`
- [ ] T051 [US2] Implement `tasks.Service.CreateTaskTree(ctx, goalID, proposerID, tree)` that walks the proposed tree, assigns depth, builds ancestry snapshot (≤16 KB cap check), inserts all rows in a single transaction, and posts a single "tree materialized" system message to the goal channel — in `internal/tasks/service.go`
- [ ] T052 [US2] Implement `tasks.Service.TransitionTo(ctx, taskID, newStatus)` with state-machine guarding and automatic system-message posting to the goal channel — in `internal/tasks/service.go`
- [ ] T053 [P] [US2] Write table-driven unit tests for `CreateTaskTree` (depth assignment, ancestry correctness, 16 KB overflow rejection) and `TransitionTo` (valid and invalid transitions) in `internal/tasks/service_test.go`
- [ ] T054 [P] [US2] Write real-SQLite integration test `TestTaskTreeMaterialization` verifying atomicity (partial failure rolls back) in `internal/tasks/integration_test.go`
- [ ] T055 [US2] Implement MCP tool `propose_task_tree` handler in `internal/mcp/tools_tasks.go` per `contracts/mcp-tools.md` §2 — writes approval message; wires the reaction-workflow callback to invoke `tasks.Service.CreateTaskTree` on approval
- [ ] T056 [P] [US2] Write MCP tool test `TestProposeTaskTreeMCP` in `internal/mcp/tools_tasks_test.go`
- [ ] T057 [US2] Register `propose_task_tree` in `internal/mcp/server.go`
- [ ] T058 [US2] Extend `internal/api/handlers_goals.go` `GET /api/goals/:id` response to include the materialized task tree (recursive CTE fetch)
- [ ] T059 [P] [US2] Extend `web/src/routes/goals/[id]/+page.svelte` with a recursive Svelte task-tree component (collapsible, status badges, per-task spend) and wire the SSE event stream for real-time updates
- [ ] T060 [US2] Rebuild embedded Web UI via `make web`
- [ ] T061 [US2] Write the coordinator's fixed system prompt in `internal/coordinator/default_prompt.md` covering: role, allowed tools, JSON output format for tree proposals, delegation cap reminder — per research.md D13
- [ ] T062 [US2] Ship the pre-built coordinator config (name, model, system prompt from T061, tool scope) in `examples/doc-gardener/configs/coordinator.json`
- [ ] T063 [US2] Create `auto_approve.sh` helper in `examples/doc-gardener/auto_approve.sh` that polls `#approvals` every 1 s and posts `approve` reactions

**Checkpoint**: Running the example with `--auto-approve` produces a task tree in `tasks` table and a timeline entry in the goal channel.

---

## Phase 5: User Story 3 — Propose spawning a specialist agent (Priority: P1)

**Goal**: Coordinator calls `propose_agent`; server pre-checks delegation cap + spawn depth; human approves; new `agents` row is materialized with `config_hash`, `parent_agent_id`, `spawn_depth`, seeded reputation.

**Independent Test**: Call `propose_agent` from the coordinator with a valid proposal and a violating proposal (tool scope exceeds parent). Assert the violating one is rejected server-side. Approve the valid one. Assert new `agents` row with hash, parent, depth, and reputation seed.

**Maps to**: Spec US3, FR-010, FR-011, FR-012, FR-013, FR-014, FR-015, FR-016, FR-017, FR-018.

- [ ] T070 [US3] Implement `AgentProposal` type and repository in `internal/agents/proposals.go`
- [ ] T071 [US3] Implement `agents.ProposalService.Propose(ctx, proposal)` that runs delegation-cap pre-check (via `trust.DelegationCap`), depth-cap check, then writes `agent_proposals` row and posts `#approvals` message — in `internal/agents/proposals.go`
- [ ] T072 [US3] Implement `agents.ProposalService.Approve(ctx, proposalID, approverID)` that runs inside a transaction: (a) re-check caps; (b) compute `config_hash`; (c) insert `agents` row with parent/depth; (d) mint new API key; (e) seed `reputation_evidence` at 70 % of parent's rolling score; (f) DM proposer with credentials — in `internal/agents/proposals.go`
- [ ] T073 [US3] Implement `agents.ProposalService.Reject(ctx, proposalID, reason)` with DM to proposer
- [ ] T074 [P] [US3] Write table-driven unit tests covering: valid proposal, depth-cap violation, delegation-cap violation (all tier/tool combinations) in `internal/agents/proposals_test.go` — **covers SC-005**
- [ ] T075 [P] [US3] Write integration test `TestProposeAndMaterialize` with real SQLite in `internal/agents/proposals_integration_test.go`
- [ ] T076 [US3] Implement MCP tool `propose_agent` handler in `internal/mcp/tools_spawn.go` per `contracts/mcp-tools.md` §3
- [ ] T077 [P] [US3] Write MCP test `TestProposeAgentMCP` in `internal/mcp/tools_spawn_test.go`
- [ ] T078 [US3] Register `propose_agent` in `internal/mcp/server.go`
- [ ] T079 [US3] Wire the reaction-workflow callback so that an `approve` reaction on an `#approvals` message of type `agent_proposal` calls `ProposalService.Approve`
- [ ] T080 [P] [US3] Add "spawned agents" panel to `web/src/routes/goals/[id]/+page.svelte` showing each agent's `config_hash` (first 12 chars), parent, autonomy tier, and rolling reputation
- [ ] T081 [US3] Update the coordinator's system prompt in `internal/coordinator/default_prompt.md` to include the `propose_agent` tool description and the expected JSON format
- [ ] T082 [US3] Ship three specialist templates in `examples/doc-gardener/configs/` — `docs-scanner.json`, `cli-verifier.json`, `commit-watcher.json` (per research.md D16, with model, prompt, tool scope, MCP)

**Checkpoint**: Running the example produces a task tree, then coordinator proposes 1–3 specialists which are auto-approved and materialized.

---

## Phase 6: User Story 4 — Specialist claims a task and runs on a heartbeat (Priority: P1)

**Goal**: Atomic claim + reactor wakes the agent + harness runs the subprocess with injected context + task transitions to `awaiting_verification`.

**Independent Test**: Given an approved task and a matching specialist, call `claim_task`; wait for heartbeat; assert `harness_runs` row with `task_id`, leaf-task spend updated, task at `awaiting_verification`.

**Maps to**: Spec US4, FR-005, FR-007, FR-009, FR-028, FR-037, FR-038, FR-039.

- [ ] T090 [US4] Implement `tasks.Service.ClaimTask(ctx, taskID, agentID)` as the atomic optimistic-lock UPDATE per data-model.md, returning `ErrAlreadyClaimed` on 0 rows affected — in `internal/tasks/service.go`
- [ ] T091 [P] [US4] Write concurrency integration test `TestClaimTask_Concurrent` spawning 100 goroutines racing on the same task, asserting exactly one winner — in `internal/tasks/claim_test.go` — **covers SC-004**
- [ ] T092 [US4] Implement `tasks.Service.RollupCosts(ctx, rootTaskID)` using the recursive CTE from data-model.md — in `internal/tasks/service.go`
- [ ] T093 [P] [US4] Write test `TestRollupCosts_Recursive` building a 4-level tree and verifying sums — in `internal/tasks/rollup_test.go`
- [ ] T094 [US4] Implement MCP tool `claim_task` in `internal/mcp/tools_tasks.go` per `contracts/mcp-tools.md` §4
- [ ] T095 [P] [US4] Write MCP test `TestClaimTaskMCP` (including `ErrAlreadyClaimed` and `ErrAgentQuarantined` paths) in `internal/mcp/tools_tasks_test.go`
- [ ] T096 [US4] Extend `internal/harness/reactor/reactor.go` with three new wake sources (`task_assignment`, `task_timer`, `verification_requested`); reuse existing `pending_work` coalescing
- [ ] T097 [US4] Wire the reactor to invoke `secrets.BuildEnv(ctx, agentID, taskID)` when preparing subprocess env for a task-context run, and to write `harness_runs.task_id` on completion
- [ ] T098 [US4] Wire the reactor's post-run path to increment the leaf task's `spent_tokens` / `spent_dollars_cents` and transition the task to `awaiting_verification` inside the same transaction as the `harness_runs` insert
- [ ] T099 [P] [US4] Write integration test `TestReactorTaskHeartbeat_EndToEnd` using a mock subprocess that emits a synthetic cost, asserting the task state machine advances and `harness_runs.task_id` is set — in `internal/harness/reactor/reactor_task_test.go`
- [ ] T100 [P] [US4] Extend the `docs-scanner` and `cli-verifier` specialist wrapper scripts in `examples/doc-gardener/wrapper.sh` to read `SYNAPBUS_TASK_ID`, `SYNAPBUS_TASK_DESCRIPTION`, `SYNAPBUS_TASK_ANCESTRY` env vars set by the harness
- [ ] T101 [US4] Propagate OTel trace context across the reactor → subprocess hop (reuse existing TRACEPARENT plumbing from fee73e3)

**Checkpoint**: The example runs through claim → heartbeat → subprocess → completion message posted to goal channel.

---

## Phase 7: User Story 5 — Verification gate (Priority: P1)

**Goal**: Tasks in `awaiting_verification` are verified via one of `auto`, `peer`, `command` verifier kinds; verdicts drive `done`/`failed`; reputation is appended on success.

**Independent Test**: Configure a task with each verifier kind; transition through verification; assert correct terminal state and reputation row appended.

**Maps to**: Spec US5, FR-034, FR-035, FR-036.

- [ ] T110 [US5] Implement the verifier dispatcher in `internal/tasks/verifier.go` — parses `verifier_config_json`, routes to auto/peer/command handler, writes the resulting `tasks.status`
- [ ] T111 [US5] Implement `peer` verifier: call the configured peer's `verify_task` tool via the in-process MCP client in `internal/tasks/verifier_peer.go`
- [ ] T112 [US5] Implement `command` verifier: shell out with task env, enforce timeout, exit code 0 → approve in `internal/tasks/verifier_command.go`
- [ ] T113 [US5] On verdict, append `reputation_evidence` row with the appropriate `score_delta` (+1.0 approve / -1.0 reject) via `trust.AppendEvidence` — wire in `internal/tasks/verifier.go`
- [ ] T114 [US5] On verification failure, DM the coordinator of the task's goal with task id, verdict, and reason
- [ ] T115 [P] [US5] Write unit test `TestVerifier_AutoApprove`, `TestVerifier_CommandExitCodes`, `TestVerifier_PeerApprove_PeerReject` in `internal/tasks/verifier_test.go`
- [ ] T116 [US5] Implement MCP tool `verify_task` in `internal/mcp/tools_tasks.go` per `contracts/mcp-tools.md` §5 — gated on the caller being the configured peer verifier
- [ ] T117 [P] [US5] Write MCP test `TestVerifyTaskMCP` with positive and negative paths in `internal/mcp/tools_tasks_test.go`
- [ ] T118 [US5] Register `verify_task` in `internal/mcp/server.go`
- [ ] T119 [US5] Wire the reactor to invoke the verifier dispatcher as a new wake source `verification_requested` when a task enters `awaiting_verification`

**Checkpoint**: A task with each verifier kind runs through to `done` or `failed`; reputation ledger grows.

---

## Phase 8: User Story 9 — Doc-gardener example runs end-to-end (Priority: P1)

**Goal**: `./start.sh && ./run_task.sh --auto-approve && ./report.sh` produces `report.html` showing the full run.

**Independent Test**: Run the script trio from a clean checkout; assert `report.html` exists and contains the required sections.

**Maps to**: Spec US9, FR-043, FR-044, FR-045, FR-046, SC-002, SC-003.

> Note: User Story 9 is a P1 integration-level story — it depends on US1 through US5. It is placed after US5 deliberately so that the "doc-gardener runs" phase is the capstone of the MVP.

- [ ] T130 [US9] Complete `examples/doc-gardener/start.sh` per `quickstart.md` §1: preflight, rebuild, launch, create user `algis`, create coordinator, create `#approvals` + `#requests` channels
- [ ] T131 [US9] Complete `examples/doc-gardener/run_task.sh` per `quickstart.md` §2: DM coordinator, launch `auto_approve.sh`, poll goal channel for `FINAL:` message, 5-min timeout
- [ ] T132 [US9] Complete `examples/doc-gardener/stop.sh` per `cold-topic-explainer` pattern
- [ ] T133 [US9] Complete `examples/doc-gardener/auto_approve.sh` polling script
- [ ] T134 [US9] Write `examples/doc-gardener/wrapper.sh` subprocess entrypoint reading `SYNAPBUS_TASK_*` env vars and invoking the chosen LLM CLI
- [ ] T135 [P] [US9] Write the Go HTML report generator at `examples/doc-gardener/report.go` (cmd-line tool) that queries the DB via the internal `goals.Service.GetGoalSnapshot(id)` and renders the template
- [ ] T136 [P] [US9] Write the Go text/template at `examples/doc-gardener/report.html.tmpl` with the 6 sections from research.md D17 (header / tree / agents / cost / timeline / artifacts) with inline dark-mode CSS
- [ ] T137 [US9] Write `examples/doc-gardener/report.sh` that builds the report binary, runs it against the last goal id (from `.last_goal_id`), and opens `report.html`
- [ ] T138 [US9] Implement `goals.Service.GetGoalSnapshot(ctx, goalID) (*GoalSnapshot, error)` assembling goal + tree + agents + cost breakdown + timeline — in `internal/goals/snapshot.go`
- [ ] T139 [P] [US9] Write unit test `TestGoalSnapshot_ContainsAllSections` asserting every required field in `internal/goals/snapshot_test.go`
- [ ] T140 [US9] Write `examples/doc-gardener/README.md` per the `cold-topic-explainer/README.md` pattern, with the expected flow, commands, and troubleshooting from `quickstart.md`
- [ ] T141 [US9] End-to-end smoke test: run `./start.sh && ./run_task.sh --auto-approve && ./report.sh` against the current checkout; iterate on failures until it produces a valid report — **covers SC-002, SC-003**

**Checkpoint**: 🎯 **MVP DONE** — the doc-gardener example runs end-to-end and produces an HTML report. Ship-ready.

---

## Phase 9: User Story 6 — Resource-request protocol (Priority: P2)

**Goal**: Agents can ask the human for missing secrets; humans provide them via CLI; subsequent runs get the env var.

**Maps to**: Spec US6, FR-025, FR-026, FR-027, FR-028, FR-029, FR-030, SC-012.

- [ ] T150 [US6] Implement MCP tool `request_resource` in `internal/mcp/tools_resources.go` per `contracts/mcp-tools.md` §6
- [ ] T151 [P] [US6] Write MCP test `TestRequestResourceMCP` in `internal/mcp/tools_resources_test.go`
- [ ] T152 [US6] Implement MCP tool `list_resources` in `internal/mcp/tools_resources.go` per `contracts/mcp-tools.md` §7 (names-only, never values)
- [ ] T153 [P] [US6] Write MCP test `TestListResourcesMCP_NeverReturnsValues` asserting values are never exposed
- [ ] T154 [US6] Register both tools in `internal/mcp/server.go`
- [ ] T155 [US6] Implement cobra subcommand `synapbus secrets set|get|list|revoke` in `cmd/synapbus/cmd_secrets.go` — `get` returns availability only, never value; `set` encrypts and stores scoped
- [ ] T156 [P] [US6] Write cobra integration test in `cmd/synapbus/cmd_secrets_test.go`
- [ ] T157 [US6] Wire the resource-request workflow: on `secrets set` for a pending `resource_requests` row, mark `fulfilled`, DM the requester, and re-queue the agent for a heartbeat
- [ ] T158 [P] [US6] Write end-to-end integration test `TestRequestResource_RoundTrip` — agent requests, human sets, agent runs, subprocess sees env var — in `internal/secrets/e2e_test.go` — **covers SC-012**
- [ ] T159 [P] [US6] Add `/api/secrets/:scope_type/:scope_id` REST endpoint in `internal/api/handlers_secrets.go` returning names only; tests in `internal/api/handlers_secrets_test.go`

**Checkpoint**: A specialist can request a credential and the human can fulfill it without restarting anything.

---

## Phase 10: User Story 7 — Budget cascade, soft alert, auto-pause (Priority: P2)

**Goal**: Goals hit 80 % → soft alert; hit 100 % → paused; budgets cascade across delegation.

**Maps to**: Spec US7, FR-019, FR-020, FR-021, FR-022, SC-006.

- [ ] T170 [US7] Implement `goals.Service.CheckBudgetAndLaunch(ctx, goalID, newCost) error` that runs inside the same transaction as the `harness_runs` insert per research.md D7 — in `internal/goals/budget.go`
- [ ] T171 [US7] Implement soft-alert posting (idempotent, only on prior-below-80 + now-at-or-above-80) in `internal/goals/budget.go`
- [ ] T172 [US7] Implement hard auto-pause (transition goal to `paused`, post pause message, DM owner) in `internal/goals/budget.go`
- [ ] T173 [US7] Wire `goals.Service.CheckBudgetAndLaunch` into the reactor's pre-run transaction in `internal/harness/reactor/reactor.go`
- [ ] T174 [US7] Implement `tasks.Service.CreateSubTask` to subtract child budget from parent's remaining budget, failing with `ErrBudgetInsufficient` if over
- [ ] T175 [US7] Implement `goals.Service.SpendByBillingCode(ctx, goalID) (map[string]Cost, error)` rollup query
- [ ] T176 [P] [US7] Write integration test `TestBudgetRace_80AndPause` with two concurrent runs, asserting exactly one soft alert and the goal auto-pauses — in `internal/goals/budget_test.go` — **covers SC-006**
- [ ] T177 [P] [US7] Write unit test `TestBillingCodeRollup` asserting per-billing-code totals in `internal/goals/budget_test.go`
- [ ] T178 [US7] Implement MCP tool `resume_goal` in `internal/mcp/tools_goals.go` per `contracts/mcp-tools.md` §8
- [ ] T179 [P] [US7] Write MCP test `TestResumeGoalMCP` in `internal/mcp/tools_goals_test.go`
- [ ] T180 [US7] Register `resume_goal` in `internal/mcp/server.go`
- [ ] T181 [P] [US7] Add "Budget" + "Billing code breakdown" panels to `web/src/routes/goals/[id]/+page.svelte`

**Checkpoint**: Goals auto-pause at budget ceiling; the Web UI shows breakdowns.

---

## Phase 11: User Story 8 — Quarantine on low reputation (Priority: P3)

**Goal**: Agents whose rolling reputation drops below 0.3 are auto-quarantined and cannot claim tasks.

**Maps to**: Spec US8, FR-023, FR-024.

- [ ] T190 [US8] Implement `trust.CheckQuarantine(ctx, agentID)` in `internal/trust/ledger.go` running a ticker every N seconds (configurable, default 60) plus on every evidence append
- [ ] T191 [US8] Implement `agents.Service.Quarantine(ctx, agentID, reason)` and `Unquarantine(ctx, agentID)` in `internal/agents/service.go`
- [ ] T192 [US8] Reject `claim_task` from quarantined agents with `ErrAgentQuarantined` — wire in `internal/tasks/service.go`
- [ ] T193 [P] [US8] Write unit test `TestQuarantine_BelowThreshold` and `TestQuarantine_ClaimRejected` in `internal/agents/service_test.go`
- [ ] T194 [US8] Implement MCP tool `unquarantine_agent` in `internal/mcp/tools_goals.go` per `contracts/mcp-tools.md` §9
- [ ] T195 [P] [US8] Write MCP test `TestUnquarantineMCP` in `internal/mcp/tools_goals_test.go`
- [ ] T196 [US8] Register `unquarantine_agent` in `internal/mcp/server.go`
- [ ] T197 [P] [US8] Add "Quarantined" indicator to agent detail page in `web/src/routes/agents/[name]/+page.svelte`

**Checkpoint**: Reputation drop triggers quarantine; human can lift it via CLI or UI.

---

## Phase 12: Polish & Cross-Cutting Concerns

**Purpose**: Full test sweep, cross-compile verification, docs, and the finishing touches needed to ship.

- [ ] T210 Run the full test suite: `go test ./...` with `-race` and fix any flakes
- [ ] T211 Cross-compile verification: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` and `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...` — **covers SC-010**
- [ ] T212 [P] Verify all new MCP tools appear in `tools/list` via an MCP transport integration test in `internal/mcp/integration_test.go` — **covers SC-008**
- [ ] T213 [P] Web UI smoke test: load `/goals` with 100 seeded goals × 1000 tasks and assert load time < 1 s in `web/src/routes/goals/+page.svelte.test.ts` — **covers SC-011**
- [ ] T214 Update the project-level `CLAUDE.md` (already auto-updated by /speckit.plan) with any additional notes about running the doc-gardener example
- [ ] T215 Update `examples/README.md` (or create if absent) to list both `cold-topic-explainer` and `doc-gardener` with one-line descriptions and links
- [ ] T216 [P] Write a top-level `docs/dynamic-agents.md` explaining the trust model, spawn primitive, and verification workflow for new users
- [ ] T217 Run the complete SC checklist against the built binary and generated report — confirm every SC-0xx item is met
- [ ] T218 Commit the full branch with a descriptive message; push to GitHub
- [ ] T219 Final smoke: wipe `./examples/doc-gardener/data` and rerun `./start.sh && ./run_task.sh --auto-approve && ./report.sh` in one shot to confirm reproducibility from a clean state
- [ ] T220 Review `specs/018-dynamic-agent-spawning/checklists/requirements.md` — mark done after T219 passes

---

## Dependency Graph

```text
Phase 1 (Setup)                       — no dependencies
    ↓
Phase 2 (Foundational)                — needs Phase 1
    ↓
Phase 3 (US1 — MVP start)             — needs Phase 2
    ↓
Phase 4 (US2)                         — needs US1 (goal exists before tree)
    ↓
Phase 5 (US3)                         — needs US2 (tasks exist before spawn proposals target them)
    ↓
Phase 6 (US4)                         — needs US3 (specialists must exist before claims)
    ↓
Phase 7 (US5 — verification)          — needs US4 (tasks must be in awaiting_verification)
    ↓
Phase 8 (US9 — doc-gardener E2E)      — needs US1–US5 (MVP capstone)
    ↓ [MVP ships here]
Phase 9 (US6 — resources) ─┐          — needs US4 (run path exists); can run parallel to Phase 10 & 11
Phase 10 (US7 — budget) ──┤          — needs US4 (harness_runs path)
Phase 11 (US8 — quarantine) ┘         — needs Phase 2 (trust primitive)
    ↓
Phase 12 (Polish)                     — needs everything above
```

## Parallel Execution Opportunities

**Within Phase 1**: T002, T003, T004 run in parallel.

**Within Phase 2**: T015, T016 can run after migrations are written. T019 can run after T017 and T018. T022 and T023 run in parallel after T017.

**Within Phase 3 (US1)**: T033, T034, T036, T038, T039, T040, T041 all [P] — different files.

**Within Phase 4 (US2)**: T053, T054, T056, T059 all [P].

**Within Phase 5 (US3)**: T074, T075, T077, T080 all [P].

**Within Phase 6 (US4)**: T091, T093, T095, T099, T100 all [P].

**Within Phase 7 (US5)**: T115, T117 [P].

**Within Phase 8 (US9)**: T135, T136, T139 [P].

**Phase 9, 10, 11**: can run in parallel to each other after US5 is done. Inside each phase, [P] tasks run in parallel.

**Within Phase 12**: T212, T213, T216 [P].

## Implementation Strategy

### MVP Scope (ship-ready)

Phases 1, 2, 3, 4, 5, 6, 7, 8 — this gets you US1 through US5 and the doc-gardener example running end-to-end. **At the end of Phase 8 the feature is demoable and the HTML report renders.** This is the minimum to ship.

### Post-MVP

Phases 9, 10, 11 can be shipped as follow-up PRs without re-cutting the MVP. Phase 12 (polish) gates the final merge.

### Task Count

| Phase | Story | Count | Parallel |
|---|---|---|---|
| 1 — Setup | — | 5 | 3 |
| 2 — Foundational | — | 14 | 5 |
| 3 — US1 | P1 | 14 | 7 |
| 4 — US2 | P1 | 14 | 4 |
| 5 — US3 | P1 | 13 | 5 |
| 6 — US4 | P1 | 12 | 5 |
| 7 — US5 | P1 | 10 | 2 |
| 8 — US9 | P1 (E2E) | 12 | 3 |
| 9 — US6 | P2 | 10 | 5 |
| 10 — US7 | P2 | 12 | 5 |
| 11 — US8 | P3 | 8 | 4 |
| 12 — Polish | — | 11 | 3 |
| **Total** | | **135** | **51 [P]** |

## Independent Test Criteria (summary)

| Story | Priority | Independent test |
|---|---|---|
| US1 | P1 | `create_goal` → row + channel + coordinator exist |
| US2 | P1 | Coordinator posts tree proposal → approve → tasks materialized atomically |
| US3 | P1 | `propose_agent` valid + invalid → approval → new agent with hash/parent/seeded rep |
| US4 | P1 | 100-way concurrent claim → exactly one winner; claim → heartbeat → run → awaiting_verification |
| US5 | P1 | Each verifier kind (auto/peer/command) drives correct terminal state + reputation row |
| US6 | P2 | Request resource → set secret → next run sees env var |
| US7 | P2 | Race two runs at 80 % → exactly one alert; cumulative at 100 % → paused |
| US8 | P3 | Reputation drop → auto-quarantine + claim rejected; unquarantine restores |
| US9 | P1 | Full example pipeline produces `report.html` with 6 sections non-empty |
