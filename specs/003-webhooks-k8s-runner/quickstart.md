# Quickstart: Webhooks & K8s Job Runner

## Prerequisites

- SynapBus running (`./synapbus serve --data ./data`)
- Two registered agents (e.g., `sender-bot` and `processor-bot`)

## 1. Register a Webhook (via MCP)

Using any MCP client connected to SynapBus:

```
Tool: register_webhook
Arguments:
  url: "https://your-endpoint.example.com/webhook"
  events: ["message.received"]
```

Response includes the HMAC secret (save it — shown only once):
```json
{
  "webhook_id": 1,
  "secret": "a1b2c3d4e5f6...",
  "status": "active"
}
```

## 2. Send a Message to Trigger Delivery

From another agent:
```
Tool: send_message
Arguments:
  to_agent: "processor-bot"
  body: "Please analyze dataset #42"
  priority: 7
```

Within seconds, your webhook endpoint receives:
```
POST /webhook HTTP/1.1
Content-Type: application/json
X-SynapBus-Signature: sha256=abc123...
X-SynapBus-Event: message.received
X-SynapBus-Delivery: d-xyz789
X-SynapBus-Depth: 0

{
  "event": "message.received",
  "message": { "id": 5, "from": "sender-bot", "body": "Please analyze dataset #42", ... },
  "agent": "processor-bot",
  "timestamp": "2026-03-14T12:00:00Z"
}
```

## 3. Verify the Signature

```python
import hmac, hashlib, json

secret = b"a1b2c3d4e5f6..."  # from registration
body = request.body  # raw bytes
expected = "sha256=" + hmac.new(secret, body, hashlib.sha256).hexdigest()
assert request.headers["X-SynapBus-Signature"] == expected
```

## 4. View Delivery History (Web UI)

Navigate to **Agents → processor-bot → Webhooks** to see:
- Registered webhooks with status
- Delivery history (success/failure)
- Dead-lettered deliveries with retry button

## 5. Kubernetes Job Handler (K8s only)

When SynapBus runs inside a K8s cluster:

```
Tool: register_k8s_handler
Arguments:
  image: "my-processor:latest"
  events: ["message.received"]
  namespace: "agent-jobs"
  resources: {"memory": "512Mi", "cpu": "250m"}
  env: {"MODEL": "gpt-4"}
```

When a message arrives, SynapBus creates a K8s Job:
```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: synapbus-processor-bot-42
  namespace: agent-jobs
spec:
  activeDeadlineSeconds: 600
  ttlSecondsAfterFinished: 3600
  template:
    spec:
      containers:
      - name: handler
        image: my-processor:latest
        env:
        - name: SYNAPBUS_MESSAGE_ID
          value: "42"
        - name: SYNAPBUS_MESSAGE_BODY
          value: "Please analyze dataset #42"
        - name: SYNAPBUS_FROM_AGENT
          value: "sender-bot"
        - name: MODEL
          value: "gpt-4"
        resources:
          limits:
            memory: 512Mi
            cpu: 250m
      restartPolicy: Never
```

## 6. Real-World Example: Multi-Agent Pipeline

```
┌──────────┐    message     ┌──────────┐   webhook    ┌──────────────┐
│ Scanner  │───────────────→│ SynapBus │────────────→│ Analyzer     │
│ Agent    │                │          │              │ (webhook)    │
└──────────┘                │          │              └──────┬───────┘
                            │          │                     │
                            │          │    K8s Job          │ message
                            │          │◄────────────────────┘
                            │          │──────────────→┌──────────────┐
                            │          │               │ Reporter     │
                            └──────────┘               │ (K8s Job)   │
                                                       └──────────────┘
```

1. Scanner agent sends findings to SynapBus
2. Analyzer's webhook fires, processes findings, sends summary back
3. Reporter's K8s Job fires, generates PDF report in a container

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SYNAPBUS_WEBHOOK_WORKERS` | `10` | Concurrent webhook delivery goroutines |
| `SYNAPBUS_ALLOW_HTTP_WEBHOOKS` | `false` | Allow HTTP (non-HTTPS) webhook URLs |
| `SYNAPBUS_ALLOW_PRIVATE_NETWORKS` | `false` | Allow webhooks to private IPs |

## Verification Checklist

- [ ] Register a webhook and see it in `list_webhooks`
- [ ] Send a message and verify webhook delivery (check headers + signature)
- [ ] Verify SSRF protection: try registering `http://127.0.0.1/hook` (should fail)
- [ ] Verify dead letters: register a webhook pointing to a non-existent URL, send a message, check dead letters after retries
- [ ] (K8s only) Register a K8s handler and verify Job creation
