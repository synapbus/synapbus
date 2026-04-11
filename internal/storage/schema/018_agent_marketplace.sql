-- 018: Agent marketplace — reputation ledger + 'awarded' reaction (spec 016)
--
-- Records one row per completed auction task per (agent, domain). A task
-- with multiple domain tags produces multiple rows, so reputation is queryable
-- as a vector across domains (FR-013).

-- Widen the reactions CHECK constraint to allow the new 'awarded' reaction
-- used on auction-channel bid messages (FR-008). SQLite cannot ALTER a CHECK
-- constraint in place, so we rebuild the table.
CREATE TABLE IF NOT EXISTS message_reactions_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL,
    reaction TEXT NOT NULL CHECK(reaction IN ('approve', 'reject', 'in_progress', 'awarded', 'done', 'published')),
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(message_id, agent_name, reaction)
);

INSERT INTO message_reactions_new (id, message_id, agent_name, reaction, metadata, created_at)
SELECT id, message_id, agent_name, reaction, metadata, created_at FROM message_reactions;

DROP TABLE message_reactions;
ALTER TABLE message_reactions_new RENAME TO message_reactions;

CREATE INDEX IF NOT EXISTS idx_reactions_message ON message_reactions(message_id);
CREATE INDEX IF NOT EXISTS idx_reactions_agent ON message_reactions(agent_name);
CREATE INDEX IF NOT EXISTS idx_reactions_type ON message_reactions(reaction);

CREATE TABLE IF NOT EXISTS agent_reputation (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_name        TEXT    NOT NULL,
    domain            TEXT    NOT NULL,
    task_id           INTEGER,                          -- source task; nullable for seed/manual entries
    estimated_tokens  INTEGER NOT NULL DEFAULT 0,
    actual_tokens     INTEGER NOT NULL DEFAULT 0,
    success_score     REAL    NOT NULL DEFAULT 1.0,     -- 0.0 (failure) .. 1.0 (perfect)
    difficulty_weight REAL    NOT NULL DEFAULT 1.0,     -- per-task difficulty multiplier
    completed_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_reputation_agent_domain
    ON agent_reputation(agent_name, domain);

CREATE INDEX IF NOT EXISTS idx_agent_reputation_domain
    ON agent_reputation(domain);

CREATE INDEX IF NOT EXISTS idx_agent_reputation_task
    ON agent_reputation(task_id);

INSERT INTO schema_migrations (version) VALUES (18);
