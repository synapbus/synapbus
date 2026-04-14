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
	"github.com/synapbus/synapbus/internal/harness"
	k8spkg "github.com/synapbus/synapbus/internal/k8s"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/metrics"

	"github.com/google/uuid"
)

// Backend kinds returned by agentBackendKind. These drive the dispatch
// fork: k8s uses the fast-return + async poller path; everything else
// goes through the harness registry in a goroutine.
const (
	backendK8s        = "k8s"
	backendSubprocess = "subprocess"
	backendWebhook    = "webhook"
	backendNone       = ""
)

// Reactor is the reactive agent triggering engine.
// SecretProvider builds the env map of scoped secrets to inject into a
// reactive subprocess run. Returning an error is non-fatal — the run
// proceeds without any injected secrets and the error is logged.
type SecretProvider interface {
	BuildEnvMap(ctx context.Context, userID, agentID, taskID int64) (map[string]string, error)
}

type Reactor struct {
	store          *Store
	agentStore     agents.AgentStore
	runner         k8spkg.JobRunner
	registry       *harness.Registry
	notifier       FailureNotifier
	reactions      ReactionNotifier
	secrets        SecretProvider
	logger         *slog.Logger
}

// FailureNotifier sends system DMs on job failure.
type FailureNotifier interface {
	NotifyFailure(ctx context.Context, ownerAgentName, agentName, triggerFrom, triggerEvent string, durationMs int64, errorSummary string) error
}

