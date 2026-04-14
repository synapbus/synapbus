-- 022: Agent proposals + resource requests.
--
-- Agent proposals: a pending request by an existing agent to spawn a new
-- specialist sub-agent. Delegation-cap and spawn-depth checks run at
-- propose time; approval via the reactions workflow on #approvals.
--
-- Resource requests: an agent asking the human owner for a missing
-- secret (API key, credential) via #requests.

CREATE TABLE agent_proposals (
    id                          INTEGER PRIMARY KEY AUTOINCREMENT,
    proposer_agent_id           INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    goal_id                     INTEGER NOT NULL REFERENCES goals(id)  ON DELETE CASCADE,
    parent_task_id              INTEGER          REFERENCES goal_tasks(id)  ON DELETE SET NULL,
    proposed_name               TEXT    NOT NULL,
    proposed_model              TEXT    NOT NULL,
    proposed_system_prompt      TEXT    NOT NULL,
    proposed_tool_scope_json    TEXT    NOT NULL DEFAULT '[]',
    proposed_skills_json        TEXT    NOT NULL DEFAULT '[]',
    proposed_mcp_servers_json   TEXT    NOT NULL DEFAULT '[]',
    proposed_subagents_json     TEXT    NOT NULL DEFAULT '[]',
    proposed_autonomy_tier      TEXT    NOT NULL
                                            CHECK (proposed_autonomy_tier IN ('supervised','assisted','autonomous')),
    reason                      TEXT    NOT NULL DEFAULT '',
    status                      TEXT    NOT NULL DEFAULT 'pending'
                                            CHECK (status IN ('pending','approved','rejected','materialized','cancelled')),
    approval_message_id         INTEGER REFERENCES messages(id) ON DELETE SET NULL,
    materialized_agent_id       INTEGER REFERENCES agents(id)   ON DELETE SET NULL,
    rejection_reason            TEXT,
    created_at                  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    decided_at                  DATETIME
);

CREATE INDEX idx_proposals_goal   ON agent_proposals(goal_id);
CREATE INDEX idx_proposals_parent ON agent_proposals(parent_task_id);
CREATE INDEX idx_proposals_status ON agent_proposals(status);

CREATE TABLE resource_requests (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    requester_agent_id      INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    task_id                 INTEGER NOT NULL REFERENCES goal_tasks(id)  ON DELETE CASCADE,
    resource_name           TEXT    NOT NULL,
    resource_type           TEXT    NOT NULL,
    reason                  TEXT    NOT NULL,
    status                  TEXT    NOT NULL DEFAULT 'pending'
                                        CHECK (status IN ('pending','fulfilled','rejected','revoked')),
    request_message_id      INTEGER REFERENCES messages(id) ON DELETE SET NULL,
    created_at              DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    fulfilled_at            DATETIME
);

CREATE INDEX idx_requests_task      ON resource_requests(task_id);
CREATE INDEX idx_requests_requester ON resource_requests(requester_agent_id);
CREATE INDEX idx_requests_status    ON resource_requests(status);
