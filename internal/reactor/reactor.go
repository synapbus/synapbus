// Package reactor provides the reactive agent triggering engine.
// When a DM or @mention targets an agent with trigger_mode='reactive',
// the reactor evaluates rate limits and creates a K8s Job to run the agent.
package reactor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/dispatcher"
	k8spkg "github.com/synapbus/synapbus/internal/k8s"
	"github.com/synapbus/synapbus/internal/metrics"
)

// Reactor is the reactive agent triggering engine.
type Reactor struct {
	store        *Store
	agentStore   agents.AgentStore
	runner       k8spkg.JobRunner
	notifier     FailureNotifier
	logger       *slog.Logger
}

// FailureNotifier sends system DMs on job failure.
type FailureNotifier interface {
	NotifyFailure(ctx context.Context, ownerAgentName, agentName, triggerFrom, triggerEvent string, durationMs int64, errorSummary string) error
}

// New creates a new Reactor.
func New(store *Store, agentStore agents.AgentStore, runner k8spkg.JobRunner, logger *slog.Logger) *Reactor {
	return &Reactor{
		store:      store,
		agentStore: agentStore,
		runner:     runner,
		logger:     logger.With("component", "reactor"),
	}
}

// SetFailureNotifier sets the notifier for sending failure DMs.
func (r *Reactor) SetFailureNotifier(n FailureNotifier) {
	r.notifier = n
}

// Dispatch implements dispatcher.EventDispatcher. Called by MultiDispatcher
// when a message event occurs.
func (r *Reactor) Dispatch(ctx context.Context, event dispatcher.MessageEvent) error {
	switch event.EventType {
	case "message.received":
		// DM to an agent
		return r.evaluateTrigger(ctx, event.ToAgent, event)
	case "message.mentioned":
		// @mentions in channel messages
		for _, mentioned := range event.MentionedAgents {
			// Self-mention filter: agent can't trigger itself
			if mentioned == event.FromAgent {
				continue
			}
			if err := r.evaluateTrigger(ctx, mentioned, event); err != nil {
				r.logger.ErrorContext(ctx, "reactor trigger eval failed",
					"agent", mentioned,
					"error", err,
				)
			}
		}
		return nil
	default:
		return nil // Ignore other event types
	}
}

// evaluateTrigger runs the decision chain for a single agent.
func (r *Reactor) evaluateTrigger(ctx context.Context, agentName string, event dispatcher.MessageEvent) error {
	// 1. Get agent config
	agent, err := r.agentStore.GetAgentByName(ctx, agentName)
	if err != nil {
		return nil // Agent doesn't exist, skip silently
	}

	// 2. Check trigger mode
	if agent.TriggerMode != agents.TriggerModeReactive {
		return nil // Not reactive, skip
	}

	// 3. Check K8s image configured
	if agent.K8sImage == "" {
		r.logger.Warn("reactive agent has no k8s_image configured", "agent", agentName)
		r.recordSkippedRun(ctx, agentName, event, StatusFailed, "no k8s_image configured")
		return nil
	}

	// 4. Check K8s runner available
	if !r.runner.IsAvailable() {
		r.logger.Warn("K8s runner not available for reactive trigger", "agent", agentName)
		r.recordSkippedRun(ctx, agentName, event, StatusFailed, "K8s runner not available")
		return nil
	}

	// 5. Extract depth from event metadata
	depth := event.Depth

	// 6. Check trigger depth
	if depth >= agent.MaxTriggerDepth {
		r.logger.Info("trigger depth exceeded", "agent", agentName, "depth", depth, "max", agent.MaxTriggerDepth)
		r.recordSkippedRun(ctx, agentName, event, StatusDepthExceeded, "")
		return nil
	}

	// 7. Check daily budget
	todayCount, err := r.store.CountTodayRuns(ctx, agentName)
	if err != nil {
		return fmt.Errorf("count today runs: %w", err)
	}
	if todayCount >= agent.DailyTriggerBudget {
		r.logger.Info("daily trigger budget exhausted", "agent", agentName, "count", todayCount, "budget", agent.DailyTriggerBudget)
		r.recordSkippedRun(ctx, agentName, event, StatusBudgetExhausted, "")
		return nil
	}

	// 8. Check cooldown
	lastRun, err := r.store.GetLastRunTime(ctx, agentName)
	if err != nil {
		return fmt.Errorf("get last run time: %w", err)
	}
	if lastRun != nil {
		elapsed := time.Since(*lastRun)
		if elapsed < time.Duration(agent.CooldownSeconds)*time.Second {
			r.logger.Info("agent on cooldown", "agent", agentName, "elapsed", elapsed, "cooldown", agent.CooldownSeconds)
			// Set pending_work so we retry after cooldown
			_ = r.agentStore.SetPendingWork(ctx, agentName, true)
			r.recordSkippedRun(ctx, agentName, event, StatusCooldownSkipped, "")
			return nil
		}
	}

	// 9. Check if agent is currently running
	running, err := r.store.IsAgentRunning(ctx, agentName)
	if err != nil {
		return fmt.Errorf("check agent running: %w", err)
	}
	if running {
		r.logger.Info("agent already running, setting pending_work", "agent", agentName)
		_ = r.agentStore.SetPendingWork(ctx, agentName, true)
		r.recordSkippedRun(ctx, agentName, event, StatusQueued, "")
		return nil
	}

	// 10. All checks pass — create K8s Job
	return r.createJob(ctx, agent, event, depth)
}

