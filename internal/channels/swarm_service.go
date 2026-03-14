package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/synapbus/synapbus/internal/trace"
)

// SwarmService handles task auction and stigmergy patterns.
type SwarmService struct {
	taskStore    TaskStore
	channelStore ChannelStore
	tracer       *trace.Tracer
	logger       *slog.Logger
}

// NewSwarmService creates a new swarm service.
func NewSwarmService(taskStore TaskStore, channelStore ChannelStore, tracer *trace.Tracer) *SwarmService {
	return &SwarmService{
		taskStore:    taskStore,
		channelStore: channelStore,
		tracer:       tracer,
		logger:       slog.Default().With("component", "swarm"),
	}
}

// PostTask creates a new task on an auction channel.
func (s *SwarmService) PostTask(ctx context.Context, channelID int64, agentName, title, description string, requirements json.RawMessage, deadline *time.Time) (*Task, error) {
	// Verify channel exists and is auction type
	ch, err := s.channelStore.GetChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if ch.Type != TypeAuction {
		return nil, fmt.Errorf("post_task requires a channel of type 'auction', got '%s'", ch.Type)
	}

	// Verify agent is channel member
	isMember, err := s.channelStore.IsMember(ctx, channelID, agentName)
	if err != nil {
		return nil, fmt.Errorf("check membership: %w", err)
	}
	if !isMember {
		return nil, ErrNotChannelMember
	}

	// Validate deadline is in the future (if provided)
	if deadline != nil && deadline.Before(time.Now()) {
		return nil, fmt.Errorf("deadline must be in the future")
	}

	if requirements == nil || len(requirements) == 0 {
		requirements = json.RawMessage("{}")
	}

	task := &Task{
		ChannelID:    channelID,
		PostedBy:     agentName,
		Title:        title,
		Description:  description,
		Requirements: requirements,
		Deadline:     deadline,
		Status:       TaskStatusOpen,
	}

	if err := s.taskStore.CreateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}

	s.logger.Info("task posted",
		"task_id", task.ID,
		"channel_id", channelID,
		"posted_by", agentName,
		"title", title,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "swarm.task_posted", map[string]any{
			"task_id":      task.ID,
			"channel_id":   channelID,
			"channel_name": ch.Name,
			"title":        title,
		})
	}

	return task, nil
}

// BidOnTask creates a bid on an open task.
func (s *SwarmService) BidOnTask(ctx context.Context, taskID int64, agentName string, capabilities json.RawMessage, timeEstimate, message string) (*Bid, error) {
	// Get task
	task, err := s.taskStore.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}

	// Verify task is open
	if task.Status != TaskStatusOpen {
		return nil, fmt.Errorf("cannot bid on task with status '%s'; task must be 'open'", task.Status)
	}

	// Verify agent is not the poster
	if task.PostedBy == agentName {
		return nil, fmt.Errorf("cannot bid on your own task")
	}

	// Verify agent is member of task's channel
	isMember, err := s.channelStore.IsMember(ctx, task.ChannelID, agentName)
	if err != nil {
		return nil, fmt.Errorf("check membership: %w", err)
	}
	if !isMember {
		return nil, ErrNotChannelMember
	}

	if capabilities == nil || len(capabilities) == 0 {
		capabilities = json.RawMessage("{}")
	}

	bid := &Bid{
		TaskID:       taskID,
		AgentName:    agentName,
		Capabilities: capabilities,
		TimeEstimate: timeEstimate,
		Message:      message,
	}

	if err := s.taskStore.CreateBid(ctx, bid); err != nil {
		return nil, err
	}

	s.logger.Info("bid submitted",
		"bid_id", bid.ID,
		"task_id", taskID,
		"agent", agentName,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "swarm.bid_submitted", map[string]any{
			"bid_id":  bid.ID,
			"task_id": taskID,
		})
	}

	return bid, nil
}

