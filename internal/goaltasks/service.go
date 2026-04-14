package goaltasks

import (
	"context"
	"fmt"
	"log/slog"
)

// Service is the high-level API for creating, claiming, and advancing tasks.
type Service struct {
	store  *Store
	logger *slog.Logger
}

// NewService constructs a task service.
func NewService(store *Store, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, logger: logger}
}

// Store exposes the backing store for callers that need direct access
// (e.g. the HTML report generator).
func (s *Service) Store() *Store {
	return s.store
}

// CreateTreeInput captures the arguments of CreateTree.
type CreateTreeInput struct {
	GoalID          int64
	CreatedByAgent  *int64
	CreatedByUser   *int64
	Root            TreeNode
	InitialStatus   string // defaults to StatusApproved (for auto-approved flows)
	DefaultBilling  string
}

// CreateTree materializes a tree of tasks under a goal in a single
// transaction. Ancestry is denormalized at create time. Returns the
// root task id and the flat list of all created ids in insertion order.
func (s *Service) CreateTree(ctx context.Context, in CreateTreeInput) (rootTaskID int64, allIDs []int64, err error) {
	if in.InitialStatus == "" {
		in.InitialStatus = StatusApproved
	}
	tx, err := s.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var walk func(node TreeNode, parentID *int64, depth int, ancestry []AncestryNode) (int64, error)
	walk = func(node TreeNode, parentID *int64, depth int, ancestry []AncestryNode) (int64, error) {
		billing := node.BillingCode
		if billing == "" {
			billing = in.DefaultBilling
		}
		t := &Task{
			GoalID:             in.GoalID,
			ParentTaskID:       parentID,
			Ancestry:           ancestry,
			Depth:              depth,
			Title:              node.Title,
			Description:        node.Description,
			AcceptanceCriteria: node.AcceptanceCriteria,
			CreatedByAgentID:   in.CreatedByAgent,
			CreatedByUserID:    in.CreatedByUser,
			Status:             in.InitialStatus,
			BillingCode:        billing,
			BudgetTokens:       node.BudgetTokens,
			BudgetDollarsCents: node.BudgetDollarsCents,
			VerifierConfig:     node.VerifierConfig,
			HeartbeatConfig:    node.HeartbeatConfig,
		}
		id, err := s.store.Insert(ctx, tx, t)
		if err != nil {
			return 0, err
		}
		allIDs = append(allIDs, id)

		if len(node.Children) > 0 {
			childAncestry := append([]AncestryNode(nil), ancestry...)
			childAncestry = append(childAncestry, AncestryNode{
				ID:                 id,
				Title:              node.Title,
				AcceptanceCriteria: node.AcceptanceCriteria,
			})
			for _, child := range node.Children {
				if _, err := walk(child, &id, depth+1, childAncestry); err != nil {
					return 0, err
				}
			}
		}
		return id, nil
	}

	rootTaskID, err = walk(in.Root, nil, 0, nil)
	if err != nil {
		return 0, nil, err
	}
	if err = tx.Commit(); err != nil {
		return 0, nil, err
	}
	s.logger.Info("task tree created", "goal_id", in.GoalID, "root_task_id", rootTaskID, "total", len(allIDs))
	return rootTaskID, allIDs, nil
}

// Claim atomically locks a task to an agent.
func (s *Service) Claim(ctx context.Context, taskID, agentID int64, claimMessageID *int64) error {
	return s.store.ClaimAtomic(ctx, taskID, agentID, claimMessageID)
}

// Transition moves a task through the state machine.
func (s *Service) Transition(ctx context.Context, taskID int64, newStatus string, extras Extras) error {
	t, err := s.store.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if !legalTransition(t.Status, newStatus) {
		return fmt.Errorf("%w: %s → %s", ErrIllegalTransition, t.Status, newStatus)
	}
	return s.store.TransitionStatus(ctx, taskID, newStatus, extras)
}

// Get exposes the store's Get.
func (s *Service) Get(ctx context.Context, id int64) (*Task, error) {
	return s.store.Get(ctx, id)
}

// ListByGoal exposes the store's ListByGoal.
func (s *Service) ListByGoal(ctx context.Context, goalID int64) ([]*Task, error) {
	return s.store.ListByGoal(ctx, goalID)
}

// AddSpend is used by the reactor post-run to increment leaf cost.
func (s *Service) AddSpend(ctx context.Context, taskID, tokens, dollarsCents int64) error {
	return s.store.AddSpend(ctx, taskID, tokens, dollarsCents)
}

// RollupCosts exposes the store's recursive CTE.
func (s *Service) RollupCosts(ctx context.Context, rootTaskID int64) (tokens, dollarsCents int64, count int, err error) {
	return s.store.RollupCosts(ctx, rootTaskID)
}

// RollupByBillingCode exposes the per-billing-code rollup.
func (s *Service) RollupByBillingCode(ctx context.Context, rootTaskID int64) (map[string]Spend, error) {
	return s.store.RollupByBillingCode(ctx, rootTaskID)
}

// legalTransition encodes the task state machine.
func legalTransition(from, to string) bool {
	if to == StatusCancelled {
		return from != StatusDone && from != StatusFailed && from != StatusCancelled
	}
	switch from {
	case StatusProposed:
		return to == StatusApproved
	case StatusApproved:
		return to == StatusClaimed
	case StatusClaimed:
		return to == StatusInProgress || to == StatusAwaitingVerification
	case StatusInProgress:
		return to == StatusAwaitingVerification
	case StatusAwaitingVerification:
		return to == StatusDone || to == StatusFailed
	}
	return false
}
