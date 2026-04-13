// Package k8sjob is the Kubernetes-Job-backed implementation of
// harness.Harness. It wraps the existing internal/k8s.JobRunner (used by
// the reactor today) behind the harness interface so the reactor, admin
// CLI, and future callers all go through the same seam.
package k8sjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
	k8spkg "github.com/synapbus/synapbus/internal/k8s"
)

// Waiter blocks until a Kubernetes Job terminates and reports the outcome.
// It is its own interface so the harness can be unit-tested with a fake.
type Waiter interface {
	Wait(ctx context.Context, namespace, jobName string) (JobOutcome, error)
	Cancel(ctx context.Context, namespace, jobName string) error
}

// JobOutcome is the terminal state of a watched Kubernetes Job.
type JobOutcome struct {
	Success       bool
	FailureReason string
}

// Harness is the k8sjob implementation of harness.Harness.
type Harness struct {
	runner k8spkg.JobRunner
	waiter Waiter
	logger *slog.Logger
}

// New constructs a k8sjob harness. Waiter may be nil when IsAvailable()
// is false (NoopRunner fallback) — in that case Execute returns an error
// rather than panicking.
func New(runner k8spkg.JobRunner, waiter Waiter, logger *slog.Logger) *Harness {
	if logger == nil {
		logger = slog.Default()
	}
	return &Harness{
		runner: runner,
		waiter: waiter,
		logger: logger.With("harness", "k8sjob"),
	}
}

// Name returns the registered harness name.
func (h *Harness) Name() string { return "k8sjob" }

// Capabilities advertises what the k8sjob backend supports.
func (h *Harness) Capabilities() harness.Capabilities {
	return harness.Capabilities{
		SystemPrompt:   false, // passed via env vars, not a dedicated slot
		SessionResume:  false, // each job is a cold start
		Skills:         false,
		OTelNative:     true, // child honours OTEL_* env vars
		MaxConcurrency: 10,
	}
}

// TestEnvironment checks the underlying runner is available.
func (h *Harness) TestEnvironment(ctx context.Context) error {
	if h.runner == nil || !h.runner.IsAvailable() {
		return errors.New("k8sjob: JobRunner is not available (not running in-cluster)")
	}
	return nil
}

// Provision is a no-op for k8sjob. Per-run configuration is passed
// entirely via env vars at Execute time.
func (h *Harness) Provision(ctx context.Context, agent *agents.Agent) error {
	return nil
}

// Execute builds a K8s Job for the request, waits for it to terminate,
// and returns the captured logs plus exit code.
//
// If the Waiter is nil (e.g. when constructed against a NoopRunner), the
// harness returns an error immediately rather than blocking forever.
func (h *Harness) Execute(ctx context.Context, req *harness.ExecRequest) (*harness.ExecResult, error) {
	if req == nil {
		return nil, errors.New("k8sjob: nil ExecRequest")
	}
	if req.Agent == nil {
		return nil, errors.New("k8sjob: ExecRequest.Agent is required")
	}
	if err := h.TestEnvironment(ctx); err != nil {
		return nil, err
	}
	if h.waiter == nil {
		return nil, errors.New("k8sjob: no Waiter configured")
	}

	handler := BuildHandler(req.Agent)
	// Merge caller-provided env overrides and run metadata on top of
	// whatever the handler already carries. Last write wins.
	if handler.Env == nil {
		handler.Env = map[string]string{}
	}
	handler.Env["SYNAPBUS_RUN_ID"] = req.RunID
	for k, v := range req.Env {
		handler.Env[k] = v
	}

	msg := buildJobMessage(req)
	if req.Budget.MaxWallClock > 0 {
		handler.TimeoutSeconds = int(req.Budget.MaxWallClock.Seconds())
	}

	jobName, err := h.runner.CreateJob(ctx, handler, msg)
	if err != nil {
		return nil, fmt.Errorf("k8sjob: create job: %w", err)
	}

	ns := handler.Namespace
	if ns == "" {
		ns = h.runner.GetNamespace()
	}

	h.logger.Info("k8sjob launched",
		"job_name", jobName,
		"namespace", ns,
		"agent", req.AgentName,
		"run_id", req.RunID,
	)

	outcome, err := h.waiter.Wait(ctx, ns, jobName)
	if err != nil {
		// Fetch whatever logs we can before bailing out.
		logs, _ := h.runner.GetJobLogs(ctx, ns, jobName)
		return &harness.ExecResult{
			ExitCode: 2, // distinguishable from plain "failed" (exit=1)
			Logs:     trimLogs(logs, 128),
		}, fmt.Errorf("k8sjob: wait: %w", err)
	}

	logs, logErr := h.runner.GetJobLogs(ctx, ns, jobName)
	if logErr != nil {
		h.logger.Warn("k8sjob: fetch logs failed",
			"job_name", jobName, "error", logErr,
		)
	}

	res := &harness.ExecResult{
		ExitCode: exitCodeFromOutcome(outcome),
		Logs:     trimLogs(logs, 128),
	}
	if !outcome.Success && outcome.FailureReason != "" {
		// Surface the K8s-reported reason in the logs excerpt so
		// callers writing to harness_runs can see both.
		if res.Logs != "" {
			res.Logs = outcome.FailureReason + "\n\n" + res.Logs
		} else {
			res.Logs = outcome.FailureReason
		}
	}

	// Best-effort: parse the tail of stdout as JSON (common pattern for
	// agents emitting a final result envelope). If it parses, stash it.
	if rj := extractResultJSON(logs); rj != nil {
		res.ResultJSON = rj
	}
	return res, nil
}

