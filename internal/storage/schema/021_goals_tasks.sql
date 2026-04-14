-- 021: Goals and tasks — first-class data model for dynamic agent spawning.
--
-- Goals are human-owned top-level objectives. Each goal has a backing
-- #goal-<slug> channel and a pre-built coordinator agent.
--
-- Tasks are nodes in a goal's work tree. They are single-assignee,
-- atomically claimable (optimistic-lock UPDATE), carry a denormalized
-- goal-ancestry JSON snapshot, and accumulate leaf-only cost counters
-- that roll up via a recursive CTE at read time.

CREATE TABLE goals (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    slug                    TEXT    NOT NULL UNIQUE,
    title                   TEXT    NOT NULL,
    description             TEXT    NOT NULL,
    owner_user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id              INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    coordinator_agent_id    INTEGER          REFERENCES agents(id)   ON DELETE SET NULL,
    root_task_id            INTEGER,
    status                  TEXT    NOT NULL DEFAULT 'draft'
                                        CHECK (status IN ('draft','active','paused','completed','cancelled','stuck')),
    budget_tokens           INTEGER,
    budget_dollars_cents    INTEGER,
    max_spawn_depth         INTEGER NOT NULL DEFAULT 3,
    alert_80pct_posted      INTEGER NOT NULL DEFAULT 0,
    created_at              DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at            DATETIME
);

CREATE INDEX idx_goals_owner   ON goals(owner_user_id);
CREATE INDEX idx_goals_status  ON goals(status);
CREATE INDEX idx_goals_channel ON goals(channel_id);

CREATE TABLE goal_tasks (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    goal_id                 INTEGER NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    parent_task_id          INTEGER          REFERENCES goal_tasks(id) ON DELETE CASCADE,
    ancestry_json           TEXT    NOT NULL DEFAULT '[]',
    depth                   INTEGER NOT NULL DEFAULT 0,
    title                   TEXT    NOT NULL,
    description             TEXT    NOT NULL,
    acceptance_criteria     TEXT    NOT NULL DEFAULT '',
    created_by_agent_id     INTEGER          REFERENCES agents(id) ON DELETE SET NULL,
    created_by_user_id      INTEGER          REFERENCES users(id)  ON DELETE SET NULL,
    assignee_agent_id       INTEGER          REFERENCES agents(id) ON DELETE SET NULL,
    status                  TEXT    NOT NULL DEFAULT 'proposed'
                                        CHECK (status IN ('proposed','approved','claimed','in_progress',
                                                          'awaiting_verification','done','failed','cancelled')),
    billing_code            TEXT,
    budget_tokens           INTEGER,
    budget_dollars_cents    INTEGER,
    spent_tokens            INTEGER NOT NULL DEFAULT 0,
    spent_dollars_cents     INTEGER NOT NULL DEFAULT 0,
    heartbeat_config_json   TEXT,
    verifier_config_json    TEXT,
    origin_message_id       INTEGER REFERENCES messages(id)  ON DELETE SET NULL,
    claim_message_id        INTEGER REFERENCES messages(id)  ON DELETE SET NULL,
    completion_message_id   INTEGER REFERENCES messages(id)  ON DELETE SET NULL,
    failure_reason          TEXT,
    created_at              DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    approved_at             DATETIME,
    claimed_at              DATETIME,
    started_at              DATETIME,
    completed_at            DATETIME,
    CHECK (created_by_agent_id IS NOT NULL OR created_by_user_id IS NOT NULL)
);

CREATE INDEX idx_goal_tasks_goal     ON goal_tasks(goal_id);
CREATE INDEX idx_goal_tasks_parent   ON goal_tasks(parent_task_id);
CREATE INDEX idx_goal_tasks_assignee ON goal_tasks(assignee_agent_id);
CREATE INDEX idx_goal_tasks_status   ON goal_tasks(status);
CREATE INDEX idx_goal_tasks_billing  ON goal_tasks(billing_code);