// AcceptBid accepts a bid and assigns the task to the bidding agent.
func (s *SwarmService) AcceptBid(ctx context.Context, taskID, bidID int64, agentName string) error {
	// Get task
	task, err := s.taskStore.GetTask(ctx, taskID)
	if err != nil {
		return err
	}

	// Verify caller is the task poster
	if task.PostedBy != agentName {
		return fmt.Errorf("only the task poster can accept bids")
	}

	// Verify task is open
	if task.Status != TaskStatusOpen {
		return fmt.Errorf("cannot accept bid on task with status '%s'; task must be 'open'", task.Status)
	}

	// Get the bid
	bid, err := s.taskStore.GetBid(ctx, bidID)
	if err != nil {
		return err
	}

	// Verify bid belongs to this task
	if bid.TaskID != taskID {
		return fmt.Errorf("bid %d does not belong to task %d", bidID, taskID)
	}

	// Assign the task
	if err := s.taskStore.UpdateTaskStatus(ctx, taskID, TaskStatusAssigned, bid.AgentName); err != nil {
		return fmt.Errorf("assign task: %w", err)
	}

	// Accept the winning bid
	if err := s.taskStore.UpdateBidStatus(ctx, bidID, BidStatusAccepted); err != nil {
		return fmt.Errorf("accept bid: %w", err)
	}

	// Reject all other bids
	allBids, err := s.taskStore.GetBids(ctx, taskID)
	if err != nil {
		return fmt.Errorf("get bids for rejection: %w", err)
	}
	for _, b := range allBids {
		if b.ID != bidID && b.Status == BidStatusPending {
			if err := s.taskStore.UpdateBidStatus(ctx, b.ID, BidStatusRejected); err != nil {
				s.logger.Error("failed to reject bid", "bid_id", b.ID, "error", err)
			}
		}
	}

	s.logger.Info("bid accepted",
		"task_id", taskID,
		"bid_id", bidID,
		"assigned_to", bid.AgentName,
		"accepted_by", agentName,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "swarm.bid_accepted", map[string]any{
			"task_id":     taskID,
			"bid_id":      bidID,
			"assigned_to": bid.AgentName,
		})
	}

	return nil
}

// CompleteTask marks a task as completed.
func (s *SwarmService) CompleteTask(ctx context.Context, taskID int64, agentName string) error {
	// Get task
	task, err := s.taskStore.GetTask(ctx, taskID)
	if err != nil {
		return err
	}

	// Handle idempotent completion
	if task.Status == TaskStatusCompleted && task.AssignedTo == agentName {
		return nil
	}

	// Verify task is assigned
	if task.Status != TaskStatusAssigned {
		return fmt.Errorf("cannot complete task with status '%s'; task must be 'assigned'", task.Status)
	}

	// Verify caller is the assigned agent
	if task.AssignedTo != agentName {
		return fmt.Errorf("only the assigned agent can complete the task")
	}

	// Complete the task
	if err := s.taskStore.UpdateTaskStatus(ctx, taskID, TaskStatusCompleted, ""); err != nil {
		return fmt.Errorf("complete task: %w", err)
	}

	s.logger.Info("task completed",
		"task_id", taskID,
		"completed_by", agentName,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "swarm.task_completed", map[string]any{
			"task_id": taskID,
		})
	}

	return nil
}

// ListTasks returns tasks for a channel, optionally filtered by status.
func (s *SwarmService) ListTasks(ctx context.Context, channelID int64, status string) ([]*Task, error) {
	return s.taskStore.ListTasks(ctx, channelID, status)
}

// GetTaskWithBids returns a task and all its bids.
func (s *SwarmService) GetTaskWithBids(ctx context.Context, taskID int64) (*Task, []*Bid, error) {
	task, err := s.taskStore.GetTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}

	bids, err := s.taskStore.GetBids(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}

	return task, bids, nil
}

// ExpireTasks marks expired tasks as cancelled. Called by the expiry worker.
func (s *SwarmService) ExpireTasks(ctx context.Context) (int, error) {
	count, err := s.taskStore.ExpireTasks(ctx)
	if err != nil {
		return 0, err
	}

	if count > 0 {
		s.logger.Info("expired tasks cancelled", "count", count)
		if s.tracer != nil {
			s.tracer.Record(ctx, "system", "swarm.tasks_expired", map[string]any{
				"count": count,
			})
		}
	}

	return count, nil
}
