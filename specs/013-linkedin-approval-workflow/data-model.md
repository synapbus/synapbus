# Data Model: LinkedIn Comment Approval Workflow

## Existing Entities (SynapBus - no changes needed)

### Message (messages table)
Already supports: channel_id, from_agent, body, reply_to (threading), created_at, metadata.
Workflow state is derived from reactions, not stored.

### Reaction (message_reactions table)
Already supports: message_id, agent_name, reaction type, metadata, created_at.
Types: approve, reject, in_progress, done, published.

### Channel (channels table)
Already supports: workflow_enabled, auto_approve, stalemate_remind_after, stalemate_escalate_after.

### Trust Score (agent_trust table)
Already supports: agent_name, action_type, score, adjustments_count.

## New Entity: Comment Draft Message Format

Messages posted to `#approve-linkedin-comment` follow this structured format:

```
**Comment Draft** — LinkedIn
**Target**: [URL]
**Opportunity**: [title] ([platform])
**Score**: [0.0-1.0] | **Type**: [comment_type]

---
[comment text]
---

React: ✅ approve | ❌ reject | Reply with edits before approving.
```

**Metadata** (JSON, stored in message metadata field):
```json
{
  "target_url": "https://linkedin.com/posts/...",
  "comment_type": "technical_insight",
  "score": 0.85,
  "opportunity_id": 123,
  "platform": "linkedin"
}
```

## State Machine

```
proposed → approved → in_progress → published
    ↓          ↓
 rejected   rejected
```

- **proposed**: No reactions (initial state when social commenter posts)
- **approved**: Human clicked approve reaction
- **rejected**: Human clicked reject reaction
- **in_progress**: Posting agent claimed the message for posting
- **published**: Posting agent successfully posted and reacted with published

## Thread Structure for Edits

```
Message (comment draft) — proposed/approved state
  └── Reply (human edit) — "Use this text instead: ..."
  └── Reply (human feedback) — "Tone is too promotional"
  └── Reply (posting agent) — "Published: [URL]"
```

The posting agent checks for thread replies from human agents before posting. If a human reply contains edited text, that text is used instead of the original.
