-- 019: Harness-agnostic wrappers
-- Adds per-agent harness configuration and a backend-agnostic harness_runs
-- table used by all harness implementations (k8sjob, subprocess, webhook).
-- See docs/harness-otel-design.md.

-- Per-agent harness configuration ----------------------------------------

-- Explicit harness name ("k8sjob", "subprocess", "webhook", "stub").
-- When NULL, Registry.Resolve picks a backend by fallback rules.
ALTER TABLE agents ADD COLUMN harness_name TEXT;

-- JSON-encoded argv for the subprocess backend, e.g.
--   ["claude", "--print", "--max-turns", "50"]
ALTER TABLE agents ADD COLUMN local_command TEXT;

-- Per-harness opaque config blob; parsed by the backend that owns it.
ALTER TABLE agents ADD COLUMN harness_config_json TEXT;


-- Backend-agnostic harness_runs table ------------------------------------

CREATE TABLE harness_runs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id        TEXT NOT NULL UNIQUE,
    agent_name    TEXT NOT NULL,
    backend       TEXT NOT NULL,      -- 'k8sjob' | 'subprocess' | 'webhook' | 'stub'
    message_id    INTEGER,             -- triggering message, if any
    status        TEXT NOT NULL,       -- 'pending' | 'running' | 'success' | 'failed' | 'cancelled' | 'timeout'
    exit_code     INTEGER,
    trace_id      TEXT,
    span_id       TEXT,
    session_id    TEXT,                -- backend session id for resume, if any
    tokens_in     INTEGER NOT NULL DEFAULT 0,
    tokens_out    INTEGER NOT NULL DEFAULT 0,
    tokens_cached INTEGER NOT NULL DEFAULT 0,
    cost_usd      REAL NOT NULL DEFAULT 0,
    duration_ms   INTEGER,
    result_json   TEXT,
    logs_excerpt  TEXT,                -- bounded; full logs live on disk
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at   DATETIME
);

CREATE INDEX idx_harness_runs_agent  ON harness_runs(agent_name, created_at DESC);
CREATE INDEX idx_harness_runs_status ON harness_runs(status, created_at DESC);
CREATE INDEX idx_harness_runs_trace  ON harness_runs(trace_id);
CREATE INDEX idx_harness_runs_run_id ON harness_runs(run_id);
