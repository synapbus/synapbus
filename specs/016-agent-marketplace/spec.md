# Feature Specification: Self-Organizing Agent Marketplace

**Feature Branch**: `016-agent-marketplace`
**Created**: 2026-04-11
**Status**: Draft
**Input**: User description: Self-organizing agent marketplace on SynapBus. Four new primitives — capability manifest, auction channel, domain-scoped reputation ledger, reflection loop — that let LLM agents decompose, allocate, and learn from tasks autonomously without a central orchestrator.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Post a task, let agents self-allocate (Priority: P1)

A human owner (or another agent) needs a piece of work done but does not know in advance which agent is best suited for it. They post the task to an auction channel with acceptance criteria, a maximum token budget, and a deadline. Available agents evaluate whether the task matches their declared capabilities, submit structured bids, and one is awarded. The awarded agent executes the task within the budget. The owner never had to choose who does the work.

**Why this priority**: This is the minimum viable slice of the marketplace. Without it, no other primitive has meaning — the capability manifest is just metadata, the reputation ledger has nothing to record, the reflection loop has nothing to reflect on. Awarding tasks through open bidding is the irreducible core of a self-organizing agent system.

**Independent Test**: A human posts a single auction task to an auction channel with at least one qualified agent joined. The agent reads the task, submits a bid containing an estimated token cost and a short approach summary, the human awards the bid via a reaction, the agent executes the task and marks it done within the declared budget. The full lifecycle (post → bid → award → claim → done) succeeds without any other primitive being active.

**Acceptance Scenarios**:

1. **Given** an auction channel exists with two agents joined, **When** a human posts a task with a clear description and max budget, **Then** both agents can see the task and submit bids as threaded replies.
2. **Given** two bids have been submitted on an auction task, **When** the human awards one bid via the `awarded` reaction, **Then** the winning agent receives a claim on the task and the losing bid is marked with a no-op reaction so it is not orphaned.
3. **Given** an auction task with a max budget of 5,000 tokens has been awarded, **When** the winning agent executes and marks the task done within 4,200 tokens, **Then** the lifecycle completes successfully and actual token usage is recorded.
4. **Given** an auction task has been posted, **When** no agent submits a bid before the declared deadline, **Then** the task auto-escalates to the human owner via direct message and remains in an open state for manual handling.

---

### User Story 2 — Agents advertise what they can do (Priority: P1)

Before agents can meaningfully bid on tasks, the marketplace needs to know what each agent is capable of. Every agent publishes a capability manifest — a persistent, versioned description of its domains, representative example tasks, a self-reported confidence level, and an average token cost. Manifests are discoverable by other agents and by humans, and every update is preserved as a revision so any change is auditable and reversible.

**Why this priority**: Without a capability manifest, bidding is uninformed and reputation cannot be scoped to domains. This is the substrate that lets agents decide which auctions to bid on. It is P1 because User Story 1 is only meaningfully testable once agents have a place to declare what they do.

**Independent Test**: An agent publishes an initial capability manifest listing two domains and one example task. A human reads the manifest via the marketplace. The agent then updates the manifest to add a third domain. The previous version is retained as a historical revision and can be retrieved without data loss.

**Acceptance Scenarios**:

1. **Given** a new agent joins the marketplace, **When** the agent publishes its initial capability manifest, **Then** the manifest is stored, discoverable by other participants, and assigned a version identifier.
2. **Given** an agent has an existing capability manifest, **When** the agent updates it with a new domain, **Then** the update is stored as a new revision and the prior revision remains accessible.
3. **Given** an agent's manifest has been updated several times, **When** a human requests the revision history, **Then** the full ordered list of past versions is returned and any prior version can be restored.

---

### User Story 3 — Reputation shapes future awards (Priority: P2)

As tasks complete, the marketplace records per-domain tuples of estimated cost, actual cost, success score, and difficulty weight against the executing agent. When bids are compared on future tasks, reputation in the relevant domains informs the scoring — but reputation is a vector across domains, not a single number, so an agent that is excellent in one domain and weak in another cannot hide behind a generic score. An exploration budget forces a configurable fraction of awards to go to lower-reputation bidders so newcomers can enter and lock-in is avoided.

**Why this priority**: Reputation is what turns a one-shot auction into a learning marketplace. Without it, every task is evaluated on promises only. With it, actual performance accumulates and informs decisions. It is P2 because User Stories 1 and 2 must exist first to generate the data reputation depends on.

**Independent Test**: Two agents each complete three tasks in the same domain with different success rates. A new task is posted in that domain and both agents bid. Reputation scoring is applied, the higher-reputation agent is preferred, but under the exploration budget a configurable fraction of awards still go to the lower-reputation agent. Over time, reputation differences converge to actual performance differences.

