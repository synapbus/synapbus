package trust

import (
	"context"
	"database/sql"
	"fmt"
)

// Store defines the storage interface for trust scores.
type Store interface {
	GetScore(ctx context.Context, agentName, actionType string) (*TrustScore, error)
	GetAllScores(ctx context.Context, agentName string) ([]*TrustScore, error)
	UpsertScore(ctx context.Context, agentName, actionType string, delta float64) (*TrustScore, error)
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite-backed trust store.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) GetScore(ctx context.Context, agentName, actionType string) (*TrustScore, error) {
	var ts TrustScore
	var lastAdj sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id, agent_name, action_type, score, adjustments_count, last_adjusted_at, created_at
		 FROM agent_trust WHERE agent_name = ? AND action_type = ?`,
		agentName, actionType,
	).Scan(&ts.ID, &ts.AgentName, &ts.ActionType, &ts.Score, &ts.AdjustmentsCount, &lastAdj, &ts.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return &TrustScore{AgentName: agentName, ActionType: actionType, Score: 0.0}, nil
		}
		return nil, fmt.Errorf("get trust score: %w", err)
	}
	if lastAdj.Valid {
		ts.LastAdjustedAt = &lastAdj.Time
	}
	return &ts, nil
}

func (s *SQLiteStore) GetAllScores(ctx context.Context, agentName string) ([]*TrustScore, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_name, action_type, score, adjustments_count, last_adjusted_at, created_at
		 FROM agent_trust WHERE agent_name = ?
		 ORDER BY action_type`, agentName,
	)
	if err != nil {
		return nil, fmt.Errorf("get all trust scores: %w", err)
	}
	defer rows.Close()

	var scores []*TrustScore
	for rows.Next() {
		var ts TrustScore
		var lastAdj sql.NullTime
		if err := rows.Scan(&ts.ID, &ts.AgentName, &ts.ActionType, &ts.Score, &ts.AdjustmentsCount, &lastAdj, &ts.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan trust score: %w", err)
		}
		if lastAdj.Valid {
			ts.LastAdjustedAt = &lastAdj.Time
		}
		scores = append(scores, &ts)
	}
	if scores == nil {
		scores = []*TrustScore{}
	}
	return scores, rows.Err()
}

func (s *SQLiteStore) UpsertScore(ctx context.Context, agentName, actionType string, delta float64) (*TrustScore, error) {
	// Upsert: insert if not exists, update if exists
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_trust (agent_name, action_type, score, adjustments_count, last_adjusted_at, created_at)
		 VALUES (?, ?, MAX(0.0, MIN(1.0, ?)), 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT(agent_name, action_type) DO UPDATE SET
			score = MAX(0.0, MIN(1.0, agent_trust.score + ?)),
			adjustments_count = agent_trust.adjustments_count + 1,
			last_adjusted_at = CURRENT_TIMESTAMP`,
		agentName, actionType, delta, delta,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert trust score: %w", err)
	}

	return s.GetScore(ctx, agentName, actionType)
}
