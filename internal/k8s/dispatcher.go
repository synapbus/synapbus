package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/synapbus/synapbus/internal/dispatcher"
)

// K8sDispatcher implements dispatcher.EventDispatcher by launching K8s Jobs.
type K8sDispatcher struct {
	store  K8sStore
	runner JobRunner
	logger *slog.Logger
}

// NewK8sDispatcher creates a new K8s event dispatcher.
func NewK8sDispatcher(store K8sStore, runner JobRunner, logger *slog.Logger) *K8sDispatcher {
	return &K8sDispatcher{
		store:  store,
		runner: runner,
		logger: logger,
	}
}

// Dispatch creates K8s Jobs for all active handlers matching the event.
func (d *K8sDispatcher) Dispatch(ctx context.Context, event dispatcher.MessageEvent) error {
	if !d.runner.IsAvailable() {
		return nil
	}

	targetAgent := event.ToAgent
	if targetAgent == "" {
		return nil
	}

	handlers, err := d.store.GetActiveHandlersByEvent(ctx, targetAgent, event.EventType)
	if err != nil {
		return fmt.Errorf("get handlers for event: %w", err)
	}

	for _, handler := range handlers {
		msg := &JobMessage{
			MessageID: event.MessageID,
			FromAgent: event.FromAgent,
			Body:      event.Body,
			Event:     event.EventType,
			Channel:   event.Channel,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		jobName, err := d.runner.CreateJob(ctx, handler, msg)
		if err != nil {
			d.logger.Error("failed to create K8s Job",
				"handler_id", handler.ID,
				"agent", handler.AgentName,
				"error", err,
			)

			// Record the failed job run
			now := time.Now()
			run := &K8sJobRun{
				HandlerID:     handler.ID,
				AgentName:     handler.AgentName,
				MessageID:     event.MessageID,
				JobName:       fmt.Sprintf("failed-%d", event.MessageID),
				Namespace:     handler.Namespace,
				Status:        "failed",
				FailureReason: err.Error(),
				StartedAt:     &now,
				CompletedAt:   &now,
			}
			if _, insertErr := d.store.InsertJobRun(ctx, run); insertErr != nil {
				d.logger.Error("failed to record failed job run", "error", insertErr)
			}
			continue
		}

		// Record the job run as pending
		now := time.Now()
		namespace := handler.Namespace
		if namespace == "" {
			namespace = d.runner.GetNamespace()
		}
		run := &K8sJobRun{
			HandlerID: handler.ID,
			AgentName: handler.AgentName,
			MessageID: event.MessageID,
			JobName:   jobName,
			Namespace: namespace,
			Status:    "pending",
			StartedAt: &now,
		}
		if _, err := d.store.InsertJobRun(ctx, run); err != nil {
			d.logger.Error("failed to record job run",
				"job_name", jobName,
				"error", err,
			)
		}

		d.logger.Info("K8s Job dispatched",
			"job_name", jobName,
			"handler_id", handler.ID,
			"agent", handler.AgentName,
			"event", event.EventType,
		)
	}

	return nil
}
