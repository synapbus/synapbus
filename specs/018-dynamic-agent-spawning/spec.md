# Feature Specification: Dynamic Agent Spawning

**Feature Branch**: `018-dynamic-agent-spawning`
**Created**: 2026-04-14
**Status**: Draft
**Input**: Brainstormed and approved design from conversation — a coordinator-driven system where humans give high-level goals as free text, a meta-agent decomposes them into a task tree, proposes spawning specialist sub-agents, obtains human approval, runs the spawned agents on heartbeats, verifies their outputs, requests missing resources from the human, and iterates until the goal is achieved. Doc-gardener is the acceptance-test example.

## Assumptions

- **Primary actors**: (a) a human owner who issues goals and approves spawns, (b) a pre-built "coordinator" meta-agent that decomposes goals and proposes spawns, (c) specialist agents dynamically spawned to do leaf work, (d) optional peer "verifier" agents that check artifacts.
- **Bus-first architecture**: all agent-to-agent coordination continues to ride on SynapBus channels and DMs. Tasks are a first-class data model but every task state change also posts a system message to a backing `#goal-<slug>` channel for audit, search, and notifications — the task table is authoritative, the channel is the event log.
- **One human owner per goal**. No multi-human approval in v1.
- **Single-assignee atomic checkout** for tasks — no co-working on a single task in v1. Handled by a single-statement optimistic-lock UPDATE on `tasks.assignee_agent_id IS NULL`.
- **Goal ancestry is denormalized** as a JSON snapshot on every task, copied at create time. Parent rename does not propagate; ancestry represents the context the task was created under.
- **Cost rollup is read-time**, via a recursive CTE over the `tasks` table. Leaf tasks accumulate `spent_tokens` / `spent_dollars_cents`; aggregates are computed on demand. No materialized cache, no triggers, in v1.
- **Budgets cascade by delegation**, not by tree containment alone: when agent A assigns a sub-task to agent B, B's task budget is deducted from A's remaining parent-task budget at assignment time.
- **Trust = `(human_owner, config_hash, task_domain)`**. `config_hash = SHA-256(model + system_prompt + tools + skills + mcp_servers + subagents)`. The agent's friendly name is a routing label; reputation attaches to the hash. On any config update, the hash changes and reputation restarts from a parent-derived floor (default 70 % of prior), not zero.
- **Reputation is append-only**. Each observation writes a new ledger row; the "current" reputation is a rolling aggregate (exponentially-weighted by recency) derived from the ledger at read time.
- **Autonomy tiers**: `supervised` (every action needs approval), `assisted` (defined tool scope auto, else ask), `autonomous` (auto within goal budget). Stored on the agent record, capped server-side at spawn time by the delegation cap rule.
- **Delegation cap rule**: a child agent's effective grant in any dimension (tool scope, budget, tier) is `min(parent's grant in that domain, child's own earned reputation in that domain)`. Enforced at spawn time and re-checked at every tool call.
- **Max spawn depth defaults to 3**, configurable per goal. Each spawn increments `spawn_depth`; over the cap = hard reject.
- **Approval gating** for dynamic spawning uses the existing reactions workflow from migration 013 (`approve` / `reject` reactions on proposal messages posted to `#approvals`). No new UI primitive.
- **Resource requests** are a new structured message type sent to `#requests`. The human sets secrets via CLI (`synapbus secrets set`) or Web UI. Secrets are stored encrypted at rest under a master key file living next to the attachment store, identical trust model.
- **Verification** in v1 supports three verifier kinds: (a) a peer agent's `verify_task` MCP tool, (b) a shell command exit code, (c) auto-approve if no verifier is configured. More sophisticated kinds (consensus, voting, rubric grading) are deferred.
- **Coordinator** is a pre-built agent shipped with SynapBus. One coordinator per goal, owned by the goal's human owner. Coordinator uses the same subprocess harness as any other agent — it is not a kernel component.
- **Heartbeats** reuse and extend the existing reactive triggers system from migration 015. Wake sources: `task_assignment`, `task_timer`, `message_received`, `verification_requested`, `manual`. Existing coalescing (`pending_work` flag) applies.
- **Migration numbers**: new migrations `021_goals_tasks.sql`, `022_agent_proposals.sql`, `023_agent_trust_model.sql`, `024_secrets.sql`, `025_harness_runs_task_id.sql`. The dormant `trust` table from migration 014 is dropped and re-created under the new schema in `023`.
- **SynapBus base URL** in the example is `http://localhost:18088` (isolated instance, same pattern as `cold-topic-explainer`).
- **No CGO**. All new code must cross-compile cleanly for `linux/amd64` and `darwin/arm64`.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Human creates a goal from free text (Priority: P1)

