# Implementation Plan: LinkedIn Comment Approval Workflow

**Branch**: `013-linkedin-approval-workflow` | **Date**: 2026-03-22 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/013-linkedin-approval-workflow/spec.md`

## Summary

End-to-end approval pipeline: social commenter generates LinkedIn comments → posts to `#approve-linkedin-comment` SynapBus channel → human approves/rejects via Web UI reactions → posting agent publishes approved comments to LinkedIn via browser automation → social commenter reflects on feedback and updates its CLAUDE.md/skills. This feature requires fixes to SynapBus MCP tools (react response, list_by_state filtering, get_replies tool) and new agent code in the searcher project.

## Technical Context

**Language/Version**: Go 1.25+ (SynapBus), Python 3.12 (Searcher agents)
**Primary Dependencies**: go-chi/chi, mark3labs/mcp-go, ory/fosite (SynapBus); claude-agent-sdk, httpx, psycopg (Searcher)
**Storage**: SQLite via modernc.org/sqlite (SynapBus); PostgreSQL (Searcher)
**Testing**: `go test ./...` (SynapBus); `uv run pytest` (Searcher)
**Target Platform**: linux/amd64 (K8s on kubic), darwin/arm64 (local dev)
**Project Type**: Cross-project: web-service (SynapBus) + agent scripts (Searcher)
**Performance Goals**: <30s for message submission, <10min for posting cycle
**Constraints**: Zero CGO, single binary (SynapBus); browser session required for LinkedIn posting
**Scale/Scope**: ~10 comments/day, 1 approval channel, 2 agents

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Local-First, Single Binary | PASS | No new external dependencies |
| II. MCP-Native | PASS | All agent interactions via MCP tools |
| III. Pure Go, Zero CGO | PASS | No new Go dependencies |
| IV. Multi-Tenant with Ownership | PASS | Agents have owners, reactions track agent identity |
| V. Embedded OAuth 2.1 | PASS | N/A - no auth changes |
| VI. Semantic-Ready Storage | PASS | N/A - no search changes |
| VII. Swarm Intelligence Patterns | PASS | Using workflow channels (designed for this) |
| VIII. Observable by Default | PASS | Reactions traced, trust adjusted, workflow states logged |
| IX. Progressive Complexity | PASS | Workflow is opt-in per channel |
| X. Web UI as First-Class Citizen | PASS | Reactions already work in Web UI |

**Result**: All gates pass. No violations.

## Project Structure

### Documentation (this feature)

```text
specs/013-linkedin-approval-workflow/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (MCP tool contracts)
└── tasks.md             # Phase 2 output
```

### Source Code (SynapBus - this repo)

```text
internal/
├── mcp/bridge.go                    # FIX: react response, list_by_state, add get_replies
├── reactions/store.go               # FIX: list_by_state filtering by computed state
└── mcp/tools_hybrid.go              # ADD: get_replies tool definition
```

### Source Code (Searcher - ~/repos/searcher)

```text
agents/social-commenter/
├── src/social_commenter/
│   ├── agent.py                     # MODIFY: post to #approve-linkedin-comment
│   ├── feedback.py                  # NEW: feedback reflection logic
│   └── generator.py                 # MINOR: format_approval_message update
├── CLAUDE.md                        # NEW: agent-maintained config (learning target)
└── .claude/skills/                  # NEW: agent-learned skills

agents/linkedin-poster/
├── src/linkedin_poster/
│   ├── __init__.py                  # NEW
│   ├── main.py                      # NEW: CLI entry point
│   └── agent.py                     # NEW: read approved msgs, post via browser
├── CLAUDE.md                        # NEW: posting agent config
└── pyproject.toml                   # NEW

k8s/synapbus/
└── agent-cronjobs.yaml              # MODIFY: add linkedin-poster cronjob
```

**Structure Decision**: Cross-project changes. SynapBus gets MCP tool fixes (3 files). Searcher gets a new `linkedin-poster` agent directory and social-commenter modifications. The posting agent is separated from the existing `engagement/` module to keep it SynapBus-native (reads from channel, not from PostgreSQL).

## Research Findings

### 1. SynapBus MCP Tool Issues (Confirmed via code review)

**Issue A: `react` MCP tool missing `workflow_state` in response**
- File: `internal/mcp/bridge.go:975-984`
- The REST API handler (`reactions_handler.go`) correctly returns workflow_state and full reactions
- But the MCP bridge `callReact()` only returns action, message_id, reaction, id, created_at
- Fix: After toggle, call `GetReactions()` and include `workflow_state` + `reactions` in response

**Issue B: `list_by_state` returns only message IDs**
- File: `internal/mcp/bridge.go:1035-1073`
- Agents must make N+1 calls to get message content
- Fix: Add optional `include_messages=true` parameter that returns full message bodies