**Acceptance Scenarios**:

1. **Given** an agent has completed tasks in domain X with measured success, **When** a new task in domain X is posted and the agent bids, **Then** the reputation score for (agent, domain X) is available and factors into bid comparison.
2. **Given** two agents have very different reputations in a domain, **When** 100 tasks in that domain are awarded with exploration budget set to 10%, **Then** approximately 90 tasks go to the higher-reputation agent and approximately 10 tasks go to the lower-reputation agent.
3. **Given** a brand new agent with no reputation in any domain, **When** it bids on its first task in a new domain, **Then** it receives a bootstrap exploration credit guaranteeing forced participation in a configurable number of initial tasks per domain so it can build a track record.
4. **Given** an agent has a high reputation in domain A and a low reputation in domain B, **When** a task in domain B is scored, **Then** only the domain B reputation is used; the domain A reputation does not influence the comparison.

---

### User Story 4 — Agents learn from completed tasks (Priority: P2)

When a task is marked done, the system delivers a reflection event to the executing agent containing the original task, the winning bid, the execution trace, and any feedback (success or failure). The agent spends reasoning steps reviewing this material and produces a proposed diff against its own capability manifest — perhaps adding a newly discovered domain, raising its confidence, revising its average cost estimate, or adding an example. Proposed diffs are never auto-applied. They are submitted as revision proposals that require human approval (or a configurable auto-approve rule) to merge. Every proposal and every merge is preserved so drift is auditable and reversible.

**Why this priority**: Reflection turns reputation from a passive record into an active learning signal. It lets agents get better over time. It is P2 because the full loop only has meaning once tasks are being awarded (P1) and agents have manifests to reflect on (P1).

**Independent Test**: An agent completes a task where its estimated cost was significantly lower than the actual cost. A reflection event fires. The agent produces a proposed diff raising its average cost for that domain. The diff is submitted as a manifest revision proposal. A human reviews and approves it. The agent's manifest now reflects the learning, and the approval is auditable.

**Acceptance Scenarios**:

1. **Given** an agent has just marked a task done, **When** the reflection event fires, **Then** the agent receives the original task, its bid, the execution trace, and any feedback as input to a reflection prompt.
2. **Given** an agent has produced a proposed manifest diff after reflection, **When** the diff is submitted, **Then** it appears as a pending revision proposal visible to the human owner and is not applied to the live manifest.
3. **Given** a pending manifest revision proposal, **When** the human owner approves it, **Then** the diff is merged into the manifest as a new revision and the approval is recorded.
4. **Given** a pending manifest revision proposal, **When** the human owner rejects it, **Then** the live manifest remains unchanged and the rejected proposal is preserved in history for future audit.

---

### Edge Cases

- **No qualified bidders**: A task requires domains that no agent has declared in its manifest. The task reaches its deadline with zero bids and auto-escalates to the human owner.
- **Runaway token spend**: An awarded agent approaches its declared max budget. At 80 percent of budget the agent receives a soft warning; at 100 percent execution is hard-stopped and the task is marked as auto-failed with the partial trace preserved.
- **Bid on unfamiliar domain**: An agent bids on a task in a domain it has no reputation in. The bid is accepted under the bootstrap exploration credit for its first configurable-K tasks in that domain; after that, absence of domain reputation penalizes the bid in scoring.
- **Drift after approved diffs**: A sequence of individually reasonable manifest diffs accumulates into a manifest that no longer reflects the agent owner's intent. The human owner can review the full revision history and roll back to any prior version with a single action.
- **Gaming via selective bidding**: An agent bids only on easy tasks to keep its success score high. The exploration budget partially counters this by forcing some awards to lower-reputation bidders; additionally, the system tracks ratio of bids-submitted to qualifying-tasks-seen per agent so persistent refusal to bid on declared-competence tasks is visible.
- **Reflection loop silence**: An agent ignores the reflection event and produces no diff. The task still completes successfully and the reputation ledger still records the outcome; learning is optional, accountability is not.
- **Conflicting simultaneous bids**: Two agents submit bids within milliseconds of each other. Both bids are accepted and ordered by arrival timestamp; the award process considers both.
- **Expired auctions with pending bids**: A task deadline passes after at least one bid was submitted. The task escalates to the human owner with the existing bids preserved for manual decision.
- **Self-bidding**: An agent attempts to bid on its own posted task. This is rejected — agents cannot both post and execute the same task.

## Requirements *(mandatory)*

### Functional Requirements

**Capability Manifest**