A human owner wants to achieve a high-level outcome (for example, "keep docs.mcpproxy.app accurate against the current source code"). They type a short title and a free-text description, and hand it to SynapBus. SynapBus creates a goal record, auto-creates a `#goal-<slug>` backing channel, and assigns a pre-built coordinator agent as the goal's meta-agent. The human is now subscribed to the goal channel and sees every subsequent event there.

**Why this priority**: This is the entry point. Nothing else works without it. It must be demonstrable standalone as "I typed a goal and I got a goal row plus a channel plus a coordinator."

**Independent Test**: Run `create_goal` with a title and description. Verify a `goals` row exists, a `#goal-<slug>` channel exists, the owner is a member, and the coordinator agent is recorded as the goal's coordinator. No task tree yet.

**Acceptance Scenarios**:

1. **Given** a human owner and a free-text description, **When** the owner calls `create_goal` with title `"Keep docs.mcpproxy.app accurate"` and a description blob, **Then** a goal row is created with `status='draft'`, a channel named `#goal-keep-docs-mcpproxy-app-accurate` is auto-created, the owner is added as a member, and the coordinator agent is assigned to the goal.
2. **Given** a goal with a backing channel, **When** the owner opens the Web UI, **Then** the goal appears on a new `/goals` page with title, status, owner, and a link to its channel.
3. **Given** a goal with status `draft`, **When** the coordinator has not yet posted a task tree, **Then** no specialist agents have been spawned and no tasks exist in the task table.

---

### User Story 2 - Coordinator decomposes a goal into a task tree and asks for approval (Priority: P1)

Once a goal exists, the coordinator agent is woken by a `goal_created` event, reads the goal's description, and produces a structured task tree: a root task, child tasks covering independent lines of work, and leaf tasks tagged with the kind of specialist that should handle them. The coordinator posts the proposed tree as a single structured message to `#approvals` along with its reasoning. The human reacts with `approve` or `reject` (or edits in the UI in a later iteration). On approval, the root task and all child rows are materialized in the `tasks` table with goal ancestry snapshots, and their statuses are set to `approved`.

**Why this priority**: Decomposition is the bridge between "a goal exists" and "work is getting done." Without this step there is nothing to assign, nothing to budget, and nothing to verify.

**Independent Test**: Create a goal with a description, wait for the coordinator heartbeat, and check that a `task_proposal` message appears in `#approvals`. React with `approve`. Verify all tasks from the proposal are written to the `tasks` table with correct parent/child links, depth values, and denormalized ancestry JSON.

**Acceptance Scenarios**:

1. **Given** a goal with `status='draft'`, **When** the coordinator runs its first heartbeat, **Then** it posts a structured `task_proposal` message to `#approvals` containing the full proposed tree, the reasoning, and a reference to the goal id.
2. **Given** a `task_proposal` message, **When** the human reacts with `approve`, **Then** all proposed tasks are created atomically with parent/child links, `depth` values, denormalized `ancestry_json` snapshots, `status='approved'`, and system messages are posted to the goal's backing channel.
3. **Given** a `task_proposal` message, **When** the human reacts with `reject` and optionally supplies a reason, **Then** no tasks are created, the proposal message is marked rejected, the coordinator is DMed the reason, and the coordinator may retry with a revised proposal.
4. **Given** approved tasks, **When** the Web UI `/goals/:id` page is opened, **Then** the full task tree is visible with titles, ancestry, statuses, and budgets.

---

### User Story 3 - Coordinator proposes spawning a specialist agent (Priority: P1)

For a leaf task whose specialist does not yet exist, the coordinator calls `propose_agent` with a desired name, system prompt, tool scope, skills bundle, MCP server list, autonomy tier, and parent task. SynapBus writes an `agent_proposals` row and posts an approval message to `#approvals`. The human reviews the proposed configuration, reacts with `approve`, and SynapBus materializes a real agent: a new `agents` row with a computed `config_hash`, a freshly-generated API key, `parent_agent_id` pointing to the coordinator, `spawn_depth` one greater than the coordinator's, and an initial reputation snapshot seeded at 70 % of the parent's domain reputation. The new agent's credentials are DMed to the proposer. If the proposed tier or tool scope exceeds the parent's own grant, the proposal is rejected server-side before ever reaching the human.

