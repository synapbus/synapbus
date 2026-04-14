package goaltasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Store is the SQLite-backed persistence for goal tasks.
type Store struct {
	db *sql.DB
}

// NewStore constructs a Store from a database handle.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB exposes the underlying handle for transactions.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Insert writes a single task row. The caller is responsible for
// providing a valid ancestry and depth.
func (s *Store) Insert(ctx context.Context, tx *sql.Tx, t *Task) (int64, error) {
	ancestryJSON, err := marshalAncestry(t.Ancestry)
	if err != nil {
		return 0, err
	}
	var verifierJSON, heartbeatJSON sql.NullString
	if t.VerifierConfig != nil {
		b, err := json.Marshal(t.VerifierConfig)
		if err != nil {
			return 0, err
		}
		verifierJSON = sql.NullString{String: string(b), Valid: true}
	}
	if t.HeartbeatConfig != nil {
		b, err := json.Marshal(t.HeartbeatConfig)
		if err != nil {
			return 0, err
		}
		heartbeatJSON = sql.NullString{String: string(b), Valid: true}
	}

	const q = `
		INSERT INTO goal_tasks
			(goal_id, parent_task_id, ancestry_json, depth, title, description, acceptance_criteria,
			 created_by_agent_id, created_by_user_id, assignee_agent_id, status,
			 billing_code, budget_tokens, budget_dollars_cents,
			 heartbeat_config_json, verifier_config_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var res sql.Result
	if tx != nil {
		res, err = tx.ExecContext(ctx, q,
			t.GoalID, t.ParentTaskID, ancestryJSON, t.Depth, t.Title, t.Description, t.AcceptanceCriteria,
			t.CreatedByAgentID, t.CreatedByUserID, t.AssigneeAgentID, t.Status,
			nullableString(t.BillingCode), t.BudgetTokens, t.BudgetDollarsCents,
			heartbeatJSON, verifierJSON)
	} else {
		res, err = s.db.ExecContext(ctx, q,
			t.GoalID, t.ParentTaskID, ancestryJSON, t.Depth, t.Title, t.Description, t.AcceptanceCriteria,
			t.CreatedByAgentID, t.CreatedByUserID, t.AssigneeAgentID, t.Status,
			nullableString(t.BillingCode), t.BudgetTokens, t.BudgetDollarsCents,
			heartbeatJSON, verifierJSON)
	}
	if err != nil {
		return 0, fmt.Errorf("insert goal_task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	t.ID = id
	return id, nil
}

// Get fetches a single task by id.
func (s *Store) Get(ctx context.Context, id int64) (*Task, error) {
	return s.getOne(ctx, `SELECT `+cols+` FROM goal_tasks WHERE id = ?`, id)
}

// ListByGoal returns all tasks under a goal in insertion order.
func (s *Store) ListByGoal(ctx context.Context, goalID int64) ([]*Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM goal_tasks WHERE goal_id = ? ORDER BY id`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ClaimAtomic performs the optimistic-lock claim — the core concurrency
// primitive. Returns ErrAlreadyClaimed if the task is not in state
// `approved` and unassigned.
func (s *Store) ClaimAtomic(ctx context.Context, taskID, agentID int64, claimMessageID *int64) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE goal_tasks
		   SET assignee_agent_id = ?,
		       status            = ?,
		       claimed_at        = ?,
		       claim_message_id  = ?
		 WHERE id                = ?
		   AND assignee_agent_id IS NULL
		   AND status            = ?`,
		agentID, StatusClaimed, now, claimMessageID, taskID, StatusApproved)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAlreadyClaimed
	}
	return nil
}

// TransitionStatus unconditionally moves a task to a new status. The
// service layer is responsible for legality checks before calling this.
func (s *Store) TransitionStatus(ctx context.Context, taskID int64, newStatus string, extras Extras) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE goal_tasks
		   SET status               = ?,
		       started_at           = COALESCE(started_at, CASE WHEN ? = 'in_progress' THEN ? ELSE NULL END),
		       completed_at         = CASE WHEN ? IN ('done','failed','cancelled') THEN ? ELSE completed_at END,
		       failure_reason       = COALESCE(?, failure_reason),
		       completion_message_id = COALESCE(?, completion_message_id)
		 WHERE id                   = ?`,
		newStatus, newStatus, now, newStatus, now,
		nullableString(extras.FailureReason),
		extras.CompletionMessageID,
		taskID)
	return err
}

