# Research: Dynamic Agent Spawning

**Phase**: 0 (Outline & Research)
**Date**: 2026-04-14
**Inputs**: spec.md, plan.md, prior conversation brainstorming, existing SynapBus codebase (feat/harness-otel branch)

This document captures decisions and their rationale for each design axis of the feature. All items that were `NEEDS CLARIFICATION` during brainstorming are resolved here.

---

## D1. Trust model: what is trust attached to?

**Decision**: Trust (reputation) is attached to the triple `(owner_user_id, config_hash, task_domain)`. `config_hash = SHA-256(canonical-JSON(model, system_prompt, tool_scope, skills, mcp_servers, subagents))`. The agent's friendly name is only a routing label. On any config update, the hash changes and the new hash is seeded at 70 % of the prior hash's rolling score, then re-earns evidence.

**Rationale**: The agent's capability comes from model + instructions + tools + skills. Any of the latter can be updated, so the name alone is not trustworthy. Literature (Castelfranchi/Falcone, Signet, MI9, Microsoft Agent Governance Toolkit) converges on cryptographic attestation of the configuration bundle, with per-domain reputation and owner-scoped trust. A 70 %-of-parent floor on config change balances "updates must not be free" (prevents trust laundering) against "updates must not be catastrophic" (no full reset).

**Alternatives considered**:
- *Trust attached to agent id* — rejected because it lets an owner silently swap out an agent's system prompt and retain its privileges.
- *Trust attached to model only* — rejected because a dangerous prompt + an aligned model is still dangerous.
- *Trust reset to zero on config change* — rejected because it disincentivizes iterating on agents.
- *Capability tokens instead of reputation* — deferred. V1 uses reputation + policy decisions; capability tokens would be a cleaner future model but are a much bigger rewrite.

**References**: brainstorming trust research thread (summarized Castelfranchi, Sabater & Sierra, MI9, TRiSM, Microsoft Agent Governance Toolkit, Claude Code permission model).

---

## D2. Task-vs-channel posture: where does task state live?

**Decision**: **Hybrid (tasks table authoritative, backing channel as event log)**. A first-class `tasks` table holds all mutable state. Every goal has an auto-created `#goal-<slug>` channel. Every task state transition posts a system message to that channel. The table is authoritative; the channel is the event log, free-text audit trail, human observation surface, and reaction/workflow carrier.

**Rationale**: Cost rollups, budget checks, and atomic claims require typed SQL columns. Doing those over JSON metadata on messages (pure-channels posture) forces slow JSON scans and awkward queries. But a channel is what gives humans the Slack-like observability they expect, and SynapBus's existing reactions/workflow/search infrastructure already works on channels. A dual-write model gets both benefits, at the cost of a small amount of service-layer consistency logic.

**Alternatives considered**:
- *Tasks as a new table only, no backing channel* (Paperclip-pure): rejected — loses the bus-first SynapBus advantage; humans would not see tasks in their normal chat UX.
- *Tasks as messages only with structured metadata* (channels-pure): rejected — JSON scans for cost rollup and budget checks; mixes conversation and work-tracking in one table.

---

## D3. Goal ancestry propagation

**Decision**: **Denormalized snapshot** — `ancestry_json` copied onto each task at creation time, never updated on parent rename.

**Rationale**: Ancestry is read on every LLM turn (prompt injection) and written once (task creation). Reads dominate. Denormalization means prompt assembly is a single row fetch, no joins, no recursion. Rename propagation is rare and, when it happens, the old snapshot still correctly represents the context the task was created under — which is what we want for prompt stability. Paperclip validates this approach with its "goal ancestry on every task" primitive.

**Alternatives considered**:
- *Closure table* — rejected: overkill for tree sizes < 1000 and expensive on insert.
- *Compute at read time via recursive CTE* — rejected: recomputing on every LLM turn wastes CPU + SQLite reads.

---

## D4. Cost rollup: stored or computed?

**Decision**: **Leaf-only accumulator + read-time recursive CTE**. Only leaf tasks store `spent_tokens` / `spent_dollars_cents`. Aggregates are computed on demand via `WITH RECURSIVE subtree(id) AS ( ... )`.