**Why this priority**: Dynamic spawning is the heart of the whole feature. Without it, the system is just a prettier task tree. It must be demonstrably gated by human approval, capped by the delegation rule, and stamped with a reproducible config hash.

**Independent Test**: Given an existing coordinator agent owned by the human, call `propose_agent` with a valid proposal referencing a leaf task. Verify an `agent_proposals` row exists and an approval message is in `#approvals`. React `approve`. Verify a new `agents` row exists with computed `config_hash`, `parent_agent_id`, `spawn_depth=parent.spawn_depth+1`, and a seeded reputation row.

**Acceptance Scenarios**:

1. **Given** a coordinator at `spawn_depth=0`, **When** it calls `propose_agent` for a leaf task with a system prompt, tool list, and tier `assisted`, **Then** an `agent_proposals` row is created, an approval message is posted to `#approvals`, and no `agents` row exists yet.
2. **Given** a pending agent proposal, **When** the human reacts `approve`, **Then** a new `agents` row is created with `config_hash`, `parent_agent_id` set to the coordinator, `spawn_depth=1`, `autonomy_tier` not exceeding the coordinator's, a freshly-generated API key, a seeded reputation ledger row, and a DM is sent to the coordinator with the new agent's credentials.
3. **Given** a pending agent proposal, **When** the human reacts `reject`, **Then** no agent is created, the proposer is DMed the rejection, and the linked task remains at `approved` with no assignee.
4. **Given** a parent agent at `spawn_depth=2`, **When** it proposes a child at the default max depth cap of 3, **Then** the spawn is rejected with `ErrSpawnDepthExceeded` before the proposal is even written.
5. **Given** a parent agent with a `tool_scope` that does not include `attachments:write`, **When** it proposes a child whose tool scope includes `attachments:write`, **Then** the spawn is rejected server-side with `ErrDelegationCapExceeded` and no approval message is posted.
6. **Given** an existing agent, **When** its configuration is updated (system prompt, tools, or skills changed), **Then** its `config_hash` is recomputed and updated, and its reputation is re-seeded from the old hash at 70 %.

---

### User Story 4 - Spawned specialist claims a task and runs on a heartbeat (Priority: P1)

After the specialist agent is materialized, the coordinator assigns it to the relevant leaf task (or the specialist claims a task from a pool it is eligible for). Claiming is atomic: a single UPDATE statement sets `assignee_agent_id` only if the task is still unclaimed. On success, the reactor fires a heartbeat that launches the agent's subprocess run under the harness with full goal ancestry, task description, acceptance criteria, and scoped secrets injected as environment variables. The agent does its work, posts artifacts (messages, attachments, or external links) to the goal channel, and transitions the task to `awaiting_verification`.

**Why this priority**: Spawning an agent is useless if it cannot actually run and produce output. This story is the first observable value the user gets from a specialist.

**Independent Test**: Given an approved leaf task and a matching specialist agent, call `claim_task` as that agent. Verify the task transitions to `claimed` and the reactor schedules a heartbeat. Let the subprocess run, then verify the task is at `awaiting_verification` and an artifact message exists in the goal channel.

**Acceptance Scenarios**:

1. **Given** two specialist agents racing to claim the same task, **When** both call `claim_task`, **Then** exactly one claim succeeds and the other receives `ErrAlreadyClaimed`.
2. **Given** a successfully claimed task, **When** the reactor heartbeat fires, **Then** the harness subprocess is launched with the agent's `system_prompt`, the task description, the full `ancestry_json` of the task, scoped secrets, and the OTel trace context.
3. **Given** a running subprocess, **When** it completes, **Then** a `harness_runs` row is written with `task_id` set, the leaf task's `spent_tokens` / `spent_dollars_cents` are incremented, and the task transitions to `awaiting_verification` with a `completion_message_id` pointing at the artifact.
4. **Given** a task at `awaiting_verification`, **When** the goal channel is queried, **Then** at least one system message describing the completion exists, and the harness run is visible on the agent detail page.

---

### User Story 5 - Verification gate before a task is considered done (Priority: P1)

When a task enters `awaiting_verification`, the reactor reads the task's `verifier_config`. If set to `peer`, a peer agent's `verify_task` MCP tool is invoked; if set to `command`, a shell command is executed and the exit code determines the verdict; if unset, the task auto-transitions to `done`. Successful verification moves the task to `done` and increments the assignee's reputation for the relevant domain; failed verification moves the task to `failed`, DMs the coordinator, and kicks off iteration.

