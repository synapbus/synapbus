// Package trust provides agent trust score tracking for graduated autonomy.
package trust

import (
	"errors"
	"time"
)

// Trust adjustment constants.
const (
	ApprovalIncrement = 0.05
	RejectionDecrement = 0.10
	MinScore          = 0.0
	MaxScore          = 1.0
)

// Common action types (extensible — any string is valid).
const (
	ActionResearch = "research"
	ActionPublish  = "publish"
	ActionComment  = "comment"
	ActionApprove  = "approve"
	ActionOperate  = "operate"
)

// Sentinel errors.
var (
	ErrAlreadyClaimed = errors.New("work item already claimed by another agent")
	ErrSelfReaction   = errors.New("cannot adjust trust for self-reactions")
)

// TrustScore represents an agent's trust level for a specific action type.
type TrustScore struct {
	ID               int64      `json:"id"`
	AgentName        string     `json:"agent_name"`
	ActionType       string     `json:"action_type"`
	Score            float64    `json:"score"`
	AdjustmentsCount int        `json:"adjustments_count"`
	LastAdjustedAt   *time.Time `json:"last_adjusted_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// AgentTrustSummary is a map of action_type -> score for an agent.
type AgentTrustSummary map[string]float64

// ClampScore ensures a score stays within [0.0, 1.0].
func ClampScore(score float64) float64 {
	if score < MinScore {
		return MinScore
	}
	if score > MaxScore {
		return MaxScore
	}
	return score
}

// WorkflowStateChangeEvent is the webhook payload for state transitions.
type WorkflowStateChangeEvent struct {
	MessageID   int64  `json:"message_id"`
	ChannelID   int64  `json:"channel_id,omitempty"`
	OldState    string `json:"old_state"`
	NewState    string `json:"new_state"`
	TriggeredBy string `json:"triggered_by"`
	Reaction    string `json:"reaction"`
}
