-- 031: Composite index to speed up ExpireTasks() in expiry-worker.
--
-- Root cause: `ExpireTasks` runs
--   UPDATE tasks SET status='cancelled', updated_at=CURRENT_TIMESTAMP
--    WHERE status='open' AND deadline IS NOT NULL AND deadline < ?
--
-- Pre-031 indexes were only `idx_tasks_status(status)` and `idx_tasks_channel(channel_id)`.
-- With a status cardinality of 4 and most tasks in two buckets, the planner used
-- `idx_tasks_status` to find all open rows then evaluated the deadline predicate
-- per row. As the auction-tasks table grew (kubic deploy), the worker's 30s
-- context deadline started to be exceeded on every tick, especially under WAL
-- write contention from concurrent message inserts.
--
-- Fix: composite, partial index on `(status, deadline)` covering only rows that
-- can ever be expired (status='open' AND deadline IS NOT NULL). This is the
-- exact predicate the worker uses, so SQLite can seek straight to the eligible
-- rows. The partial form keeps the index tiny once tasks transition out of
-- 'open' (the dominant steady-state).
CREATE INDEX IF NOT EXISTS idx_tasks_expiry
    ON tasks(status, deadline)
    WHERE status = 'open' AND deadline IS NOT NULL;
