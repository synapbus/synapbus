// Package goals implements the goal/task tree data model for dynamic
// agent spawning. A goal is a human-owned top-level objective with a
// backing channel and a coordinator agent; it roots a tree of
// goal_tasks assigned to specialist agents.
package goals

import "time"

// GoalStatus values.
const (
	StatusDraft     = "draft"
	StatusActive    = "active"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
	StatusStuck     = "stuck"
)

// Goal is a top-level objective.
type Goal struct {
	ID                 int64
	Slug               string
	Title              string
	Description        string
	OwnerUserID        int64
	ChannelID          int64
	CoordinatorAgentID *int64
	RootTaskID         *int64
	Status             string
	BudgetTokens       *int64
	BudgetDollarsCents *int64
	MaxSpawnDepth      int
	Alert80PctPosted   bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        *time.Time
}

// CreateGoalInput captures the public arguments of CreateGoal.
type CreateGoalInput struct {
	Title              string
	Description        string
	OwnerUserID        int64  // DB foreign key
	OwnerUsername      string // used as created_by for the backing channel
	CoordinatorAgentID *int64
	BudgetTokens       *int64
	BudgetDollarsCents *int64
	MaxSpawnDepth      int
}
