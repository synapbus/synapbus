package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// TaskStore defines the storage interface for task auction operations.
type TaskStore interface {
	CreateTask(ctx context.Context, task *Task) error
	GetTask(ctx context.Context, id int64) (*Task, error)
	ListTasks(ctx context.Context, channelID int64, status string) ([]*Task, error)
	CountTasks(ctx context.Context, channelID int64, status string) (int, error)
	UpdateTaskStatus(ctx context.Context, id int64, status, assignedTo string) error
	CreateBid(ctx context.Context, bid *Bid) error
	GetBids(ctx context.Context, taskID int64) ([]*Bid, error)
	GetBid(ctx context.Context, bidID int64) (*Bid, error)
	UpdateBidStatus(ctx context.Context, bidID int64, status string) error
	ExpireTasks(ctx context.Context) (int, error)
	CancelTasksByChannel(ctx context.Context, channelID int64) (int, error)
}

// SQLiteTaskStore implements TaskStore using SQLite.
type SQLiteTaskStore struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewSQLiteTaskStore creates a new SQLite-backed task store.
func NewSQLiteTaskStore(db *sql.DB) *SQLiteTaskStore {
	return &SQLiteTaskStore{
		db:     db,
		logger: slog.Default().With("component", "task-store"),
	}
}

// sqliteTimeFormat is the format used for storing timestamps consistently in SQLite.
const sqliteTimeFormat = "2006-01-02 15:04:05"

// CreateTask inserts a new task.
func (s *SQLiteTaskStore) CreateTask(ctx context.Context, task *Task) error {
	requirements := string(task.Requirements)
	if requirements == "" {
		requirements = "{}"
	}

	// Format deadline as SQLite-compatible timestamp string
	var deadlineStr *string
	if task.Deadline != nil {
		s := task.Deadline.UTC().Format(sqliteTimeFormat)
		deadlineStr = &s
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (channel_id, posted_by, title, description, requirements, deadline, status, assigned_to, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		task.ChannelID, task.PostedBy, task.Title, task.Description, requirements,
		deadlineStr, task.Status, task.AssignedTo,
	)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get task id: %w", err)
	}
	task.ID = id

	s.logger.Info("task created", "id", id, "title", task.Title, "channel_id", task.ChannelID)
	return nil
}

