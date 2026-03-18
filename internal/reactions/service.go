package reactions

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/synapbus/synapbus/internal/trust"
)

// StateChangeNotifier is called when a message's workflow state changes.
type StateChangeNotifier interface {
	OnWorkflowStateChanged(ctx context.Context, event trust.WorkflowStateChangeEvent)
}

// AgentTypeChecker resolves an agent's type (e.g. "human", "ai").
type AgentTypeChecker interface {
	GetAgentType(ctx context.Context, agentName string) (string, error)
}

// TrustAdjuster adjusts trust scores for agents.
type TrustAdjuster interface {
	RecordApproval(ctx context.Context, agentName, actionType string) error
	RecordRejection(ctx context.Context, agentName, actionType string) error
}

// MessageAuthorResolver looks up the author of a message.
type MessageAuthorResolver interface {
	GetMessageAuthor(ctx context.Context, messageID int64) (string, error)
}

// Service provides business logic for message reactions.
type Service struct {
	store               Store
	logger              *slog.Logger
	stateChangeNotifier StateChangeNotifier
	agentTypeChecker    AgentTypeChecker
	trustAdjuster       TrustAdjuster
	authorResolver      MessageAuthorResolver
}

// NewService creates a new reaction service.
func NewService(store Store, logger *slog.Logger) *Service {
	return &Service{
		store:  store,
		logger: logger.With("component", "reactions"),
	}
}

// SetStateChangeNotifier sets the notifier called on workflow state transitions.
func (s *Service) SetStateChangeNotifier(n StateChangeNotifier) {
	s.stateChangeNotifier = n
}

// SetAgentTypeChecker sets the checker used to resolve agent types for trust adjustments.
func (s *Service) SetAgentTypeChecker(c AgentTypeChecker) {
	s.agentTypeChecker = c
}

// SetTrustAdjuster sets the trust adjuster for recording approvals/rejections.
func (s *Service) SetTrustAdjuster(a TrustAdjuster) {
	s.trustAdjuster = a
}

// SetMessageAuthorResolver sets the resolver for looking up message authors.
func (s *Service) SetMessageAuthorResolver(r MessageAuthorResolver) {
	s.authorResolver = r
}

// ToggleResult describes what happened after a toggle operation.
type ToggleResult struct {
	Action   string    `json:"action"` // "added" or "removed"
	Reaction *Reaction `json:"reaction,omitempty"`
}

// Toggle adds a reaction if it doesn't exist, or removes it if it does.
// Returns the action taken and the reaction (if added).
func (s *Service) Toggle(ctx context.Context, messageID int64, agentName, reactionType string, metadata json.RawMessage) (*ToggleResult, error) {
	if !IsValidReaction(reactionType) {
		return nil, ErrInvalidReaction
	}

	// Capture old workflow state before any mutation
	var oldState string
	if s.stateChangeNotifier != nil {
		oldReactions, _ := s.store.GetByMessageID(ctx, messageID)
		oldState = ComputeWorkflowState(oldReactions)
	}

	// Check if reaction already exists
	exists, err := s.store.Exists(ctx, messageID, agentName, reactionType)
	if err != nil {
		return nil, fmt.Errorf("check existing reaction: %w", err)
	}

	if exists {
		// Toggle off — remove it
		if err := s.store.Delete(ctx, messageID, agentName, reactionType); err != nil {
			return nil, fmt.Errorf("remove reaction: %w", err)
		}
		s.logger.Info("reaction removed",
			"message_id", messageID,
			"agent", agentName,
			"reaction", reactionType,
		)

		// Check for workflow state change after removal
		s.notifyStateChangeIfNeeded(ctx, messageID, oldState, agentName, reactionType)

		return &ToggleResult{Action: "removed"}, nil
	}

	// Claim semantics: only one agent can have in_progress at a time
	if reactionType == ReactionInProgress {
		existing, err := s.store.GetByMessageID(ctx, messageID)
		if err != nil {
			return nil, fmt.Errorf("check existing claims: %w", err)
		}
		for _, r := range existing {
			if r.Reaction == ReactionInProgress && r.AgentName != agentName {
				return nil, fmt.Errorf("already claimed by %s", r.AgentName)
			}
		}
	}

	// Check reaction count limit
	count, err := s.store.CountByMessage(ctx, messageID)
	if err != nil {
		return nil, fmt.Errorf("count reactions: %w", err)
	}
	if count >= MaxReactionsPerMessage {
		return nil, ErrReactionLimit
	}

	// Toggle on — add it
	if metadata == nil {
		metadata = json.RawMessage("{}")
	}

	r := &Reaction{
		MessageID: messageID,
		AgentName: agentName,
		Reaction:  reactionType,
		Metadata:  metadata,
	}

	if err := s.store.Insert(ctx, r); err != nil {
		return nil, fmt.Errorf("add reaction: %w", err)
	}

	s.logger.Info("reaction added",
		"message_id", messageID,
		"agent", agentName,
		"reaction", reactionType,
	)

	// Check for workflow state change after addition
	s.notifyStateChangeIfNeeded(ctx, messageID, oldState, agentName, reactionType)

	// Adjust trust when a human approves/rejects an AI agent's message
	s.adjustTrustIfNeeded(ctx, messageID, agentName, reactionType)

	return &ToggleResult{Action: "added", Reaction: r}, nil
}

