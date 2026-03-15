# Admin Socket Contract: Channel Commands

**Feature**: 006-admin-cli-docker-fixes
**Date**: 2026-03-15

## Protocol

Unix domain socket at `/data/synapbus.sock` (default). JSON-RPC style, newline-delimited.

## New Commands

### `channels.create`

**Request**:
```json
{
  "command": "channels.create",
  "args": {
    "name": "news-feed",
    "description": "News feed channel"
  }
}
```

- `name` (string, required): Channel name. Must pass `ValidateChannelName` rules.
- `description` (string, optional): Channel description. Defaults to empty.

**Success Response**:
```json
{
  "ok": true,
  "data": {
    "id": 42,
    "name": "news-feed",
    "description": "News feed channel",
    "type": "standard",
    "is_private": false,
    "created_by": "system",
    "created_at": "2026-03-15T10:00:00Z"
  }
}
```

**Error Response** (duplicate name):
```json
{
  "ok": false,
  "error": "channel already exists"
}
```

**Error Response** (invalid name):
```json
{
  "ok": false,
  "error": "invalid channel name: ..."
}
```

---

### `channels.join`

**Request**:
```json
{
  "command": "channels.join",
  "args": {
    "channel": "news-feed",
    "agent": "my-agent"
  }
}
```

- `channel` (string, required): Channel name to join.
- `agent` (string, required): Agent name to add as member.

**Success Response**:
```json
{
  "ok": true,
  "data": {
    "channel": "news-feed",
    "agent": "my-agent",
    "status": "joined"
  }
}
```

**Success Response** (already member, idempotent):
```json
{
  "ok": true,
  "data": {
    "channel": "news-feed",
    "agent": "my-agent",
    "status": "already_member"
  }
}
```

**Error Response** (channel not found):
```json
{
  "ok": false,
  "error": "channel not found: news-feed"
}
```

## CLI Commands

### `synapbus channels create`

```
Usage:
  synapbus channels create [flags]

Flags:
      --name string          Channel name (required)
      --description string   Channel description

Global Flags:
      --socket string   Path to admin Unix socket (default "/data/synapbus.sock")
```

### `synapbus channels join`

```
Usage:
  synapbus channels join [flags]

Flags:
      --channel string   Channel name (required)
      --agent string     Agent name (required)

Global Flags:
      --socket string   Path to admin Unix socket (default "/data/synapbus.sock")
```
