-- 029_memory_dream_usage.sql — per-(date, owner) circuit-breaker counters
-- for the dream worker (feature 020 follow-up). Each row accumulates
-- daily token and job consumption so the ConsolidatorWorker can refuse
-- to dispatch further jobs once a threshold is hit. Rows are keyed by
-- UTC date (YYYY-MM-DD); the circuit naturally resets at midnight UTC
-- when Today() begins reading from a fresh row.

CREATE TABLE IF NOT EXISTS memory_dream_usage (
    date         TEXT NOT NULL,           -- YYYY-MM-DD UTC
    owner_id     TEXT NOT NULL,
    tokens_in    INTEGER NOT NULL DEFAULT 0,
    tokens_out   INTEGER NOT NULL DEFAULT 0,
    jobs_started INTEGER NOT NULL DEFAULT 0,
    jobs_succeeded INTEGER NOT NULL DEFAULT 0,
    jobs_failed  INTEGER NOT NULL DEFAULT 0,
    jobs_circuit_broken INTEGER NOT NULL DEFAULT 0,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (date, owner_id)
);
CREATE INDEX IF NOT EXISTS idx_memory_dream_usage_date ON memory_dream_usage(date);
