package goals

import (
	"context"
	"fmt"
	"log/slog"
)

// ChannelCreator abstracts the channels package so goals can auto-create
// its backing channel without a direct import cycle.
type ChannelCreator interface {
	// CreateGoalChannel creates a private blackboard channel for a goal and
	// returns its id. The implementation wraps channels.Service.CreateChannel
	// with the right ChannelType and adds the owner as a member.
	CreateGoalChannel(ctx context.Context, slug, title, description, ownerUsername string) (int64, error)
}

// Service is the high-level API for the goals package.
type Service struct {
	store   *Store
	chans   ChannelCreator
	logger  *slog.Logger
}

// NewService constructs a goals service.
func NewService(store *Store, chans ChannelCreator, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, chans: chans, logger: logger}
}

// CreateGoal writes a goal row and auto-creates its backing channel.
// Slug collisions are resolved by appending -2, -3, ... up to 99.
func (s *Service) CreateGoal(ctx context.Context, in CreateGoalInput) (*Goal, error) {
	if in.Title == "" || in.Description == "" {
		return nil, fmt.Errorf("title and description are required")
	}
	if in.MaxSpawnDepth <= 0 {
		in.MaxSpawnDepth = 3
	}
	baseSlug := slugify(in.Title)
	slug := baseSlug
	for i := 2; i < 100; i++ {
		exists, err := s.store.SlugExists(ctx, slug)
		if err != nil {
			return nil, err
		}
		if !exists {
			break
		}
		slug = fmt.Sprintf("%s-%d", baseSlug, i)
	}

	channelID, err := s.chans.CreateGoalChannel(ctx, slug, in.Title, in.Description, in.OwnerUsername)
	if err != nil {
		return nil, fmt.Errorf("create backing channel: %w", err)
	}

	g := &Goal{
		Slug:               slug,
		Title:              in.Title,
		Description:        in.Description,
		OwnerUserID:        in.OwnerUserID,
		ChannelID:          channelID,
		CoordinatorAgentID: in.CoordinatorAgentID,
		Status:             StatusDraft,
		BudgetTokens:       in.BudgetTokens,
		BudgetDollarsCents: in.BudgetDollarsCents,
		MaxSpawnDepth:      in.MaxSpawnDepth,
	}
	if _, err := s.store.Insert(ctx, g); err != nil {
		return nil, err
	}
	s.logger.Info("goal created", "goal_id", g.ID, "slug", slug, "channel_id", channelID, "owner", in.OwnerUserID)
	return g, nil
}

// GetGoal fetches a goal by id.
func (s *Service) GetGoal(ctx context.Context, id int64) (*Goal, error) {
	return s.store.Get(ctx, id)
}

// ListGoals returns goals, optionally filtered by owner.
func (s *Service) ListGoals(ctx context.Context, ownerUserID *int64, limit int) ([]*Goal, error) {
	return s.store.List(ctx, ownerUserID, limit)
}

// TransitionStatus moves a goal to a new status. Legal transitions:
//
//	draft → active | cancelled
//	active → paused | completed | stuck | cancelled
//	paused → active | cancelled
//	stuck → active | cancelled
func (s *Service) TransitionStatus(ctx context.Context, goalID int64, newStatus string) error {
	g, err := s.store.Get(ctx, goalID)
	if err != nil {
		return err
	}
	ok := legalTransition(g.Status, newStatus)
	if !ok {
		return fmt.Errorf("illegal goal transition: %s → %s", g.Status, newStatus)
	}
	return s.store.SetStatus(ctx, goalID, newStatus)
}

// BudgetVerdict describes what the budget enforcer wants the caller to do.
type BudgetVerdict struct {
	PercentBudget float64 // 0..100+
	TriggerSoftAlert bool  // first time we cross 80%
	TriggerHardPause bool  // crossed 100% and goal is still active
}

// EvaluateBudget computes current spend-vs-budget for a goal and returns
// the enforcement verdict. It does NOT mutate state on its own — the
// caller uses MarkSoftAlertPosted / TransitionStatus to apply the
// verdict once it has posted the corresponding system messages.
//
// Only dollar-cents budget is enforced in MVP (tokens are tracked but
// don't trip the cascade).
func (s *Service) EvaluateBudget(ctx context.Context, goalID int64, spentCents int64) (*BudgetVerdict, error) {
	g, err := s.store.Get(ctx, goalID)
	if err != nil {
		return nil, err
	}
	v := &BudgetVerdict{}
	if g.BudgetDollarsCents == nil || *g.BudgetDollarsCents <= 0 {
		return v, nil
	}
	v.PercentBudget = float64(spentCents) / float64(*g.BudgetDollarsCents) * 100.0
	if v.PercentBudget >= 80 && !g.Alert80PctPosted {
		v.TriggerSoftAlert = true
	}
	if v.PercentBudget >= 100 && g.Status == StatusActive {
		v.TriggerHardPause = true
	}
	return v, nil
}

// MarkSoftAlertPosted records that the 80% soft-alert was emitted.
func (s *Service) MarkSoftAlertPosted(ctx context.Context, goalID int64) error {
	return s.store.MarkSoftAlertPosted(ctx, goalID)
}

func legalTransition(from, to string) bool {
	switch from {
	case StatusDraft:
		return to == StatusActive || to == StatusCancelled
	case StatusActive:
		return to == StatusPaused || to == StatusCompleted || to == StatusStuck || to == StatusCancelled
	case StatusPaused:
		return to == StatusActive || to == StatusCancelled
	case StatusStuck:
		return to == StatusActive || to == StatusCancelled
	}
	return false
}
