// Consolidation-jobs store for feature 020 — wraps the
// `memory_consolidation_jobs` table (data-model.md). Each row is one
// dream-worker dispatch: state machine `pending → dispatched → running
// → {succeeded|partial|failed|expired}`. The partial-unique index
// `idx_consolidation_in_flight(owner_id, job_type) WHERE status IN
// ('pending','dispatched','running')` guarantees at most one in-flight
// row per (owner, job_type). Create() surfaces conflicts as
// ErrJobAlreadyInFlight so the worker can skip the dispatch cleanly.
package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Memory consolidation job types.
const (
	JobTypeReflection         = "reflection"
	JobTypeCoreRewrite        = "core_rewrite"
	JobTypeDedupContradiction = "dedup_contradiction"
	JobTypeLinkGen            = "link_gen"
)

// Memory consolidation job statuses.
const (
	JobStatusPending    = "pending"
	JobStatusDispatched = "dispatched"
	JobStatusRunning    = "running"
	JobStatusSucceeded  = "succeeded"
	JobStatusPartial    = "partial"
	JobStatusFailed     = "failed"
	JobStatusExpired    = "expired"
	// JobStatusCircuitBroken is recorded when the ConsolidatorWorker
	// declines to dispatch because the per-(owner, day) usage gate
	// fired. The job row is created so the audit log shows the
	// attempt, then Complete()d immediately with this status.
	JobStatusCircuitBroken = "circuit_broken"
)

// ErrJobAlreadyInFlight is returned by JobsStore.Create when the
// partial-unique index trips because another job of the same type is
// already pending / dispatched / running for the same owner.
var ErrJobAlreadyInFlight = errors.New("consolidation job already in flight for (owner, job_type)")

