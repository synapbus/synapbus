// Package reactions provides message reaction types, storage, and workflow state logic.
package reactions

import (
	"encoding/json"
	"errors"
	"time"
)

// Valid reaction types.
const (
	ReactionApprove    = "approve"
	ReactionReject     = "reject"
	ReactionInProgress = "in_progress"
	ReactionDone       = "done"
	ReactionPublished  = "published"
	// ReactionAwarded is used on auction-channel bid messages to designate
	// the winning bid (spec 016, FR-008). Added in feature 016.
	ReactionAwarded = "awarded"
)

// Workflow states (derived from reactions).
const (
	StateProposed   = "proposed"
	StateApproved   = "approved"
	StateInProgress = "in_progress"
	StateRejected   = "rejected"
	StateDone       = "done"
	StatePublished  = "published"
	StateAwarded    = "awarded"
)

// reactionPriority maps reaction types to their priority for state derivation.
// Higher number = higher priority = wins for badge display.
var reactionPriority = map[string]int{
	ReactionApprove:    2,
	ReactionInProgress: 3,
	ReactionReject:     4,
	ReactionAwarded:    5,
	ReactionDone:       6,
	ReactionPublished:  7,
}

// reactionToState maps reaction types to workflow states.
var reactionToState = map[string]string{
	ReactionApprove:    StateApproved,
	ReactionReject:     StateRejected,
	ReactionInProgress: StateInProgress,
	ReactionAwarded:    StateAwarded,
	ReactionDone:       StateDone,
	ReactionPublished:  StatePublished,
}

// TerminalStates are states that should not trigger stalemate checks.
var TerminalStates = map[string]bool{
	StateRejected:  true,
	StateDone:      true,
	StatePublished: true,
}

// MaxReactionsPerMessage is the safety limit.
const MaxReactionsPerMessage = 100

// Sentinel errors.
var (
	ErrInvalidReaction = errors.New("invalid reaction type: must be one of approve, reject, in_progress, awarded, done, published")
	ErrReactionLimit   = errors.New("maximum reactions per message (100) reached")
	ErrNotMember       = errors.New("only channel members can react to messages")
)

// Reaction represents a single reaction on a message.
type Reaction struct {
	ID        int64           `json:"id"`
	MessageID int64           `json:"message_id"`
	AgentName string          `json:"agent_name"`
	Reaction  string          `json:"reaction"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
}

// ValidReactions is the set of allowed reaction types.
var ValidReactions = map[string]bool{
	ReactionApprove:    true,
	ReactionReject:     true,
	ReactionInProgress: true,
	ReactionAwarded:    true,
	ReactionDone:       true,
	ReactionPublished:  true,
}

// IsValidReaction returns true if the reaction type is valid.
func IsValidReaction(r string) bool {
	return ValidReactions[r]
}

// ComputeWorkflowState derives the workflow state from a list of reactions.
// Returns "proposed" if no reactions exist.
func ComputeWorkflowState(reactions []*Reaction) string {
	if len(reactions) == 0 {
		return StateProposed
	}

	highestPriority := 0
	highestState := StateProposed

	for _, r := range reactions {
		p, ok := reactionPriority[r.Reaction]
		if ok && p > highestPriority {
			highestPriority = p
			highestState = reactionToState[r.Reaction]
		}
	}

	return highestState
}

// IsTerminalState returns true if the state should not trigger stalemate checks.
func IsTerminalState(state string) bool {
	return TerminalStates[state]
}
