-- Add owner_id to traces table for owner-scoped queries
ALTER TABLE traces ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';

-- Composite indexes for efficient owner-scoped trace queries
CREATE INDEX idx_traces_owner_timestamp ON traces(owner_id, created_at);
CREATE INDEX idx_traces_owner_agent_timestamp ON traces(owner_id, agent_name, created_at);
CREATE INDEX idx_traces_owner_action_timestamp ON traces(owner_id, action, created_at);

INSERT INTO schema_migrations (version) VALUES (3);
