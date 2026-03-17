# Data Model: SynapBus v0.7.0

## New Entities

### PushSubscription

Stores Web Push API subscription data for each user/device pair.

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| id | integer | PK, auto-increment | Unique subscription ID |
| user_id | integer | FK → users.id, NOT NULL | Owning user |
| endpoint | text | NOT NULL, UNIQUE | Push service endpoint URL |
| key_p256dh | text | NOT NULL | Client public key (base64url) |
| key_auth | text | NOT NULL | Client auth secret (base64url) |
| user_agent | text | | Browser/device identifier |
| created_at | datetime | NOT NULL, default NOW | When subscription was created |

**Relationships**: Many-to-one with users (one user can have multiple devices).

**Lifecycle**: Created when user enables push notifications. Deleted when user unsubscribes or subscription endpoint becomes invalid (410 Gone response).

### VAPIDKeys

Server-generated VAPID key pair for Web Push authentication. Stored as a file in the data directory (`{data}/vapid_keys.json`), not in SQLite.

| Field | Type | Description |
|-------|------|-------------|
| public_key | string | VAPID public key (base64url-encoded) |
| private_key | string | VAPID private key (base64url-encoded) |
| created_at | string | ISO 8601 timestamp |

**Lifecycle**: Generated once on first server start if file doesn't exist. Never rotated automatically.

## Modified Entities

### User (existing)

Add field:

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| display_name | text | | Human-readable display name (editable) |

**Note**: Check if `display_name` already exists on the users table. If so, just ensure the Settings page exposes it for editing.

### Agent (existing)

No schema changes. The `display_name` field already exists. The agent detail page will expose inline editing of this field via the existing `PUT /api/agents/{name}` endpoint.

## New SQLite Migration

**File**: `schema/012_push_subscriptions.sql`

```sql
CREATE TABLE IF NOT EXISTS push_subscriptions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL UNIQUE,
    key_p256dh TEXT NOT NULL,
    key_auth TEXT NOT NULL,
    user_agent TEXT DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_push_subscriptions_user_id ON push_subscriptions(user_id);
```

## Analytics Queries (no new tables)

Analytics are derived from existing `messages` table using aggregation queries:

### Timeline Query
```sql
SELECT strftime(?, created_at) AS bucket, COUNT(*) AS count
FROM messages
WHERE created_at >= ?
GROUP BY bucket
ORDER BY bucket
```
- Bucket format varies by span: `%H:%M` (1h/4h), `%Y-%m-%d %H:00` (24h), `%Y-%m-%d` (7d/30d)

### Top Agents Query
```sql
SELECT from_agent, COUNT(*) AS message_count
FROM messages
WHERE created_at >= ?
GROUP BY from_agent
ORDER BY message_count DESC
LIMIT 5
```

### Top Channels Query
```sql
SELECT c.name, COUNT(*) AS message_count
FROM messages m
JOIN channels c ON m.channel_id = c.id
WHERE m.created_at >= ? AND m.channel_id IS NOT NULL
GROUP BY c.name
ORDER BY message_count DESC
LIMIT 5
```
