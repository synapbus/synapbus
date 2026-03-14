package k8s

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// K8sHandler represents a registered Kubernetes job handler.
type K8sHandler struct {
	ID              int64             `json:"id"`
	AgentName       string            `json:"agent_name"`
	Image           string            `json:"image"`
	Events          []string          `json:"events"`
	Namespace       string            `json:"namespace"`
	ResourcesMemory string            `json:"resources_memory"`
	ResourcesCPU    string            `json:"resources_cpu"`
	Env             map[string]string `json:"env"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
	Status          string            `json:"status"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// K8sJobRun represents a single Kubernetes job execution.
type K8sJobRun struct {
	ID            int64      `json:"id"`
	HandlerID     int64      `json:"handler_id"`
	AgentName     string     `json:"agent_name"`
	MessageID     int64      `json:"message_id"`
	JobName       string     `json:"job_name"`
	Namespace     string     `json:"namespace"`
	Status        string     `json:"status"`
	FailureReason string     `json:"failure_reason,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// K8sStore defines the storage interface for K8s handler and job run operations.
type K8sStore interface {
	InsertHandler(ctx context.Context, handler *K8sHandler) (int64, error)
	GetHandlerByID(ctx context.Context, id int64) (*K8sHandler, error)
	GetHandlersByAgent(ctx context.Context, agentName string) ([]*K8sHandler, error)
	GetActiveHandlersByEvent(ctx context.Context, agentName string, event string) ([]*K8sHandler, error)
	UpdateHandlerStatus(ctx context.Context, id int64, status string) error
	DeleteHandler(ctx context.Context, id int64, agentName string) error
	CountHandlersByAgent(ctx context.Context, agentName string) (int, error)
	InsertJobRun(ctx context.Context, run *K8sJobRun) (int64, error)
	UpdateJobRunStatus(ctx context.Context, id int64, status string, failureReason string, startedAt *time.Time, completedAt *time.Time) error
	GetJobRunsByHandler(ctx context.Context, handlerID int64, limit int) ([]*K8sJobRun, error)
	GetJobRunsByAgent(ctx context.Context, agentName string, status string, limit int) ([]*K8sJobRun, error)
	GetJobRunByID(ctx context.Context, id int64) (*K8sJobRun, error)
	GetJobRunByJobName(ctx context.Context, jobName string) (*K8sJobRun, error)
}

// SQLiteK8sStore implements K8sStore using SQLite.
type SQLiteK8sStore struct {
	db *sql.DB
}

// NewSQLiteK8sStore creates a new SQLite-backed K8s store.
func NewSQLiteK8sStore(db *sql.DB) *SQLiteK8sStore {
	return &SQLiteK8sStore{db: db}
}

// InsertHandler inserts a new K8s handler and sets the ID on the handler struct.
func (s *SQLiteK8sStore) InsertHandler(ctx context.Context, handler *K8sHandler) (int64, error) {
	eventsJSON, err := json.Marshal(handler.Events)
	if err != nil {
		return 0, fmt.Errorf("marshal events: %w", err)
	}

	envJSON, err := json.Marshal(handler.Env)
	if err != nil {
		return 0, fmt.Errorf("marshal env: %w", err)
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO k8s_handlers (agent_name, image, events, namespace, resources_memory, resources_cpu, env, timeout_seconds, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		handler.AgentName, handler.Image, string(eventsJSON), handler.Namespace,
		handler.ResourcesMemory, handler.ResourcesCPU, string(envJSON),
		handler.TimeoutSeconds, handler.Status,
	)
	if err != nil {
		return 0, fmt.Errorf("insert k8s handler: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get handler id: %w", err)
	}
	handler.ID = id
	return id, nil
}

// GetHandlerByID retrieves a single handler by its ID.
func (s *SQLiteK8sStore) GetHandlerByID(ctx context.Context, id int64) (*K8sHandler, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_name, image, events, namespace, resources_memory, resources_cpu, env, timeout_seconds, status, created_at, updated_at
		 FROM k8s_handlers WHERE id = ?`, id,
	)
	return scanHandler(row)
}

