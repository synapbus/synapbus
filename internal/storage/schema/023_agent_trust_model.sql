-- 023: Dynamic-agent trust model columns + reputation ledger.
--
-- Coexists with the existing `agent_trust` table from migration 014
-- (which remains keyed by agent_name + action_type and is still used
-- by the reactions + hybrid MCP paths). The new ledger below is a
-- parallel append-only evidence log keyed by (config_hash, task_domain)
-- for the dynamic-spawning trust model.

ALTER TABLE agents ADD COLUMN config_hash       TEXT    NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN parent_agent_id   INTEGER REFERENCES agents(id) ON DELETE SET NULL;
ALTER TABLE agents ADD COLUMN spawn_depth       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agents ADD COLUMN system_prompt     TEXT    NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN autonomy_tier     TEXT    NOT NULL DEFAULT 'supervised';
ALTER TABLE agents ADD COLUMN tool_scope_json   TEXT    NOT NULL DEFAULT '[]';
ALTER TABLE agents ADD COLUMN quarantined_at    DATETIME;
ALTER TABLE agents ADD COLUMN quarantine_reason TEXT;

CREATE INDEX idx_agents_config_hash ON agents(config_hash);
CREATE INDEX idx_agents_parent      ON agents(parent_agent_id);

-- Append-only reputation ledger for the dynamic-spawning trust model.
-- The current rolling score is derived at read time via exponential
-- time decay; this table is the source of truth.
CREATE TABLE reputation_evidence (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    config_hash     TEXT    NOT NULL,
    owner_user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    task_domain     TEXT    NOT NULL DEFAULT 'default',
    score_delta     REAL    NOT NULL,
    evidence_ref    TEXT    NOT NULL,
    weight          REAL    NOT NULL DEFAULT 1.0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_rep_hash_domain ON reputation_evidence(config_hash, task_domain);
CREATE INDEX idx_rep_owner       ON reputation_evidence(owner_user_id);
CREATE INDEX idx_rep_created     ON reputation_evidence(created_at);
