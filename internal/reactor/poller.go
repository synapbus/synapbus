package reactor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/dispatcher"
	k8spkg "github.com/synapbus/synapbus/internal/k8s"
	"github.com/synapbus/synapbus/internal/metrics"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Poller watches active reactive runs and updates their status from K8s.
type Poller struct {
	store      *Store
	agentStore agents.AgentStore
	clientset  kubernetes.Interface
	runner     k8spkg.JobRunner
	reactor    *Reactor
	interval   time.Duration
	logger     *slog.Logger
	stopCh     chan struct{}
}

// NewPoller creates a new job status poller.
func NewPoller(store *Store, agentStore agents.AgentStore, runner k8spkg.JobRunner, reactor *Reactor, logger *slog.Logger) *Poller {
	// Extract clientset from runner if it's the real K8s runner
	var clientset kubernetes.Interface
	if kr, ok := runner.(*k8spkg.K8sJobRunner); ok {
		clientset = kr.GetClientset()
	}

	return &Poller{
		store:      store,
		agentStore: agentStore,
		clientset:  clientset,
		runner:     runner,
		reactor:    reactor,
		interval:   15 * time.Second,
		logger:     logger.With("component", "reactor-poller"),
		stopCh:     make(chan struct{}),
	}
}

// Start begins the polling loop in a background goroutine.
func (p *Poller) Start() {
	if !p.runner.IsAvailable() || p.clientset == nil {
		p.logger.Info("K8s not available, reactor poller disabled")
		return
	}
	go p.pollLoop()
	p.logger.Info("reactor poller started", "interval", p.interval)
}

// Stop signals the poller to stop.
func (p *Poller) Stop() {
	close(p.stopCh)
}

func (p *Poller) pollLoop() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.pollActiveRuns()
		}
	}
}

func (p *Poller) pollActiveRuns() {
	ctx := context.Background()

	runs, err := p.store.GetActiveRuns(ctx)
	if err != nil {
		p.logger.Error("failed to get active runs", "error", err)
		return
	}

	for _, run := range runs {
		if run.K8sJobName == "" || run.K8sNamespace == "" {
			continue
		}
		p.checkJob(ctx, run)
	}
}

func (p *Poller) checkJob(ctx context.Context, run *ReactiveRun) {
	ns := run.K8sNamespace
	jobName := run.K8sJobName

	job, err := p.clientset.BatchV1().Jobs(ns).Get(ctx, jobName, metav1.GetOptions{})
	if err != nil {
		p.logger.Warn("failed to get K8s Job status", "job", jobName, "namespace", ns, "error", err)
		return
	}

	// Check job conditions
	for _, cond := range job.Status.Conditions {
		switch cond.Type {
		case batchv1.JobComplete:
			if cond.Status == "True" {
				p.handleJobComplete(ctx, run, true, "")
				return
			}
		case batchv1.JobFailed:
			if cond.Status == "True" {
				reason := cond.Reason
				if cond.Message != "" {
					reason = reason + ": " + cond.Message
				}
				p.handleJobComplete(ctx, run, false, reason)
				return
			}
		}
	}

	// Check if active deadline exceeded
	if job.Status.Failed > 0 {
		p.handleJobComplete(ctx, run, false, "job failed (pod failure)")
		return
	}
}

func (p *Poller) handleJobComplete(ctx context.Context, run *ReactiveRun, success bool, failureReason string) {
	now := time.Now().UTC()

	// Update metrics
	metrics.ReactiveAgentState.WithLabelValues(run.AgentName).Set(0)
	if run.StartedAt != nil {
		duration := now.Sub(*run.StartedAt).Seconds()
		metrics.ReactiveRunDuration.WithLabelValues(run.AgentName).Observe(duration)
	}
	todayCount, _ := p.store.CountTodayRuns(ctx, run.AgentName)
	metrics.ReactiveBudgetUsed.WithLabelValues(run.AgentName).Set(float64(todayCount))

	if success {
		metrics.ReactiveTriggersTotal.WithLabelValues(run.AgentName, StatusSucceeded).Inc()
		_ = p.store.CompleteRun(ctx, run.ID, StatusSucceeded, "", now)
		p.logger.Info("reactive run succeeded",
			"agent", run.AgentName,
			"job", run.K8sJobName,
			"run_id", run.ID,
		)
	} else {
		// Retrieve logs
		errorLog := failureReason
		logs, err := p.runner.GetJobLogs(ctx, run.K8sNamespace, run.K8sJobName)
		if err == nil && logs != "" {
			// Keep last 100 lines
			lines := strings.Split(logs, "\n")
			if len(lines) > 100 {
				lines = lines[len(lines)-100:]
			}
			errorLog = strings.Join(lines, "\n")
		}

		metrics.ReactiveTriggersTotal.WithLabelValues(run.AgentName, StatusFailed).Inc()
		_ = p.store.CompleteRun(ctx, run.ID, StatusFailed, errorLog, now)

		p.logger.Warn("reactive run failed",
			"agent", run.AgentName,
			"job", run.K8sJobName,
			"run_id", run.ID,
			"reason", failureReason,
		)

		// Send failure notification
		var durationMs int64
		if run.StartedAt != nil {
			durationMs = now.Sub(*run.StartedAt).Milliseconds()
		}
		agent, err := p.agentStore.GetAgentByName(ctx, run.AgentName)
		if err == nil && agent != nil {
			event := dispatcher.MessageEvent{
				EventType: run.TriggerEvent,
				FromAgent: run.TriggerFrom,
			}
			p.reactor.notifyFailure(ctx, agent, event, durationMs, fmt.Sprintf("Job %s failed: %s", run.K8sJobName, failureReason))
		}
	}

	// Check for pending_work — launch coalesced run if needed
	p.checkPendingWork(ctx, run.AgentName)
}

func (p *Poller) checkPendingWork(ctx context.Context, agentName string) {
	agent, err := p.agentStore.GetAgentByName(ctx, agentName)
	if err != nil {
		return
	}

	if !agent.PendingWork {
		return
	}

	// Clear pending_work first
	_ = p.agentStore.SetPendingWork(ctx, agentName, false)

	p.logger.Info("pending_work found, launching coalesced run", "agent", agentName)

	// Create a synthetic event (coalesced — agent will pick up all pending messages via claim_messages)
	event := dispatcher.MessageEvent{
		EventType:       "message.received",
		FromAgent:       "__coalesced__",
		ToAgent:         agentName,
		Body:            "Coalesced trigger: process all pending messages.",
		MentionedAgents: nil,
		Depth:           0,
	}

	// Evaluate the trigger (it will check cooldown/budget again)
	_ = p.reactor.evaluateTrigger(ctx, agentName, event)
}
