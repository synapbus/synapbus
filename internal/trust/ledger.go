package trust

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// defaultHalfLifeDays is used when a caller passes 0 to RollingScore
// or SeedFromParent.
const defaultHalfLifeDays = 30.0

// neutralScore is the rolling score returned when no evidence exists for a
// (config_hash, task_domain) pair. 0.5 signals "no information yet".
const neutralScore = 0.5

// seedFromParentFraction is the fraction of the parent's rolling score that
// gets seeded onto a fresh child config_hash when SeedFromParent is called.
const seedFromParentFraction = 0.7

// Evidence is a single append-only entry in the reputation ledger.
//
// score_delta is unbounded (positive or negative); the rolling score is
// computed at read time by summing decayed deltas and clamping to [0, 1].
type Evidence struct {
	ID          int64
	ConfigHash  string
	OwnerUserID int64
	TaskDomain  string
	ScoreDelta  float64
	Weight      float64
	EvidenceRef string
	CreatedAt   time.Time
}

// Ledger is the new dynamic-spawning reputation store. It is fully separate
// from the legacy *Service / Store types: this one is keyed by config_hash
// and is append-only, while the legacy store is keyed by agent name and
// performs in-place upserts.
type Ledger struct {
	db *sql.DB
}

// NewLedger constructs a Ledger backed by the given *sql.DB. The caller is
// responsible for migration ordering — migration 023 must already be applied.
func NewLedger(db *sql.DB) *Ledger {
	return &Ledger{db: db}
}

// Append writes one evidence row and returns its row id.
//
// The CreatedAt field, if zero, defaults to the database's CURRENT_TIMESTAMP.
// TaskDomain defaults to "default" and Weight defaults to 1.0.
func (l *Ledger) Append(ctx context.Context, ev Evidence) (int64, error) {
	if ev.TaskDomain == "" {
		ev.TaskDomain = "default"
	}
	if ev.Weight == 0 {
		ev.Weight = 1.0
	}

	var (
		res sql.Result
		err error
	)
	if ev.CreatedAt.IsZero() {
		res, err = l.db.ExecContext(ctx,
			`INSERT INTO reputation_evidence
			   (config_hash, owner_user_id, task_domain, score_delta, evidence_ref, weight)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			ev.ConfigHash, ev.OwnerUserID, ev.TaskDomain, ev.ScoreDelta, ev.EvidenceRef, ev.Weight,
		)
	} else {
		res, err = l.db.ExecContext(ctx,
			`INSERT INTO reputation_evidence
			   (config_hash, owner_user_id, task_domain, score_delta, evidence_ref, weight, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ev.ConfigHash, ev.OwnerUserID, ev.TaskDomain, ev.ScoreDelta, ev.EvidenceRef, ev.Weight,
			ev.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
		)
	}
	if err != nil {
		return 0, fmt.Errorf("append evidence: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get last insert id: %w", err)
	}
	return id, nil
}

// RollingScore returns the time-decayed rolling reputation score for a
// (config_hash, task_domain) pair, clamped to [0.0, 1.0], plus the count of
// evidence rows considered.
//
// halfLifeDays controls how quickly old evidence loses weight. Pass 0 for the
// 30-day default.
//
// When no evidence exists, the result is (neutralScore, 0, nil).
func (l *Ledger) RollingScore(ctx context.Context, configHash, taskDomain string, halfLifeDays float64) (float64, int, error) {
	if halfLifeDays <= 0 {
		halfLifeDays = defaultHalfLifeDays
	}
	if taskDomain == "" {
		taskDomain = "default"
	}

	const q = `
		SELECT COALESCE(SUM(score_delta * weight *
			exp(-0.6931471805599453 * (julianday('now') - julianday(created_at)) / ?)), 0.5) AS score,
		COUNT(*) AS cnt
		FROM reputation_evidence
		WHERE config_hash = ? AND task_domain = ?`

	var (
		score float64
		cnt   int
	)
	if err := l.db.QueryRowContext(ctx, q, halfLifeDays, configHash, taskDomain).Scan(&score, &cnt); err != nil {
		return 0, 0, fmt.Errorf("rolling score query: %w", err)
	}

	if cnt == 0 {
		return neutralScore, 0, nil
	}

	if score < 0.0 {
		score = 0.0
	}
	if score > 1.0 {
		score = 1.0
	}
	return score, cnt, nil
}

// SeedFromParent reads the parent config's current rolling score and writes
// a single seed evidence row for the child config at
// seedFromParentFraction × parent_score.
//
// Used when a new agent config is spawned: rather than starting at the neutral
// 0.5, the child inherits 70% of the parent's reputation as a starting prior.
func (l *Ledger) SeedFromParent(ctx context.Context, parentHash, childHash string, ownerID int64, taskDomain string, halfLifeDays float64) error {
	parentScore, _, err := l.RollingScore(ctx, parentHash, taskDomain, halfLifeDays)
	if err != nil {
		return fmt.Errorf("read parent score: %w", err)
	}

	seedDelta := seedFromParentFraction * parentScore
	_, err = l.Append(ctx, Evidence{
		ConfigHash:  childHash,
		OwnerUserID: ownerID,
		TaskDomain:  taskDomain,
		ScoreDelta:  seedDelta,
		Weight:      1.0,
		EvidenceRef: fmt.Sprintf("seed_from_parent:%s", parentHash),
	})
	if err != nil {
		return fmt.Errorf("write seed evidence: %w", err)
	}
	return nil
}
