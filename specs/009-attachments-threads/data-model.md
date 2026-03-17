# Data Model: Attachments & Threads Enhancement

**Branch**: `009-attachments-threads` | **Date**: 2026-03-17

## Entity Changes

### Message (Modified)

Existing table `messages` — already has `reply_to` column via migration 007.

**New computed field** (not stored, calculated in queries):
- `reply_count` (integer): COUNT of messages where reply_to = this message's ID

**New field in API response** (joined from attachments table):
- `attachments` (array of Attachment): All attachments linked to this message via message_id FK

### Attachment (Existing, No Schema Changes)

Table `attachments` — no changes needed. Existing schema:

| Column | Type | Description |
|--------|------|-------------|
| id | INTEGER PK | Auto-increment |
| hash | TEXT NOT NULL | SHA-256 content hash |
| original_filename | TEXT NOT NULL | Original upload filename |
| size | INTEGER NOT NULL | File size in bytes |
| mime_type | TEXT NOT NULL | Detected MIME type |
| message_id | INTEGER FK | Nullable, references messages(id) |
| uploaded_by | TEXT NOT NULL | Agent/user who uploaded |
| created_at | TIMESTAMP | Upload timestamp |

### Relationships

```
Message 1 ──── 0..* Attachment  (via attachment.message_id)
Message 1 ──── 0..* Message     (via message.reply_to → parent message.id)
```

## Query Changes

### Message List with Reply Count

```sql
SELECT m.*,
       COALESCE(rc.reply_count, 0) as reply_count
FROM messages m
LEFT JOIN (
    SELECT reply_to, COUNT(*) as reply_count
    FROM messages
    WHERE reply_to IS NOT NULL
    GROUP BY reply_to
) rc ON rc.reply_to = m.id
WHERE ...
```

### Message with Attachments

```sql
SELECT a.id, a.hash, a.original_filename, a.size, a.mime_type, a.created_at
FROM attachments a
WHERE a.message_id = ?
ORDER BY a.created_at ASC
```

### Thread Replies

Already exists in store.go:
```sql
SELECT ... FROM messages WHERE reply_to = ? ORDER BY created_at ASC
```

## API Response Changes

### Message Response (enriched)

```json
{
  "id": 123,
  "body": "Hello",
  "from_agent": "research-bot",
  "reply_to": null,
  "reply_count": 3,
  "attachments": [
    {
      "hash": "abc123...",
      "original_filename": "report.pdf",
      "size": 1048576,
      "mime_type": "application/pdf",
      "is_image": false
    }
  ]
}
```

### Send Message Request (enriched)

```json
{
  "to": "agent-name",
  "body": "See attached report",
  "reply_to": 456,
  "attachments": ["abc123...", "def456..."]
}
```

## File Storage (No Changes)

Content-addressable storage structure (existing):
```
{dataDir}/attachments/
├── ab/
│   └── cd/
│       └── abcd1234...  (full SHA-256 hash as filename)
├── ef/
│   └── 01/
│       └── ef012345...
```

Backup archive mirrors this structure in tar.gz format.
