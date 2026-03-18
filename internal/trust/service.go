package trust

import (
	"context"
	"fmt"
	"log/slog"
)

// Service provides business logic for trust score management.
type Service struct {
	store  Store
	logger *slog.Logger
}

// NewService creates a new trust service.
func NewService(store Store, logger *slog.Logger) *Service {
	return &Service{
		store:  store,
		logger: logger.With("component", "trust"),
	}
}

// RecordApproval increases an agent's trust for an action type.
func (s *Service) RecordApproval(ctx context.Context, agentName, actionType string) (*TrustScore, error) {
	ts, err := s.store.UpsertScore(ctx, agentName, actionType, ApprovalIncrement)
	if err != nil {
		return nil, fmt.Errorf("record approval: %w", err)
	}
	s.logger.Info("trust increased",
		"agent", agentName,
		"action", actionType,
		"delta", ApprovalIncrement,
		"new_score", ts.Score,
	)
	return ts, nil
}

// RecordRejection decreases an agent's trust for an action type.
func (s *Service) RecordRejection(ctx context.Context, agentName, actionType string) (*TrustScore, error) {
	ts, err := s.store.UpsertScore(ctx, agentName, actionType, -RejectionDecrement)
	if err != nil {
		return nil, fmt.Errorf("record rejection: %w", err)
	}
	s.logger.Info("trust decreased",
		"agent", agentName,
		"action", actionType,
		"delta", -RejectionDecrement,
		"new_score", ts.Score,
	)
	return ts, nil
}

// GetScores returns all trust scores for an agent as a summary map.
func (s *Service) GetScores(ctx context.Context, agentName string) (AgentTrustSummary, error) {
	scores, err := s.store.GetAllScores(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("get scores: %w", err)
	}
	summary := make(AgentTrustSummary)
	for _, ts := range scores {
		summary[ts.ActionType] = ts.Score
	}
	return summary, nil
}

// GetScore returns the trust score for a specific (agent, action) pair.
func (s *Service) GetScore(ctx context.Context, agentName, actionType string) (float64, error) {
	ts, err := s.store.GetScore(ctx, agentName, actionType)
	if err != nil {
		return 0, fmt.Errorf("get score: %w", err)
	}
	return ts.Score, nil
}

// CheckAutonomy returns whether an agent has sufficient trust for an action
// given a channel's threshold.
func (s *Service) CheckAutonomy(ctx context.Context, agentName, actionType string, threshold float64) (bool, float64, error) {
	score, err := s.GetScore(ctx, agentName, actionType)
	if err != nil {
		return false, 0, err
	}
	return score >= threshold, score, nil
}
