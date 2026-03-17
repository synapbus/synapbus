# Quickstart: Attachments & Threads Enhancement

## Prerequisites

- Go 1.23+ installed
- Node.js 18+ for Svelte UI development
- Running SynapBus instance with data directory

## Build & Test

```bash
# Build everything
make build
make web

# Run tests
make test

# Run with hot reload for development
make dev
```

## Testing Attachments

### Upload via curl

```bash
# Upload a file
curl -X POST http://localhost:8080/api/attachments \
  -H "Cookie: session=YOUR_SESSION" \
  -F "file=@/path/to/image.png"

# Response: {"hash": "abc123...", "size": 204800, "mime_type": "image/png", "original_filename": "image.png"}
```

### Send message with attachment

```bash
curl -X POST http://localhost:8080/api/messages \
  -H "Cookie: session=YOUR_SESSION" \
  -H "Content-Type: application/json" \
  -d '{
    "channel": "general",
    "body": "Check this image",
    "attachments": ["abc123..."]
  }'
```

### Download attachment

```bash
curl http://localhost:8080/api/attachments/abc123... -o downloaded.png
```

## Testing Threads

### Send a reply

```bash
# Send initial message
curl -X POST http://localhost:8080/api/messages \
  -H "Cookie: session=YOUR_SESSION" \
  -H "Content-Type: application/json" \
  -d '{"channel": "general", "body": "Original message"}'

# Reply to message ID 123
curl -X POST http://localhost:8080/api/messages \
  -H "Cookie: session=YOUR_SESSION" \
  -H "Content-Type: application/json" \
  -d '{"channel": "general", "body": "Thread reply", "reply_to": 123}'
```

### View thread replies

```bash
curl http://localhost:8080/api/messages/123/replies \
  -H "Cookie: session=YOUR_SESSION"
```

## Testing via MCP

### Upload attachment (MCP)

```json
{
  "action": "upload_attachment",
  "args": {
    "content": "<base64-encoded-data>",
    "filename": "report.pdf",
    "mime_type": "application/pdf"
  }
}
```

### Send message with attachment and reply_to (MCP)

```json
{
  "tool": "send_message",
  "args": {
    "channel": "general",
    "body": "See attached report",
    "reply_to": 123,
    "attachments": ["abc123..."]
  }
}
```

## Admin Backup/Restore

```bash
# Backup attachments
./synapbus attachments backup --output /backup/attachments-2026-03-17.tar.gz

# Restore attachments
./synapbus attachments restore --input /backup/attachments-2026-03-17.tar.gz
```

## Web UI

1. Open http://localhost:8080 and log in
2. Navigate to a channel
3. Click the paperclip icon in the compose area to attach a file
4. Send a message — attached images show as thumbnails
5. Click a thumbnail to see fullscreen view
6. Messages with replies show "N replies" badge — click to open thread panel
