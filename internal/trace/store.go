package trace

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// StoredTrace represents a trace entry read from the database (legacy type).
type StoredTrace struct {
	ID        int64
	AgentName string
	Action    string
	Details   string
	Error     sql.NullString
	CreatedAt time.Time
}

// TraceStore defines the interface for reading and managing trace entries.
type TraceStore interface {
	// Legacy methods (kept for backward compatibility)
	GetTraces(ctx context.Context, agentName string, limit int) ([]*StoredTrace, error)
	GetTracesByAction(ctx context.Context, action string, limit int) ([]*StoredTrace, error)

	// Enhanced query methods
	Insert(ctx context.Context, t *Trace) error
	Query(ctx context.Context, f TraceFilter) ([]Trace, int, error)
	QueryStream(ctx context.Context, f TraceFilter, fn func(Trace) error) error
	CountByAction(ctx context.Context, ownerID string) (map[string]int64, error)
	DeleteOlderThan(ctx context.Context, before time.Time) (int64, error)
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

	return scanStoredTraces(rows)
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

	return scanStoredTraces(rows)
}

// Insert adds a single trace entry to the database.
func (s *SQLiteTraceStore) Insert(ctx context.Context, t *Trace) error {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO traces (owner_id, agent_name, action, details, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		t.OwnerID, t.AgentName, t.Action, string(t.Details), nullString(t.Error), t.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("insert trace: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get trace id: %w", err)
	}
	t.ID = id
	return nil
}

// Query returns traces matching the filter, along with the total count of matching records.
func (s *SQLiteTraceStore) Query(ctx context.Context, f TraceFilter) ([]Trace, int, error) {
	f.Normalize()

	where, args := buildWhereClause(f)

	// Get total count
	countQuery := "SELECT COUNT(*) FROM traces" + where
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count traces: %w", err)
	}

	// Get paginated results
	query := "SELECT id, owner_id, agent_name, action, details, error, created_at FROM traces" +
		where + " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, f.PageSize, f.Offset())

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query traces: %w", err)
	}
	defer rows.Close()

	traces, err := scanNewTraces(rows)
	if err != nil {
		return nil, 0, err
	}

	return traces, total, nil
}

// QueryStream iterates over matching traces and calls fn for each one, without loading all into memory.
func (s *SQLiteTraceStore) QueryStream(ctx context.Context, f TraceFilter, fn func(Trace) error) error {
	// For streaming, remove pagination — stream all matching rows
	f.Page = 0
	f.PageSize = 0

	where, args := buildWhereClause(f)

	query := "SELECT id, owner_id, agent_name, action, details, error, created_at FROM traces" +
		where + " ORDER BY created_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("stream traces: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var t Trace
		var details string
		var errStr sql.NullString
		if err := rows.Scan(&t.ID, &t.OwnerID, &t.AgentName, &t.Action, &details, &errStr, &t.Timestamp); err != nil {
			return fmt.Errorf("scan trace: %w", err)
		}
		t.Details = json.RawMessage(details)
		if errStr.Valid {
			t.Error = errStr.String
		}
		if err := fn(t); err != nil {
			return err
		}
	}
	return rows.Err()
}

// CountByAction returns the count of traces grouped by action type for a given owner.
func (s *SQLiteTraceStore) CountByAction(ctx context.Context, ownerID string) (map[string]int64, error) {
	query := "SELECT action, COUNT(*) FROM traces WHERE owner_id = ? GROUP BY action ORDER BY COUNT(*) DESC"
	rows, err := s.db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, fmt.Errorf("count by action: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int64)
	for rows.Next() {
		var action string
		var count int64
		if err := rows.Scan(&action, &count); err != nil {
			return nil, err
		}
		counts[action] = count
	}
	return counts, rows.Err()
}

// DeleteOlderThan removes traces older than the given time and returns the count of deleted rows.
func (s *SQLiteTraceStore) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM traces WHERE created_at < ?", before,
	)
	if err != nil {
		return 0, fmt.Errorf("delete old traces: %w", err)
	}
	return result.RowsAffected()
}

// buildWhereClause constructs the WHERE clause and args from a TraceFilter.
func buildWhereClause(f TraceFilter) (string, []any) {
	var conditions []string
	var args []any

	if f.OwnerID != "" {
		conditions = append(conditions, "owner_id = ?")
		args = append(args, f.OwnerID)
	}
	if f.AgentName != "" {
		conditions = append(conditions, "agent_name = ?")
		args = append(args, f.AgentName)
	}
	if f.Action != "" {
		conditions = append(conditions, "action = ?")
		args = append(args, f.Action)
	}
	if f.Since != nil {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, *f.Since)
	}
	if f.Until != nil {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, *f.Until)
	}

	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func scanStoredTraces(rows *sql.Rows) ([]*StoredTrace, error) {
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

func scanNewTraces(rows *sql.Rows) ([]Trace, error) {
	var traces []Trace
	for rows.Next() {
		var t Trace
		var details string
		var errStr sql.NullString
		if err := rows.Scan(&t.ID, &t.OwnerID, &t.AgentName, &t.Action, &details, &errStr, &t.Timestamp); err != nil {
			return nil, fmt.Errorf("scan trace: %w", err)
		}
		t.Details = json.RawMessage(details)
		if errStr.Valid {
			t.Error = errStr.String
		}
		traces = append(traces, t)
	}
	if traces == nil {
		traces = []Trace{}
	}
	return traces, rows.Err()
}