// GetHandlersByAgent retrieves all handlers for the given agent.
func (s *SQLiteK8sStore) GetHandlersByAgent(ctx context.Context, agentName string) ([]*K8sHandler, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_name, image, events, namespace, resources_memory, resources_cpu, env, timeout_seconds, status, created_at, updated_at
		 FROM k8s_handlers WHERE agent_name = ?
		 ORDER BY created_at DESC`, agentName,
	)
	if err != nil {
		return nil, fmt.Errorf("get handlers by agent: %w", err)
	}
	defer rows.Close()

	return scanHandlers(rows)
}

// GetActiveHandlersByEvent retrieves active handlers for the given agent that listen to the specified event.
func (s *SQLiteK8sStore) GetActiveHandlersByEvent(ctx context.Context, agentName string, event string) ([]*K8sHandler, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_name, image, events, namespace, resources_memory, resources_cpu, env, timeout_seconds, status, created_at, updated_at
		 FROM k8s_handlers WHERE agent_name = ? AND status = 'active'
		 ORDER BY created_at DESC`, agentName,
	)
	if err != nil {
		return nil, fmt.Errorf("get active handlers by event: %w", err)
	}
	defer rows.Close()

	var handlers []*K8sHandler
	for rows.Next() {
		h, err := scanHandlerFromRows(rows)
		if err != nil {
			return nil, err
		}
		for _, e := range h.Events {
			if e == event {
				handlers = append(handlers, h)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active handlers: %w", err)
	}
	if handlers == nil {
		handlers = []*K8sHandler{}
	}
	return handlers, nil
}

// UpdateHandlerStatus updates the status of a handler.
func (s *SQLiteK8sStore) UpdateHandlerStatus(ctx context.Context, id int64, status string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE k8s_handlers SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("update handler status: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("handler not found")
	}
	return nil
}

// DeleteHandler deletes a handler only if it is owned by the specified agent.
func (s *SQLiteK8sStore) DeleteHandler(ctx context.Context, id int64, agentName string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM k8s_handlers WHERE id = ? AND agent_name = ?`,
		id, agentName,
	)
	if err != nil {
		return fmt.Errorf("delete handler: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("handler not found or not owned by agent")
	}
	return nil
}

// CountHandlersByAgent returns the number of handlers for the given agent.
func (s *SQLiteK8sStore) CountHandlersByAgent(ctx context.Context, agentName string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM k8s_handlers WHERE agent_name = ?`, agentName,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count handlers by agent: %w", err)
	}
	return count, nil
}

// InsertJobRun inserts a new job run record and sets the ID on the run struct.
func (s *SQLiteK8sStore) InsertJobRun(ctx context.Context, run *K8sJobRun) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO k8s_job_runs (handler_id, agent_name, message_id, job_name, namespace, status, failure_reason, started_at, completed_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		run.HandlerID, run.AgentName, run.MessageID, run.JobName, run.Namespace,
		run.Status, run.FailureReason, run.StartedAt, run.CompletedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("insert job run: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get job run id: %w", err)
	}
	run.ID = id
	return id, nil
}

// UpdateJobRunStatus updates the status and related fields of a job run.
func (s *SQLiteK8sStore) UpdateJobRunStatus(ctx context.Context, id int64, status string, failureReason string, startedAt *time.Time, completedAt *time.Time) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE k8s_job_runs SET status = ?, failure_reason = ?, started_at = ?, completed_at = ? WHERE id = ?`,
		status, failureReason, startedAt, completedAt, id,
	)
	if err != nil {
		return fmt.Errorf("update job run status: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("job run not found")
	}
	return nil
}

// GetJobRunsByHandler retrieves job runs for a specific handler, ordered by most recent first.
func (s *SQLiteK8sStore) GetJobRunsByHandler(ctx context.Context, handlerID int64, limit int) ([]*K8sJobRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, handler_id, agent_name, message_id, job_name, namespace, status, failure_reason, started_at, completed_at, created_at
		 FROM k8s_job_runs WHERE handler_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`, handlerID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get job runs by handler: %w", err)
	}
	defer rows.Close()

	return scanJobRuns(rows)
}

// GetJobRunsByAgent retrieves job runs for an agent, optionally filtered by status.
func (s *SQLiteK8sStore) GetJobRunsByAgent(ctx context.Context, agentName string, status string, limit int) ([]*K8sJobRun, error) {
	if limit <= 0 {
		limit = 50
	}

	var query string
	var args []any

	if status != "" {
		query = `SELECT id, handler_id, agent_name, message_id, job_name, namespace, status, failure_reason, started_at, completed_at, created_at
			 FROM k8s_job_runs WHERE agent_name = ? AND status = ?
			 ORDER BY created_at DESC
			 LIMIT ?`
		args = []any{agentName, status, limit}
	} else {
		query = `SELECT id, handler_id, agent_name, message_id, job_name, namespace, status, failure_reason, started_at, completed_at, created_at
			 FROM k8s_job_runs WHERE agent_name = ?
			 ORDER BY created_at DESC
			 LIMIT ?`
		args = []any{agentName, limit}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get job runs by agent: %w", err)
	}
	defer rows.Close()

	return scanJobRuns(rows)
}