// Job is one row in `memory_consolidation_jobs`.
type Job struct {
	ID            int64            `json:"id"`
	OwnerID       string           `json:"owner_id"`
	JobType       string           `json:"job_type"`
	Status        string           `json:"status"`
	TriggerReason string           `json:"trigger_reason"`
	DispatchToken string           `json:"dispatch_token,omitempty"`
	HarnessRunID  string           `json:"harness_run_id,omitempty"`
	Actions       []map[string]any `json:"actions"`
	Summary       string           `json:"summary,omitempty"`
	Error         string           `json:"error,omitempty"`
	LeaseUntil    *time.Time       `json:"lease_until,omitempty"`
	StartedAt     *time.Time       `json:"started_at,omitempty"`
	FinishedAt    *time.Time       `json:"finished_at,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
}

// JobsStore wraps the `memory_consolidation_jobs` table.
type JobsStore struct {
	db *sql.DB
}

// NewJobsStore returns a store rooted at db.
func NewJobsStore(db *sql.DB) *JobsStore {
	return &JobsStore{db: db}
}

// Create inserts a `pending` row for the (owner, jobType) pair. If
// another job of the same type is already in flight, returns
// ErrJobAlreadyInFlight (mapped from the partial-unique-index conflict).
func (s *JobsStore) Create(ctx context.Context, ownerID, jobType, triggerReason string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("jobs store: nil store")
	}
	if ownerID == "" {
		return 0, fmt.Errorf("jobs store: empty owner_id")
	}
	if jobType == "" {
		return 0, fmt.Errorf("jobs store: empty job_type")
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_consolidation_jobs
		   (owner_id, job_type, status, trigger_reason)
		 VALUES (?, ?, 'pending', ?)`,
		ownerID, jobType, triggerReason,
	)
	if err != nil {
		// modernc.org/sqlite surfaces unique-constraint conflicts via
		// error strings; the partial-unique index is the only UNIQUE
		// constraint that can fire here for INSERT.
		if isUniqueConstraint(err) {
			return 0, ErrJobAlreadyInFlight
		}
		return 0, fmt.Errorf("jobs store: insert: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// Dispatch flips a pending row to `dispatched` and stamps the harness
// run id and dispatch token. Returns an error if the row is not in
// `pending` state.
func (s *JobsStore) Dispatch(ctx context.Context, jobID int64, harnessRunID, token string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("jobs store: nil store")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE memory_consolidation_jobs
		    SET status = 'dispatched',
		        harness_run_id = ?,
		        dispatch_token = ?
		  WHERE id = ? AND status = 'pending'`,
		harnessRunID, token, jobID,
	)
	if err != nil {
		return fmt.Errorf("jobs store: dispatch: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("jobs store: dispatch: job %d not in pending state", jobID)
	}
	return nil
}

// Lease flips `dispatched` → `running` and sets lease_until + started_at.
// Called by the worker once the harness has confirmed the run started.
func (s *JobsStore) Lease(ctx context.Context, jobID int64, until time.Time) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("jobs store: nil store")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE memory_consolidation_jobs
		    SET status = 'running',
		        lease_until = ?,
		        started_at = CURRENT_TIMESTAMP
		  WHERE id = ? AND status IN ('dispatched', 'pending')`,
		until.UTC(), jobID,
	)
	if err != nil {
		return fmt.Errorf("jobs store: lease: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("jobs store: lease: job %d not in dispatched/pending state", jobID)
	}
	return nil
}

// AppendAction reads the current `actions` JSON array, appends `action`,
// and writes it back. Wrapped in a single transaction so concurrent
// MCP tool calls within one job serialize cleanly.
func (s *JobsStore) AppendAction(ctx context.Context, jobID int64, action map[string]any) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("jobs store: nil store")
	}
	if action == nil {
		return fmt.Errorf("jobs store: nil action")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("jobs store: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var current string
	if err := tx.QueryRowContext(ctx,
		`SELECT actions FROM memory_consolidation_jobs WHERE id = ?`, jobID,
	).Scan(&current); err != nil {
		return fmt.Errorf("jobs store: read actions: %w", err)
	}

	var arr []map[string]any
	if current == "" || current == "null" {
		arr = []map[string]any{}
	} else if err := json.Unmarshal([]byte(current), &arr); err != nil {
		// Corrupt JSON — start fresh rather than fail forever.
		arr = []map[string]any{}
	}
	arr = append(arr, action)
	b, err := json.Marshal(arr)
	if err != nil {
		return fmt.Errorf("jobs store: marshal actions: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE memory_consolidation_jobs SET actions = ? WHERE id = ?`,
		string(b), jobID,
	); err != nil {
		return fmt.Errorf("jobs store: write actions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("jobs store: commit: %w", err)
	}
	return nil
}

// Complete sets `status`, `summary`, `error`, `finished_at` and clears
// `lease_until`. Idempotent — repeated calls keep the first finished_at.
func (s *JobsStore) Complete(ctx context.Context, jobID int64, status, summary, errMsg string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("jobs store: nil store")
	}
	switch status {
	case JobStatusSucceeded, JobStatusPartial, JobStatusFailed, JobStatusExpired, JobStatusCircuitBroken:
		// ok
	default:
		return fmt.Errorf("jobs store: invalid completion status %q", status)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE memory_consolidation_jobs
		    SET status = ?,
		        summary = COALESCE(NULLIF(?, ''), summary),
		        error = COALESCE(NULLIF(?, ''), error),
		        finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP),
		        lease_until = NULL
		  WHERE id = ?`,
		status, summary, errMsg, jobID,
	)
	if err != nil {
		return fmt.Errorf("jobs store: complete: %w", err)
	}
	return nil
}

// Get returns the row for jobID, or (nil, sql.ErrNoRows).
func (s *JobsStore) Get(ctx context.Context, jobID int64) (*Job, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, owner_id, job_type, status, trigger_reason,
		        COALESCE(dispatch_token, ''),
		        COALESCE(harness_run_id, ''),
		        actions,
		        COALESCE(summary, ''),
		        COALESCE(error, ''),
		        lease_until, started_at, finished_at, created_at
		   FROM memory_consolidation_jobs WHERE id = ?`, jobID,
	)
	return scanJob(row.Scan)
}

// ListRecent returns the most-recent jobs for the given owner.
func (s *JobsStore) ListRecent(ctx context.Context, ownerID string, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, owner_id, job_type, status, trigger_reason,
		        COALESCE(dispatch_token, ''),
		        COALESCE(harness_run_id, ''),
		        actions,
		        COALESCE(summary, ''),
		        COALESCE(error, ''),
		        lease_until, started_at, finished_at, created_at
		   FROM memory_consolidation_jobs
		  WHERE owner_id = ?
		  ORDER BY created_at DESC, id DESC
		  LIMIT ?`, ownerID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("jobs store: list recent: %w", err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jobs store: iterate: %w", err)
	}
	return out, nil
}

// ActiveJob returns the in-flight job for (owner, jobType), if any. nil
// when no row matches.
func (s *JobsStore) ActiveJob(ctx context.Context, ownerID, jobType string) (*Job, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, owner_id, job_type, status, trigger_reason,
		        COALESCE(dispatch_token, ''),
		        COALESCE(harness_run_id, ''),
		        actions,
		        COALESCE(summary, ''),
		        COALESCE(error, ''),
		        lease_until, started_at, finished_at, created_at
		   FROM memory_consolidation_jobs
		  WHERE owner_id = ? AND job_type = ?
		    AND status IN ('pending', 'dispatched', 'running')
		  ORDER BY id DESC LIMIT 1`,
		ownerID, jobType,
	)
	j, err := scanJob(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

type scanFn func(dest ...any) error

func scanJob(scan scanFn) (*Job, error) {
	var (
		j          Job
		actions    string
		leaseUntil sql.NullTime
		startedAt  sql.NullTime
		finishedAt sql.NullTime
	)
	err := scan(
		&j.ID, &j.OwnerID, &j.JobType, &j.Status, &j.TriggerReason,
		&j.DispatchToken, &j.HarnessRunID, &actions,
		&j.Summary, &j.Error,
		&leaseUntil, &startedAt, &finishedAt, &j.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if actions != "" && actions != "null" {
		_ = json.Unmarshal([]byte(actions), &j.Actions)
	}
	if leaseUntil.Valid {
		t := leaseUntil.Time
		j.LeaseUntil = &t
	}
	if startedAt.Valid {
		t := startedAt.Time
		j.StartedAt = &t
	}
	if finishedAt.Valid {
		t := finishedAt.Time
		j.FinishedAt = &t
	}
	return &j, nil
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	// modernc.org/sqlite returns errors whose string contains
	// "constraint failed: UNIQUE" or "SQLITE_CONSTRAINT_UNIQUE". Match
	// loosely so we don't depend on a specific build.
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "UNIQUE")
}