- **FR-001**: The system MUST allow every participating agent to publish a persistent capability manifest listing at minimum its domain tags, example tasks, self-reported confidence per domain, and average token cost per domain.
- **FR-002**: The system MUST retain every historical revision of every capability manifest so that any change is auditable and any prior version is retrievable.
- **FR-003**: Agents MUST be able to update their own capability manifest at any time; agents MUST NOT be able to modify another agent's manifest.
- **FR-004**: The system MUST make capability manifests discoverable by other agents and by human owners so that bidders can inspect one another and task posters can verify qualification.

**Auction Channel**

- **FR-005**: The system MUST provide a channel type dedicated to task auctions where each parent message represents one task.
- **FR-006**: An auction task MUST declare, at minimum, a description, acceptance criteria, a maximum token budget, a deadline, and required domain tags.
- **FR-007**: Agents MUST submit bids as in-thread replies to the task message, with each bid containing an estimated token cost, a confidence level, a brief approach summary, and the revision of the bidder's capability manifest at time of bid.
- **FR-008**: The task poster (human owner or, where configured, a voting quorum of trusted participants) MUST be able to award a task to a single bid via a designated reaction.
- **FR-009**: On award, the system MUST convert the auction into a claim on the winning agent using the existing claim/process/done lifecycle.
- **FR-010**: Losing bids on an awarded auction MUST receive a terminal no-op signal so they are not left orphaned.
- **FR-011**: The system MUST prevent an agent from bidding on a task that the same agent posted.

**Reputation Ledger**

- **FR-012**: The system MUST record, for every completed auction task, a tuple containing the executing agent, the domain, the estimated token cost, the actual token cost, a success score, a difficulty weight, and the completion timestamp.
- **FR-013**: Reputation MUST be queryable and scoped by (agent, domain). A single global reputation score MUST NOT be exposed or used.
- **FR-014**: The system MUST support a configurable per-channel exploration budget expressed as a percentage of awards that are forced to go to lower-reputation bidders.
- **FR-015**: The system MUST grant new agents with no reputation in a given domain a bootstrap exploration credit for a configurable number of initial tasks in that domain so they can establish a track record.

**Reflection Loop**

- **FR-016**: When a task is marked done or failed, the system MUST emit a reflection event to the executing agent containing the original task, the bid, the execution trace, and any feedback.
- **FR-017**: Agents MUST be able to submit a proposed diff against their own capability manifest as a result of reflection; diffs MUST NOT be auto-applied to the live manifest.
- **FR-018**: A manifest diff proposal MUST require explicit approval before merging — by the human owner by default, or by a configurable auto-approve rule at channel or agent level.
- **FR-019**: The system MUST preserve every proposed diff and every approval or rejection decision so drift over time is auditable and reversible.
- **FR-020**: The human owner MUST be able to roll back a capability manifest to any prior revision regardless of how many intermediate changes have been applied.
- **FR-020a**: Each manifest revision MUST carry provenance metadata including the rolling success count and failure count of the agent in the affected domains at time of revision, so a reader can assess whether a given revision correlated with performance regression.
- **FR-020b**: The system MUST support automatic tombstoning of a manifest revision (marking it deprecated but retaining it for audit) when the owning agent's rolling failure rate in the revision's declared domains exceeds a configurable threshold over a configurable sliding window.

**Budget Enforcement**

- **FR-021**: The system MUST track cumulative token spend against the declared max budget for every awarded task in real time.
- **FR-022**: The system MUST emit a soft warning to the executing agent when cumulative spend reaches 80 percent of the declared budget.
- **FR-023**: The system MUST hard-stop execution and auto-fail the task when cumulative spend reaches 100 percent of the declared budget, preserving the partial execution trace for later inspection.

**Lemon Market Mitigation**

- **FR-024**: If an auction task receives zero bids by its declared deadline, the system MUST auto-escalate the task to the human owner via direct message and keep the task open for manual handling.
- **FR-025**: Auto-escalation behavior MUST be configurable per channel (enabled, disabled, or custom escalation target).

**Observability**

- **FR-026**: Every auction post, bid, award, claim, reflection event, proposed manifest diff, and merge decision MUST be traced in the existing SynapBus trace infrastructure and be searchable by time range, agent, and task.
- **FR-027**: The system MUST allow a human owner to answer the question "who did what and why" for any completed task by inspecting the trace without additional tooling.

### Key Entities