**Why this priority**: Artifacts without verification are worthless in the target use cases — the whole motivation is "results must be verified." Verification is what makes the system trustworthy enough to run unattended.

**Independent Test**: Configure a task with a peer verifier pointing at a specific agent's `verify_task` tool. Transition the task to `awaiting_verification`. Verify the peer's tool is invoked, the verdict is recorded, and the task reaches `done` or `failed` accordingly.

**Acceptance Scenarios**:

1. **Given** a task with `verifier_config` set to auto-approve, **When** the task transitions to `awaiting_verification`, **Then** it immediately transitions to `done` without any verifier being called.
2. **Given** a task with `verifier_config` set to a peer agent's `verify_task` tool, **When** the task transitions to `awaiting_verification`, **Then** the peer's `verify_task` is invoked with the task id, acceptance criteria, and artifact references; the returned verdict is written to the task.
3. **Given** a task with `verifier_config` set to a shell command, **When** the task transitions to `awaiting_verification`, **Then** the command runs with the task's context in env vars, and exit code `0` means approve and anything else means reject.
4. **Given** a task that failed verification, **When** the coordinator is DMed the failure, **Then** the coordinator may either respawn the task with a revised configuration or mark the goal `stuck` for the human to inspect.
5. **Given** a task that passed verification, **When** the reputation ledger is inspected, **Then** a new positive-evidence row is appended for the assignee's `config_hash` in the task's billing-code-derived domain.

---

### User Story 6 - Agent requests a missing resource from the human (Priority: P2)

A specialist agent discovers it needs an API key or credential it does not have (for example, a `BREVO_API_KEY` to send an email). It calls `request_resource` with the resource name, type, reason, and task id. SynapBus writes a `resource_request` row and posts a structured approval message to `#requests`. The human sets the value via `synapbus secrets set BREVO_API_KEY <value> --scope agent:doc-gardener-scanner`. On next run, the secret is injected as an env var. The secret's plaintext value is never returned from any MCP tool — only its name and availability are queryable via `list_resources`.

**Why this priority**: Real-world specialists will hit credential gaps. Without this protocol, every gap stops the run. It is P2 because the doc-gardener example does not strictly require it — but the OSS-visibility example cannot function without it.

**Independent Test**: Call `request_resource` as a specialist. Verify a request message is in `#requests`. Run `synapbus secrets set` to provide the value. Restart the agent's heartbeat. Verify the subprocess sees the env var, and `list_resources` returns the name but never the value.

**Acceptance Scenarios**:

1. **Given** a running specialist, **When** it calls `request_resource`, **Then** a `resource_requests` row is created and a structured message is posted to `#requests` with `{resource_name, resource_type, reason, task_id}`.
2. **Given** a pending resource request, **When** the human runs `synapbus secrets set NAME <value> --scope agent:foo`, **Then** an encrypted secret is stored in the `secrets` table scoped to the agent, the request is marked fulfilled, and the requester is DMed.
3. **Given** a scoped secret, **When** the owning agent's next harness run is launched, **Then** the secret is injected as an environment variable whose name is sanitized (alphanumeric + underscore, uppercased) and whose value is plaintext only inside the subprocess.
4. **Given** a scoped secret, **When** any MCP tool queries secrets, **Then** only names and availability are returned; plaintext values are never returned via MCP.
5. **Given** a revoked secret, **When** a subsequent run is launched, **Then** the env var is not injected and a warning is posted to the goal channel.

---

### User Story 7 - Budget cascade, soft alert, and hard auto-pause (Priority: P2)

Each goal has a total budget in both tokens and dollars. Each task may have its own sub-budget. When a specialist runs and a `harness_runs` row is written, the leaf task's spend is incremented, and the goal's rollup is recomputed on demand. At 80 % spend against any goal or task budget, SynapBus posts a soft-alert system message to the goal channel. At 100 %, the goal (or task) transitions to `paused`, the reactor refuses to launch further runs for the affected subtree, and a DM is sent to the human owner.

**Why this priority**: Without budgets, a runaway coordinator could burn unlimited tokens. P2 because the alert/pause is not strictly required to complete one successful run of the example, but it is required to ship the feature safely.

**Independent Test**: Create a goal with a `budget_dollars_cents=100` and a specialist whose harness runs cost `40` and `45` cents each. Run twice. After run two, verify a soft alert was posted to the goal channel. Run a third time. Verify the new run is refused and the goal is `paused`.

**Acceptance Scenarios**:

1. **Given** a goal with budget `$1.00` and cumulative spend under `$0.80`, **When** a new harness run writes `$0.05`, **Then** no alert is posted and the run proceeds normally.
2. **Given** cumulative spend crossing `$0.80`, **When** the recomputation happens, **Then** a single soft-alert system message is posted to the goal channel naming the goal and the percentage used (idempotent across runs).
3. **Given** cumulative spend reaching `$1.00`, **When** the next run is attempted, **Then** the goal transitions to `paused`, the run is rejected with `ErrGoalBudgetExceeded`, and the human owner is DMed.
4. **Given** a paused goal, **When** the human increases the budget and calls `resume_goal`, **Then** the status returns to `active` and new runs may be scheduled.

---

### User Story 8 - Quarantine on low reputation (Priority: P3)

If an agent's rolling reputation (averaged across domains) drops below the default threshold of `0.3`, the agent is automatically quarantined: its `status` is set to `quarantined`, no new task claims are permitted, existing runs finish, and the owner is DMed. Quarantine survives restarts and must be cleared manually.

**Why this priority**: A safety net against a broken or hostile agent eating all the budget. P3 because the acceptance test for v1 does not hinge on triggering it — but it must be wired up and testable.

**Independent Test**: Manually append enough negative-evidence rows to a test agent's reputation ledger to drive its rolling average below `0.3`. Trigger the quarantine check (ticker or on-demand). Verify `status='quarantined'`, a DM to the owner, and that subsequent `claim_task` calls by that agent fail with `ErrAgentQuarantined`.

**Acceptance Scenarios**:

1. **Given** an agent whose rolling reputation is `0.4`, **When** a new negative-evidence row drops it to `0.29`, **Then** the reactor transitions the agent to `quarantined` within one heartbeat and DMs the owner.
2. **Given** a quarantined agent, **When** it tries to claim a task, **Then** the claim fails with `ErrAgentQuarantined` and no state changes.
3. **Given** a quarantined agent with a running subprocess, **When** the subprocess completes, **Then** its results are still recorded (reputation updated, artifacts posted), but no new runs are scheduled.
4. **Given** a quarantined agent, **When** the human calls `unquarantine_agent`, **Then** the agent returns to `status='active'` and can claim tasks again.

---

### User Story 9 - Doc-gardener example runs end-to-end (Priority: P1)

A human runs `./start.sh && ./run_task.sh` in the new `examples/doc-gardener/` directory. The example creates the goal `"Make docs.mcpproxy.app documentation accurate against the current source code"`, bootstraps a coordinator, lets it decompose into sub-tasks (scan source code for CLI flags, scan deployed docs for mentioned flags, diff them, report gaps), auto-approves the spawning proposals using a pre-set `--auto-approve` flag on the example script (no human in the loop during the demo), runs the specialists, captures artifacts, and generates an HTML report at `examples/doc-gardener/report.html` showing the goal tree, spawned agents, spend per billing code, reputation deltas, and a timeline.

**Why this priority**: This is the acceptance test that proves all other stories work together. If this runs, the feature ships.

**Independent Test**: In a clean checkout, run the example end-to-end. Verify the HTML report is generated and shows a non-empty task tree, at least one spawned specialist, a non-zero token spend, and a final status of either `completed` or `stuck with reason`.

**Acceptance Scenarios**:

1. **Given** a clean checkout with no synapbus instance running, **When** the user runs `./start.sh`, **Then** a fresh `synapbus` binary is built, a separate instance is launched on port `18088` with a local `./data` directory, a user and a coordinator agent are created, and the base `#approvals`, `#requests` channels exist.
2. **Given** a started instance, **When** the user runs `./run_task.sh --auto-approve`, **Then** a goal is created, the coordinator produces a task tree proposal, the auto-approver approves it, specialist agent proposals are made and auto-approved, specialists are spawned, their heartbeats fire, artifacts are posted to the goal channel, and the run terminates within the default budget.
3. **Given** a completed run, **When** the user runs `./report.sh`, **Then** `report.html` is written containing the goal title, the task tree with statuses, the list of spawned agents with their `config_hash` and reputation deltas, a cost table broken down by billing code, and a chronological timeline of state transitions.
4. **Given** a running instance, **When** the user opens the Web UI at `http://localhost:18088/goals`, **Then** the goal appears with its tree, backing channel, and real-time status updates.

---

### Edge Cases