// notifyStateChangeIfNeeded fires the state change notifier if the workflow state changed.
func (s *Service) notifyStateChangeIfNeeded(ctx context.Context, messageID int64, oldState, agentName, reactionType string) {
	if s.stateChangeNotifier == nil {
		return
	}
	newReactions, err := s.store.GetByMessageID(ctx, messageID)
	if err != nil {
		return
	}
	newState := ComputeWorkflowState(newReactions)
	if newState != oldState {
		s.stateChangeNotifier.OnWorkflowStateChanged(ctx, trust.WorkflowStateChangeEvent{
			MessageID:   messageID,
			OldState:    oldState,
			NewState:    newState,
			TriggeredBy: agentName,
			Reaction:    reactionType,
		})
	}
}

// adjustTrustIfNeeded adjusts trust when a human reacts approve/reject to an AI agent's message.
func (s *Service) adjustTrustIfNeeded(ctx context.Context, messageID int64, reactorName, reactionType string) {
	if s.trustAdjuster == nil || s.agentTypeChecker == nil || s.authorResolver == nil {
		return
	}

	// Only approve and reject adjust trust
	if reactionType != ReactionApprove && reactionType != ReactionReject {
		return
	}

	// Check if the reactor is a human
	reactorType, err := s.agentTypeChecker.GetAgentType(ctx, reactorName)
	if err != nil || reactorType != "human" {
		return
	}

	// Get the message author
	authorName, err := s.authorResolver.GetMessageAuthor(ctx, messageID)
	if err != nil || authorName == "" {
		return
	}

	// Check if the author is an AI agent
	authorType, err := s.agentTypeChecker.GetAgentType(ctx, authorName)
	if err != nil || authorType != "ai" {
		return
	}

	// Adjust trust for the AI agent
	actionType := trust.ActionPublish
	if reactionType == ReactionApprove {
		if err := s.trustAdjuster.RecordApproval(ctx, authorName, actionType); err != nil {
			s.logger.Warn("trust approval failed",
				"agent", authorName,
				"reactor", reactorName,
				"error", err,
			)
		}
	} else {
		if err := s.trustAdjuster.RecordRejection(ctx, authorName, actionType); err != nil {
			s.logger.Warn("trust rejection failed",
				"agent", authorName,
				"reactor", reactorName,
				"error", err,
			)
		}
	}
}

// Remove explicitly removes a reaction.
func (s *Service) Remove(ctx context.Context, messageID int64, agentName, reactionType string) error {
	if !IsValidReaction(reactionType) {
		return ErrInvalidReaction
	}

	if err := s.store.Delete(ctx, messageID, agentName, reactionType); err != nil {
		return fmt.Errorf("remove reaction: %w", err)
	}

	s.logger.Info("reaction removed",
		"message_id", messageID,
		"agent", agentName,
		"reaction", reactionType,
	)
	return nil
}

// GetReactions returns all reactions for a message and the computed workflow state.
func (s *Service) GetReactions(ctx context.Context, messageID int64) ([]*Reaction, string, error) {
	reactions, err := s.store.GetByMessageID(ctx, messageID)
	if err != nil {
		return nil, "", fmt.Errorf("get reactions: %w", err)
	}

	state := ComputeWorkflowState(reactions)
	return reactions, state, nil
}

// GetReactionsByMessageIDs returns reactions grouped by message ID.
func (s *Service) GetReactionsByMessageIDs(ctx context.Context, messageIDs []int64) (map[int64][]*Reaction, error) {
	return s.store.GetByMessageIDs(ctx, messageIDs)
}

// ListByState returns message IDs in a channel that have the given workflow state.
func (s *Service) ListByState(ctx context.Context, channelID int64, state string) ([]int64, error) {
	return s.store.GetMessageIDsByState(ctx, channelID, state)
}