// ReactionNotifier lets the reactor mark the triggering DM with a
// workflow reaction (in_progress / done / reject) so the sender can
// see at a glance whether their message is being processed. Errors
// are logged, never returned — reactions are cosmetic and must not
// block the main dispatch flow.
type ReactionNotifier interface {
	AddReaction(ctx context.Context, messageID int64, agentName, reactionType string) error
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

// SetHarnessRegistry wires the harness registry used for non-K8s
// reactive runs (subprocess, webhook). K8s runs continue to go through
// the existing JobRunner + poller path for restart-safety.
func (r *Reactor) SetHarnessRegistry(reg *harness.Registry) {
	r.registry = reg
}

// SetSecretProvider wires the component that reads scoped secrets for
// the reactor to inject into subprocess runs as env vars.
func (r *Reactor) SetSecretProvider(p SecretProvider) {
	r.secrets = p
}

// SetReactionNotifier wires the component that marks triggering DMs
// with in_progress / done / reject reactions. Optional — a nil
// notifier simply skips the reaction step.
func (r *Reactor) SetReactionNotifier(n ReactionNotifier) {
	r.reactions = n
}

// react is a best-effort wrapper that swallows and logs errors so the
// dispatch flow is never blocked by a reaction-store hiccup.
func (r *Reactor) react(ctx context.Context, messageID int64, agentName, reactionType string) {
	if r.reactions == nil || messageID <= 0 {
		return
	}
	if err := r.reactions.AddReaction(ctx, messageID, agentName, reactionType); err != nil {
		r.logger.Debug("reaction notifier failed",
			"message_id", messageID,
			"agent", agentName,
			"reaction", reactionType,
			"error", err,
		)
	}
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
	// 0. Ignore system messages — stalemate worker notifications, retention warnings,
	// and other automated DMs should NOT trigger reactive runs. They're notifications
	// meant for the human owner, not actionable work for agents.
	if event.FromAgent == "system" {
		return nil
	}

	// 1. Get agent config
	agent, err := r.agentStore.GetAgentByName(ctx, agentName)
	if err != nil {
		return nil // Agent doesn't exist, skip silently
	}

	// 2. Check trigger mode
	if agent.TriggerMode != agents.TriggerModeReactive {
		return nil // Not reactive, skip
	}

	// 2a. Refuse to dispatch to a quarantined agent. Quarantine is set
	// when the agent's rolling reputation drops below the threshold
	// (0.3 by default). Existing in-flight runs are allowed to finish
	// but no new reactive runs will spawn.
	if agent.QuarantinedAt != nil {
		r.logger.Info("reactive dispatch to quarantined agent refused",
			"agent", agentName,
			"quarantined_at", agent.QuarantinedAt,
			"reason", agent.QuarantineReason,
		)
		r.recordSkippedRun(ctx, agentName, event, StatusFailed, "agent quarantined: "+agent.QuarantineReason)
		return nil
	}

	// 3. Pick a backend. K8s agents (k8s_image set) keep the existing
	// createJob + async poller path for restart safety. Everything else
	// goes through the harness registry in a goroutine.
	kind := r.agentBackendKind(agent)
	switch kind {
	case backendNone:
		r.logger.Warn("reactive agent has no backend configured",
			"agent", agentName,
			"has_k8s_image", agent.K8sImage != "",
			"has_local_command", agent.LocalCommand != "",
			"has_harness_config", agent.HarnessConfigJSON != "",
		)
		r.recordSkippedRun(ctx, agentName, event, StatusFailed, "no backend configured (set k8s_image, local_command, or harness_config_json.url)")
		return nil
	case backendK8s:
		// 4. K8s-specific precondition: runner must be available.
		if !r.runner.IsAvailable() {
			r.logger.Warn("K8s runner not available for reactive trigger", "agent", agentName)
			r.recordSkippedRun(ctx, agentName, event, StatusFailed, "K8s runner not available")
			return nil
		}
	case backendSubprocess, backendWebhook:
		// 4. Harness-specific precondition: registry must be wired and
		// the resolver must pick the backend we think we should get.
		if r.registry == nil {
			r.logger.Warn("harness registry not configured for non-K8s reactive trigger", "agent", agentName, "kind", kind)
			r.recordSkippedRun(ctx, agentName, event, StatusFailed, "harness registry not wired")
			return nil
		}
		if _, resolveErr := r.registry.Resolve(agent); resolveErr != nil {
			r.logger.Warn("harness registry cannot resolve backend",
				"agent", agentName, "kind", kind, "error", resolveErr,
			)
			r.recordSkippedRun(ctx, agentName, event, StatusFailed, "harness resolve: "+resolveErr.Error())
			return nil
		}
	}

	// 5. Extract depth from event metadata
	depth := event.Depth

	// 6. Check trigger depth (applies to ALL backends uniformly)
	if depth >= agent.MaxTriggerDepth {
		r.logger.Info("trigger depth exceeded", "agent", agentName, "depth", depth, "max", agent.MaxTriggerDepth)
		r.recordSkippedRun(ctx, agentName, event, StatusDepthExceeded, "")
		return nil
	}

	// 7. Check daily budget (applies to ALL backends uniformly)
	todayCount, err := r.store.CountTodayRuns(ctx, agentName)
	if err != nil {
		return fmt.Errorf("count today runs: %w", err)
	}
	if todayCount >= agent.DailyTriggerBudget {
		r.logger.Info("daily trigger budget exhausted", "agent", agentName, "count", todayCount, "budget", agent.DailyTriggerBudget)
		r.recordSkippedRun(ctx, agentName, event, StatusBudgetExhausted, "")
		return nil
	}

	// 8. Check cooldown (applies to ALL backends uniformly)
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

	// 9. Check if agent is currently running (applies to ALL backends)
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

	// 10. All checks pass — dispatch via the appropriate backend.
	if kind == backendK8s {
		return r.createJob(ctx, agent, event, depth)
	}
	return r.dispatchHarness(ctx, agent, event, depth)
}

// agentBackendKind inspects an agent's configuration and picks the
// single backend it should dispatch through. Priority matches the
// harness.Registry resolver defaults: k8s_image wins when set,
// local_command picks subprocess, harness_config_json with a url
// picks webhook. Returns backendNone when nothing is configured.
func (r *Reactor) agentBackendKind(agent *agents.Agent) string {
	if agent == nil {
		return backendNone
	}
	if agent.HarnessName != "" {
		// Explicit selection wins.
		switch agent.HarnessName {
		case "k8sjob":
			return backendK8s
		case "subprocess":
			return backendSubprocess
		case "webhook":
			return backendWebhook
		}
	}
	if agent.K8sImage != "" {
		return backendK8s
	}
	if agent.LocalCommand != "" {
		return backendSubprocess
	}
	if agent.HarnessConfigJSON != "" && strings.Contains(agent.HarnessConfigJSON, "\"url\"") {
		return backendWebhook
	}
	return backendNone
}

// dispatchHarness handles non-K8s reactive runs. It writes the
// ReactiveRun row synchronously (so callers see status=running on
// return, matching the K8s path), then spawns a goroutine that blocks
// on Registry.Execute and writes the terminal status when done.
func (r *Reactor) dispatchHarness(ctx context.Context, agent *agents.Agent, event dispatcher.MessageEvent, depth int) error {
	body := event.Body
	if len(body) > 4096 {
		body = body[:4096] + " [truncated]"
	}

	now := time.Now().UTC()
	run := &ReactiveRun{
		AgentName:    agent.Name,
		TriggerEvent: event.EventType,
		TriggerDepth: depth,
		TriggerFrom:  event.FromAgent,
		Status:       StatusRunning,
		StartedAt:    &now,
	}
	if event.MessageID > 0 {
		run.TriggerMessageID = &event.MessageID
	}

	runID, err := r.store.InsertRun(ctx, run)
	if err != nil {
		return fmt.Errorf("insert reactive run: %w", err)
	}

	_ = r.agentStore.SetPendingWork(ctx, agent.Name, false)
	metrics.ReactiveTriggersTotal.WithLabelValues(agent.Name, StatusRunning).Inc()
	metrics.ReactiveAgentState.WithLabelValues(agent.Name).Set(1)

	// Eyes-on: mark the triggering DM in_progress as the targeted
	// agent so the sender's Web UI shows visual progress.
	r.react(ctx, event.MessageID, agent.Name, "in_progress")

	r.logger.Info("reactive harness dispatch",
		"agent", agent.Name,
		"backend", r.agentBackendKind(agent),
		"trigger_from", event.FromAgent,
		"trigger_event", event.EventType,
		"depth", depth,
		"run_id", runID,
	)

	req := &harness.ExecRequest{
		RunID:         uuid.NewString(),
		AgentName:     agent.Name,
		Agent:         agent,
		ReactiveRunID: runID,
		Message: &messaging.Message{
			ID:        event.MessageID,
			FromAgent: event.FromAgent,
			ToAgent:   event.ToAgent,
			Body:      body,
		},
		Env: map[string]string{
			"SYNAPBUS_TRIGGER_DEPTH": fmt.Sprintf("%d", depth),
			"SYNAPBUS_EVENT":         event.EventType,
			"SYNAPBUS_FROM_AGENT":    event.FromAgent,
		},
	}

	// Inject scoped secrets as env vars (user + agent scope; task scope
	// is added when a task ID becomes available in the trigger path).
	if r.secrets != nil {
		if secretEnv, serr := r.secrets.BuildEnvMap(ctx, agent.OwnerID, agent.ID, 0); serr == nil {
			for k, v := range secretEnv {
				req.Env[k] = v
			}
		} else {
			r.logger.Warn("secret provider failed — continuing without injection",
				"agent", agent.Name, "error", serr)
		}
	}

	// Block on a detached context so a cancelled incoming request
	// does not kill in-flight work. Callers get a fast return above.
	go r.runHarness(runID, agent, event, req)
	return nil
}

// runHarness is the goroutine body that actually blocks on the
// harness. It MUST update the reactive_runs row on every exit path.
func (r *Reactor) runHarness(runID int64, agent *agents.Agent, event dispatcher.MessageEvent, req *harness.ExecRequest) {
	// Detached context — independent of the caller's Dispatch ctx so
	// the harness can outlive a short-lived HTTP handler. We still
	// propagate a generous timeout as a safety net.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
	defer cancel()

	startedAt := time.Now().UTC()

	res, err := r.registry.Execute(ctx, agent, req)
	completedAt := time.Now().UTC()
	durationMs := completedAt.Sub(startedAt).Milliseconds()

	status := StatusSucceeded
	errorLog := ""
	if err != nil {
		status = StatusFailed
		errorLog = err.Error()
	} else if res != nil && res.ExitCode != 0 {
		status = StatusFailed
		errorLog = res.Logs
		const logsCap = 8 * 1024
		if len(errorLog) > logsCap {
			errorLog = errorLog[len(errorLog)-logsCap:]
		}
	}

	_ = r.store.CompleteRun(ctx, runID, status, errorLog, completedAt)

	// Metrics — mirror what the poller does for K8s runs.
	metrics.ReactiveAgentState.WithLabelValues(agent.Name).Set(0)
	metrics.ReactiveRunDuration.WithLabelValues(agent.Name).Observe(float64(durationMs) / 1000.0)
	metrics.ReactiveTriggersTotal.WithLabelValues(agent.Name, status).Inc()
	todayCount, _ := r.store.CountTodayRuns(ctx, agent.Name)
	metrics.ReactiveBudgetUsed.WithLabelValues(agent.Name).Set(float64(todayCount))

	// Mark the triggering DM with the terminal reaction. Done wins
	// over in_progress via priority (see reactions.reactionPriority),
	// so we don't need to remove the in_progress reaction first.
	if status == StatusFailed {
		r.react(context.Background(), event.MessageID, agent.Name, "reject")
		r.logger.Warn("reactive harness run failed",
			"agent", agent.Name,
			"run_id", runID,
			"error", errorLog,
		)
		r.notifyFailure(context.Background(), agent, event, durationMs, errorLog)
	} else {
		r.react(context.Background(), event.MessageID, agent.Name, "done")
		r.logger.Info("reactive harness run succeeded",
			"agent", agent.Name,
			"run_id", runID,
			"duration_ms", durationMs,
		)
	}
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

	// Add trigger depth env var to handler
	handler.Env["SYNAPBUS_TRIGGER_DEPTH"] = fmt.Sprintf("%d", depth)

	// Create K8s Job FIRST (before DB insert to avoid stuck runs on SQLITE_BUSY)
	jobName, err := r.runner.CreateJob(ctx, handler, msg)
	if err != nil {
		errMsg := fmt.Sprintf("K8s Job creation failed: %s", err.Error())
		r.recordSkippedRun(ctx, agent.Name, event, StatusFailed, errMsg)
		r.notifyFailure(ctx, agent, event, 0, errMsg)
		return fmt.Errorf("create K8s job: %w", err)
	}

	ns := handler.Namespace
	if ns == "" {
		ns = r.runner.GetNamespace()
	}

	// Insert run record with job name already set (single atomic write)
	now := time.Now().UTC()
	run := &ReactiveRun{
		AgentName:        agent.Name,
		TriggerMessageID: &event.MessageID,
		TriggerEvent:     event.EventType,
		TriggerDepth:     depth,
		TriggerFrom:      event.FromAgent,
		Status:           StatusRunning,
		K8sJobName:       jobName,
		K8sNamespace:     ns,
		StartedAt:        &now,
	}

	runID, err := r.store.InsertRun(ctx, run)
	if err != nil {
		r.logger.Error("failed to record reactive run (job already created)",
			"agent", agent.Name, "job", jobName, "error", err)
		runID = 0
	}

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
