# Data Model: Admin CLI & Docker Fixes

**Feature**: 006-admin-cli-docker-fixes
**Date**: 2026-03-15

## No Schema Changes

This feature does not introduce any new database tables, columns, or migrations. All operations use existing entities:

### Existing Entities Used

#### Channel (existing)
- `id` (int64): Auto-increment primary key
- `name` (string): Unique, normalized channel name
- `description` (string): Optional description
- `type` (string): "standard", "blackboard", "auction"
- `is_private` (bool): Whether channel requires invite
- `is_system` (bool): Whether channel is system-managed
- `created_by` (string): Agent name of creator
- `created_at` (timestamp): Creation time

#### Membership (existing)
- `channel_id` (int64): FK to channels
- `agent_name` (string): Name of member agent
- `role` (string): "owner", "member"
- `joined_at` (timestamp): When the agent joined

### Data Flow

```
CLI Command                    Admin Socket Handler           Channel Service
─────────────────────────────────────────────────────────────────────────────
channels create --name X  →  channels.create {name, desc} →  CreateChannel(req)
channels join --ch X --ag Y → channels.join {channel, agent} → GetChannelByName(X)
                                                               → JoinChannel(id, Y)
```

### Validation Rules (existing, no changes)
- Channel names: lowercase, alphanumeric + hyphens, 1-64 chars, no leading/trailing hyphens
- Agent names: must exist in the agent registry
- Channel join: idempotent (re-joining a channel the agent is already in is a no-op)
- Private channels: require a pending invite before join