**Rationale**: For the target scale (dozens of goals, hundreds of tasks), SQLite recursive CTE is sub-millisecond. Triggers that cascade spend up the tree on every `harness_runs` insert introduce write amplification, correctness risk on goal deletion, and fragility across migrations. If scale ever requires it, a materialized `goal_cost_cache` table can be added later without breaking the API.

**Alternatives considered**:
- *Triggers with cascading updates*: rejected for fragility.
- *Materialized cache table with manual refresh*: deferred; YAGNI for v1.
- *Event-sourced ledger with periodic projection*: over-engineered.

---

## D5. Atomic task claim

**Decision**: Single-statement `UPDATE tasks SET assignee_agent_id=?, status='claimed', claimed_at=? WHERE id=? AND assignee_agent_id IS NULL AND status='approved'`. Inspect `rowsAffected`: 0 → return `ErrAlreadyClaimed`.

**Rationale**: SQLite's write-ahead-log + serializable isolation makes this safe. No explicit locking, no CAS loops, no retries. The existing `claim_messages` pattern already uses this model successfully. Verified under 100-concurrent-goroutines integration test (SC-004).

**Alternatives considered**:
- *`SELECT ... FOR UPDATE`*: SQLite does not support this; would force WAL-exclusive mode and serialize all writers.
- *Advisory locks via a separate table*: adds two writes and a transaction for every claim.

---

## D6. Heartbeat integration with existing reactive triggers

**Decision**: Extend `internal/harness/reactor/reactor.go` with three new wake sources: `task_assignment`, `task_timer`, `verification_requested`. Reuse the existing `pending_work` coalescing flag (unchanged). New fields on `reactive_runs` are not needed — the reactor identifies task-context runs by `task_id` on the `harness_runs` row it writes.

**Rationale**: The reactor already handles rate limiting, cooldown, daily budget, depth propagation, OTel context. Reusing it means new wake sources inherit all of those safety primitives for free. The only new logic is (a) "on task assignment, look up the assignee and enqueue a wakeup" and (b) "on task timer expiration, check eligibility and enqueue a wakeup".

**Alternatives considered**:
- *New parallel scheduler just for tasks*: rejected — duplicates cooldown/budget logic, violates Principle IX (progressive complexity).
- *Cron jobs*: rejected — the existing reactor already composes with webhooks and K8s Jobs; cron would fragment.

---

## D7. Budget race and auto-pause correctness

**Decision**: Budget check is performed **inside the same transaction** that writes the `harness_runs` row. Pseudocode:

```
BEGIN;
  SELECT goal.status, budget_*, cumulative_spend_via_cte;
  IF paused OR cumulative + new_cost > budget THEN
    ROLLBACK;
    return ErrGoalBudgetExceeded;
  END IF;
  INSERT INTO harness_runs (...);
  UPDATE tasks SET spent_tokens = spent_tokens + ?, spent_dollars_cents = spent_dollars_cents + ? WHERE id = ?;
  IF new_cumulative_spend >= 0.8 * budget AND prior < 0.8 * budget THEN
    INSERT INTO messages ('soft alert', ...);
  END IF;
  IF new_cumulative_spend >= budget THEN
    UPDATE goals SET status='paused' WHERE id=?;
    INSERT INTO messages ('hard pause', ...);
    INSERT INTO messages ('DM to owner', ...);
  END IF;
COMMIT;
```

**Rationale**: Wrapping in a transaction means two concurrent runs can't both pass a pre-check and jointly blow the budget; SQLite's WAL serialization will make one of them see the other's committed state. The 80 % soft alert is idempotent-by-check (`prior < 0.8 * budget`). The auto-pause and DM are emitted in the same transaction so an observer sees the alert ⇒ the goal is already paused.

**Alternatives considered**:
- *Check before transaction*: vulnerable to the race described.
- *Compensating adjustment after commit*: possible but introduces ledger complexity; the transaction approach is simpler.

---

## D8. Secret encryption at rest

**Decision**: **NaCl secretbox** (XSalsa20-Poly1305) via `golang.org/x/crypto/nacl/secretbox`. Master key loaded from `<data-dir>/secrets.key` (32 bytes, 0o600). Auto-generate at first start if missing. Each secret row stores `nonce (24 bytes) || ciphertext` in a BLOB column.

**Rationale**: Pure Go (Principle III). Standard library-adjacent. Already used widely in the Go ecosystem. `secretbox` is authenticated encryption — prevents silent tampering. Master key file pattern matches the existing attachment store's on-disk master key convention, so there's only one "local secret" a user must protect.