**Issue C: `list_by_state` doesn't properly filter by computed state**
- File: `internal/reactions/store.go:143-174`
- Comment on line 168: "filter in app layer" — but app layer filtering doesn't happen
- A message with both `approve` and `reject` reactions appears in both states
- Fix: Fetch candidates then verify with `ComputeWorkflowState()` in the service layer

**Issue D: No `get_replies` MCP tool**
- Agents can't read message threads via MCP
- REST API has it at `/api/messages/{id}/replies`
- Fix: Add `get_replies` action to MCP bridge

### 2. Searcher Agent Architecture

**Social commenter current flow:**
1. Reads opportunities from PostgreSQL
2. Scores and evaluates via Claude
3. Generates comments
4. Posts to `#approvals` channel via `_mcp_send_channel()`
5. Posts run summary to `#general`

**Changes needed:**
- Redirect to `#approve-linkedin-comment` (channel name change)
- Add feedback reflection at start of each run
- Load CLAUDE.md from git, update it, commit

**Posting agent (new):**
- Queries `list_by_state(channel="approve-linkedin-comment", state="approved")`
- For each approved message: extract URL + comment, check thread for edits
- Post via Chrome/Playwright MCP (existing pattern in `engagement/linkedin/poster.py`)
- React with "published" on success or "rejected" on failure

### 3. Agent Configuration Management

**Decision**: Store CLAUDE.md and .claude/skills under each agent's directory in the searcher repo.
**Rationale**: Git provides versioning, agents can commit changes, changes are auditable.
**Alternative rejected**: Storing in SynapBus (adds complexity, not version-controlled).

## Implementation Phases

### Phase 1: SynapBus MCP Tool Fixes (this repo)

**1A. Fix `callReact` to return workflow_state**
- Edit `internal/mcp/bridge.go:callReact()`
- After toggle, call `GetReactions()` to get current state and reactions
- Return `workflow_state` and `reactions` in response
- Test: `go test ./internal/mcp/ -run TestReactReturnsWorkflowState`

**1B. Fix `list_by_state` filtering**
- Edit `internal/reactions/store.go:GetMessageIDsByState()`
- For non-proposed states: fetch candidate IDs, then verify each with `ComputeWorkflowState()`
- Alternatively: do the filtering in `Service.ListByState()` after fetching candidates
- Test: `go test ./internal/reactions/ -run TestListByStateFiltersCorrectly`

**1C. Enhance `list_by_state` to include message content**
- Edit `internal/mcp/bridge.go:callListByState()`
- Add optional `include_messages` boolean parameter
- When true, fetch full messages for the returned IDs
- Test: `go test ./internal/mcp/ -run TestListByStateIncludesMessages`

**1D. Add `get_replies` MCP tool**
- Add `case "get_replies"` in bridge.go dispatch
- Implement `callGetReplies()` using existing `store.GetReplies()`
- Register tool in `tools_hybrid.go`
- Test: `go test ./internal/mcp/ -run TestGetReplies`

### Phase 2: Create Approval Channel (operational)

- Create `#approve-linkedin-comment` channel via admin CLI
- Enable workflow: `kubectl exec -n synapbus deploy/synapbus -- /synapbus channel update approve-linkedin-comment --workflow-enabled`
- Verify via Web UI

### Phase 3: Social Commenter Changes (searcher repo)

**3A. Redirect to new channel**
- Edit `agent.py:_submit_to_approvals()` to post to `approve-linkedin-comment`
- Update `synapbus_client.py:build_approval_message()` format if needed

**3B. Add feedback reflection module**
- Create `agents/social-commenter/src/social_commenter/feedback.py`
- `read_feedback()`: Query list_by_state for approved/rejected messages since last run
- `reflect_on_feedback()`: Use Claude to analyze patterns in approved vs rejected
- `update_agent_config()`: Modify CLAUDE.md and .claude/skills based on reflection
- `commit_and_push()`: Git commit + push changes
- Integrate into main.py startup sequence (before generating new comments)

**3C. Create agent CLAUDE.md**
- Create `agents/social-commenter/CLAUDE.md` with initial writing rules
- Create `agents/social-commenter/.claude/skills/` with initial skills

### Phase 4: LinkedIn Posting Agent (searcher repo)

**4A. Create posting agent**
- New `agents/linkedin-poster/` directory with `main.py`, `agent.py`
- Connect to SynapBus, query approved messages
- For each: extract URL/comment, check thread for edits
- Post via Claude Agent SDK + Chrome/Playwright MCP
- React with published/rejected on SynapBus

**4B. K8s deployment**
- Add linkedin-poster CronJob to `k8s/synapbus/agent-cronjobs.yaml`
- Register agent in SynapBus with API key
- Docker image update to include new agent

### Phase 5: End-to-End Testing

- Run social commenter (local or K8s)
- Verify message appears in Web UI
- Approve/reject via Web UI reactions
- Run posting agent
- Verify LinkedIn comment posted
- Run social commenter again
- Verify CLAUDE.md updated and committed
