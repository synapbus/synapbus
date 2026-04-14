# Data Model: Dynamic Agent Spawning

**Phase**: 1 (Design)
**Date**: 2026-04-14

Schema changes captured in five new migrations. Sequenced after the latest existing migration (`020_harness_run_detail.sql`). All SQL is SQLite (modernc.org/sqlite, pure Go).

---

## Migration 021 — Goals and tasks

**File**: `internal/storage/schema/021_goals_tasks.sql`

```sql
-- Goals: top-level user objectives.
CREATE TABLE goals (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    slug                    TEXT    NOT NULL UNIQUE,
    title                   TEXT    NOT NULL,
    description             TEXT    NOT NULL,
    owner_user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id              INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    coordinator_agent_id    INTEGER          REFERENCES agents(id)   ON DELETE SET NULL,
    root_task_id            INTEGER          REFERENCES tasks(id)    ON DELETE SET NULL,
    status                  TEXT    NOT NULL DEFAULT 'draft'
                                        CHECK (status IN ('draft','active','paused','completed','cancelled','stuck')),
    budget_tokens           INTEGER,        -- NULL = no cap
    budget_dollars_cents    INTEGER,        -- NULL = no cap
    max_spawn_depth         INTEGER NOT NULL DEFAULT 3,
    created_at              DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at            DATETIME
);

CREATE INDEX idx_goals_owner   ON goals(owner_user_id);
CREATE INDEX idx_goals_status  ON goals(status);
CREATE INDEX idx_goals_channel ON goals(channel_id);

-- Tasks: nodes in a goal's work tree.
CREATE TABLE tasks (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    goal_id                 INTEGER NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    parent_task_id          INTEGER          REFERENCES tasks(id) ON DELETE CASCADE,
    ancestry_json           TEXT    NOT NULL DEFAULT '[]',    -- denormalized snapshot, cap 16 KB at app layer
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
    spent_tokens            INTEGER NOT NULL DEFAULT 0,      -- leaf-only; aggregate via recursive CTE
    spent_dollars_cents     INTEGER NOT NULL DEFAULT 0,
    heartbeat_config_json   TEXT,                            -- {source, interval_sec, max_budget}
    verifier_config_json    TEXT,                            -- {kind, ...} — see research D10
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

CREATE INDEX idx_tasks_goal        ON tasks(goal_id);
CREATE INDEX idx_tasks_parent      ON tasks(parent_task_id);
CREATE INDEX idx_tasks_assignee    ON tasks(assignee_agent_id);
CREATE INDEX idx_tasks_status      ON tasks(status);
CREATE INDEX idx_tasks_billing     ON tasks(billing_code);
```

### State machine

```
proposed ─(human approve)→ approved
                              │
                              │ claim_task (atomic UPDATE)
                              ▼
                          claimed ─→ in_progress ─→ awaiting_verification
                                                         │
                                                    ┌────┴────┐
                                                    ▼         ▼
                                                  done      failed
(any state) ─→ cancelled
```

### Atomic claim SQL

```sql
UPDATE tasks
   SET assignee_agent_id = :agent_id,
       status            = 'claimed',
       claimed_at        = CURRENT_TIMESTAMP,
       claim_message_id  = :msg_id
 WHERE id                = :task_id
   AND assignee_agent_id IS NULL
   AND status            = 'approved';
```

Inspect `rowsAffected`: 0 → `ErrAlreadyClaimed`.

### Cost rollup SQL

```sql
WITH RECURSIVE subtree(id) AS (
    SELECT id FROM tasks WHERE id = :root_task_id
    UNION ALL
    SELECT t.id FROM tasks t
    JOIN subtree s ON t.parent_task_id = s.id
)
SELECT
    COALESCE(SUM(spent_tokens), 0)       AS total_tokens,
    COALESCE(SUM(spent_dollars_cents),0) AS total_dollars_cents,
    COUNT(*)                             AS task_count
FROM tasks
WHERE id IN subtree;
```

---

## Migration 022 — Agent proposals and resource requests

**File**: `internal/storage/schema/022_agent_proposals.sql`