// createJob creates a K8s Job for the reactive trigger.
func (r *Reactor) createJob(ctx context.Context, agent *agents.Agent, event dispatcher.MessageEvent, depth int) error {
	// Build handler from agent config
	handler := r.buildHandler(agent)

	body := event.Body
	if len(body) > 4096 {
		body = body[:4096] + " [truncated]"
	}

	msg := &k8spkg.JobMessage{
		MessageID: event.MessageID,
		FromAgent: event.FromAgent,
		Body:      body,
		Event:     event.EventType,
		Channel:   event.Channel,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	now := time.Now().UTC()
	run := &ReactiveRun{
		AgentName:        agent.Name,
		TriggerMessageID: &event.MessageID,
		TriggerEvent:     event.EventType,
		TriggerDepth:     depth,
		TriggerFrom:      event.FromAgent,
		Status:           StatusRunning,
		StartedAt:        &now,
	}

	// Insert run record first
	runID, err := r.store.InsertRun(ctx, run)
	if err != nil {
		return fmt.Errorf("insert reactive run: %w", err)
	}

	// Add trigger depth env var to handler
	handler.Env["SYNAPBUS_TRIGGER_DEPTH"] = fmt.Sprintf("%d", depth)

	// Create K8s Job
	jobName, err := r.runner.CreateJob(ctx, handler, msg)
	if err != nil {
		// Record failure
		errMsg := fmt.Sprintf("K8s Job creation failed: %s", err.Error())
		_ = r.store.CompleteRun(ctx, runID, StatusFailed, errMsg, time.Now().UTC())
		r.notifyFailure(ctx, agent, event, 0, errMsg)
		return fmt.Errorf("create K8s job: %w", err)
	}

	// Update run with job name
	ns := handler.Namespace
	if ns == "" {
		ns = r.runner.GetNamespace()
	}
	_ = r.store.UpdateRunStatus(ctx, runID, StatusRunning, jobName, ns, &now)

	// Clear pending_work since we're launching
	_ = r.agentStore.SetPendingWork(ctx, agent.Name, false)

	metrics.ReactiveTriggersTotal.WithLabelValues(agent.Name, StatusRunning).Inc()
	metrics.ReactiveAgentState.WithLabelValues(agent.Name).Set(1)

	r.logger.Info("reactive K8s Job created",
		"agent", agent.Name,
		"job", jobName,
		"trigger_from", event.FromAgent,
		"trigger_event", event.EventType,
		"depth", depth,
		"run_id", runID,
	)

	return nil
}

// buildHandler constructs a K8sHandler from agent config.
func (r *Reactor) buildHandler(agent *agents.Agent) *k8spkg.K8sHandler {
	env := map[string]string{}

	// Parse k8s_env_json
	if agent.K8sEnvJSON != "" {
		var envMap map[string]json.RawMessage
		if err := json.Unmarshal([]byte(agent.K8sEnvJSON), &envMap); err == nil {
			for k, v := range envMap {
				// Plain string values
				var str string
				if err := json.Unmarshal(v, &str); err == nil {
					env[k] = str
					continue
				}
				// Secret refs are handled at K8s level; for now pass as-is
				// (the K8s runner would need extension for secretKeyRef)
				env[k] = strings.Trim(string(v), "\"")
			}
		}
	}

	// Resource presets — default matches CronJob config (agent SDK needs ~1-2Gi)
	memory := "2Gi"
	cpu := "500m"
	if agent.K8sResourcePreset == "small" {
		memory = "512Mi"
		cpu = "100m"
	}

	timeout := 3600 // 1 hour (matches CronJob config)

	handler := &k8spkg.K8sHandler{
		AgentName:       agent.Name,
		Image:           agent.K8sImage,
		Events:          []string{"message.received", "message.mentioned"},
		Namespace:       "", // Use runner's namespace
		ResourcesMemory: memory,
		ResourcesCPU:    cpu,
		Env:             env,
		TimeoutSeconds:  timeout,
		Status:          "active",
		Args:            []string{"--max-turns", "50", "--model", "claude-sonnet-4-6"},
		VolumeMounts: []k8spkg.VolumeMount{
			{Name: "claude-config", MountPath: "/app/.claude", ReadOnly: false},
			{Name: "workspace", MountPath: "/app/workspace", ReadOnly: false},
		},
		Volumes: []k8spkg.Volume{
			{Name: "claude-config", HostPath: "/home/user/.claude"},
			{Name: "workspace", EmptyDir: true},
		},
	}

	// Override args for social-commenter (uses opus, more turns)
	if agent.Name == "social-commenter" {
		handler.Args = []string{"--max-turns", "80", "--model", "claude-opus-4-6"}
	}

	return handler
}

// RetryRun retries a failed run.
func (r *Reactor) RetryRun(ctx context.Context, runID int64) (*ReactiveRun, error) {
	run, err := r.store.GetRunByID(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}
	if run.Status != StatusFailed {
		return nil, fmt.Errorf("can only retry failed runs, current status: %s", run.Status)
	}

	agent, err := r.agentStore.GetAgentByName(ctx, run.AgentName)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}

	// Create a synthetic event for the retry
	event := dispatcher.MessageEvent{
		EventType: run.TriggerEvent,
		MessageID: 0,
		FromAgent: run.TriggerFrom,
		ToAgent:   run.AgentName,
		Body:      "",
		Depth:     run.TriggerDepth,
	}
	if run.TriggerMessageID != nil {
		event.MessageID = *run.TriggerMessageID
	}

	if err := r.createJob(ctx, agent, event, run.TriggerDepth); err != nil {
		return nil, err
	}

	// Return the newly created run
	runs, _, err := r.store.ListRuns(ctx, run.AgentName, StatusRunning, 1, 0)
	if err != nil || len(runs) == 0 {
		return nil, fmt.Errorf("retry succeeded but couldn't find new run")
	}
	return runs[0], nil
}