// GetTask returns a task by ID.
func (s *SQLiteTaskStore) GetTask(ctx context.Context, id int64) (*Task, error) {
	var task Task
	var requirements string
	var deadlineStr sql.NullString
	var assignedTo sql.NullString

	err := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, posted_by, title, description, requirements, deadline, status, assigned_to, created_at, updated_at
		 FROM tasks WHERE id = ?`, id,
	).Scan(&task.ID, &task.ChannelID, &task.PostedBy, &task.Title, &task.Description,
		&requirements, &deadlineStr, &task.Status, &assignedTo, &task.CreatedAt, &task.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task not found: %d", id)
		}
		return nil, fmt.Errorf("get task: %w", err)
	}

	task.Requirements = json.RawMessage(requirements)
	if deadlineStr.Valid {
		t, err := time.Parse(sqliteTimeFormat, deadlineStr.String)
		if err == nil {
			task.Deadline = &t
		}
	}
	if assignedTo.Valid {
		task.AssignedTo = assignedTo.String
	}
	return &task, nil
}

// ListTasks returns tasks for a channel, optionally filtered by status.
func (s *SQLiteTaskStore) ListTasks(ctx context.Context, channelID int64, status string) ([]*Task, error) {
	var query string
	var args []any

	if status != "" {
		query = `SELECT id, channel_id, posted_by, title, description, requirements, deadline, status, assigned_to, created_at, updated_at
				 FROM tasks WHERE channel_id = ? AND status = ? ORDER BY created_at DESC`
		args = []any{channelID, status}
	} else {
		query = `SELECT id, channel_id, posted_by, title, description, requirements, deadline, status, assigned_to, created_at, updated_at
				 FROM tasks WHERE channel_id = ? ORDER BY created_at DESC`
		args = []any{channelID}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	return scanTasks(rows)
}

// CountTasks returns the total number of tasks for a channel, optionally filtered by status.
func (s *SQLiteTaskStore) CountTasks(ctx context.Context, channelID int64, status string) (int, error) {
	var count int
	var err error

	if status != "" {
		err = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM tasks WHERE channel_id = ? AND status = ?`,
			channelID, status,
		).Scan(&count)
	} else {
		err = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM tasks WHERE channel_id = ?`,
			channelID,
		).Scan(&count)
	}
	if err != nil {
		return 0, fmt.Errorf("count tasks: %w", err)
	}
	return count, nil
}

// UpdateTaskStatus updates a task's status and optionally assigned_to.
func (s *SQLiteTaskStore) UpdateTaskStatus(ctx context.Context, id int64, status, assignedTo string) error {
	var result sql.Result
	var err error

	if assignedTo != "" {
		result, err = s.db.ExecContext(ctx,
			`UPDATE tasks SET status = ?, assigned_to = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			status, assignedTo, id)
	} else {
		result, err = s.db.ExecContext(ctx,
			`UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			status, id)
	}
	if err != nil {
		return fmt.Errorf("update task status: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("task not found: %d", id)
	}
	return nil
}

// CreateBid inserts a new bid on a task.
func (s *SQLiteTaskStore) CreateBid(ctx context.Context, bid *Bid) error {
	capabilities := string(bid.Capabilities)
	if capabilities == "" {
		capabilities = "{}"
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO task_bids (task_id, agent_name, capabilities, time_estimate, message, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		bid.TaskID, bid.AgentName, capabilities, bid.TimeEstimate, bid.Message, BidStatusPending,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return fmt.Errorf("agent %s has already bid on task %d", bid.AgentName, bid.TaskID)
		}
		return fmt.Errorf("insert bid: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get bid id: %w", err)
	}
	bid.ID = id
	bid.Status = BidStatusPending

	s.logger.Info("bid created", "id", id, "task_id", bid.TaskID, "agent", bid.AgentName)
	return nil
}

// GetBids returns all bids for a task.
func (s *SQLiteTaskStore) GetBids(ctx context.Context, taskID int64) ([]*Bid, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id, agent_name, capabilities, time_estimate, message, status, created_at
		 FROM task_bids WHERE task_id = ? ORDER BY created_at ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("get bids: %w", err)
	}
	defer rows.Close()

	return scanBids(rows)
}

// GetBid returns a bid by ID.
func (s *SQLiteTaskStore) GetBid(ctx context.Context, bidID int64) (*Bid, error) {
	var bid Bid
	var capabilities string
	var timeEstimate sql.NullString

	err := s.db.QueryRowContext(ctx,
		`SELECT id, task_id, agent_name, capabilities, time_estimate, message, status, created_at
		 FROM task_bids WHERE id = ?`, bidID,
	).Scan(&bid.ID, &bid.TaskID, &bid.AgentName, &capabilities, &timeEstimate, &bid.Message, &bid.Status, &bid.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("bid not found: %d", bidID)
		}
		return nil, fmt.Errorf("get bid: %w", err)
	}

	bid.Capabilities = json.RawMessage(capabilities)
	if timeEstimate.Valid {
		bid.TimeEstimate = timeEstimate.String
	}
	return &bid, nil
}

// UpdateBidStatus updates a bid's status.
func (s *SQLiteTaskStore) UpdateBidStatus(ctx context.Context, bidID int64, status string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE task_bids SET status = ? WHERE id = ?`,
		status, bidID,
	)
	if err != nil {
		return fmt.Errorf("update bid status: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("bid not found: %d", bidID)
	}
	return nil
}

// ExpireTasks marks all open tasks past their deadline as cancelled.
func (s *SQLiteTaskStore) ExpireTasks(ctx context.Context) (int, error) {
	// Use a string-formatted timestamp for consistent SQLite comparison
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	result, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE status = ? AND deadline IS NOT NULL AND deadline < ?`,
		TaskStatusCancelled, TaskStatusOpen, now,
	)
	if err != nil {
		return 0, fmt.Errorf("expire tasks: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	return int(rowsAffected), nil
}

// CancelTasksByChannel cancels all open tasks for a channel (used before channel deletion).
func (s *SQLiteTaskStore) CancelTasksByChannel(ctx context.Context, channelID int64) (int, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE channel_id = ? AND status = ?`,
		TaskStatusCancelled, channelID, TaskStatusOpen,
	)
	if err != nil {
		return 0, fmt.Errorf("cancel tasks by channel: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	return int(rowsAffected), nil
}

// scanTasks scans multiple task rows.
func scanTasks(rows *sql.Rows) ([]*Task, error) {
	var tasks []*Task
	for rows.Next() {
		var task Task
		var requirements string
		var deadlineStr sql.NullString
		var assignedTo sql.NullString

		err := rows.Scan(&task.ID, &task.ChannelID, &task.PostedBy, &task.Title, &task.Description,
			&requirements, &deadlineStr, &task.Status, &assignedTo, &task.CreatedAt, &task.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}

		task.Requirements = json.RawMessage(requirements)
		if deadlineStr.Valid {
			t, err := time.Parse(sqliteTimeFormat, deadlineStr.String)
			if err == nil {
				task.Deadline = &t
			}
		}
		if assignedTo.Valid {
			task.AssignedTo = assignedTo.String
		}
		tasks = append(tasks, &task)
	}
	if tasks == nil {
		tasks = []*Task{}
	}
	return tasks, rows.Err()
}

// scanBids scans multiple bid rows.
func scanBids(rows *sql.Rows) ([]*Bid, error) {
	var bids []*Bid
	for rows.Next() {
		var bid Bid
		var capabilities string
		var timeEstimate sql.NullString

		err := rows.Scan(&bid.ID, &bid.TaskID, &bid.AgentName, &capabilities, &timeEstimate,
			&bid.Message, &bid.Status, &bid.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan bid: %w", err)
		}

		bid.Capabilities = json.RawMessage(capabilities)
		if timeEstimate.Valid {
			bid.TimeEstimate = timeEstimate.String
		}
		bids = append(bids, &bid)
	}
	if bids == nil {
		bids = []*Bid{}
	}
	return bids, rows.Err()
}