// AddSpend increments a leaf task's spend counters after a harness run.
func (s *Store) AddSpend(ctx context.Context, taskID int64, tokens, dollarsCents int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE goal_tasks
		    SET spent_tokens        = spent_tokens + ?,
		        spent_dollars_cents = spent_dollars_cents + ?
		  WHERE id = ?`, tokens, dollarsCents, taskID)
	return err
}

// RollupCosts returns the total spend under a task subtree (inclusive).
func (s *Store) RollupCosts(ctx context.Context, rootTaskID int64) (tokens, dollarsCents int64, count int, err error) {
	row := s.db.QueryRowContext(ctx, `
		WITH RECURSIVE subtree(id) AS (
		    SELECT id FROM goal_tasks WHERE id = ?
		    UNION ALL
		    SELECT t.id FROM goal_tasks t
		    JOIN subtree s ON t.parent_task_id = s.id
		)
		SELECT COALESCE(SUM(spent_tokens), 0),
		       COALESCE(SUM(spent_dollars_cents), 0),
		       COUNT(*)
		  FROM goal_tasks WHERE id IN subtree`, rootTaskID)
	err = row.Scan(&tokens, &dollarsCents, &count)
	return
}

// RollupByBillingCode returns spend grouped by billing code within a subtree.
func (s *Store) RollupByBillingCode(ctx context.Context, rootTaskID int64) (map[string]Spend, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE subtree(id) AS (
		    SELECT id FROM goal_tasks WHERE id = ?
		    UNION ALL
		    SELECT t.id FROM goal_tasks t
		    JOIN subtree s ON t.parent_task_id = s.id
		)
		SELECT COALESCE(billing_code, ''), SUM(spent_tokens), SUM(spent_dollars_cents)
		  FROM goal_tasks WHERE id IN subtree
		 GROUP BY billing_code`, rootTaskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Spend{}
	for rows.Next() {
		var code string
		var tokens, dollars int64
		if err := rows.Scan(&code, &tokens, &dollars); err != nil {
			return nil, err
		}
		out[code] = Spend{Tokens: tokens, DollarsCents: dollars}
	}
	return out, rows.Err()
}

// Extras carries optional fields for TransitionStatus.
type Extras struct {
	FailureReason       string
	CompletionMessageID *int64
}

// Spend is a tokens+dollars pair for rollups.
type Spend struct {
	Tokens       int64
	DollarsCents int64
}

// --- internal helpers ---

const cols = `id, goal_id, parent_task_id, ancestry_json, depth, title, description, acceptance_criteria,
	created_by_agent_id, created_by_user_id, assignee_agent_id, status,
	billing_code, budget_tokens, budget_dollars_cents, spent_tokens, spent_dollars_cents,
	heartbeat_config_json, verifier_config_json,
	origin_message_id, claim_message_id, completion_message_id, failure_reason,
	created_at, approved_at, claimed_at, started_at, completed_at`

type rowLike interface {
	Scan(dest ...any) error
}

func (s *Store) getOne(ctx context.Context, q string, args ...any) (*Task, error) {
	row := s.db.QueryRowContext(ctx, q, args...)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	return t, err
}

func scanTask(r rowLike) (*Task, error) {
	t := &Task{}
	var (
		billing       sql.NullString
		ancestry      string
		verifierJSON  sql.NullString
		heartbeatJSON sql.NullString
		failureReason sql.NullString
	)
	err := r.Scan(
		&t.ID, &t.GoalID, &t.ParentTaskID, &ancestry, &t.Depth, &t.Title, &t.Description, &t.AcceptanceCriteria,
		&t.CreatedByAgentID, &t.CreatedByUserID, &t.AssigneeAgentID, &t.Status,
		&billing, &t.BudgetTokens, &t.BudgetDollarsCents, &t.SpentTokens, &t.SpentDollarsCents,
		&heartbeatJSON, &verifierJSON,
		&t.OriginMessageID, &t.ClaimMessageID, &t.CompletionMessageID, &failureReason,
		&t.CreatedAt, &t.ApprovedAt, &t.ClaimedAt, &t.StartedAt, &t.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	if billing.Valid {
		t.BillingCode = billing.String
	}
	if failureReason.Valid {
		t.FailureReason = failureReason.String
	}
	if heartbeatJSON.Valid && heartbeatJSON.String != "" {
		hc := &HeartbeatConfig{}
		if err := json.Unmarshal([]byte(heartbeatJSON.String), hc); err == nil {
			t.HeartbeatConfig = hc
		}
	}
	if verifierJSON.Valid && verifierJSON.String != "" {
		vc := &VerifierConfig{}
		if err := json.Unmarshal([]byte(verifierJSON.String), vc); err == nil {
			t.VerifierConfig = vc
		}
	}
	nodes, err := unmarshalAncestry(ancestry)
	if err != nil {
		return nil, err
	}
	t.Ancestry = nodes
	return t, nil
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
