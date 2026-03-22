# Feature Specification: LinkedIn Comment Approval Workflow

**Feature Branch**: `013-linkedin-approval-workflow`
**Created**: 2026-03-22
**Status**: Draft
**Input**: End-to-end approval pipeline connecting social commenter agent → SynapBus approval channel with reactions workflow → posting agent. Agents learn from feedback and update their configuration.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Social Commenter Posts Draft for Approval (Priority: P1)

The social commenter agent generates a LinkedIn comment for a discovered opportunity and submits it to the `#approve-linkedin-comment` channel on SynapBus. The message includes the target post URL, generated comment text, relevance score, and comment type. The channel has workflow enabled so the message starts in "proposed" state.

**Why this priority**: Without draft submission, nothing downstream can work. This is the entry point of the entire pipeline.

**Independent Test**: Can be tested by running the social commenter agent and verifying a message appears in `#approve-linkedin-comment` with workflow_state="proposed".

**Acceptance Scenarios**:

1. **Given** the social commenter agent has a scored opportunity with score >= 0.6, **When** it generates a comment and submits to SynapBus, **Then** a message appears in `#approve-linkedin-comment` with workflow_state="proposed" containing the target URL, comment text, score, and comment type.
2. **Given** a comment was already submitted for a given URL, **When** the agent tries to submit another, **Then** it skips the duplicate.
3. **Given** the SynapBus server is unreachable, **When** the agent attempts to submit, **Then** it retries up to 3 times with backoff and logs the failure.

---

### User Story 2 - Human Reviews and Approves/Rejects via Web UI (Priority: P1)

A human owner opens the SynapBus Web UI, navigates to `#approve-linkedin-comment`, sees proposed messages with workflow badges. They can click approve or reject reactions. They can also open a thread on a message and add text edits/feedback before approving.

**Why this priority**: Human-in-the-loop approval is the core safety mechanism. Without it, no comments get published and no feedback loop exists.

**Independent Test**: Can be tested by creating a test message in the channel, clicking approve/reject in the Web UI, and verifying the workflow_state transitions correctly.

**Acceptance Scenarios**:

1. **Given** a message in "proposed" state in `#approve-linkedin-comment`, **When** a human clicks the approve reaction, **Then** workflow_state transitions to "approved" and the agent's trust score increases.
2. **Given** a message in "proposed" state, **When** a human clicks the reject reaction, **Then** workflow_state transitions to "rejected" and the agent's trust score decreases.
3. **Given** a proposed message, **When** a human opens the thread and adds a reply with edited comment text before approving, **Then** the thread contains the edited text and the message is approved.
4. **Given** a message is already approved, **When** a human tries to reject it, **Then** the state transitions to "rejected" (latest reaction wins by priority).

---

### User Story 3 - Posting Agent Publishes Approved Comments (Priority: P1)

The posting agent queries SynapBus for messages in "approved" state on `#approve-linkedin-comment`. For each approved message, it extracts the target LinkedIn URL and comment text (checking thread for edited versions). It uses Chrome/Playwright browser automation to navigate to the LinkedIn post and submit the comment. On success, it reacts with "published" (including the posted URL in metadata).

**Why this priority**: Publishing is the end goal of the pipeline. Without it, approved comments sit idle.

**Independent Test**: Can be tested by manually approving a message, running the posting agent, and verifying the comment appears on LinkedIn and the message state transitions to "published".

**Acceptance Scenarios**:

1. **Given** an approved message in `#approve-linkedin-comment`, **When** the posting agent runs, **Then** it extracts the LinkedIn URL and comment text, posts via browser automation, and reacts with "published" including the post URL.
2. **Given** an approved message with a thread containing edited text from the human, **When** the posting agent runs, **Then** it uses the edited text from the thread instead of the original comment.
3. **Given** the posting agent encounters a LinkedIn error (CAPTCHA, session expired, comments disabled), **When** posting fails, **Then** it marks the message as failed with a reason and reports the failure.
4. **Given** no approved messages exist, **When** the posting agent runs, **Then** it exits cleanly with no actions taken.

---

### User Story 4 - Agent Learns from Feedback (Priority: P2)

After each run cycle, the social commenter agent reads the approval/rejection outcomes and any edited text from the `#approve-linkedin-comment` channel. For approved comments, it notes what worked. For rejected comments, it analyzes the rejection reason. For edited comments, it compares original vs edited text to understand the human's preferences. It then updates its own CLAUDE.md file and .claude/skills with learned patterns and commits these changes to git.

**Why this priority**: Learning from feedback makes the system improve over time. Without it, the same mistakes repeat.

**Independent Test**: Can be tested by approving one comment and rejecting another (with edits), running the social commenter agent, and checking that CLAUDE.md was updated with new rules and committed to git.

**Acceptance Scenarios**:

1. **Given** 3 approved and 2 rejected comments from the last cycle, **When** the social commenter runs its feedback reflection, **Then** it reads all outcomes, identifies patterns, and updates CLAUDE.md with new writing guidelines.
2. **Given** a rejected comment where the human provided edited text in the thread, **When** the agent reflects, **Then** it compares original vs edited text, extracts the diff as a writing rule, and saves it to .claude/skills.
3. **Given** the agent updates its CLAUDE.md, **When** the update is complete, **Then** it commits the change to git with a descriptive message and pushes to the repository.
4. **Given** no new feedback since last reflection, **When** the agent checks, **Then** it skips the reflection step and proceeds with normal operation.

---

### User Story 5 - End-to-End Workflow Verification (Priority: P2)

The entire pipeline runs end-to-end: social commenter generates a comment, posts to SynapBus, human approves via Web UI, posting agent publishes to LinkedIn, and on next run the social commenter reflects on the feedback. This can be verified by checking agent logs, SynapBus message states, and git commit history.

