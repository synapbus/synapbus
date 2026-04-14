package goals

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store is the SQLite-backed persistence for goals.
type Store struct {
	db *sql.DB
}

// NewStore constructs a Store from a database handle.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying handle so the service layer can start its
// own transactions (e.g. when creating a goal + channel atomically).
func (s *Store) DB() *sql.DB {
	return s.db
}

// SlugExists reports whether any goal already has the given slug.
func (s *Store) SlugExists(ctx context.Context, slug string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM goals WHERE slug = ?`, slug).Scan(&n)
	return n > 0, err
}

// Insert writes a new goal row and returns its id. Caller is responsible
// for providing a valid channel_id (auto-created by the service layer).
func (s *Store) Insert(ctx context.Context, g *Goal) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO goals
			(slug, title, description, owner_user_id, channel_id, coordinator_agent_id,
			 status, budget_tokens, budget_dollars_cents, max_spawn_depth)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.Slug, g.Title, g.Description, g.OwnerUserID, g.ChannelID, g.CoordinatorAgentID,
		g.Status, g.BudgetTokens, g.BudgetDollarsCents, g.MaxSpawnDepth)
	if err != nil {
		return 0, fmt.Errorf("insert goal: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	g.ID = id
	return id, nil
}

// Get fetches a single goal by id.
func (s *Store) Get(ctx context.Context, id int64) (*Goal, error) {
	g := &Goal{}
	var alert int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, slug, title, description, owner_user_id, channel_id, coordinator_agent_id,
		       root_task_id, status, budget_tokens, budget_dollars_cents, max_spawn_depth,
		       alert_80pct_posted, created_at, updated_at, completed_at
		FROM goals WHERE id = ?`, id).Scan(
		&g.ID, &g.Slug, &g.Title, &g.Description, &g.OwnerUserID, &g.ChannelID, &g.CoordinatorAgentID,
		&g.RootTaskID, &g.Status, &g.BudgetTokens, &g.BudgetDollarsCents, &g.MaxSpawnDepth,
		&alert, &g.CreatedAt, &g.UpdatedAt, &g.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrGoalNotFound
	}
	if err != nil {
		return nil, err
	}
	g.Alert80PctPosted = alert != 0
	return g, nil
}

// List returns goals optionally filtered by owner.
func (s *Store) List(ctx context.Context, ownerUserID *int64, limit int) ([]*Goal, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
	)
	if ownerUserID != nil {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, slug, title, description, owner_user_id, channel_id, coordinator_agent_id,
			       root_task_id, status, budget_tokens, budget_dollars_cents, max_spawn_depth,
			       alert_80pct_posted, created_at, updated_at, completed_at
			FROM goals WHERE owner_user_id = ? ORDER BY id DESC LIMIT ?`, *ownerUserID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, slug, title, description, owner_user_id, channel_id, coordinator_agent_id,
			       root_task_id, status, budget_tokens, budget_dollars_cents, max_spawn_depth,
			       alert_80pct_posted, created_at, updated_at, completed_at
			FROM goals ORDER BY id DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Goal, 0, limit)
	for rows.Next() {
		g := &Goal{}
		var alert int
		if err := rows.Scan(
			&g.ID, &g.Slug, &g.Title, &g.Description, &g.OwnerUserID, &g.ChannelID, &g.CoordinatorAgentID,
			&g.RootTaskID, &g.Status, &g.BudgetTokens, &g.BudgetDollarsCents, &g.MaxSpawnDepth,
			&alert, &g.CreatedAt, &g.UpdatedAt, &g.CompletedAt,
		); err != nil {
			return nil, err
		}
		g.Alert80PctPosted = alert != 0
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetRootTask updates the goal's root_task_id.
func (s *Store) SetRootTask(ctx context.Context, goalID, rootTaskID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE goals SET root_task_id = ?, updated_at = ? WHERE id = ?`,
		rootTaskID, time.Now().UTC(), goalID)
	return err
}

// SetStatus transitions a goal's status.
func (s *Store) SetStatus(ctx context.Context, goalID int64, newStatus string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE goals SET status = ?, updated_at = ? WHERE id = ?`,
		newStatus, time.Now().UTC(), goalID)
	return err
}

// MarkSoftAlertPosted flips the idempotency flag so the 80 % alert is
// posted only once per goal.
func (s *Store) MarkSoftAlertPosted(ctx context.Context, goalID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE goals SET alert_80pct_posted = 1 WHERE id = ?`, goalID)
	return err
}

// slugify normalizes a title into a URL-safe slug.
func slugify(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_' || r == '/' || r == '.':
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		default:
			// drop
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "goal"
	}
	return out
}

// Sentinel errors.
var ErrGoalNotFound = errors.New("goal not found")
