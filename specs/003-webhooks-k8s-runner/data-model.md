# Data Model: Webhooks & Kubernetes Job Runner

**Feature**: 003-webhooks-k8s-runner
**Date**: 2026-03-14

## Entities

### Webhook

Registered HTTP endpoint for event-driven delivery to an agent.

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| id | INTEGER | PRIMARY KEY AUTOINCREMENT | Unique identifier |
| agent_name | TEXT | NOT NULL, FK agents(name) | Owning agent |
| url | TEXT | NOT NULL | Delivery URL (HTTPS required in prod) |
| events | TEXT | NOT NULL | JSON array of subscribed events |
| secret_hash | TEXT | NOT NULL | SHA-256 hash of HMAC signing secret |
| status | TEXT | NOT NULL DEFAULT 'active' | active, disabled |
| consecutive_failures | INTEGER | NOT NULL DEFAULT 0 | Auto-disable at 50 |
| created_at | DATETIME | NOT NULL DEFAULT CURRENT_TIMESTAMP | Registration time |
| updated_at | DATETIME | NOT NULL DEFAULT CURRENT_TIMESTAMP | Last modification |

**Constraints**:
- Maximum 3 webhooks per agent (enforced in application layer with COUNT check before insert)
- Unique constraint on (agent_name, url) to prevent duplicate registrations
- Events values: `message.received`, `message.mentioned`, `channel.message`

**Indexes**:
- `idx_webhooks_agent_name` ON (agent_name) — lookup by agent
- `idx_webhooks_agent_status` ON (agent_name, status) — active webhooks per agent

---

### WebhookDelivery

Record of a webhook delivery attempt with full audit trail.

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| id | INTEGER | PRIMARY KEY AUTOINCREMENT | Unique delivery ID |
| webhook_id | INTEGER | NOT NULL, FK webhooks(id) | Parent webhook |
| agent_name | TEXT | NOT NULL | Target agent |
| event | TEXT | NOT NULL | Event type that triggered delivery |
| message_id | INTEGER | NOT NULL | Triggering message ID |
| payload | TEXT | NOT NULL | JSON payload sent/to-send |
| status | TEXT | NOT NULL DEFAULT 'pending' | pending, delivered, retrying, dead_lettered |
| http_status | INTEGER | | Last HTTP response code |
| attempts | INTEGER | NOT NULL DEFAULT 0 | Number of delivery attempts |
| max_attempts | INTEGER | NOT NULL DEFAULT 3 | Maximum retry attempts |
| last_error | TEXT | | Last error message |
| next_retry_at | DATETIME | | Scheduled retry time |
| depth | INTEGER | NOT NULL DEFAULT 0 | Webhook chain depth |
| created_at | DATETIME | NOT NULL DEFAULT CURRENT_TIMESTAMP | Creation time |
| delivered_at | DATETIME | | Successful delivery time |

**Indexes**:
- `idx_deliveries_webhook_id` ON (webhook_id) — delivery history per webhook
- `idx_deliveries_status` ON (status) — find pending/retrying deliveries
- `idx_deliveries_agent_status` ON (agent_name, status) — agent's dead letters
- `idx_deliveries_next_retry` ON (next_retry_at) WHERE status = 'retrying' — retry queue
- `idx_deliveries_created_at` ON (created_at) — purge old dead letters

**State Transitions**:
```
pending → delivered (HTTP 2xx)
pending → retrying (HTTP 4xx/5xx, attempts < max_attempts)
retrying → delivered (HTTP 2xx on retry)
retrying → retrying (HTTP 4xx/5xx, attempts < max_attempts)
retrying → dead_lettered (attempts >= max_attempts)
pending → dead_lettered (depth exceeded, blocked IP, rate exceeded timeout)
dead_lettered → pending (manual retry from Web UI)
```

---

### K8sHandler

Registered Kubernetes Job template for event-driven processing.

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| id | INTEGER | PRIMARY KEY AUTOINCREMENT | Unique identifier |
| agent_name | TEXT | NOT NULL, FK agents(name) | Owning agent |
| image | TEXT | NOT NULL | Container image to run |
| events | TEXT | NOT NULL | JSON array of subscribed events |
| namespace | TEXT | NOT NULL | K8s namespace for Jobs |
| resources_memory | TEXT | NOT NULL DEFAULT '256Mi' | Memory limit |
| resources_cpu | TEXT | NOT NULL DEFAULT '100m' | CPU limit |
| env | TEXT | NOT NULL DEFAULT '{}' | JSON object of env vars |
| timeout_seconds | INTEGER | NOT NULL DEFAULT 600 | Job activeDeadlineSeconds |
| status | TEXT | NOT NULL DEFAULT 'active' | active, disabled |
| created_at | DATETIME | NOT NULL DEFAULT CURRENT_TIMESTAMP | Registration time |
| updated_at | DATETIME | NOT NULL DEFAULT CURRENT_TIMESTAMP | Last modification |

**Constraints**:
- Maximum 3 K8s handlers per agent (enforced in application layer)
- Unique constraint on (agent_name, image, namespace) to prevent duplicates
- timeout_seconds: min 60, max 3600

