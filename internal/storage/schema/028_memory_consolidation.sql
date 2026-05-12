-- 028_memory_consolidation.sql — proactive memory + dream worker.
--
-- Adds six tables backing per-(owner, agent) core memory, typed memory
-- links, dream-worker consolidation jobs, owner-pinned memories,
-- single-use dispatch tokens, and a 24h rolling audit ring of what was
-- injected into each tool response. Adds one derived view
-- `memory_status` over `memory_consolidation_jobs.actions` so retrieval
-- can filter out soft-deleted / superseded memories without touching
-- the hot `messages` table.
--
-- `owner_id` is stored as TEXT (string form of `agents.owner_id`) so
-- that retrieval can carry the value through unchanged from
-- `auth.ContextAgent(ctx)`; the auth middleware already exposes it as a
-- string via `trace.ContextWithOwnerID`.
--
-- All `CREATE TABLE` / `CREATE INDEX` statements use `IF NOT EXISTS`
-- so this migration is safe to re-run. CHECK constraints enforce enum
-- columns. The partial unique index on
-- `memory_consolidation_jobs(owner_id, job_type)` guarantees at most
-- one in-flight job per (owner, job_type).

-- 1. Per-(owner, agent) identity-and-context blob.
CREATE TABLE IF NOT EXISTS memory_core (
    owner_id      TEXT NOT NULL,
    agent_name    TEXT NOT NULL,
    blob          TEXT NOT NULL,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_by    TEXT NOT NULL,
    PRIMARY KEY (owner_id, agent_name)
);
CREATE INDEX IF NOT EXISTS idx_memory_core_owner ON memory_core(owner_id);

-- 2. Directed typed edges between two message ids.
CREATE TABLE IF NOT EXISTS memory_links (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    src_message_id INTEGER NOT NULL,
    dst_message_id INTEGER NOT NULL,
    relation_type  TEXT NOT NULL CHECK (relation_type IN (
        'refines', 'contradicts', 'examples', 'related',
        'duplicate_of', 'superseded_by',
        'mention', 'reply_to', 'channel_cooccurrence'
    )),
    owner_id       TEXT NOT NULL,
    created_by     TEXT NOT NULL,
    metadata       TEXT NOT NULL DEFAULT '{}',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(src_message_id, dst_message_id, relation_type)
);
CREATE INDEX IF NOT EXISTS idx_memory_links_owner ON memory_links(owner_id);
CREATE INDEX IF NOT EXISTS idx_memory_links_src   ON memory_links(src_message_id);
CREATE INDEX IF NOT EXISTS idx_memory_links_dst   ON memory_links(dst_message_id);
CREATE INDEX IF NOT EXISTS idx_memory_links_type  ON memory_links(relation_type);

-- 3. Audit log of dispatched dream jobs and their resulting actions.
CREATE TABLE IF NOT EXISTS memory_consolidation_jobs (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_id       TEXT NOT NULL,
    job_type       TEXT NOT NULL CHECK (job_type IN (
        'reflection', 'core_rewrite', 'dedup_contradiction', 'link_gen'
    )),
    status         TEXT NOT NULL CHECK (status IN (
        'pending', 'dispatched', 'running', 'succeeded', 'partial', 'failed', 'expired'
    )) DEFAULT 'pending',
    trigger_reason TEXT NOT NULL,
    dispatch_token TEXT,
    harness_run_id TEXT,
    actions        TEXT NOT NULL DEFAULT '[]',
    summary        TEXT,
    error          TEXT,
    lease_until    DATETIME,
    started_at     DATETIME,
    finished_at    DATETIME,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_consolidation_owner_status
    ON memory_consolidation_jobs(owner_id, status);
CREATE INDEX IF NOT EXISTS idx_consolidation_lease
    ON memory_consolidation_jobs(lease_until)
    WHERE status = 'running';
CREATE UNIQUE INDEX IF NOT EXISTS idx_consolidation_in_flight
    ON memory_consolidation_jobs(owner_id, job_type)
    WHERE status IN ('pending', 'dispatched', 'running');

-- 4. Owner-pinned message ids that bypass the relevance floor.
CREATE TABLE IF NOT EXISTS memory_pins (
    owner_id   TEXT NOT NULL,
    message_id INTEGER NOT NULL,
    pinned_by  TEXT NOT NULL,
    note       TEXT,
    pinned_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (owner_id, message_id)
);
CREATE INDEX IF NOT EXISTS idx_memory_pins_owner ON memory_pins(owner_id);

-- 5. Single-use, owner-bound, job-bound dispatch tokens.
CREATE TABLE IF NOT EXISTS memory_dispatch_tokens (
    token                TEXT PRIMARY KEY,
    owner_id             TEXT NOT NULL,
    consolidation_job_id INTEGER NOT NULL,
    issued_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at           DATETIME NOT NULL,
    used_at              DATETIME,
    revoked_at           DATETIME,
    FOREIGN KEY (consolidation_job_id) REFERENCES memory_consolidation_jobs(id)
);
CREATE INDEX IF NOT EXISTS idx_memory_tokens_job
    ON memory_dispatch_tokens(consolidation_job_id);

-- 6. 24-hour rolling audit ring of what was injected for each tool call.
CREATE TABLE IF NOT EXISTS memory_injections (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_id           TEXT NOT NULL,
    agent_name         TEXT NOT NULL,
    tool_name          TEXT NOT NULL,
    packet_size_chars  INTEGER NOT NULL,
    packet_items_count INTEGER NOT NULL,
    message_ids        TEXT NOT NULL,
    core_blob_included BOOLEAN NOT NULL DEFAULT 0,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_memory_injections_owner_time
    ON memory_injections(owner_id, created_at);

-- 7. Derived view: memory_status. Tells retrieval whether a message is
--    active / soft_deleted / superseded based on completed consolidation
--    jobs' action records. Owners can override via a future restore
--    action (out of scope here).
CREATE VIEW IF NOT EXISTS memory_status AS
WITH actions AS (
    SELECT
        j.owner_id,
        json_extract(act.value, '$.target_message_id')  AS message_id,
        json_extract(act.value, '$.tool')                AS tool,
        json_extract(act.value, '$.args.keep_id')        AS keep_id,
        json_extract(act.value, '$.args.b_id')           AS superseded_by,
        json_extract(act.value, '$.args.reason')         AS reason,
        j.finished_at                                    AS at
    FROM memory_consolidation_jobs j, json_each(j.actions) act
    WHERE j.status IN ('succeeded', 'partial')
)
SELECT
    message_id,
    owner_id,
    CASE
        WHEN MAX(CASE WHEN tool='memory_supersede' THEN at END) IS NOT NULL THEN 'superseded'
        WHEN MAX(CASE WHEN tool='memory_mark_duplicate' AND keep_id != message_id THEN at END) IS NOT NULL THEN 'soft_deleted'
        ELSE 'active'
    END AS status,
    MAX(CASE WHEN tool='memory_supersede' THEN superseded_by END) AS superseded_by,
    MAX(CASE WHEN tool='memory_mark_duplicate' AND keep_id != message_id THEN at END) AS soft_deleted_at,
    MAX(reason) AS reason
FROM actions
GROUP BY message_id, owner_id;
