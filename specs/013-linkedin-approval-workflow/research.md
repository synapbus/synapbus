# Research: LinkedIn Comment Approval Workflow

## Decision 1: SynapBus MCP Tool Fixes

**Decision**: Fix 4 issues in SynapBus MCP bridge before building agent workflow.
**Rationale**: Agents need reliable MCP tools. Current bugs (missing workflow_state in react response, incorrect list_by_state filtering) would cause agent failures.
**Alternatives considered**: Working around bugs in agent code (rejected: fragile, defeats MCP-native principle).

## Decision 2: Posting Agent Architecture

**Decision**: Create a new standalone `linkedin-poster` agent in the searcher repo that reads approved messages from SynapBus (not from PostgreSQL).
**Rationale**: SynapBus is the source of truth for the approval workflow. Reading from SynapBus makes the agent independent of the DB and consistent with the MCP-native approach.
**Alternatives considered**: Extending existing `engagement/` posting module to read from SynapBus (rejected: tightly coupled to PostgreSQL schema, would require dual-path logic).

## Decision 3: Agent Configuration Management

**Decision**: Store CLAUDE.md and .claude/skills in the searcher git repo under each agent's directory.
**Rationale**: Git provides version history, auditable changes, and agents can commit via `gh` CLI.
**Alternatives considered**: Storing config in SynapBus messages (rejected: not version-controlled). Storing in a separate repo (rejected: adds complexity).

## Decision 4: Feedback Reflection Approach

**Decision**: Use Claude Agent SDK to reflect on approved/rejected comments, generate writing rules, and update CLAUDE.md.
**Rationale**: Claude can analyze patterns in human feedback (what was approved, what was rejected, what was edited) and derive actionable rules.
**Alternatives considered**: Rule-based pattern extraction (rejected: too rigid, can't understand nuanced feedback).

## Decision 5: Channel Name

**Decision**: `approve-linkedin-comment` (not `approvals`, not `approve-comments`).
**Rationale**: Platform-specific channels allow different workflow settings per platform. Future channels: `approve-hn-comment`, `approve-reddit-comment`.
**Alternatives considered**: Reusing `#approvals` (rejected: mixes LinkedIn with other content types, can't set LinkedIn-specific workflow settings).
