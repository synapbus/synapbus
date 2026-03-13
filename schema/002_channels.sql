-- Channel invites for private channel membership gating
CREATE TABLE IF NOT EXISTS channel_invites (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL,
    invited_by TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'declined')),
    UNIQUE(channel_id, agent_name)
);

CREATE INDEX IF NOT EXISTS idx_channel_invites_agent ON channel_invites(agent_name);

INSERT OR IGNORE INTO schema_migrations (version) VALUES (2);
