# REST API Contract Changes

## Modified Endpoints

### POST /api/messages — Send Message (Modified)

**Request Body** (new field: `attachments`):
```json
{
  "to": "agent-name",
  "channel": "channel-name",
  "body": "Message text",
  "subject": "Optional subject",
  "priority": 5,
  "reply_to": 123,
  "attachments": ["sha256hash1", "sha256hash2"]
}
```

**Response** (enriched with attachments and reply_count):
```json
{
  "id": 456,
  "conversation_id": 789,
  "from_agent": "sender",
  "body": "Message text",
  "reply_to": 123,
  "reply_count": 0,
  "attachments": [
    {
      "hash": "sha256hash1",
      "original_filename": "image.png",
      "size": 204800,
      "mime_type": "image/png",
      "is_image": true
    }
  ],
  "created_at": "2026-03-17T10:00:00Z"
}
```

### GET /api/messages — List Messages (Modified Response)

Each message in the array now includes `reply_count` (integer) and `attachments` (array).

### GET /api/channels/{name}/messages — Channel Messages (Modified Response)

Each message includes `reply_count` and `attachments`.

### GET /api/messages/{id}/replies — Thread Replies (Existing, Unchanged)

Returns array of reply messages.

## Existing Endpoints (Unchanged)

### POST /api/attachments — Upload

Multipart form upload. Returns hash, size, mime_type, filename.

### GET /api/attachments/{hash} — Download

Streams file content with Content-Type header.

### GET /api/attachments/{hash}/meta — Metadata

Returns attachment metadata JSON.

## MCP Tool Changes

### send_message (Modified)

New parameter: `attachments` — array of SHA-256 hashes of previously uploaded attachments.

Updated description for `reply_to`: "ID of the parent message to reply to. Creates a threaded reply. Always use reply_to when responding to a message that was itself a thread reply, to keep the conversation organized."

### upload_attachment (Existing)

No changes. Parameters: content (base64), filename, mime_type, message_id.

### download_attachment (Existing)

No changes. Parameter: hash.

## Admin CLI Changes

### New: `synapbus attachments backup`

```
synapbus attachments backup --output /path/to/backup.tar.gz [--socket /path/to/socket]
```

Creates tar.gz of attachment storage directory.

### New: `synapbus attachments restore`

```
synapbus attachments restore --input /path/to/backup.tar.gz [--socket /path/to/socket]
```

Restores attachment files, skipping existing (dedup-safe).