```sql
CREATE TABLE agent_proposals (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    proposer_agent_id       INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    goal_id                 INTEGER NOT NULL REFERENCES goals(id)  ON DELETE CASCADE,
    parent_task_id          INTEGER          REFERENCES tasks(id)  ON DELETE SET NULL,
    proposed_name           TEXT    NOT NULL,
    proposed_model          TEXT    NOT NULL,
    proposed_system_prompt  TEXT    NOT NULL,
    proposed_tool_scope_json TEXT   NOT NULL DEFAULT '[]',
    proposed_skills_json    TEXT    NOT NULL DEFAULT '[]',
    proposed_mcp_servers_json TEXT  NOT NULL DEFAULT '[]',
    proposed_subagents_json TEXT    NOT NULL DEFAULT '[]',
    proposed_autonomy_tier  TEXT    NOT NULL
                                        CHECK (proposed_autonomy_tier IN ('supervised','assisted','autonomous')),
    reason                  TEXT    NOT NULL DEFAULT '',
    status                  TEXT    NOT NULL DEFAULT 'pending'
                                        CHECK (status IN ('pending','approved','rejected','materialized','cancelled')),
    approval_message_id     INTEGER REFERENCES messages(id) ON DELETE SET NULL,
    materialized_agent_id   INTEGER REFERENCES agents(id)   ON DELETE SET NULL,
    rejection_reason        TEXT,
    created_at              DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    decided_at              DATETIME
);

CREATE INDEX idx_proposals_goal   ON agent_proposals(goal_id);
CREATE INDEX idx_proposals_parent ON agent_proposals(parent_task_id);
CREATE INDEX idx_proposals_status ON agent_proposals(status);

CREATE TABLE resource_requests (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    requester_agent_id      INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    task_id                 INTEGER NOT NULL REFERENCES tasks(id)  ON DELETE CASCADE,
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
```

---

## Migration 023 — Agent trust model

**File**: `internal/storage/schema/023_agent_trust_model.sql`

```sql
-- Drop the dormant trust table from migration 014 and recreate under the new schema.
DROP TABLE IF EXISTS trust;

-- Extend the agents table with trust-related columns.
ALTER TABLE agents ADD COLUMN config_hash       TEXT    NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN parent_agent_id   INTEGER          REFERENCES agents(id) ON DELETE SET NULL;
ALTER TABLE agents ADD COLUMN spawn_depth       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agents ADD COLUMN system_prompt     TEXT    NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN autonomy_tier     TEXT    NOT NULL DEFAULT 'supervised'
                                                        CHECK (autonomy_tier IN ('supervised','assisted','autonomous'));
ALTER TABLE agents ADD COLUMN tool_scope_json   TEXT    NOT NULL DEFAULT '[]';
ALTER TABLE agents ADD COLUMN quarantined_at    DATETIME;
ALTER TABLE agents ADD COLUMN quarantine_reason TEXT;

CREATE INDEX idx_agents_config_hash  ON agents(config_hash);
CREATE INDEX idx_agents_parent       ON agents(parent_agent_id);

-- One-time data migration: extract system_prompt from harness_config_json where present.
-- (Executed in Go migration runner code; the SQL here is schema-only.)

-- Append-only reputation ledger. Rolling score computed at read time.
CREATE TABLE reputation_evidence (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    config_hash     TEXT    NOT NULL,
    owner_user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    task_domain     TEXT    NOT NULL DEFAULT 'default',
    score_delta     REAL    NOT NULL,                 -- positive = success, negative = failure
    evidence_ref    TEXT    NOT NULL,                 -- e.g., 'task:123 verified=peer:456'
    weight          REAL    NOT NULL DEFAULT 1.0,     -- optional amplifier
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_rep_hash_domain ON reputation_evidence(config_hash, task_domain);
CREATE INDEX idx_rep_owner       ON reputation_evidence(owner_user_id);
CREATE INDEX idx_rep_created     ON reputation_evidence(created_at);
```

### Rolling score query

```sql
-- HALF_LIFE_DAYS default 30. Pass in as :half_life_days.
SELECT
    COALESCE(
        SUM(score_delta * weight *
            exp(-0.6931471805599453 * (julianday('now') - julianday(created_at)) / :half_life_days)),
        0.5    -- neutral default for no evidence
    ) AS rolling_score
FROM reputation_evidence
WHERE config_hash = :hash
  AND task_domain = :domain;
```

Clamp `[0.0, 1.0]` in Go. `exp()` is available in SQLite 3.35+ (modernc.org/sqlite embeds a recent version).

---

## Migration 024 — Secrets

**File**: `internal/storage/schema/024_secrets.sql`

```sql
CREATE TABLE secrets (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL,                 -- display/env name (uppercased, A-Z0-9_)
    scope_type      TEXT    NOT NULL
                                CHECK (scope_type IN ('user','agent','task')),
    scope_id        INTEGER NOT NULL,
    value_blob      BLOB    NOT NULL,                 -- nonce(24) || ciphertext
    created_by      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at      DATETIME,
    last_used_at    DATETIME
);

CREATE UNIQUE INDEX idx_secrets_scope_name ON secrets(scope_type, scope_id, name) WHERE revoked_at IS NULL;
CREATE INDEX idx_secrets_scope ON secrets(scope_type, scope_id);
```

---

## Migration 025 — harness_runs.task_id

**File**: `internal/storage/schema/025_harness_runs_task_id.sql`

```sql
ALTER TABLE harness_runs ADD COLUMN task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL;

CREATE INDEX idx_harness_runs_task ON harness_runs(task_id);
```

---

## Entity relationship (ASCII)

