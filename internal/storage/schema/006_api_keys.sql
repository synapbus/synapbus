-- API key management with permissions
-- Supports user-level and agent-level API keys with fine-grained permissions

CREATE TABLE IF NOT EXISTS api_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id),
    agent_id INTEGER REFERENCES agents(id),  -- NULL = user-level key
    name TEXT NOT NULL,                       -- human-readable label
    key_prefix TEXT NOT NULL,                 -- first 8 chars for identification
    key_hash TEXT NOT NULL,                   -- bcrypt hash of full key
    permissions TEXT NOT NULL DEFAULT '{}',   -- JSON: {"read": true, "write": true, "admin": false}
    allowed_channels TEXT NOT NULL DEFAULT '[]', -- JSON array of channel names, empty = all
    read_only INTEGER NOT NULL DEFAULT 0,
    expires_at TIMESTAMP,                    -- NULL = never expires
    last_used_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at TIMESTAMP                     -- soft-delete
);

CREATE INDEX idx_api_keys_user ON api_keys(user_id);
CREATE INDEX idx_api_keys_agent ON api_keys(agent_id);
CREATE INDEX idx_api_keys_prefix ON api_keys(key_prefix);
