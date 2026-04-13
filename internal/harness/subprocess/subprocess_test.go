package subprocess_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/harness/subprocess"
	"github.com/synapbus/synapbus/internal/messaging"
)

func requirePosix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("subprocess tests use /bin/sh; not applicable on Windows")
	}
}

func newAgent(cmd string) *agents.Agent {
	return &agents.Agent{Name: "test-agent", LocalCommand: cmd}
}

func TestSubprocess_ImplementsInterface(t *testing.T) {
	var _ harness.Harness = (*subprocess.Harness)(nil)
}

func TestSubprocess_NameAndCapabilities(t *testing.T) {
	h := subprocess.New(subprocess.Config{}, nil)
	if h.Name() != "subprocess" {
		t.Fatalf("Name = %q", h.Name())
	}
	caps := h.Capabilities()
	if !caps.SystemPrompt || !caps.SessionResume || !caps.OTelNative {
		t.Fatalf("caps missing expected flags: %+v", caps)
	}
}

func TestSubprocess_TestEnvironment_OK(t *testing.T) {
	h := subprocess.New(subprocess.Config{BaseDir: t.TempDir()}, nil)
	if err := h.TestEnvironment(context.Background()); err != nil {
		t.Fatalf("TestEnvironment err = %v", err)
	}
}

func TestSubprocess_TestEnvironment_MissingDir(t *testing.T) {
	h := subprocess.New(subprocess.Config{BaseDir: "/nonexistent/does/not/exist/xyz"}, nil)
	if err := h.TestEnvironment(context.Background()); err == nil {
		t.Fatal("expected error for missing base dir")
	}
}

func TestSubprocess_Execute_Success(t *testing.T) {
	requirePosix(t)

	h := subprocess.New(subprocess.Config{BaseDir: t.TempDir()}, nil)
	req := &harness.ExecRequest{
		RunID:     "run-success",
		AgentName: "test",
		Agent:     newAgent(`["sh","-c","echo hello world"]`),
	}
	res, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Logs, "hello world") {
		t.Errorf("logs missing stdout: %q", res.Logs)
	}
}

func TestSubprocess_Execute_NonZeroExit(t *testing.T) {
	requirePosix(t)

	h := subprocess.New(subprocess.Config{BaseDir: t.TempDir()}, nil)
	req := &harness.ExecRequest{
		RunID:     "run-fail",
		AgentName: "test",
		Agent:     newAgent(`["sh","-c","echo oops; exit 7"]`),
	}
	res, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", res.ExitCode)
	}
	if !strings.Contains(res.Logs, "oops") {
		t.Errorf("logs missing stdout: %q", res.Logs)
	}
}

func TestSubprocess_Execute_StderrCaptured(t *testing.T) {
	requirePosix(t)

	h := subprocess.New(subprocess.Config{BaseDir: t.TempDir()}, nil)
	req := &harness.ExecRequest{
		RunID:     "run-stderr",
		AgentName: "test",
		Agent:     newAgent(`["sh","-c","echo first; echo bad >&2; exit 0"]`),
	}
	res, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if !strings.Contains(res.Logs, "first") {
		t.Errorf("logs missing stdout: %q", res.Logs)
	}
	if !strings.Contains(res.Logs, "bad") {
		t.Errorf("logs missing stderr: %q", res.Logs)
	}
	if !strings.Contains(res.Logs, "-- stderr --") {
		t.Errorf("logs missing stderr header: %q", res.Logs)
	}
}

func TestSubprocess_Execute_EnvPropagation(t *testing.T) {
	requirePosix(t)

	h := subprocess.New(subprocess.Config{BaseDir: t.TempDir()}, nil)
	req := &harness.ExecRequest{
		RunID:     "run-env",
		AgentName: "my-agent",
		Agent:     newAgent(`["sh","-c","echo run=$SYNAPBUS_RUN_ID agent=$SYNAPBUS_AGENT custom=$CUSTOM"]`),
		Env:       map[string]string{"CUSTOM": "xyz"},
	}
	res, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if !strings.Contains(res.Logs, "run=run-env") {
		t.Errorf("run id not propagated: %q", res.Logs)
	}
	if !strings.Contains(res.Logs, "agent=my-agent") {
		t.Errorf("agent name not propagated: %q", res.Logs)
	}
	if !strings.Contains(res.Logs, "custom=xyz") {
		t.Errorf("caller env not propagated: %q", res.Logs)
	}
}

func TestSubprocess_Execute_ReadsResultJSON(t *testing.T) {
	requirePosix(t)

	baseDir := t.TempDir()
	h := subprocess.New(subprocess.Config{BaseDir: baseDir, KeepWorkdirOnSuccess: true}, nil)
	req := &harness.ExecRequest{
		RunID:     "run-json",
		AgentName: "test",
		Agent: newAgent(`["sh","-c","printf '{\"answer\":42,\"ok\":true}' > result.json"]`),
	}
	res, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if len(res.ResultJSON) == 0 {
		t.Fatal("ResultJSON empty")
	}
	if !strings.Contains(string(res.ResultJSON), `"answer":42`) {
		t.Errorf("ResultJSON = %s", res.ResultJSON)
	}

	// Verify the workdir is still present (KeepWorkdirOnSuccess=true)
	if _, err := os.Stat(filepath.Join(baseDir, "run-json", "result.json")); err != nil {
		t.Errorf("workdir removed despite KeepWorkdirOnSuccess: %v", err)
	}
}