```
users ──1─┬─< goals ──1─< tasks ──*── messages (origin/claim/completion)
          │      │          │
          │      │          ├─< harness_runs
          │      │          │
          │      1          │
          │      │          │
          │      channels ──┘
          │
          ├─< agents ──*─< agent_proposals ──1─→ agents (materialized)
          │      │
          │      ├─< reputation_evidence (by config_hash)
          │      │
          │      └─< resource_requests ──*── secrets
          │
          └─< secrets (scope=user)

tasks ──< secrets (scope=task)
agents ──< secrets (scope=agent)
```

---

## Go types (shape)

```go
// internal/goals/types.go
type Goal struct {
    ID                   int64
    Slug                 string
    Title                string
    Description          string
    OwnerUserID          int64
    ChannelID            int64
    CoordinatorAgentID   *int64
    RootTaskID           *int64
    Status               GoalStatus  // draft|active|paused|completed|cancelled|stuck
    BudgetTokens         *int64
    BudgetDollarsCents   *int64
    MaxSpawnDepth        int
    CreatedAt, UpdatedAt, CompletedAt *time.Time
}

// internal/tasks/types.go
type Task struct {
    ID                  int64
    GoalID              int64
    ParentTaskID        *int64
    Ancestry            []AncestryNode    // deserialized ancestry_json
    Depth               int
    Title               string
    Description         string
    AcceptanceCriteria  string
    CreatedByAgentID    *int64
    CreatedByUserID     *int64
    AssigneeAgentID     *int64
    Status              TaskStatus
    BillingCode         string
    BudgetTokens        *int64
    BudgetDollarsCents  *int64
    SpentTokens         int64
    SpentDollarsCents   int64
    HeartbeatConfig     *HeartbeatConfig
    VerifierConfig      *VerifierConfig
    OriginMessageID     *int64
    ClaimMessageID      *int64
    CompletionMessageID *int64
    FailureReason       string
    Timestamps          TaskTimestamps
}

type AncestryNode struct {
    ID                  int64  `json:"id"`
    Title               string `json:"title"`
    AcceptanceCriteria  string `json:"acceptance_criteria,omitempty"`
}

type HeartbeatConfig struct {
    Source       string `json:"source"`      // task_assignment | task_timer | verification_requested
    IntervalSec  int    `json:"interval_sec,omitempty"`
    MaxBudget    int    `json:"max_budget,omitempty"`
}

type VerifierConfig struct {
    Kind       string `json:"kind"`               // auto | peer | command
    AgentID    int64  `json:"agent_id,omitempty"`
    Cmd        string `json:"cmd,omitempty"`
    Cwd        string `json:"cwd,omitempty"`
    TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// internal/trust/types.go
type Evidence struct {
    ID            int64
    ConfigHash    string
    OwnerUserID   int64
    TaskDomain    string
    ScoreDelta    float64
    EvidenceRef   string
    Weight        float64
    CreatedAt     time.Time
}

// internal/secrets/types.go
type Secret struct {
    ID          int64
    Name        string
    ScopeType   ScopeType
    ScopeID     int64
    CreatedBy   int64
    CreatedAt   time.Time
    RevokedAt   *time.Time
    LastUsedAt  *time.Time
    // value_blob is never loaded into this struct directly — accessed only via store.Decrypt(id)
}
```

---

## Validation rules

| Rule | Layer | Enforcement |
|---|---|---|
| Goal slug unique | DB | `UNIQUE INDEX` on `goals.slug` |
| Task ancestry ≤ 16 KB | App | `len(ancestry_json) > 16*1024 → ErrAncestryOverflow` before insert |
| Task state transitions match state machine | App | Guarded transition function in `tasks.Service.TransitionTo` |
| Exactly one `created_by_*` set on task | DB | `CHECK` constraint |
| Autonomy tier enum | DB | `CHECK` constraint |
| Child spawn depth = parent + 1, ≤ goal.max_spawn_depth | App | Guard in `agents.Service.MaterializeFromProposal` |
| Child tool scope ⊆ parent tool scope | App | `trust.DelegationCap(parent, proposed).IsSuperset()` check in `propose_agent` handler |
| Budget invariant at delegation | App | Inside `CreateSubTask` transaction, check parent's remaining budget |
| Config hash stable and deterministic | App | Unit test with shuffled input arrays |
| Secret name sanitization | App | Regex `^[A-Z0-9_]+$` enforced on write, auto-uppercased & stripped |
| Reputation score clamp | App | Clamp `[0,1]` after rolling-query computation |
| Goal status transitions respect current state | App | Guarded transition in `goals.Service.TransitionTo` |

---

## Data migration — extracting `system_prompt` from existing agents

Run once in the `023` migration's Go runner (before any new code touches `agents.system_prompt`):

```go
// For each existing agent with harness_config_json containing claude_md/gemini_md/agents_md,
// extract the primary prompt field and write it to the new system_prompt column.
// Preference order: claude_md > gemini_md > agents_md > "" (empty).
// Idempotent: safe to re-run — only touches rows where system_prompt = ''.
```

Also compute `config_hash` for every existing agent and write it to the new column. Reputation ledger starts empty; existing agents default to neutral score (`0.5`).

**End of data-model.md — ready for contracts.**