func (r *Reactor) recordSkippedRun(ctx context.Context, agentName string, event dispatcher.MessageEvent, status, errorLog string) {
	metrics.ReactiveTriggersTotal.WithLabelValues(agentName, status).Inc()
	run := &ReactiveRun{
		AgentName:    agentName,
		TriggerEvent: event.EventType,
		TriggerDepth: event.Depth,
		TriggerFrom:  event.FromAgent,
		Status:       status,
		ErrorLog:     errorLog,
	}
	if event.MessageID > 0 {
		run.TriggerMessageID = &event.MessageID
	}
	_, _ = r.store.InsertRun(ctx, run)
}

func (r *Reactor) notifyFailure(ctx context.Context, agent *agents.Agent, event dispatcher.MessageEvent, durationMs int64, errorSummary string) {
	if r.notifier == nil {
		return
	}
	// Find the owner's human agent name
	ownerAgent, err := r.agentStore.GetHumanAgentByOwner(ctx, agent.OwnerID)
	if err != nil || ownerAgent == nil {
		r.logger.Warn("could not find owner agent for failure notification", "agent", agent.Name)
		return
	}
	_ = r.notifier.NotifyFailure(ctx, ownerAgent.Name, agent.Name, event.FromAgent, event.EventType, durationMs, errorSummary)
}
