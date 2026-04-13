package k8sjob_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/harness/k8sjob"
	k8spkg "github.com/synapbus/synapbus/internal/k8s"
	"github.com/synapbus/synapbus/internal/messaging"
)

// fakeRunner is a copy of the reactor test's fakeRunner — wrapping
// k8spkg.JobRunner so we don't pull in a real clientset.
type fakeRunner struct {
	available   bool
	lastEnv     map[string]string
	lastHandler *k8spkg.K8sHandler
	lastMessage *k8spkg.JobMessage
	createErr   error
	logs        string
	logsErr     error
}

func (f *fakeRunner) IsAvailable() bool     { return f.available }
func (f *fakeRunner) GetNamespace() string  { return "test-ns" }
func (f *fakeRunner) GetJobLogs(_ context.Context, _, _ string) (string, error) {
	return f.logs, f.logsErr
}

func (f *fakeRunner) CreateJob(_ context.Context, handler *k8spkg.K8sHandler, msg *k8spkg.JobMessage) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	f.lastHandler = handler
	f.lastMessage = msg
	f.lastEnv = make(map[string]string, len(handler.Env))
	for k, v := range handler.Env {
		f.lastEnv[k] = v
	}
	return "synapbus-" + handler.AgentName + "-job", nil
}

type fakeWaiter struct {
	outcome k8sjob.JobOutcome
	err     error
	delay   time.Duration

	cancelCalls []string
}

func (w *fakeWaiter) Wait(ctx context.Context, ns, jobName string) (k8sjob.JobOutcome, error) {
	if w.delay > 0 {
		select {
		case <-time.After(w.delay):
		case <-ctx.Done():
			return k8sjob.JobOutcome{}, ctx.Err()
		}
	}
	return w.outcome, w.err
}

func (w *fakeWaiter) Cancel(_ context.Context, _ string, jobName string) error {
	w.cancelCalls = append(w.cancelCalls, jobName)
	return nil
}

func newTestAgent(name, image string) *agents.Agent {
	return &agents.Agent{
		Name:              name,
		K8sImage:          image,
		K8sResourcePreset: "default",
	}
}

func TestHarness_ImplementsInterface(t *testing.T) {
	var _ harness.Harness = (*k8sjob.Harness)(nil)
}

func TestHarness_NameAndCapabilities(t *testing.T) {
	h := k8sjob.New(nil, nil, nil)
	if h.Name() != "k8sjob" {
		t.Fatalf("Name = %q", h.Name())
	}
	caps := h.Capabilities()
	if !caps.OTelNative {
		t.Fatal("expected OTelNative = true")
	}
	if caps.MaxConcurrency == 0 {
		t.Fatal("expected non-zero MaxConcurrency")
	}
}

func TestHarness_TestEnvironment_NoRunner(t *testing.T) {
	h := k8sjob.New(nil, nil, nil)
	if err := h.TestEnvironment(context.Background()); err == nil {
		t.Fatal("expected error for nil runner")
	}
}

func TestHarness_TestEnvironment_Unavailable(t *testing.T) {
	h := k8sjob.New(&fakeRunner{available: false}, nil, nil)
	if err := h.TestEnvironment(context.Background()); err == nil {
		t.Fatal("expected error for unavailable runner")
	}
}

func TestHarness_TestEnvironment_OK(t *testing.T) {
	h := k8sjob.New(&fakeRunner{available: true}, nil, nil)
	if err := h.TestEnvironment(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHarness_Provision_NoOp(t *testing.T) {
	h := k8sjob.New(&fakeRunner{available: true}, nil, nil)
	if err := h.Provision(context.Background(), newTestAgent("a", "foo:v1")); err != nil {
		t.Fatalf("Provision err = %v, want nil", err)
	}
}

func TestHarness_Execute_Success(t *testing.T) {
	runner := &fakeRunner{
		available: true,
		logs:      "hello world\n{\"ok\":true,\"tokens\":42}",
	}
	waiter := &fakeWaiter{outcome: k8sjob.JobOutcome{Success: true}}
	h := k8sjob.New(runner, waiter, nil)

	req := &harness.ExecRequest{
		RunID:     "r-1",
		AgentName: "researcher",
		Agent:     newTestAgent("researcher", "ghcr.io/example/agent:v1"),
		Message: &messaging.Message{
			ID:        42,
			FromAgent: "human",
			Body:      "do the thing",
		},
		Env: map[string]string{"EXTRA": "yes"},
	}

	res, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Logs, "hello world") {
		t.Errorf("logs missing stdout: %q", res.Logs)
	}
	if string(res.ResultJSON) == "" {
		t.Errorf("ResultJSON empty, want parsed envelope")
	}
	if !strings.Contains(string(res.ResultJSON), "\"ok\":true") {
		t.Errorf("ResultJSON not parsed: %s", res.ResultJSON)
	}

	// Verify env propagation
	if runner.lastEnv["SYNAPBUS_RUN_ID"] != "r-1" {
		t.Errorf("SYNAPBUS_RUN_ID = %q, want r-1", runner.lastEnv["SYNAPBUS_RUN_ID"])
	}
	if runner.lastEnv["EXTRA"] != "yes" {
		t.Errorf("EXTRA env not propagated: %v", runner.lastEnv)
	}
	// Verify JobMessage carried message context
	if runner.lastMessage.MessageID != 42 {
		t.Errorf("MessageID = %d", runner.lastMessage.MessageID)
	}
	if runner.lastMessage.FromAgent != "human" {
		t.Errorf("FromAgent = %q", runner.lastMessage.FromAgent)
	}
}

func TestHarness_Execute_Failure(t *testing.T) {
	runner := &fakeRunner{
		available: true,
		logs:      "error: file not found",
	}
	waiter := &fakeWaiter{
		outcome: k8sjob.JobOutcome{Success: false, FailureReason: "BackoffLimitExceeded"},
	}
	h := k8sjob.New(runner, waiter, nil)

	req := &harness.ExecRequest{
		RunID:     "r-2",
		AgentName: "researcher",
		Agent:     newTestAgent("researcher", "ghcr.io/example/agent:v1"),
	}
	res, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute err = %v, want nil for graceful failure", err)
	}
	if res.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", res.ExitCode)
	}
	if !strings.Contains(res.Logs, "BackoffLimitExceeded") {
		t.Errorf("logs missing failure reason: %q", res.Logs)
	}
}