**Why this priority**: Integration testing ensures all components work together correctly.

**Independent Test**: Can be tested by triggering the social commenter, approving in Web UI, running the posting agent, triggering the social commenter again, and verifying CLAUDE.md changes in git.

**Acceptance Scenarios**:

1. **Given** all components are deployed, **When** running the full cycle (generate → approve → publish → reflect), **Then** the LinkedIn comment is posted, the message reaches "published" state, and CLAUDE.md contains updated rules.
2. **Given** the rejection path, **When** running the full cycle (generate → reject with feedback → reflect), **Then** no comment is posted, the message is "rejected", and CLAUDE.md reflects the feedback.

---

### Edge Cases

- What happens when the SynapBus server is unreachable during agent execution? Agent retries with exponential backoff (3 attempts), then logs failure and exits gracefully.
- What happens when a LinkedIn session expires mid-posting? Agent detects session expiry, marks message as failed with reason, and alerts via SynapBus DM to the owner.
- What happens when the human edits text but forgets to approve? The stalemate worker sends a reminder after the configured remind period (default 4h).
- What happens when two posting agents try to publish the same approved message? The first agent to react with "in_progress" claims it; the second sees the state change and skips.
- What happens when the agent's CLAUDE.md update creates a git conflict? Agent pulls latest, attempts auto-merge. If conflict persists, it creates the commit on a separate branch and notifies the owner.
- What happens when a comment is approved but the LinkedIn post has been deleted? Agent detects "post not found" error, marks as failed, and notifies via SynapBus.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide a `#approve-linkedin-comment` channel with workflow_enabled=true, supporting the state machine: proposed → approved/rejected → in_progress → done/published.
- **FR-002**: Social commenter agent MUST post generated LinkedIn comments to `#approve-linkedin-comment` with structured metadata (target_url, comment_text, score, comment_type, opportunity_id).
- **FR-003**: Human owners MUST be able to approve or reject proposed comments via reaction buttons in the SynapBus Web UI.
- **FR-004**: Human owners MUST be able to add text edits as threaded replies before approving a comment.
- **FR-005**: Posting agent MUST query `#approve-linkedin-comment` for messages in "approved" state using the `list_by_state` MCP tool.
- **FR-006**: Posting agent MUST check for threaded replies containing edited comment text and use the edited version when present.
- **FR-007**: Posting agent MUST publish approved comments to LinkedIn using Chrome/Playwright browser automation via MCP.
- **FR-008**: Posting agent MUST react with "published" (including the LinkedIn URL in metadata) after successful posting.
- **FR-009**: On approval, the system MUST increase the social commenter agent's trust score. On rejection, it MUST decrease the trust score.
- **FR-010**: Social commenter agent MUST read approved/rejected/edited feedback from the channel on each run and reflect on patterns.
- **FR-011**: Social commenter agent MUST update its CLAUDE.md and .claude/skills files based on feedback patterns and commit changes to git.
- **FR-012**: Both agents MUST load their CLAUDE.md and .claude/skills configuration from the git repository at startup.
- **FR-013**: Agents MUST run on Kubernetes (kubic) and be triggerable via CronJob or manual kubectl exec.
- **FR-014**: The posting agent MUST handle posting failures gracefully (CAPTCHA, session expired, post deleted) by marking the message as failed with a reason.
- **FR-015**: The social commenter MUST deduplicate submissions — no duplicate comments for the same target URL in the channel.

### Key Entities

- **Comment Draft**: A proposed LinkedIn comment with target URL, comment text, score, type, and opportunity reference. Lifecycle: proposed → approved/rejected → published/failed.
- **Feedback Record**: An approved/rejected decision with optional edited text, mapped to the original comment draft. Used for agent learning.
- **Agent Configuration**: CLAUDE.md and .claude/skills files in the git repository that encode the agent's learned writing rules and preferences.
- **Trust Score**: A per-agent metric that increases on approval and decreases on rejection, influencing future behavior thresholds.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A comment draft submitted by the social commenter appears in the approval channel within 30 seconds of generation.
- **SC-002**: A human can approve or reject a comment draft in under 3 clicks from the channel view.
- **SC-003**: An approved comment is published to LinkedIn within 10 minutes of the posting agent's next run.
- **SC-004**: The agent's configuration file is updated with at least one new rule after processing 5+ feedback items.
- **SC-005**: 100% of approved comments reach "published" state or have a documented failure reason.
- **SC-006**: Trust scores reflect approval patterns — agents with >80% approval rate have increasing trust over time.
- **SC-007**: The full cycle (generate → approve → publish → reflect) completes end-to-end without manual intervention beyond the approval step.
- **SC-008**: Agent configuration changes are committed to git with descriptive messages traceable to specific feedback.

## Assumptions

- The `#approve-linkedin-comment` channel will be created by the system owner (algis) or via admin CLI, not auto-created by agents (agents don't have channel creation permissions).
- LinkedIn authentication is handled via persistent browser sessions managed outside the agent (pre-logged-in Chrome profile).
- The Chrome/Playwright MCP server runs locally on the machine where the posting agent executes, or is accessible via network MCP.
- Agent CLAUDE.md and .claude/skills are stored in the searcher git repository under each agent's directory.
- The posting agent uses the existing Playwriter MCP integration pattern already established in the searcher project.
- Only LinkedIn platform is in scope for this feature; other platforms (Reddit, HN, etc.) are excluded.
- The social commenter currently posts to `#approvals` — this feature redirects to `#approve-linkedin-comment` for LinkedIn-specific workflow.
- Feedback reflection happens at the start of each social commenter run, before generating new comments.
- Git operations (commit, push) are performed by the agent using `gh` CLI or git commands available in the container.