// GetJobRunByID retrieves a single job run by its database ID.
func (s *SQLiteK8sStore) GetJobRunByID(ctx context.Context, id int64) (*K8sJobRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, handler_id, agent_name, message_id, job_name, namespace, status, failure_reason, started_at, completed_at, created_at
		 FROM k8s_job_runs WHERE id = ?`, id,
	)
	return scanJobRun(row)
}

// GetJobRunByJobName retrieves a single job run by its Kubernetes job name.
func (s *SQLiteK8sStore) GetJobRunByJobName(ctx context.Context, jobName string) (*K8sJobRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, handler_id, agent_name, message_id, job_name, namespace, status, failure_reason, started_at, completed_at, created_at
		 FROM k8s_job_runs WHERE job_name = ?`, jobName,
	)
	return scanJobRun(row)
}

// scanHandler scans a single handler from sql.Row.
func scanHandler(row *sql.Row) (*K8sHandler, error) {
	var h K8sHandler
	var eventsJSON, envJSON string

	err := row.Scan(
		&h.ID, &h.AgentName, &h.Image, &eventsJSON, &h.Namespace,
		&h.ResourcesMemory, &h.ResourcesCPU, &envJSON, &h.TimeoutSeconds,
		&h.Status, &h.CreatedAt, &h.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(eventsJSON), &h.Events); err != nil {
		return nil, fmt.Errorf("unmarshal events: %w", err)
	}
	if h.Events == nil {
		h.Events = []string{}
	}

	if err := json.Unmarshal([]byte(envJSON), &h.Env); err != nil {
		return nil, fmt.Errorf("unmarshal env: %w", err)
	}
	if h.Env == nil {
		h.Env = map[string]string{}
	}

	return &h, nil
}

// scanHandlerFromRows scans a single handler from sql.Rows.
func scanHandlerFromRows(rows *sql.Rows) (*K8sHandler, error) {
	var h K8sHandler
	var eventsJSON, envJSON string

	err := rows.Scan(
		&h.ID, &h.AgentName, &h.Image, &eventsJSON, &h.Namespace,
		&h.ResourcesMemory, &h.ResourcesCPU, &envJSON, &h.TimeoutSeconds,
		&h.Status, &h.CreatedAt, &h.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan handler: %w", err)
	}

	if err := json.Unmarshal([]byte(eventsJSON), &h.Events); err != nil {
		return nil, fmt.Errorf("unmarshal events: %w", err)
	}
	if h.Events == nil {
		h.Events = []string{}
	}

	if err := json.Unmarshal([]byte(envJSON), &h.Env); err != nil {
		return nil, fmt.Errorf("unmarshal env: %w", err)
	}
	if h.Env == nil {
		h.Env = map[string]string{}
	}

	return &h, nil
}

// scanHandlers scans multiple handler rows.
func scanHandlers(rows *sql.Rows) ([]*K8sHandler, error) {
	var handlers []*K8sHandler
	for rows.Next() {
		h, err := scanHandlerFromRows(rows)
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate handlers: %w", err)
	}
	if handlers == nil {
		handlers = []*K8sHandler{}
	}
	return handlers, nil
}

// scanJobRun scans a single job run from sql.Row.
func scanJobRun(row *sql.Row) (*K8sJobRun, error) {
	var run K8sJobRun
	var failureReason sql.NullString
	var startedAt, completedAt sql.NullTime

	err := row.Scan(
		&run.ID, &run.HandlerID, &run.AgentName, &run.MessageID, &run.JobName,
		&run.Namespace, &run.Status, &failureReason, &startedAt, &completedAt,
		&run.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if failureReason.Valid {
		run.FailureReason = failureReason.String
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}

	return &run, nil
}

// scanJobRunFromRows scans a single job run from sql.Rows.
func scanJobRunFromRows(rows *sql.Rows) (*K8sJobRun, error) {
	var run K8sJobRun
	var failureReason sql.NullString
	var startedAt, completedAt sql.NullTime

	err := rows.Scan(
		&run.ID, &run.HandlerID, &run.AgentName, &run.MessageID, &run.JobName,
		&run.Namespace, &run.Status, &failureReason, &startedAt, &completedAt,
		&run.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan job run: %w", err)
	}

	if failureReason.Valid {
		run.FailureReason = failureReason.String
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}

	return &run, nil
}

// scanJobRuns scans multiple job run rows.
func scanJobRuns(rows *sql.Rows) ([]*K8sJobRun, error) {
	var runs []*K8sJobRun
	for rows.Next() {
		run, err := scanJobRunFromRows(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job runs: %w", err)
	}
	if runs == nil {
		runs = []*K8sJobRun{}
	}
	return runs, nil
}
