package trace

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// StoredTrace represents a trace entry read from the database.
type StoredTrace struct {
	ID        int64
	AgentName string
	Action    string
	Details   string
	Error     sql.NullString
	CreatedAt time.Time
}

// TraceStore defines the interface for reading trace entries.
type TraceStore interface {
	GetTraces(ctx context.Context, agentName string, limit int) ([]*StoredTrace, error)
	GetTracesByAction(ctx context.Context, action string, limit int) ([]*StoredTrace, error)
}

// SQLiteTraceStore implements TraceStore using SQLite.
type SQLiteTraceStore struct {
	db *sql.DB
}

// NewSQLiteTraceStore creates a new SQLite-backed trace store.
func NewSQLiteTraceStore(db *sql.DB) *SQLiteTraceStore {
	return &SQLiteTraceStore{db: db}
}

func (s *SQLiteTraceStore) GetTraces(ctx context.Context, agentName string, limit int) ([]*StoredTrace, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_name, action, details, error, created_at
		 FROM traces WHERE agent_name = ?
		 ORDER BY created_at DESC LIMIT ?`,
		agentName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query traces: %w", err)
	}
	defer rows.Close()

	return scanTraces(rows)
}

func (s *SQLiteTraceStore) GetTracesByAction(ctx context.Context, action string, limit int) ([]*StoredTrace, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_name, action, details, error, created_at
		 FROM traces WHERE action = ?
		 ORDER BY created_at DESC LIMIT ?`,
		action, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query traces: %w", err)
	}
	defer rows.Close()

	return scanTraces(rows)
}

func scanTraces(rows *sql.Rows) ([]*StoredTrace, error) {
	var traces []*StoredTrace
	for rows.Next() {
		var t StoredTrace
		if err := rows.Scan(&t.ID, &t.AgentName, &t.Action, &t.Details, &t.Error, &t.CreatedAt); err != nil {
			return nil, err
		}
		traces = append(traces, &t)
	}
	if traces == nil {
		traces = []*StoredTrace{}
	}
	return traces, rows.Err()
}