// Cancel deletes the K8s Job whose name equals runID. This is the
// convention used by SynapBus today: the harness returns the K8s Job
// name as the run identifier, so callers can cancel by run id.
func (h *Harness) Cancel(ctx context.Context, runID string) error {
	if h.waiter == nil {
		return errors.New("k8sjob: no Waiter configured")
	}
	ns := ""
	if h.runner != nil {
		ns = h.runner.GetNamespace()
	}
	return h.waiter.Cancel(ctx, ns, runID)
}

// -- helpers --------------------------------------------------------------

// BuildHandler constructs a K8sHandler from an agent config. Exported so
// the reactor can share the exact same logic when it eventually routes
// through the harness registry.
func BuildHandler(agent *agents.Agent) *k8spkg.K8sHandler {
	env := map[string]string{}

	if agent.K8sEnvJSON != "" {
		var envMap map[string]json.RawMessage
		if err := json.Unmarshal([]byte(agent.K8sEnvJSON), &envMap); err == nil {
			for k, v := range envMap {
				var s string
				if err := json.Unmarshal(v, &s); err == nil {
					env[k] = s
					continue
				}
				env[k] = strings.Trim(string(v), "\"")
			}
		}
	}

	memory := "2Gi"
	cpu := "500m"
	if agent.K8sResourcePreset == "small" {
		memory = "512Mi"
		cpu = "100m"
	}

	handler := &k8spkg.K8sHandler{
		AgentName:       agent.Name,
		Image:           agent.K8sImage,
		Events:          []string{"message.received", "message.mentioned"},
		ResourcesMemory: memory,
		ResourcesCPU:    cpu,
		Env:             env,
		TimeoutSeconds:  3600,
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

	if agent.Name == "social-commenter" {
		handler.Args = []string{"--max-turns", "80", "--model", "claude-opus-4-6"}
	}

	return handler
}

func buildJobMessage(req *harness.ExecRequest) *k8spkg.JobMessage {
	m := &k8spkg.JobMessage{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if req.Message != nil {
		m.MessageID = req.Message.ID
		m.FromAgent = req.Message.FromAgent
		m.Body = req.Message.Body
	}
	return m
}

func exitCodeFromOutcome(o JobOutcome) int {
	if o.Success {
		return 0
	}
	return 1
}

// trimLogs keeps the last n lines. Consistent with reactor poller's
// existing 100-line cap; we default a bit higher here.
func trimLogs(s string, n int) string {
	if s == "" || n <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// extractResultJSON looks for the last non-empty line of logs and tries
// to parse it as a JSON object. Returns nil on failure (very common —
// agents may not emit a result envelope at all).
func extractResultJSON(logs string) json.RawMessage {
	if logs == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(logs, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if len(line) < 2 || line[0] != '{' {
			return nil
		}
		var probe any
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			return nil
		}
		return json.RawMessage(line)
	}
	return nil
}
