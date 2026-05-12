-- 030_dream_parallelism.sql
--
-- Allow concurrent dream-worker dispatches of the same (owner, job_type)
-- by adding a `slot` discriminator. Slot 0 is the historical behaviour
-- (one in-flight per type). Slots 1..N-1 are used when the worker / CLI
-- fans out to drain a backlog quickly.

ALTER TABLE memory_consolidation_jobs
    ADD COLUMN slot INTEGER NOT NULL DEFAULT 0;

DROP INDEX IF EXISTS idx_consolidation_in_flight;

CREATE UNIQUE INDEX IF NOT EXISTS idx_consolidation_in_flight
    ON memory_consolidation_jobs(owner_id, job_type, slot)
    WHERE status IN ('pending', 'dispatched', 'running');
