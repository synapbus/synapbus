// Dream-worker daily usage tracker + circuit breaker (feature 020
// follow-up). Wraps the `memory_dream_usage` table from migration 029.
//
// Each row aggregates one (UTC date, owner_id) bucket. The
// ConsolidatorWorker:
//
//  1. Calls UsageGate.Allow() before Create+Issue+Execute. If today's
//     counters exceed any configured threshold the gate returns
//     allowed=false with a reason code; the worker then records a
//     `circuit_broken` job row and skips dispatch.
//  2. On successful Execute completion, calls RecordCompletion with
//     the harness.ExecResult.Usage tokens so tomorrow's gate decisions
//     incorporate today's spend.
//
// The circuit "resets" naturally — Today() pivots on UTC date so the
// first call after midnight UTC reads a fresh row with zero counters.
package messaging

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DreamDailyUsage is one row of `memory_dream_usage`.
type DreamDailyUsage struct {
	Date              string    `json:"date"`
	OwnerID           string    `json:"owner_id"`
	TokensIn          int64     `json:"tokens_in"`
	TokensOut         int64     `json:"tokens_out"`
	JobsStarted       int       `json:"jobs_started"`
	JobsSucceeded     int       `json:"jobs_succeeded"`
	JobsFailed        int       `json:"jobs_failed"`
	JobsCircuitBroken int       `json:"jobs_circuit_broken"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// DreamUsageStore wraps the `memory_dream_usage` table.
type DreamUsageStore struct {
	db *sql.DB
	// now is overridable in tests so callers can pin the UTC date used
	// when bucketing.
	now func() time.Time
}

// NewDreamUsageStore returns a store rooted at db.
func NewDreamUsageStore(db *sql.DB) *DreamUsageStore {
	return &DreamUsageStore{db: db, now: time.Now}
}

// utcDate returns the YYYY-MM-DD key for the store's clock.
func (s *DreamUsageStore) utcDate() string {
	return s.now().UTC().Format("2006-01-02")
}

// upsert is the common path for all increments — UPSERT (date, owner_id).
// Counters are added with COALESCE so deltas accumulate cleanly.
func (s *DreamUsageStore) upsert(ctx context.Context, ownerID string,
	dIn, dOut int64, dStart, dSuc, dFail, dCB int,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	if ownerID == "" {
		return fmt.Errorf("dream usage: empty owner_id")
	}
	date := s.utcDate()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_dream_usage
		  (date, owner_id, tokens_in, tokens_out,
		   jobs_started, jobs_succeeded, jobs_failed, jobs_circuit_broken, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(date, owner_id) DO UPDATE SET
		  tokens_in = tokens_in + excluded.tokens_in,
		  tokens_out = tokens_out + excluded.tokens_out,
		  jobs_started = jobs_started + excluded.jobs_started,
		  jobs_succeeded = jobs_succeeded + excluded.jobs_succeeded,
		  jobs_failed = jobs_failed + excluded.jobs_failed,
		  jobs_circuit_broken = jobs_circuit_broken + excluded.jobs_circuit_broken,
		  updated_at = CURRENT_TIMESTAMP
	`, date, ownerID, dIn, dOut, dStart, dSuc, dFail, dCB)
	if err != nil {
		return fmt.Errorf("dream usage: upsert: %w", err)
	}
	return nil
}

// RecordStart increments today's jobs_started for ownerID.
func (s *DreamUsageStore) RecordStart(ctx context.Context, ownerID string) error {
	return s.upsert(ctx, ownerID, 0, 0, 1, 0, 0, 0)
}

// RecordCompletion increments tokens and the appropriate per-status
// counter. status ∈ {succeeded, failed, circuit_broken}. Unknown
// statuses are counted as failed so the gate errs on the safe side.
func (s *DreamUsageStore) RecordCompletion(ctx context.Context, ownerID string, tokensIn, tokensOut int64, status string) error {
	var dSuc, dFail, dCB int
	switch status {
	case JobStatusSucceeded:
		dSuc = 1
	case JobStatusCircuitBroken:
		dCB = 1
	case JobStatusPartial:
		// Partial counts as succeeded for the circuit-breaker — it ran.
		dSuc = 1
	default:
		dFail = 1
	}
	return s.upsert(ctx, ownerID, tokensIn, tokensOut, 0, dSuc, dFail, dCB)
}

// Today returns today's counters for ownerID. Missing row → zero-value.
func (s *DreamUsageStore) Today(ctx context.Context, ownerID string) (DreamDailyUsage, error) {
	if s == nil || s.db == nil {
		return DreamDailyUsage{}, nil
	}
	if ownerID == "" {
		return DreamDailyUsage{}, fmt.Errorf("dream usage: empty owner_id")
	}
	date := s.utcDate()
	u := DreamDailyUsage{Date: date, OwnerID: ownerID}
	err := s.db.QueryRowContext(ctx, `
		SELECT tokens_in, tokens_out, jobs_started, jobs_succeeded,
		       jobs_failed, jobs_circuit_broken, updated_at
		  FROM memory_dream_usage
		 WHERE date = ? AND owner_id = ?
	`, date, ownerID).Scan(
		&u.TokensIn, &u.TokensOut, &u.JobsStarted, &u.JobsSucceeded,
		&u.JobsFailed, &u.JobsCircuitBroken, &u.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return u, nil
	}
	if err != nil {
		return u, fmt.Errorf("dream usage: today: %w", err)
	}
	return u, nil
}

// Cleanup deletes usage rows older than olderThanDays. Default callers
// can pass 30 to keep a month of history for diagnostics.
func (s *DreamUsageStore) Cleanup(ctx context.Context, olderThanDays int) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	if olderThanDays <= 0 {
		return 0, nil
	}
	cutoff := s.now().UTC().AddDate(0, 0, -olderThanDays).Format("2006-01-02")
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_dream_usage WHERE date < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("dream usage: cleanup: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// UsageGate is the circuit breaker the ConsolidatorWorker consults
// before dispatching a job. It reads today's counters via DreamUsageStore
// and compares them against the per-day limits in MemoryConfig.
type UsageGate struct {
	cfg   MemoryConfig
	store *DreamUsageStore
}

// NewUsageGate ties a MemoryConfig to a DreamUsageStore.
func NewUsageGate(cfg MemoryConfig, store *DreamUsageStore) *UsageGate {
	return &UsageGate{cfg: cfg, store: store}
}

// Allow returns (allowed, reasonCode, err). When allowed is false the
// caller should record a `circuit_broken` job and skip dispatch.
// reasonCode is one of {tokens_in_exceeded, tokens_out_exceeded,
// jobs_exceeded, ""}. err is non-nil only for database failures —
// callers may treat err != nil as "fail open" to avoid wedging the
// worker on a transient store glitch.
func (g *UsageGate) Allow(ctx context.Context, ownerID string) (bool, string, error) {
	if g == nil || g.store == nil {
		return true, "", nil
	}
	u, err := g.store.Today(ctx, ownerID)
	if err != nil {
		return true, "", err
	}
	if g.cfg.DreamDailyTokenLimitIn > 0 && u.TokensIn >= g.cfg.DreamDailyTokenLimitIn {
		return false, "tokens_in_exceeded", nil
	}
	if g.cfg.DreamDailyTokenLimitOut > 0 && u.TokensOut >= g.cfg.DreamDailyTokenLimitOut {
		return false, "tokens_out_exceeded", nil
	}
	if g.cfg.DreamDailyJobLimit > 0 && u.JobsStarted >= g.cfg.DreamDailyJobLimit {
		return false, "jobs_exceeded", nil
	}
	return true, "", nil
}
