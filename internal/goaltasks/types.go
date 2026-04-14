// Package goaltasks implements the work-task tree rooted in a goal.
// Table name is goal_tasks (not tasks) because the legacy channel
// task-auction feature already owns the tasks table.
//
// Tasks are single-assignee, atomically claimable, and carry a
// denormalized goal-ancestry snapshot so every subprocess run can
// see the full root-to-parent context without recursive queries.
package goaltasks

import (
	"encoding/json"
	"errors"
	"time"
)

// Task status values.
const (
	StatusProposed             = "proposed"
	StatusApproved             = "approved"
	StatusClaimed              = "claimed"
	StatusInProgress           = "in_progress"
	StatusAwaitingVerification = "awaiting_verification"
	StatusDone                 = "done"
	StatusFailed               = "failed"
	StatusCancelled            = "cancelled"
)

// Verifier kinds.
const (
	VerifierKindAuto    = "auto"
	VerifierKindPeer    = "peer"
	VerifierKindCommand = "command"
)

// AncestryNode is one entry in a task's denormalized ancestry chain,
// copied from the root down to the parent at create time.
type AncestryNode struct {
	ID                 int64  `json:"id"`
	Title              string `json:"title"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`
}

// VerifierConfig describes how to verify a task once the assignee
// reports it complete. Exactly one kind is present.
type VerifierConfig struct {
	Kind       string `json:"kind"`
	AgentID    int64  `json:"agent_id,omitempty"`
	Cmd        string `json:"cmd,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// HeartbeatConfig controls how the reactor wakes the assignee.
type HeartbeatConfig struct {
	Source      string `json:"source"`
	IntervalSec int    `json:"interval_sec,omitempty"`
}

// Task is a node in a goal's task tree.
type Task struct {
	ID                  int64
	GoalID              int64
	ParentTaskID        *int64
	Ancestry            []AncestryNode
	Depth               int
	Title               string
	Description         string
	AcceptanceCriteria  string
	CreatedByAgentID    *int64
	CreatedByUserID     *int64
	AssigneeAgentID     *int64
	Status              string
	BillingCode         string
	BudgetTokens        *int64
	BudgetDollarsCents  *int64
	SpentTokens         int64
	SpentDollarsCents   int64
	HeartbeatConfig     *HeartbeatConfig
	VerifierConfig      *VerifierConfig
	OriginMessageID     *int64
	ClaimMessageID      *int64
	CompletionMessageID *int64
	FailureReason       string
	CreatedAt           time.Time
	ApprovedAt          *time.Time
	ClaimedAt           *time.Time
	StartedAt           *time.Time
	CompletedAt         *time.Time
}

// TreeNode is the input shape for CreateTree — a recursive task spec.
type TreeNode struct {
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	AcceptanceCriteria string          `json:"acceptance_criteria,omitempty"`
	BillingCode        string          `json:"billing_code,omitempty"`
	BudgetTokens       *int64          `json:"budget_tokens,omitempty"`
	BudgetDollarsCents *int64          `json:"budget_dollars_cents,omitempty"`
	VerifierConfig     *VerifierConfig `json:"verifier_config,omitempty"`
	HeartbeatConfig    *HeartbeatConfig `json:"heartbeat_config,omitempty"`
	Children           []TreeNode      `json:"children,omitempty"`
}

// MaxAncestryBytes caps the denormalized ancestry blob on any single task.
const MaxAncestryBytes = 16 * 1024

// marshalAncestry serializes an ancestry chain. Returns ErrAncestryOverflow
// if the result exceeds MaxAncestryBytes.
func marshalAncestry(nodes []AncestryNode) (string, error) {
	b, err := json.Marshal(nodes)
	if err != nil {
		return "", err
	}
	if len(b) > MaxAncestryBytes {
		return "", ErrAncestryOverflow
	}
	return string(b), nil
}

// unmarshalAncestry parses the stored JSON back into a chain.
func unmarshalAncestry(s string) ([]AncestryNode, error) {
	if s == "" || s == "[]" {
		return nil, nil
	}
	var nodes []AncestryNode
	if err := json.Unmarshal([]byte(s), &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

// Sentinel errors.
var (
	ErrTaskNotFound      = errors.New("task not found")
	ErrAlreadyClaimed    = errors.New("task already claimed by another agent")
	ErrIllegalTransition = errors.New("illegal task status transition")
	ErrAncestryOverflow  = errors.New("task ancestry exceeds 16 KB cap")
)