func TestHarness_Execute_CreateJobError(t *testing.T) {
	runner := &fakeRunner{
		available: true,
		createErr: errors.New("api server down"),
	}
	h := k8sjob.New(runner, &fakeWaiter{}, nil)
	_, err := h.Execute(context.Background(), &harness.ExecRequest{
		RunID: "r", AgentName: "a", Agent: newTestAgent("a", "foo:v1"),
	})
	if err == nil || !strings.Contains(err.Error(), "create job") {
		t.Fatalf("err = %v, want create job error", err)
	}
}

func TestHarness_Execute_NilAgent(t *testing.T) {
	h := k8sjob.New(&fakeRunner{available: true}, &fakeWaiter{}, nil)
	_, err := h.Execute(context.Background(), &harness.ExecRequest{RunID: "r"})
	if err == nil {
		t.Fatal("expected error for nil agent")
	}
}

func TestHarness_Execute_NilRequest(t *testing.T) {
	h := k8sjob.New(&fakeRunner{available: true}, &fakeWaiter{}, nil)
	_, err := h.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestHarness_Execute_BudgetOverridesTimeout(t *testing.T) {
	runner := &fakeRunner{available: true}
	waiter := &fakeWaiter{outcome: k8sjob.JobOutcome{Success: true}}
	h := k8sjob.New(runner, waiter, nil)

	req := &harness.ExecRequest{
		RunID:     "r",
		AgentName: "a",
		Agent:     newTestAgent("a", "foo:v1"),
		Budget:    harness.Budget{MaxWallClock: 90 * time.Second},
	}
	_, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if runner.lastHandler.TimeoutSeconds != 90 {
		t.Errorf("TimeoutSeconds = %d, want 90", runner.lastHandler.TimeoutSeconds)
	}
}

func TestHarness_Execute_ContextCancel(t *testing.T) {
	runner := &fakeRunner{available: true}
	waiter := &fakeWaiter{
		outcome: k8sjob.JobOutcome{Success: true},
		delay:   500 * time.Millisecond,
	}
	h := k8sjob.New(runner, waiter, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := h.Execute(ctx, &harness.ExecRequest{
		RunID: "r", AgentName: "a", Agent: newTestAgent("a", "foo:v1"),
	})
	if err == nil {
		t.Fatal("expected error when context cancelled before wait completes")
	}
}

func TestHarness_Cancel_PropagatesToWaiter(t *testing.T) {
	runner := &fakeRunner{available: true}
	waiter := &fakeWaiter{}
	h := k8sjob.New(runner, waiter, nil)
	if err := h.Cancel(context.Background(), "my-job"); err != nil {
		t.Fatalf("Cancel err = %v", err)
	}
	if len(waiter.cancelCalls) != 1 || waiter.cancelCalls[0] != "my-job" {
		t.Fatalf("cancelCalls = %v", waiter.cancelCalls)
	}
}

func TestBuildHandler_AppliesResourcePreset(t *testing.T) {
	a := newTestAgent("small-agent", "x:v1")
	a.K8sResourcePreset = "small"
	h := k8sjob.BuildHandler(a)
	if h.ResourcesMemory != "512Mi" || h.ResourcesCPU != "100m" {
		t.Errorf("small preset: got mem=%s cpu=%s", h.ResourcesMemory, h.ResourcesCPU)
	}
}

func TestBuildHandler_SocialCommenterUsesOpus(t *testing.T) {
	a := newTestAgent("social-commenter", "x:v1")
	h := k8sjob.BuildHandler(a)
	found := false
	for _, arg := range h.Args {
		if arg == "claude-opus-4-6" {
			found = true
		}
	}
	if !found {
		t.Errorf("social-commenter args = %v, want opus", h.Args)
	}
}

func TestBuildHandler_ParsesK8sEnvJSON(t *testing.T) {
	a := newTestAgent("a", "x:v1")
	a.K8sEnvJSON = `{"GIT_REPO":"owner/repo","OTHER":"val"}`
	h := k8sjob.BuildHandler(a)
	if h.Env["GIT_REPO"] != "owner/repo" {
		t.Errorf("GIT_REPO = %q", h.Env["GIT_REPO"])
	}
	if h.Env["OTHER"] != "val" {
		t.Errorf("OTHER = %q", h.Env["OTHER"])
	}
}