func TestSubprocess_Execute_WritesMessageJSON(t *testing.T) {
	requirePosix(t)

	baseDir := t.TempDir()
	h := subprocess.New(subprocess.Config{BaseDir: baseDir, KeepWorkdirOnSuccess: true}, nil)
	req := &harness.ExecRequest{
		RunID:     "run-msg",
		AgentName: "test",
		Agent:     newAgent(`["sh","-c","cat message.json > result.json"]`),
		Message: &messaging.Message{
			ID:        123,
			FromAgent: "alice",
			Body:      "hi",
		},
	}
	res, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if !strings.Contains(string(res.ResultJSON), `"from_agent":"alice"`) {
		t.Errorf("message.json missing from_agent: %s", res.ResultJSON)
	}
	if !strings.Contains(string(res.ResultJSON), `"body":"hi"`) {
		t.Errorf("message.json missing body: %s", res.ResultJSON)
	}
}

func TestSubprocess_Execute_WorkdirCleanedOnSuccess(t *testing.T) {
	requirePosix(t)

	baseDir := t.TempDir()
	h := subprocess.New(subprocess.Config{BaseDir: baseDir}, nil)
	req := &harness.ExecRequest{
		RunID:     "run-clean",
		AgentName: "test",
		Agent:     newAgent(`["sh","-c","true"]`),
	}
	_, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "run-clean")); !os.IsNotExist(err) {
		t.Errorf("workdir not cleaned up: err=%v", err)
	}
}

func TestSubprocess_Execute_WorkdirKeptOnFailure(t *testing.T) {
	requirePosix(t)

	baseDir := t.TempDir()
	h := subprocess.New(subprocess.Config{BaseDir: baseDir}, nil)
	req := &harness.ExecRequest{
		RunID:     "run-forensic",
		AgentName: "test",
		Agent:     newAgent(`["sh","-c","exit 5"]`),
	}
	_, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "run-forensic")); err != nil {
		t.Errorf("workdir removed on failure (should be kept): %v", err)
	}
}

func TestSubprocess_Execute_BudgetTimeout(t *testing.T) {
	requirePosix(t)

	h := subprocess.New(subprocess.Config{BaseDir: t.TempDir()}, nil)
	req := &harness.ExecRequest{
		RunID:     "run-timeout",
		AgentName: "test",
		Agent:     newAgent(`["sh","-c","sleep 5"]`),
		Budget:    harness.Budget{MaxWallClock: 50 * time.Millisecond},
	}
	start := time.Now()
	_, err := h.Execute(context.Background(), req)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected budget timeout error")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Errorf("err = %v, want 'budget' in message", err)
	}
	if elapsed > time.Second {
		t.Errorf("budget not enforced; elapsed = %s", elapsed)
	}
}

func TestSubprocess_Execute_ContextCancel(t *testing.T) {
	requirePosix(t)

	h := subprocess.New(subprocess.Config{BaseDir: t.TempDir()}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	req := &harness.ExecRequest{
		RunID:     "run-cancel",
		AgentName: "test",
		Agent:     newAgent(`["sh","-c","sleep 5"]`),
	}
	start := time.Now()
	_, _ = h.Execute(ctx, req)
	if time.Since(start) > time.Second {
		t.Error("context cancel did not stop child")
	}
}

func TestSubprocess_Execute_NilAgent(t *testing.T) {
	h := subprocess.New(subprocess.Config{BaseDir: t.TempDir()}, nil)
	_, err := h.Execute(context.Background(), &harness.ExecRequest{RunID: "r"})
	if err == nil {
		t.Fatal("expected error for nil agent")
	}
}

func TestSubprocess_Execute_NoLocalCommand(t *testing.T) {
	h := subprocess.New(subprocess.Config{BaseDir: t.TempDir()}, nil)
	_, err := h.Execute(context.Background(), &harness.ExecRequest{
		RunID: "r",
		Agent: &agents.Agent{Name: "no-cmd"},
	})
	if err == nil {
		t.Fatal("expected ErrNoLocalCommand")
	}
}

func TestSubprocess_Execute_AcceptsWhitespaceArgvForm(t *testing.T) {
	requirePosix(t)

	h := subprocess.New(subprocess.Config{BaseDir: t.TempDir()}, nil)
	// Non-JSON form: simple whitespace-split.
	req := &harness.ExecRequest{
		RunID:     "run-ws",
		AgentName: "test",
		Agent:     &agents.Agent{Name: "a", LocalCommand: "echo plain-form"},
	}
	res, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if !strings.Contains(res.Logs, "plain-form") {
		t.Errorf("whitespace form failed: %q", res.Logs)
	}
}
