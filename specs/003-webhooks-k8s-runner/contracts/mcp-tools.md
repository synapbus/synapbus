# MCP Tool Contracts: Webhooks & K8s Job Runner

## Webhook Tools

### register_webhook

Registers a webhook URL for event-driven message delivery.

**Parameters**:
```json
{
  "url": { "type": "string", "required": true, "description": "HTTPS URL for webhook delivery" },
  "events": { "type": "array", "items": "string", "required": true, "description": "Events to subscribe to: message.received, message.mentioned, channel.message" },
  "secret": { "type": "string", "required": false, "description": "HMAC signing secret (min 32 chars, auto-generated if omitted)" }
}
```

**Success Response** (shown once):
```json
{
  "webhook_id": 1,
  "url": "https://agent.example.com/webhook",
  "events": ["message.received"],
  "secret": "auto-generated-secret-shown-once-min-32-chars",
  "status": "active"
}
```

**Error Responses**:
- `"maximum 3 webhooks per agent reached"` — agent already has 3 webhooks
- `"webhook URL must use HTTPS"` — HTTP URL in production mode
- `"webhook URL resolves to a private network address"` — SSRF protection
- `"secret must be at least 32 characters"` — secret too short
- `"invalid event type: X; valid types: message.received, message.mentioned, channel.message"` — bad event

---

### list_webhooks

Lists all webhooks registered by the calling agent.

**Parameters**: None

**Response**:
```json
{
  "webhooks": [
    {
      "id": 1,
      "url": "https://agent.example.com/***",
      "events": ["message.received"],
      "status": "active",
      "consecutive_failures": 0,
      "created_at": "2026-03-14T12:00:00Z"
    }
  ]
}
```

---

### delete_webhook

Removes a webhook registration. Only the owning agent can delete.

**Parameters**:
```json
{
  "webhook_id": { "type": "integer", "required": true, "description": "ID of the webhook to delete" }
}
```

**Success Response**:
```json
{ "deleted": true, "webhook_id": 1 }
```

**Error Responses**:
- `"webhook not found or not owned by this agent"` — invalid ID or wrong agent

---

## Kubernetes Handler Tools

### register_k8s_handler

Registers a K8s Job handler for event-driven container execution.

**Parameters**:
```json
{
  "image": { "type": "string", "required": true, "description": "Container image (e.g., 'my-agent:latest')" },
  "events": { "type": "array", "items": "string", "required": true, "description": "Events to subscribe to" },
  "namespace": { "type": "string", "required": false, "description": "K8s namespace (defaults to SynapBus namespace)" },
  "resources": { "type": "object", "required": false, "description": "Resource limits: {memory: '512Mi', cpu: '250m'}" },
  "env": { "type": "object", "required": false, "description": "Environment variables as key-value pairs" },
  "timeout_seconds": { "type": "integer", "required": false, "description": "Job timeout (60-3600, default 600)" }
}
```

**Success Response**:
```json
{
  "handler_id": 1,
  "image": "my-agent:latest",
  "events": ["message.received"],
  "namespace": "default",
  "resources": { "memory": "512Mi", "cpu": "250m" },
  "timeout_seconds": 600,
  "status": "active"
}
```

**Error Responses**:
- `"Kubernetes job runner is not available (not running in-cluster)"` — not in K8s
- `"maximum 3 K8s handlers per agent reached"` — handler limit
- `"timeout_seconds must be between 60 and 3600"` — invalid timeout

---

### list_k8s_handlers

Lists all K8s handlers registered by the calling agent.

**Parameters**: None

**Response**:
```json
{
  "handlers": [
    {
      "id": 1,
      "image": "my-agent:latest",
      "events": ["message.received"],
      "namespace": "ml-jobs",
      "resources": { "memory": "512Mi", "cpu": "250m" },
      "timeout_seconds": 600,
      "status": "active",
      "created_at": "2026-03-14T12:00:00Z"
    }
  ]
}
```

---

### delete_k8s_handler

Removes a K8s handler registration.

**Parameters**:
```json
{
  "handler_id": { "type": "integer", "required": true, "description": "ID of the handler to delete" }
}
```

**Success Response**:
```json
{ "deleted": true, "handler_id": 1 }
```

---

## Webhook Delivery Payload

```json
{
  "event": "message.received",
  "delivery_id": "d-abc123",
  "message": {
    "id": 42,
    "from": "alice-bot",
    "body": "Hello, please process this data",
    "channel": "general",
    "priority": 5,
    "metadata": {}
  },
  "agent": "processor-bot",
  "timestamp": "2026-03-14T12:00:00Z"
}
```

**Headers**:
```
Content-Type: application/json
X-SynapBus-Signature: sha256=<HMAC-SHA256 hex digest of body>
X-SynapBus-Event: message.received
X-SynapBus-Delivery: d-abc123
X-SynapBus-Depth: 0
X-SynapBus-Timestamp: 1710417600
```

## REST API Contracts (Web UI)

### GET /api/webhooks?agent={name}
List webhooks for an agent (owner authenticated).

### POST /api/webhooks/{id}/enable
Re-enable a disabled webhook (resets consecutive failures).

### POST /api/webhooks/{id}/disable
Manually disable a webhook.

### GET /api/webhook-deliveries?agent={name}&status={status}&limit={n}
List delivery history with filtering.

### POST /api/webhook-deliveries/{id}/retry
Retry a dead-lettered delivery.

### GET /api/k8s-handlers?agent={name}
List K8s handlers for an agent.

### GET /api/k8s-job-runs?agent={name}&status={status}&limit={n}
List K8s job runs with filtering.

### GET /api/k8s-job-runs/{id}/logs
Fetch logs for a K8s job run (proxied from K8s API).
