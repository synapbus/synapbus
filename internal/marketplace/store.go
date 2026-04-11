// Package marketplace implements the agent marketplace MVP (spec 016):
// capability manifests (via wiki), auction channels (via existing swarm
// service), and the domain-scoped reputation ledger.
package marketplace

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ReputationEntry is one row in the reputation ledger — a single
// (agent, domain) outcome from a completed auction task (FR-012).
type ReputationEntry struct {
	ID               int64     `json:"id"`
	AgentName        string    `json:"agent_name"`
	Domain           string    `json:"domain"`
	TaskID           int64     `json:"task_id,omitempty"`
	EstimatedTokens  int64     `json:"estimated_tokens"`
	ActualTokens     int64     `json:"actual_tokens"`
	SuccessScore     float64   `json:"success_score"`
	DifficultyWeight float64   `json:"difficulty_weight"`
	CompletedAt      time.Time `json:"completed_at"`
}

// ReputationSummary aggregates entries for one (agent, domain) pair.
type ReputationSummary struct {
	AgentName            string  `json:"agent_name"`
	Domain               string  `json:"domain"`
	TasksCompleted       int     `json:"tasks_completed"`
	AvgSuccessScore      float64 `json:"avg_success_score"`
	WeightedSuccessScore float64 `json:"weighted_success_score"`
	AvgEstimatedTokens   int64   `json:"avg_estimated_tokens"`
	AvgActualTokens      int64   `json:"avg_actual_tokens"`
}

// Store persists reputation entries.
type Store struct {
	db *sql.DB
}

// NewStore creates a reputation store backed by the given SQLite handle.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// RecordEntry inserts one reputation row for (agent, domain) after a task completes.
func (s *Store) RecordEntry(ctx context.Context, e *ReputationEntry) error {
	if e.AgentName == "" {
		return fmt.Errorf("agent_name is required")
	}
	if e.Domain == "" {
		return fmt.Errorf("domain is required")
	}
	if e.DifficultyWeight == 0 {
		e.DifficultyWeight = 1.0
	}

	var taskID any
	if e.TaskID > 0 {
		taskID = e.TaskID
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_reputation
		    (agent_name, domain, task_id, estimated_tokens, actual_tokens,
		     success_score, difficulty_weight, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		e.AgentName, e.Domain, taskID,
		e.EstimatedTokens, e.ActualTokens,
		e.SuccessScore, e.DifficultyWeight,
	)
	if err != nil {
		return fmt.Errorf("insert reputation entry: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("get reputation entry id: %w", err)
	}
	e.ID = id
	return nil
}

// ListEntries returns raw entries for (agent, domain) ordered by newest first.
// If domain is empty, all domains are returned. If agent is empty, the query is
// rejected — reputation is always scoped to an agent (FR-013).
func (s *Store) ListEntries(ctx context.Context, agent, domain string, limit int) ([]*ReputationEntry, error) {
	if agent == "" {
		return nil, fmt.Errorf("agent is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var rows *sql.Rows
	var err error
	if domain == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, agent_name, domain, COALESCE(task_id,0),
			        estimated_tokens, actual_tokens, success_score,
			        difficulty_weight, completed_at
			   FROM agent_reputation
			  WHERE agent_name = ?
			  ORDER BY completed_at DESC
			  LIMIT ?`,
			agent, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, agent_name, domain, COALESCE(task_id,0),
			        estimated_tokens, actual_tokens, success_score,
			        difficulty_weight, completed_at
			   FROM agent_reputation
			  WHERE agent_name = ? AND domain = ?
			  ORDER BY completed_at DESC
			  LIMIT ?`,
			agent, domain, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list reputation: %w", err)
	}
	defer rows.Close()

	var out []*ReputationEntry
	for rows.Next() {
		var e ReputationEntry
		if err := rows.Scan(&e.ID, &e.AgentName, &e.Domain, &e.TaskID,
			&e.EstimatedTokens, &e.ActualTokens, &e.SuccessScore,
			&e.DifficultyWeight, &e.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan reputation row: %w", err)
		}
		out = append(out, &e)
	}
	if out == nil {
		out = []*ReputationEntry{}
	}
	return out, rows.Err()
}

// Summary computes aggregates for (agent, domain). Returns zero-values if
// there are no entries, and a count of 0.
func (s *Store) Summary(ctx context.Context, agent, domain string) (*ReputationSummary, error) {
	if agent == "" {
		return nil, fmt.Errorf("agent is required")
	}
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}

	var (
		count            int
		sumSuccess       sql.NullFloat64
		sumWeightedScore sql.NullFloat64
		sumWeights       sql.NullFloat64
		sumEstimated     sql.NullFloat64
		sumActual        sql.NullFloat64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT
		    COUNT(*),
		    SUM(success_score),
		    SUM(success_score * difficulty_weight),
		    SUM(difficulty_weight),
		    SUM(estimated_tokens),
		    SUM(actual_tokens)
		 FROM agent_reputation
		 WHERE agent_name = ? AND domain = ?`,
		agent, domain,
	).Scan(&count, &sumSuccess, &sumWeightedScore, &sumWeights, &sumEstimated, &sumActual)
	if err != nil {
		return nil, fmt.Errorf("reputation summary: %w", err)
	}

	sum := &ReputationSummary{
		AgentName:      agent,
		Domain:         domain,
		TasksCompleted: count,
	}
	if count > 0 {
		if sumSuccess.Valid {
			sum.AvgSuccessScore = sumSuccess.Float64 / float64(count)
		}
		if sumWeights.Valid && sumWeights.Float64 > 0 && sumWeightedScore.Valid {
			sum.WeightedSuccessScore = sumWeightedScore.Float64 / sumWeights.Float64
		}
		if sumEstimated.Valid {
			sum.AvgEstimatedTokens = int64(sumEstimated.Float64 / float64(count))
		}
		if sumActual.Valid {
			sum.AvgActualTokens = int64(sumActual.Float64 / float64(count))
		}
	}
	return sum, nil
}
