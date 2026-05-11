# Feature Specification: Proactive Memory & Dream Worker

**Feature Branch**: `020-proactive-memory-dream-worker`
**Created**: 2026-05-11
**Status**: Draft
**Input**: User description: "Proactive owner-scoped agent memory: SynapBus pushes minimal relevant memory to agents on every MCP call, and a background 'dream' worker consolidates memory offline."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Agent gets relevant prior context without asking (Priority: P1)

An agent owned by a human starts working on a task — opening a new session, claiming inbound messages, replying to a teammate, or running a search. Today the agent has to remember to query a memory store first, so it usually doesn't, and it loses continuity with prior work. With this feature, SynapBus automatically attaches a small "what you should know right now" packet to every meaningful tool response. The agent gets the three to five most relevant prior memories scoped to its human owner — without spending a tool call to fetch them.

**Why this priority**: This is the headline complaint. Today the memory pool exists (about 500 entries in #open-brain) but is invisible per-task. Fixing this restores continuity across agent sessions immediately, even before any background processing exists.

**Independent Test**: Deploy injection only (no dream worker). Run a single agent against SynapBus; observe that responses from status-check, inbox-claim, message-send, and search tools include a context packet of relevant prior memories. Measure that the agent makes one fewer search call per task on average.

**Acceptance Scenarios**:

1. **Given** an agent owned by human H has 200 prior memories in the pool, **When** the agent calls a status-check tool, **Then** the response includes up to 5 relevant memories scoped to H, ranked by combined recency + similarity, sized within a configurable token budget.
2. **Given** an agent sends a message containing the phrase "Kuzu graph DB", **When** the send completes, **Then** the response includes prior memories that mention Kuzu, graph databases, or related decisions made by the owner's agent fleet.
3. **Given** agent A is owned by human H1 and agent B is owned by human H2, **When** both call the same tool with identical arguments, **Then** A and B receive disjoint memory packets — each only sees memories from its own owner's pool.
4. **Given** the configured token budget is set to zero, **When** any agent calls any tool, **Then** no context packet is attached (feature is fully opt-out by configuration).
5. **Given** the underlying retrieval finds no memory above the relevance floor, **When** an agent calls a tool, **Then** the response contains a context packet field that is empty or omitted entirely — no irrelevant filler.

---

### User Story 2 - Agent has a stable "who am I, what am I doing" anchor (Priority: P2)

Each agent in an owner's fleet has a small editable identity-and-context blob (a "core memory") that captures its role, current focus, and protocols it has been told to follow. This blob is always included in the agent's session-start response. When the agent's recent activity changes its focus, the dream worker rewrites this blob so the agent doesn't have to re-derive context from scratch each session.

**Why this priority**: Per Letta's published numbers, sleep-time rewrites of core memory yield meaningful accuracy gains and lower per-turn cost. Without P1 this is invisible; with P1, the core blob becomes the most-injected piece of memory.

**Independent Test**: Manually write a core-memory blob for one agent via the admin interface. Confirm that the agent receives this blob on every session-start tool call. Have the dream worker (or a human, simulating it) rewrite the blob. Confirm the next session-start call returns the new content.

**Acceptance Scenarios**:

1. **Given** human owner H sets a core memory of 800 characters for agent A, **When** A calls a session-start tool, **Then** the response includes the exact core-memory text alongside any relevance-scored memories.
2. **Given** a core memory exceeds the configured size cap, **When** anyone attempts to save it, **Then** the save is rejected with a clear error referencing the cap.
3. **Given** no core memory exists for agent A, **When** A calls a session-start tool, **Then** the response omits the core-memory field entirely — no errors, no empty placeholder.

---

### User Story 3 - Memory pool stays high-quality without human intervention (Priority: P2)

Without maintenance, the memory pool accumulates near-duplicates, stale facts, and contradictions, and retrieval quality decays. A background "dream" worker periodically dispatches consolidation work to a designated agent (which uses dedicated memory tools to do the actual reading and writing). The worker handles four kinds of jobs: writing higher-level abstractions when enough new memories have accumulated; rewriting each agent's core memory nightly; deduplicating near-identical memories and resolving contradictions; and linking related memories so retrieval surfaces the cluster, not just one item.

**Why this priority**: Memory hygiene compounds over weeks. Without it, P1 still works on day one but degrades. With it, retrieval quality holds steady and improves as the pool grows.

**Independent Test**: Seed the pool with 50 deliberately overlapping memories (5 paraphrases of the same fact, 3 contradictions, 20 unrelated items). Run the dream worker once. Verify: paraphrases are marked as duplicates with one canonical winner; contradictions are flagged with an explicit supersession; unrelated items are linked to relevant neighbors where applicable; an audit log records every action taken.

**Acceptance Scenarios**:

1. **Given** N new unprocessed memories have accumulated for owner H since the last reflection, where N exceeds the configured watermark, **When** the dream worker runs its periodic check, **Then** it dispatches a reflection job that produces 3–5 higher-level abstractions and writes them back as new memories tagged as reflections, with provenance links to the source memories.
2. **Given** two memories with cosine similarity above 0.95 and the same factual claim, **When** the dedup job runs, **Then** one is kept as canonical and the other is soft-deleted with a duplicate-of link.
3. **Given** memory A states "X is true" and memory B (newer) states "X is false", **When** the contradiction job runs, **Then** A is marked as superseded by B with a reason recorded, and retrieval no longer surfaces A by default.
4. **Given** the dream worker is configured to dispatch jobs to a specific agent, **When** a trigger fires, **Then** the worker invokes that agent through the existing harness/spawn mechanism (not via a direct message), and the dispatched agent uses only the dedicated memory tools to do the work — it cannot bypass the audit log.
5. **Given** an owner has zero new memories since the last consolidation, **When** the dream worker runs, **Then** no consolidation job is dispatched for that owner — no wasted cost.

---

### User Story 4 - Human owner can inspect and override memory operations (Priority: P3)

A human owner can see what's in their memory pool, what was injected on recent tool calls, and what the dream worker has done. They can correct mistakes: undo a bad dedup, restore a soft-deleted memory, mark a memory as protected so it can't be auto-consolidated, or pin/unpin a memory so it always (or never) gets injected.

**Why this priority**: Trust is a precondition for autonomy. Without visibility, owners won't enable the dream worker on real data. The injection layer (P1) can ship before this exists, but the dream worker shouldn't be enabled on production data until owners can audit it.

**Independent Test**: Use the Web UI to view all memories for one owner. Soft-delete a memory; verify it disappears from retrieval. Undo the delete; verify it comes back. Pin a memory; verify it shows up in every relevant injection regardless of similarity score.

**Acceptance Scenarios**:

1. **Given** an owner navigates to a Memory section in the Web UI, **When** they view it, **Then** they see all their memories with provenance, links, and consolidation history.
2. **Given** the dream worker has marked memory M as duplicate, **When** the owner clicks "restore", **Then** M is reactivated and the duplicate-of link is removed; the audit log records the override.
3. **Given** an owner pins memory M, **When** any of their agents calls an injection-eligible tool, **Then** M is included in the context packet even if its similarity score is below the relevance floor (subject to the token budget).

---

### Edge Cases

- **No embedding provider configured**: The system gracefully degrades to full-text-only retrieval for injection. The dream worker's link/dedup jobs that depend on vector similarity are skipped, with a warning logged. Manual reflections and supersessions still work.
- **A dispatched consolidation job fails mid-flight**: The audit log records the failure with an error and the partial work done. The next periodic check retries the job. Repeated failures on the same job for the same owner pause future dispatches for that job type and surface an alert to the owner.
- **A consolidation job runs longer than the wallclock budget**: The worker terminates the job at the budget, records what was completed, and resumes on the next run from where it stopped.
- **Two consolidation jobs targeting the same memory arrive concurrently** (e.g. a manual owner edit during a nightly run): The owner edit wins; the consolidation job logs a "stale target" outcome and skips the conflicting action.
- **Memory pool grows beyond a reasonable working-set size for one owner**: Retrieval still returns top-K within the relevance floor; consolidation jobs operate on a sliding window of recent + cluster-relevant memories rather than the full set.
- **A pinned memory loses relevance to current activity**: The pin is honored regardless. Owners are responsible for unpinning. Document this clearly in the UI.
- **An agent without an owner attempts to call an injection-eligible tool**: Injection is skipped (no owner to scope to). The tool itself still works.
- **The injection token budget would be exceeded by a single memory**: Truncate the memory to fit; mark the truncation in the packet so the agent knows there's more.
- **A consolidation tool is called by an agent outside the dream worker dispatch flow**: Reject — only the worker-dispatched session can call these tools, identified via a one-time dispatch token.

## Requirements *(mandatory)*

### Functional Requirements

#### Memory pool and ownership

- **FR-001**: The system MUST maintain a memory pool scoped to each human owner. Every memory MUST be attributable to the owner of the agent that produced it.
- **FR-002**: Retrieval MUST filter strictly by the requesting agent's owner. An agent MUST NOT see any memory belonging to a different owner under any retrieval path.
- **FR-003**: The system MUST recognize messages on designated "memory channels" (including `#open-brain` and channels explicitly flagged as memory channels) as eligible memories. Other channel messages are not eligible by default.
- **FR-004**: The system MUST support soft-deletion (status flag) for memories, with the ability to restore a soft-deleted memory by an authorized human owner.
- **FR-005**: The system MUST support temporal supersession: marking memory A as obsolete because memory B replaces it, with a recorded reason. Superseded memories are excluded from default retrieval but remain in the audit history.

#### Proactive context injection

- **FR-006**: The system MUST attach a relevant-context packet to the response of designated tool calls. The packet MUST include up to a configurable number of memories (default 3–5) and stay within a configurable token budget (default approximately 500 tokens).
- **FR-007**: Retrieval for injection MUST use the existing hybrid (semantic + full-text) ranking with the existing relevance floor. Items below the floor MUST be excluded.
- **FR-008**: The set of injection-eligible tools MUST include at minimum: status-check, inbox-claim, inbox-read, message-send, search, and the generic execute tool. Pure metadata tools (e.g. listing one's own agents) MAY be excluded.
- **FR-009**: If the response context for a tool call contains a query, message body, or current channel topic, that text MUST be used as the retrieval query. Otherwise, recent owner activity (last 24h) is used as the implicit query.
- **FR-010**: The system MUST support a per-agent "core memory" blob (size-capped, default ~1KB). When present, it MUST be included in every status-check response in addition to retrieval-based memories.
- **FR-011**: Owners MUST be able to pin and unpin individual memories. Pinned memories MUST be included in every injection-eligible tool response for that owner's agents, subject only to the token budget — bypassing the relevance floor.
- **FR-012**: The injection feature MUST be disable-able per owner and globally via configuration. With injection disabled, tool responses match their current shape exactly (no extra field).

#### Dream worker (background consolidation)

- **FR-013**: A background worker MUST periodically check each owner's memory pool against trigger conditions for four consolidation job types: reflection-on-watermark, sleep-time core-memory rewrite, deduplication-and-contradiction-resolution, and link generation.
- **FR-014**: When a trigger fires, the worker MUST dispatch the job to a configured consolidation agent via the existing harness/spawn mechanism. The worker MUST NOT use direct messaging or any path that itself triggers reactive runs.
- **FR-015**: The dispatched consolidation agent MUST only use the dedicated memory-consolidation tool surface (list, write-reflection, rewrite-core, mark-duplicate, supersede, add-link). Every call MUST be recorded in an immutable audit log scoped to the owner.
- **FR-016**: The worker MUST honor a per-owner wallclock budget per consolidation pass (default 10 minutes). When the budget is exceeded, in-progress work is recorded and the pass terminates.
- **FR-017**: The system MUST automatically derive lightweight reference links (mention, reply-to, channel co-occurrence) between memories without invoking the consolidation agent. These links MUST be available to retrieval alongside agent-generated links.
- **FR-018**: Reflection memories MUST be tagged so they are distinguishable from raw memories. Source-memory provenance MUST be recorded.
- **FR-019**: The dream worker MUST be disable-able per owner and globally via configuration. The injection layer (P1) MUST function correctly when the dream worker is disabled.
- **FR-020**: A consolidation agent's dispatch session MUST be identified by a one-time dispatch token. Memory-consolidation tools MUST reject calls from sessions without a valid token.

#### Human-owner audit and override

- **FR-021**: Human owners MUST be able to view all memories in their pool, including soft-deleted ones, via the Web UI.
- **FR-022**: For each memory, the UI MUST surface: original source, provenance (parent reflections / supersessions / duplicates), links to related memories, current status, and pinning state.
- **FR-023**: Owners MUST be able to: pin/unpin a memory, soft-delete or restore a memory, undo a consolidation action recorded in the audit log, and mark a memory as "protected" so the dream worker cannot soft-delete or supersede it.
- **FR-024**: The audit log MUST be searchable by owner, agent, action type, and time range.
- **FR-025**: Recent injections (what was attached to which tool call) MUST be inspectable for at least the last 24 hours so owners can debug "why did my agent know that?"

### Key Entities *(include if feature involves data)*

- **Memory**: A retained piece of context belonging to an owner. Has source (origin message or reflection), status (active/soft-deleted/superseded), embedding (when available), provenance (parents), and a pinned/protected flag set.
- **Core memory blob**: A small, owner+agent-scoped editable text. One per (owner, agent) pair. Replaces itself wholesale on rewrite.
- **Memory link**: A directed typed edge between two memories. Type ∈ {refines, contradicts, examples, related, duplicate-of, superseded-by, mention, reply-to, channel-cooccurrence}.
- **Consolidation job**: A scheduled or watermark-triggered unit of dream work. Has owner, type, trigger reason, dispatch token, status, start/end time, summary of actions taken, and outcome (success/partial/failed).
- **Audit entry**: An immutable record of every memory mutation — dedup, supersession, link addition, core rewrite, pin/unpin, restore — including actor (agent or human), dispatch token if any, and before/after diff.
- **Dispatch token**: A short-lived single-use credential that authorizes a consolidation agent to call memory-consolidation tools for a specific owner. Bound to one job.

## Assumptions

- **Default eligible memory channels**: `#open-brain` plus any channel explicitly flagged via channel metadata as a memory channel. Owners can mark additional channels as memory channels through the admin interface.
- **Default trigger watermark for reflection**: 20 new unprocessed memories per owner since the last reflection job.
- **Default deep-pass schedule**: 03:00 UTC daily for sleep-time core-memory rewrite. Owners can shift this per-owner.
- **Default consolidation agent**: Claude Code, invoked through the existing agent-spawn / harness path. Other agents are configurable per-owner.
- **Retention**: Memories are NOT subject to the standard message retention policy (currently 12 months). They are retained until explicitly deleted or superseded, regardless of age.
- **Token estimation**: Token counts for the injection budget are computed by a simple character-based heuristic (≈ 4 characters per token) — exact tokenization is not required for budget enforcement.
- **Concurrent dispatch**: At most one consolidation job per (owner, job type) runs at a time. Jobs for different owners can run concurrently up to a global worker pool limit (default 4).
- **Embedding provider availability**: When no embedding provider is configured, the system degrades gracefully — injection uses full-text-only retrieval; dedup and link-generation jobs that depend on vector similarity are skipped with a logged warning.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For every injection-eligible tool call where the owner's memory pool contains at least one item, the response includes a non-empty relevant-context packet ≥ 95% of the time (the 5% accounts for cases where the relevance floor rejects everything).
- **SC-002**: Median added latency per tool call from injection stays below 50 milliseconds, measured end-to-end including retrieval and packet assembly.
- **SC-003**: When measured on a benchmark dogfood run of the searcher swarm before and after this feature, the average number of explicit memory-search tool calls per agent task drops by at least 40%, without a regression in task completion rate.
- **SC-004**: Agents complete a "catch up on prior context" benchmark task using at least 30% fewer total tokens (system + tool inputs) with this feature enabled versus disabled.
- **SC-005**: A full owner-level dream-worker pass completes within the configured wallclock budget (default 10 minutes per owner) on a memory pool of up to 5,000 memories, on the reference deployment.
- **SC-006**: After one week of dream-worker operation on a seeded test pool (500 memories with deliberate duplicates and contradictions), the duplicate rate (memories with cosine similarity > 0.95 and overlapping content) drops below 5%, down from a seeded baseline of 20%.
- **SC-007**: Owners can audit every memory mutation made in the past 30 days through the Web UI with response time under 2 seconds.
- **SC-008**: Zero cross-owner memory leakage detected in adversarial testing: agents owned by H1 cannot retrieve, view, or be injected with any memory created by H2's agents.
- **SC-009**: When injection is disabled via configuration, response payload sizes for the affected tools are within 1% of pre-feature baseline.