**Alternatives considered**:
- *age encryption*: adds a dependency and is meant for files, not small BLOBs.
- *AES-GCM via stdlib*: equivalent security; `nacl/secretbox` is simpler (no key-wrapping, no IV management dance).
- *Plaintext storage with filesystem 0600*: rejected — even a crash dump of the SQLite file would leak all secrets.

---

## D9. Reputation rolling score + time decay

**Decision**: Append-only `reputation_evidence(config_hash, domain, score_delta, evidence_ref, created_at)`. Rolling score computed at read time as:

```
rolling_score(config_hash, domain) =
  sum over evidence rows of (score_delta * exp(-ln(2) * age_days / HALF_LIFE_DAYS))
  clamped to [0.0, 1.0]
  neutral default = 0.5 if evidence_count == 0
```

`HALF_LIFE_DAYS` defaults to 30, configurable via env.

**Rationale**: Exponential decay over days is standard for recency-weighted reputation. Computing at read time avoids storing a stale rolling column that must be refreshed on every write. For the target scale, computing over an agent's evidence rows is sub-millisecond. A neutral `0.5` default prevents quarantine of never-evaluated agents (edge case from spec).

**Alternatives considered**:
- *Naive average*: ignores recency; a bad early run haunts the agent forever.
- *Stored rolling column with background updater*: extra moving part.
- *Bayesian belief update (beta distribution)*: mathematically elegant but overkill for v1.

---

## D10. Verification kinds

**Decision**: Three verifier kinds in v1, stored as `verifier_config JSON` on the task:

| Kind | JSON schema | Reactor action |
|---|---|---|
| `auto` | `{"kind":"auto"}` (or NULL) | Auto-transition `awaiting_verification → done` |
| `peer` | `{"kind":"peer","agent_id":123}` | Call the peer's `verify_task` MCP tool with task context |
| `command` | `{"kind":"command","cmd":"bash -lc 'make verify'","cwd":"./examples/doc-gardener","timeout_sec":60}` | Run shell command with task env; exit 0 = approve |

**Rationale**: Covers the three high-leverage cases: trivial tasks don't need verification, peer review for judgment tasks, deterministic tests for code/output tasks. More sophisticated kinds (consensus, voting, rubric grading, LLM-as-judge) can stack on top of `peer` in v2 without migration.

**Alternatives considered**:
- *Single peer-only verifier*: insufficient — testable outputs (does CLI flag exist?) want shell verification.
- *LLM-as-judge as a fourth kind*: subsumed by `peer` — the peer can be an LLM-judge agent.

---

## D11. Approval gating via reactions (vs. new primitive)

**Decision**: Reuse the existing reactions + workflow infrastructure from migration 013. The coordinator posts a `task_proposal` or `agent_proposal` message to `#approvals`; the human reacts `approve` or `reject`; the existing reaction → workflow-state hook dispatches the approval handler. No new UI primitive.

**Rationale**: Already wired up. Already supports Web UI buttons and CLI reactions. Already produces audit events. Zero new code.

**Alternatives considered**:
- *New `approvals` table with buttons*: duplicates existing state machine.
- *Email-based approvals*: out of scope; SynapBus is local-first.

---

## D12. Config hash canonical form

**Decision**: Canonical-JSON is sorted-keys, no whitespace, recursive. Input fields (in order): `model`, `system_prompt`, `tool_scope` (sorted array of strings), `skills` (sorted array), `mcp_servers` (sorted array of `{name, url, transport}` objects), `subagents` (sorted array of names). Everything else on the agent record (name, description, status, ownership, etc.) is excluded.

**Rationale**: Stability across runs requires a canonical serialization — otherwise map iteration order produces hash drift. `tool_scope`, `skills`, and `mcp_servers` are sorted so equivalent configs in different orderings hash the same. Excluding owner/name/status is deliberate: trust should travel with capability, not identity.

**Alternatives considered**:
- *Hash the entire agent row*: leaks trust on cosmetic changes (renaming).
- *Hash only model + system_prompt*: weak — tool scope changes the agent's capability.

---

## D13. Coordinator LLM prompt strategy

**Decision**: The pre-built coordinator ships with a **fixed system prompt** (checked into `examples/doc-gardener/configs/coordinator.json` for the example; also bundled inside `internal/coordinator/default_prompt.md` for general use). The prompt:

1. Tells the coordinator its role: decompose goals, propose task trees, propose specialist spawns, monitor and iterate, never act directly on leaf tasks.
2. Lists the MCP tools it is allowed to call: `create_goal`, `propose_task_tree`, `propose_agent`, `request_resource`, `list_resources`, `my_status`, `send_message`, `search_messages`.
3. Gives a structured output format for `propose_task_tree` and `propose_agent` that matches the expected JSON schema of the MCP tools.
4. Enforces the delegation cap rule: "never propose a child whose tools or tier exceed your own."
5. Tells it to post progress updates to the goal channel, not DMs.

The coordinator **does not** run its own planner (tree-of-thought, HTN). It relies on the base LLM's agentic reasoning, following Paperclip's and Hermes's practice. V1 uses Gemini Flash (cheap, capable enough) as the default model, configurable.

**Rationale**: Structured planners add complexity and lock the coordinator into one decomposition style. A well-prompted LLM agent with the right tool scope does the job well enough for v1 and leaves room for iteration. Paperclip and Hermes both validate this choice empirically.

**Alternatives considered**:
- *Pre-built HTN / tree-of-thought planner*: out of scope, future work.
- *Multiple coordinator templates for different domains*: YAGNI; one general coordinator is enough for v1.

---

## D14. Resource request → secret injection workflow

**Decision**: End-to-end flow:

1. Agent calls `request_resource(name, type, reason, task_id)` MCP tool.
2. SynapBus writes `resource_requests` row and posts a structured message to `#requests` (workflow-enabled channel).
3. Human reviews, runs `synapbus secrets set NAME VALUE --scope agent:<name>` (or uses Web UI) — value is encrypted via NaCl secretbox and stored in the `secrets` table with the given scope.
4. SynapBus marks the resource request `fulfilled`, DMs the requester ("BREVO_API_KEY is now available for you"), and re-queues the agent for a heartbeat.
5. On the next subprocess launch, `internal/secrets/injector.go` builds an env map of all secrets in scope `user:<owner_id> ∪ agent:<agent_id> ∪ task:<task_id>`, sanitizes the names (`[A-Z0-9_]+`, uppercased), and sets them as env vars in the subprocess.
6. `list_resources` MCP tool returns only `{name, scope, available: bool}`, never values. Values are accessible only inside the subprocess via `os.Getenv`.

**Rationale**: Minimal surface area. Values never traverse the MCP wire. Scope precedence is `task > agent > user` (closest wins). Name sanitization prevents shell-injection of weird characters into env.

**Alternatives considered**:
- *Return secret values from `list_resources`*: rejected — violates least-privilege.
- *File-based secret delivery (drop into workdir)*: rejected — more moving parts and leaks plaintext to disk.

---

## D15. Web UI architecture for /goals pages

**Decision**: Two new Svelte 5 routes under `web/src/routes/goals/`. Index page lists goals (title, status, owner, total spend, last activity). Detail page shows:

- Goal header (title, status, budgets, owner)
- Task tree (recursive Svelte component, collapsible, status badges, per-task spend)
- Timeline (SSE-fed stream of events from the goal channel)
- Spawned agents panel (list with config_hash, reputation score, autonomy tier)
- Cost breakdown (by billing code)

Data comes from two new REST endpoints under `internal/api/handlers_goals.go`:

- `GET /api/goals` → list with summary fields
- `GET /api/goals/:id` → full snapshot (goal + tree + agents + costs)
- Real-time updates via the existing `/api/sse` pipe, filtered client-side to the goal's channel id

**Rationale**: Follows existing Svelte 5 + Tailwind conventions. Reuses existing SSE infrastructure. Two endpoints keep the API surface minimal. The task tree is recursive and collapsible — the natural UX for the ancestry model.

**Alternatives considered**:
- *One mega-endpoint `/api/goal-tree/:id`*: fine, same thing with a different name.
- *GraphQL*: too much new infrastructure for two pages.

---

## D16. Doc-gardener example concrete scope

**Decision**: V1 doc-gardener ships with a coordinator + **three specialist templates**:

| Specialist | Model | Job |
|---|---|---|
| `docs-scanner` | Gemini Flash | Read `docs.mcpproxy.app` pages (HTTP fetch), extract every CLI flag and config option mentioned; post findings to goal channel as structured `#finding` messages |
| `cli-verifier` | Gemini Flash | Run `mcpproxy --help` and grep for each flag reported by `docs-scanner`; post `#verified` / `#missing` reactions on the finding messages |
| `commit-watcher` | Gemini Flash | (Stub in v1) Post a placeholder message that this agent would watch `mcpproxy-go` commits and report drift |

Coordinator's expected decomposition: root task = "verify docs match source" → children `scan-docs` (for docs-scanner), `extract-source-flags` (for cli-verifier), `diff-and-report` (auto with the command verifier, runs a `diff` shell command on the two artifact sets).

The `--auto-approve` mode is a simple poller script `auto_approve.sh` that runs in the background, polls `#approvals` every 1 s, and posts `approve` reactions on any pending proposal messages. In absence of `--auto-approve`, the human must approve via Web UI or CLI reaction.

**Rationale**: This is a self-contained demo that doesn't require external API keys (uses existing Gemini credentials like cold-topic-explainer), does real work (actually verifies docs), and demonstrates all of: goal creation, task decomposition, dynamic spawning, heartbeats, verification, artifact channels, HTML report.

**Alternatives considered**:
- *More specialists (full doc regeneration)*: out of scope for the v1 acceptance test; what matters is that the machinery works, not that the demo produces perfect docs.
- *No auto-approve*: unacceptable — the example must be runnable unattended for CI and demo videos.

---

## D17. Rich HTML report structure

**Decision**: A Go `text/template` (not `html/template` — we trust our own data and want full HTML control) reads the DB via `internal/goals/service.GetGoalSnapshot(id)` and renders into `examples/doc-gardener/report.html`. Report sections:

1. **Header** — goal title, status, budget vs spent, start/end timestamps
2. **Task tree** — hierarchical list with status badges, per-task spend, verifier kind, duration
3. **Spawned agents** — table with name, config_hash (short), parent, autonomy tier, rolling reputation before/after, evidence count
4. **Cost breakdown** — per billing code totals, per agent totals, per task level
5. **Timeline** — chronological list of all events (task state transitions, agent spawns, alerts, DM-to-human)
6. **Artifacts** — links to attachment hashes, message IDs, goal channel URL for live exploration

Inline CSS (no external files), dark mode by default matching the Web UI palette. Single-file output for easy sharing.

**Rationale**: `text/template` gives full layout control without the HTML escaping of `html/template` (which is desirable here because the template author — us — is trusted and wants to embed raw HTML snippets). Single-file HTML with inline CSS means you can scp it off the laptop and open it anywhere. The sections map 1:1 to the spec's user stories, so they also double as acceptance evidence.

**Alternatives considered**:
- *html/template*: rejected — escaping everything is a pain for this kind of rich report where we want to include our own HTML snippets for badges and trees.
- *Svelte-based report page*: would require browser + dev server; doesn't work for offline sharing.
- *Markdown → HTML via a library*: adds dependency, less control.

---

## Consolidated reference table

| Axis | Decision | Section |
|---|---|---|
| Trust attach point | `(owner, config_hash, domain)` | D1 |
| Task storage | Table authoritative + backing channel event log | D2 |
| Ancestry propagation | Denormalized snapshot | D3 |
| Cost rollup | Leaf-only + recursive CTE at read | D4 |
| Atomic claim | Optimistic UPDATE with WHERE | D5 |
| Heartbeats | Extend existing reactor with 3 new wake sources | D6 |
| Budget race | Inside the same SQLite transaction | D7 |
| Secret encryption | NaCl secretbox, local master key file | D8 |
| Rolling reputation | Exponential decay over append-only ledger | D9 |
| Verification kinds | auto / peer / command | D10 |
| Approvals | Reuse reactions workflow (migration 013) | D11 |
| Config hash form | Canonical-JSON of model + prompt + tools + skills + mcp + subagents | D12 |
| Coordinator LLM | Fixed prompt, default Gemini Flash | D13 |
| Resource injection | Env vars in subprocess, values never cross MCP | D14 |
| Web UI | Two new Svelte routes + two REST endpoints + SSE | D15 |
| Example scope | Coordinator + 3 specialists, auto-approve script | D16 |
| HTML report | Single-file text/template output | D17 |

**All design axes resolved. Ready for Phase 1.**
