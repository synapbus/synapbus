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