**Indexes**:
- `idx_k8s_handlers_agent` ON (agent_name) — lookup by agent
- `idx_k8s_handlers_agent_status` ON (agent_name, status) — active handlers per agent

---

### K8sJobRun

Record of a Kubernetes Job execution triggered by a message event.

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| id | INTEGER | PRIMARY KEY AUTOINCREMENT | Unique run ID |
| handler_id | INTEGER | NOT NULL, FK k8s_handlers(id) | Parent handler |
| agent_name | TEXT | NOT NULL | Target agent |
| message_id | INTEGER | NOT NULL | Triggering message ID |
| job_name | TEXT | NOT NULL | K8s Job name |
| namespace | TEXT | NOT NULL | K8s namespace |
| status | TEXT | NOT NULL DEFAULT 'pending' | pending, running, succeeded, failed |
| failure_reason | TEXT | | Reason for failure |
| started_at | DATETIME | | Job start time |
| completed_at | DATETIME | | Job completion time |
| created_at | DATETIME | NOT NULL DEFAULT CURRENT_TIMESTAMP | Record creation |

**Indexes**:
- `idx_job_runs_handler` ON (handler_id) — runs per handler
- `idx_job_runs_agent_status` ON (agent_name, status) — agent's active jobs
- `idx_job_runs_job_name` ON (job_name) — lookup by K8s Job name (for watcher updates)

**State Transitions**:
```
pending → running (Job observed as Active)
running → succeeded (Job completed successfully)
running → failed (Job failed or deadline exceeded)
pending → failed (K8s API error creating Job)
```

## Relationships

```
Agent (1) ──────── (0..3) Webhook
Webhook (1) ────── (0..*) WebhookDelivery
Message (1) ────── (0..*) WebhookDelivery

Agent (1) ──────── (0..3) K8sHandler
K8sHandler (1) ─── (0..*) K8sJobRun
Message (1) ────── (0..*) K8sJobRun
```

## Migration: 009_webhooks.sql

```sql
-- Webhook registrations
CREATE TABLE IF NOT EXISTS webhooks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_name TEXT NOT NULL REFERENCES agents(name) ON DELETE CASCADE,
    url TEXT NOT NULL,
    events TEXT NOT NULL DEFAULT '[]',
    secret_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'disabled')),
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(agent_name, url)
);

CREATE INDEX IF NOT EXISTS idx_webhooks_agent_name ON webhooks(agent_name);
CREATE INDEX IF NOT EXISTS idx_webhooks_agent_status ON webhooks(agent_name, status);

-- Webhook delivery tracking
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    webhook_id INTEGER NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL,
    event TEXT NOT NULL,
    message_id INTEGER NOT NULL,
    payload TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'delivered', 'retrying', 'dead_lettered')),
    http_status INTEGER,
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    last_error TEXT,
    next_retry_at DATETIME,
    depth INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_deliveries_webhook_id ON webhook_deliveries(webhook_id);
CREATE INDEX IF NOT EXISTS idx_deliveries_status ON webhook_deliveries(status);
CREATE INDEX IF NOT EXISTS idx_deliveries_agent_status ON webhook_deliveries(agent_name, status);
CREATE INDEX IF NOT EXISTS idx_deliveries_next_retry ON webhook_deliveries(next_retry_at) WHERE status = 'retrying';
CREATE INDEX IF NOT EXISTS idx_deliveries_created_at ON webhook_deliveries(created_at);

-- K8s handler registrations
CREATE TABLE IF NOT EXISTS k8s_handlers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_name TEXT NOT NULL REFERENCES agents(name) ON DELETE CASCADE,
    image TEXT NOT NULL,
    events TEXT NOT NULL DEFAULT '[]',
    namespace TEXT NOT NULL,
    resources_memory TEXT NOT NULL DEFAULT '256Mi',
    resources_cpu TEXT NOT NULL DEFAULT '100m',
    env TEXT NOT NULL DEFAULT '{}',
    timeout_seconds INTEGER NOT NULL DEFAULT 600 CHECK(timeout_seconds >= 60 AND timeout_seconds <= 3600),
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'disabled')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(agent_name, image, namespace)
);

CREATE INDEX IF NOT EXISTS idx_k8s_handlers_agent ON k8s_handlers(agent_name);
CREATE INDEX IF NOT EXISTS idx_k8s_handlers_agent_status ON k8s_handlers(agent_name, status);

-- K8s job run tracking
CREATE TABLE IF NOT EXISTS k8s_job_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    handler_id INTEGER NOT NULL REFERENCES k8s_handlers(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL,
    message_id INTEGER NOT NULL,
    job_name TEXT NOT NULL,
    namespace TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'running', 'succeeded', 'failed')),
    failure_reason TEXT,
    started_at DATETIME,
    completed_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_job_runs_handler ON k8s_job_runs(handler_id);
CREATE INDEX IF NOT EXISTS idx_job_runs_agent_status ON k8s_job_runs(agent_name, status);
CREATE INDEX IF NOT EXISTS idx_job_runs_job_name ON k8s_job_runs(job_name);
```