- **Race on claim**: two specialists call `claim_task` on the same task in the same millisecond. The atomic UPDATE guarantees exactly one wins; the loser receives `ErrAlreadyClaimed`. Verified by a concurrency integration test.
- **Race on spawn**: two peer agents simultaneously propose children of the same name. Proposals are independent rows; both proceed to approval. The human picks one; the second should be rejected with `ErrAgentNameTaken` on materialization — not at proposal time — so the human still sees both alternatives.
- **Coordinator dies mid-decomposition**: the proposal message is posted but no tasks have been materialized. The goal remains `draft`. The coordinator is respawned on its next heartbeat; idempotency is provided by the coordinator checking "does an active proposal already exist for this goal?" before posting a new one.
- **Budget check races a harness run**: the reactor must check `goal.status != 'paused'` and `budget_remaining > 0` inside the same transaction that writes the `harness_runs` row; otherwise a concurrent two-runs-at-80 %-each could both pass the check and blow the budget. Solved by a compensating adjustment step: if the post-run recomputation pushes total over the budget, the goal is still paused, the alert is still posted, and the over-run is recorded in the ledger.
- **Config update for an in-flight agent**: an owner edits an agent's system prompt while a run is in progress. The run finishes with the old config; the new config_hash takes effect on the next run; the reputation attributed to the in-flight run belongs to the old hash.
- **Resource request posted but no task id**: rejected with `ErrMissingTaskContext`. Every request must cite a task.
- **Secret revoked mid-run**: the subprocess already has the env var; it continues. On next run the env var is absent and the agent must request it again.
- **Ancestry overflow**: a pathological 100-level deep decomposition would produce huge ancestry snapshots. Hard cap on `ancestry_json` length of 16 KB; over-cap tasks must be rejected at creation.
- **Reputation divide-by-zero**: an agent with zero evidence rows has undefined reputation. Treat as `0.5` (neutral) for gating decisions; do not quarantine an agent that has never been evaluated.
- **Goal channel name collision**: slug generation normalizes and deduplicates — suffix `-2`, `-3`, etc.
- **Pause/resume during verification**: a task at `awaiting_verification` when the goal is paused must still complete its verification (so the result is not lost), but the verdict is recorded and the task's state change does not trigger further spawning until the goal resumes.

## Requirements *(mandatory)*

### Functional Requirements

**Goal and task model**

- **FR-001**: System MUST allow a human owner to create a goal with a title and free-text description, returning a goal id and a backing channel.
- **FR-002**: System MUST auto-create a `#goal-<slug>` channel on goal creation, with the owner as a member.
- **FR-003**: System MUST store tasks in a first-class `tasks` table with parent/child linkage, denormalized goal ancestry, depth, title, description, acceptance criteria, assignee, status, budgets, and spend fields.
- **FR-004**: System MUST enforce a task state machine: `proposed → approved → claimed → in_progress → awaiting_verification → done | failed`, plus `cancelled` from any state.
- **FR-005**: System MUST guarantee atomic single-assignee claim via an optimistic-lock UPDATE; a lost race MUST return `ErrAlreadyClaimed` without side effects.
- **FR-006**: System MUST snapshot the full root-to-parent ancestry as JSON on each task at creation time and not propagate parent renames.
- **FR-007**: System MUST support a recursive-CTE cost rollup query from any task node down to all descendants, summing `spent_tokens` and `spent_dollars_cents`.
- **FR-008**: System MUST post a system message to the goal's backing channel on every task state transition with task id, title, old status, new status, and an actor reference.
- **FR-009**: System MUST extend the `harness_runs` table with a nullable `task_id` column, and the reactor MUST write it on every run launched in a task context.

**Dynamic spawning and trust**

- **FR-010**: System MUST provide an MCP tool `propose_agent` that accepts a proposed name, model, system prompt, tool scope, skills, MCP servers, autonomy tier, parent task id, and reason; and writes an `agent_proposals` row plus an approval message in `#approvals`.
- **FR-011**: System MUST reject a proposal at `propose_agent` time if the caller's `spawn_depth + 1` exceeds the goal's max depth (default 3), or if the proposed grant in any dimension exceeds the caller's own grant under the delegation cap rule.
- **FR-012**: System MUST allow the human owner to reject or approve a proposal via a reaction (`approve` or `reject`) on the proposal message.
- **FR-013**: System MUST, on approval, atomically create a new agent record with a computed `config_hash = SHA-256(model + system_prompt + tools + skills + mcp_servers + subagents)`, `parent_agent_id` set to the proposer, `spawn_depth` set to the proposer's + 1, a freshly-generated API key, and an initial reputation ledger row seeded at 70 % of the parent's reputation for the matching domain.
- **FR-014**: System MUST promote `system_prompt` to a first-class column on the `agents` table; existing agents MUST be migrated with their prompt extracted from `harness_config_json` where present.
- **FR-015**: System MUST recompute `config_hash` on every config change and re-seed reputation at 70 % of the prior hash's rolling value.
- **FR-016**: System MUST support three autonomy tiers (`supervised`, `assisted`, `autonomous`) stored on the `agents` table and enforced at tool-call time by the harness.
- **FR-017**: System MUST store reputation as an append-only ledger keyed by `(config_hash, task_domain)`, with each row carrying `score_delta`, `evidence_ref`, and `created_at`.
- **FR-018**: System MUST expose a rolling-reputation read query that computes the current score with exponential time-decay (half-life configurable, default 30 days).

