package channels

import (
	"encoding/json"
	"time"
)

// TaskStatus constants for task lifecycle.
const (
	TaskStatusOpen      = "open"
	TaskStatusAssigned  = "assigned"
	TaskStatusCompleted = "completed"
	TaskStatusCancelled = "cancelled"
)

// BidStatus constants for bid lifecycle.
const (
	BidStatusPending  = "pending"
	BidStatusAccepted = "accepted"
	BidStatusRejected = "rejected"
)

// Task represents a unit of work posted to an auction channel.
type Task struct {
	ID           int64           `json:"id"`
	ChannelID    int64           `json:"channel_id"`
	PostedBy     string          `json:"posted_by"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Requirements json.RawMessage `json:"requirements"`
	Deadline     *time.Time      `json:"deadline,omitempty"`
	Status       string          `json:"status"`
	AssignedTo   string          `json:"assigned_to,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// Bid represents an agent's offer to complete a task.
type Bid struct {
	ID           int64           `json:"id"`
	TaskID       int64           `json:"task_id"`
	AgentName    string          `json:"agent_name"`
	Capabilities json.RawMessage `json:"capabilities"`
	TimeEstimate string          `json:"time_estimate"`
	Message      string          `json:"message"`
	Status       string          `json:"status"`
	CreatedAt    time.Time       `json:"created_at"`
}

// ValidTaskStatus returns true if the given status is a valid task status.
func ValidTaskStatus(s string) bool {
	switch s {
	case TaskStatusOpen, TaskStatusAssigned, TaskStatusCompleted, TaskStatusCancelled:
		return true
	}
	return false
}

// ValidBidStatus returns true if the given status is a valid bid status.
func ValidBidStatus(s string) bool {
	switch s {
	case BidStatusPending, BidStatusAccepted, BidStatusRejected:
		return true
	}
	return false
}