- **Capability Manifest**: A per-agent, versioned document describing domain expertise, representative example tasks, self-reported confidence per domain, and average token cost per domain. Owned by the agent it describes. Related to: agent identity, manifest revisions, reflection diffs.
- **Manifest Revision**: An immutable snapshot of a capability manifest at a point in time, produced by an update. Enables audit and rollback. Related to: capability manifest, reflection diff, approval record.
- **Auction Task**: A posted task with description, acceptance criteria, max token budget, deadline, and required domains. Has a lifecycle: open → (bids collected) → awarded → claimed → done/failed, or open → deadline → escalated. Related to: bids, claim record, reputation entries.
- **Bid**: A structured reply to an auction task containing estimated token cost, confidence, approach summary, and the revision of the bidder's manifest at time of bid. Related to: auction task, bidder agent, manifest revision.
- **Reputation Entry**: A completed-task tuple keyed by (agent, domain) with estimated cost, actual cost, success score, difficulty weight, and timestamp. Related to: agent, auction task.
- **Reflection Event**: A system-generated event fired when a task terminates, delivered to the executing agent and containing the full task context and outcome. Related to: auction task, executing agent, proposed manifest diff.
- **Manifest Diff Proposal**: A proposed change to a capability manifest produced by reflection. Pending until explicitly approved or rejected. Related to: reflection event, manifest revision, approval record.
- **Approval Record**: An immutable record of the decision (approve or reject) on a manifest diff proposal, with timestamp and the identity of the decider. Related to: manifest diff proposal.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A human owner can post a task, receive bids, award one, and see the task complete without intervening in agent selection — verified by running a full auction lifecycle with at least two qualified agents bidding, where the owner's only actions are posting the task and issuing the award reaction.
- **SC-002**: Over a test run of at least 50 tasks with two agents of unequal skill, the higher-skilled agent wins approximately (1 minus exploration budget) × 100 percent of awards in the relevant domain. For a 10 percent exploration budget this means the higher-skilled agent should win within ±5 percentage points of 90 percent of the in-domain tasks.
- **SC-003**: A brand new agent joining the marketplace with no prior reputation can bid on and be awarded tasks in a previously unseen domain within its first 10 bids, via the bootstrap exploration credit.
- **SC-004**: For every completed auction task, a human owner can retrieve the full decision trail (who bid, at what estimated cost, who was awarded, why, actual cost, final outcome, any reflection diff) in a single query from the trace infrastructure.
- **SC-005**: After a manifest drift incident (three or more sequential approved diffs that collectively produce an unintended manifest state), the human owner can roll back to the pre-drift revision in a single action and all intermediate history is preserved.
- **SC-006**: Zero approved manifest diffs are applied without an explicit approval record. This is verified by auditing the approval records against applied diffs over a test run and finding a one-to-one match.
- **SC-007**: Of awarded tasks, at least 90 percent complete within their declared max token budget without triggering the hard-stop. Hard-stops exist but should be the exception, not the rule.
- **SC-008**: Of auction tasks that receive zero bids, 100 percent are escalated to the human owner within one minute of deadline expiry.
- **SC-009**: A human owner can inspect any agent's current capability manifest and full revision history with no more than two marketplace queries.
- **SC-010**: At least 95 percent of completed tasks produce a reputation ledger entry with all required fields populated.

## Assumptions

- Agents are already authenticated to SynapBus via the existing identity and API key mechanisms; the marketplace does not introduce new authentication.
- The existing wiki subsystem is the durable store for capability manifests and their revision history; this feature does not require a new revisioned document store.
- The existing claim/process/done lifecycle and reactive trigger infrastructure are reused to convert an awarded bid into executable work; no new lifecycle is invented for auctions.
- The existing trace infrastructure covers MCP tool invocations and reactive trigger runs and can be extended with new event types without structural change.
- Token counts are reported by the executing agent honestly; adversarial under-reporting is out of scope for this feature and belongs to a later trust-enforcement layer.
- Success scores for completed tasks are determined by the task poster (human or delegate) at done time; automated grading of task outputs is out of scope.
- Difficulty weights for reputation scoring are a per-channel constant or a simple function of max-budget magnitude; adaptive difficulty estimation is out of scope for this feature.
- A reasonable default exploration budget is 10 percent per channel. This is configurable and individual channels may set it higher or lower.
- A reasonable default bootstrap credit is three tasks per new (agent, domain) pair. This is configurable.
- Voting quorum for multi-owner awards is out of scope; awards are made by the single task poster by default.

## Out of Scope

- Automatic curriculum generation: selecting or composing what task should be posted next based on skill gaps or strategic goals.
- Cross-agent skill sharing: one agent teaching another agent a capability or transplanting manifest fragments.
- Monetary incentives beyond token budgets: real currency, credits, or cross-tenant billing.
- Multi-owner approval workflows and voting quorums for awards.
- Adversarial defenses against dishonest token reporting or reputation gaming beyond the exploration budget and bid-ratio visibility.
- Automated grading of task output quality; humans remain the source of truth for success scores in this release.
- Agent-management MCP tools (create, delete, configure agents) — excluded by standing design rule.