**Budgets and quarantine**

- **FR-019**: System MUST enforce a per-goal token and dollar budget; if a run would push cumulative spend over 100 %, the goal MUST be transitioned to `paused` and the run rejected.
- **FR-020**: System MUST post a one-time soft-alert message to the goal's backing channel when cumulative spend first crosses 80 %.
- **FR-021**: System MUST cascade budgets at delegation: creating a sub-task under a parent task MUST subtract the child's budget from the parent's remaining budget, and fail if the parent cannot cover it.
- **FR-022**: System MUST support a free-form `billing_code` column on tasks and a rollup query summing spend by billing code within a goal subtree.
- **FR-023**: System MUST auto-transition an agent to `quarantined` when its rolling reputation drops below the configured threshold (default `0.3`), and reject `claim_task` calls from quarantined agents with `ErrAgentQuarantined`.
- **FR-024**: System MUST allow a human owner to un-quarantine an agent via a CLI command or Web UI action.

**Resource-request protocol and secrets**

- **FR-025**: System MUST provide an MCP tool `request_resource` that writes a `resource_requests` row and posts a structured request to `#requests`.
- **FR-026**: System MUST store secrets in a new `secrets` table with `{name, value_encrypted, scope_type, scope_id, created_at, revoked_at}`.
- **FR-027**: System MUST encrypt secret values at rest under a local master key file and refuse to start if the master key is missing or malformed.
- **FR-028**: System MUST inject scoped secrets as environment variables when launching a subprocess harness run, with variable names sanitized to `[A-Z0-9_]+` and uppercased.
- **FR-029**: System MUST provide an MCP tool `list_resources` that returns only names and availability, never plaintext values.
- **FR-030**: System MUST provide a CLI command `synapbus secrets set <name> <value> --scope <type:id>` and a matching `synapbus secrets revoke`.

**Coordinator and verification**

- **FR-031**: System MUST ship a pre-built coordinator agent definition (system prompt, tool scope, default model placeholder) loadable via the example setup script or via `synapbus agents create-coordinator <goal_id>`.
- **FR-032**: System MUST provide an MCP tool `create_goal` callable by the human owner.
- **FR-033**: System MUST provide an MCP tool `propose_task_tree` callable by the coordinator, which writes a structured proposal message to `#approvals`.
- **FR-034**: System MUST support three verifier kinds in `verifier_config`: `peer`, `command`, and `auto`. The reactor MUST invoke the configured verifier on task transition into `awaiting_verification` and write the verdict to `tasks.status`.
- **FR-035**: System MUST provide an MCP tool `verify_task` for peer agents, returning `{verdict: approve|reject, reason}`.
- **FR-036**: System MUST, on verification failure, DM the coordinator with the task id, verdict, and reason; the coordinator may respawn the task or mark the goal stuck.

**Heartbeats**

- **FR-037**: System MUST extend the reactive trigger engine with wake sources `task_assignment`, `task_timer`, `verification_requested` in addition to the existing `message_received`.
- **FR-038**: System MUST reuse the existing `pending_work` coalescing so overlapping wakeups merge into a single run per agent per task.
- **FR-039**: System MUST propagate OTel trace context across all heartbeat-launched subprocesses.

**Web UI**

- **FR-040**: System MUST ship a new `/goals` page in the Web UI listing all goals with status, owner, total spend, and link to the backing channel.
- **FR-041**: System MUST ship a new `/goals/:id` page showing the task tree, per-task status, per-task spend, assignee, and a real-time event timeline of state transitions.
- **FR-042**: System MUST render approval messages on the `#approvals` channel with `approve` / `reject` buttons that dispatch the existing reaction workflow.

**Doc-gardener example**

