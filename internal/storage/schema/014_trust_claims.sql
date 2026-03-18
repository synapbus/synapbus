-- Trust scores per (agent, action_type) for graduated autonomy
CREATE TABLE IF NOT EXISTS agent_trust (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_name TEXT NOT NULL,
    action_type TEXT NOT NULL,
    score REAL NOT NULL DEFAULT 0.0,
    adjustments_count INTEGER NOT NULL DEFAULT 0,
    last_adjusted_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(agent_name, action_type)
);

CREATE INDEX idx_trust_agent ON agent_trust(agent_name);

-- Channel autonomy thresholds
ALTER TABLE channels ADD COLUMN publish_threshold REAL NOT NULL DEFAULT 0.8;
ALTER TABLE channels ADD COLUMN approve_threshold REAL NOT NULL DEFAULT 0.6;
