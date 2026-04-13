package stub_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/harness/stub"
)

func TestStub_ImplementsHarness(t *testing.T) {
	var _ harness.Harness = (*stub.Harness)(nil)
}

func TestStub_NameAndCapabilities(t *testing.T) {
	s := stub.New()
	if s.Name() != "stub" {
		t.Fatalf("Name = %q, want stub", s.Name())
	}
	caps := s.Capabilities()
	if caps.OTelNative {
		t.Fatalf("stub should not claim OTelNative by default")
	}
}

func TestStub_Execute_ReturnsConfiguredResult(t *testing.T) {
	s := stub.New()
	s.Result = &harness.ExecResult{ExitCode: 42, Logs: "hi"}

	got, err := s.Execute(context.Background(), &harness.ExecRequest{RunID: "r", AgentName: "a"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got.ExitCode != 42 || got.Logs != "hi" {
		t.Fatalf("got %+v, want ExitCode=42 Logs=hi", got)
	}
	if len(s.Calls) != 1 {
		t.Fatalf("Calls len = %d, want 1", len(s.Calls))
	}
	if s.Calls[0].AgentName != "a" {
		t.Fatalf("recorded call agent = %q, want a", s.Calls[0].AgentName)
	}
}

func TestStub_Execute_ReturnsError(t *testing.T) {
	s := stub.New()
	wantErr := errors.New("nope")
	s.Err = wantErr

	_, err := s.Execute(context.Background(), &harness.ExecRequest{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestStub_Execute_RespectsContextCancel(t *testing.T) {
	s := stub.New()
	s.ExecDelay = 2 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := s.Execute(ctx, &harness.ExecRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("context cancel did not interrupt delay")
	}
}

func TestStub_Cancel_RecordsRunID(t *testing.T) {
	s := stub.New()
	if err := s.Cancel(context.Background(), "r-1"); err != nil {
		t.Fatalf("Cancel error: %v", err)
	}
	if len(s.CancelCalls) != 1 || s.CancelCalls[0] != "r-1" {
		t.Fatalf("CancelCalls = %v, want [r-1]", s.CancelCalls)
	}
}

func TestStub_Provision_ReturnsConfiguredError(t *testing.T) {
	s := stub.New()
	s.ProvisionErr = errors.New("bad")
	err := s.Provision(context.Background(), &agents.Agent{Name: "a"})
	if err == nil || err.Error() != "bad" {
		t.Fatalf("Provision err = %v, want 'bad'", err)
	}
}

func TestStub_TestEnvironment_ReturnsConfiguredError(t *testing.T) {
	s := stub.New()
	if err := s.TestEnvironment(context.Background()); err != nil {
		t.Fatalf("default TestEnvironment err = %v, want nil", err)
	}
	s.PreflightErr = errors.New("nope")
	if err := s.TestEnvironment(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