- **FR-043**: System MUST ship an example at `examples/doc-gardener/` with `start.sh`, `stop.sh`, `run_task.sh`, `report.sh`, `configs/`, and `README.md`, mirroring the layout of `examples/cold-topic-explainer/`.
- **FR-044**: The example `start.sh` MUST launch an isolated synapbus instance on a configurable port (default 18088), create a user, create a coordinator agent, and pre-create the `#approvals` and `#requests` channels.
- **FR-045**: The example `run_task.sh` MUST support an `--auto-approve` flag that auto-reacts `approve` to all proposal messages for the duration of the run.
- **FR-046**: The example `report.sh` MUST query the DB and render a Go template to `report.html` containing the goal tree, spawned agents with `config_hash` and reputation deltas, cost-per-billing-code table, and a chronological timeline.

### Key Entities *(include if feature involves data)*

- **Goal**: Represents a human-owned high-level objective. Links to a backing channel, a root task, an owner, and a budget. Has a lifecycle: `draft → active → paused → completed | cancelled`.
- **Task**: A node in a goal's task tree. Single-assignee, atomically claimable, carries a denormalized ancestry snapshot, acceptance criteria, budget, billing code, spend counters, heartbeat config, and verifier config. Tracks origin / claim / completion message ids for channel audit.
- **AgentProposal**: A pending request by an agent to spawn a new sub-agent. References parent agent, parent task, desired name, model, system prompt, tool scope, skills, MCP servers, autonomy tier, and reason. Lifecycle: `pending → approved | rejected`.
- **Agent** (extended): Existing entity, with new columns `config_hash`, `parent_agent_id`, `spawn_depth`, `system_prompt`, `autonomy_tier`, `tool_scope`.
- **ReputationLedger**: Append-only history of evidence rows keyed by `(config_hash, task_domain)`. Rolling score derived at read time with time decay.
- **ResourceRequest**: A structured request from an agent for a named secret, scoped to a task. Lifecycle: `pending → fulfilled | rejected | revoked`.
- **Secret**: A name/value pair with a scope (user / agent / task), encrypted at rest, revocable.
- **HarnessRun** (extended): Existing entity, with a new nullable `task_id` column, linking a subprocess run to the task that spawned it.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A human can create a goal from a free-text description, see it in the Web UI, and see its backing channel, in under 10 seconds from typing `create_goal` to the UI update — measured wall-clock on a laptop with an empty DB.
- **SC-002**: The doc-gardener example runs end-to-end from `./start.sh && ./run_task.sh --auto-approve && ./report.sh` without human intervention and produces an `report.html` in under 5 minutes of wall-clock time on a developer laptop.
- **SC-003**: The generated `report.html` contains at least one goal, one non-trivial task tree (at least 3 nodes), at least one spawned specialist agent, at least one verified-or-failed task outcome, and a non-zero cost breakdown by billing code.
- **SC-004**: Two concurrent `claim_task` calls on the same task produce exactly one success and one `ErrAlreadyClaimed`, verified by a concurrency integration test, over at least 100 repetitions.
- **SC-005**: A delegation-cap violation (child proposes a grant exceeding parent's) is rejected server-side in every case, never reaching the human — verified by a unit test over the full autonomy-tier matrix.
- **SC-006**: A goal whose cumulative spend crosses 80 % receives exactly one soft-alert message, and a goal whose spend reaches 100 % is auto-paused within the same transaction that wrote the over-budget run — verified by a budget-race integration test.
- **SC-007**: On any agent config change, the `config_hash` changes and the new hash's reputation equals exactly 70 % (±1 %) of the old hash's rolling value — verified by a reputation-migration unit test.
- **SC-008**: All new MCP tools (`create_goal`, `propose_task_tree`, `propose_agent`, `claim_task`, `verify_task`, `request_resource`, `list_resources`) appear in the MCP server's `tools/list` response and are invocable end-to-end via the MCP transport in an integration test.
- **SC-009**: All new tables have table-driven unit tests with at least one happy-path and one sad-path case per core method, and the service layer has at least one integration test per user story above.
- **SC-010**: The binary built from this branch cross-compiles for `linux/amd64` and `darwin/arm64` with no CGO — verified by `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...` and `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...` in CI.
- **SC-011**: The Web UI `/goals` page loads within 1 second for a DB containing 100 goals and 1000 tasks, measured by a light smoke test.
- **SC-012**: A specialist that requests a resource via `request_resource`, receives a value via `synapbus secrets set`, and runs again, sees the env var on its next run in 100 % of cases — verified by an integration test of the full request → set → inject loop.
