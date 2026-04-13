package harness_test

import (
	"context"
	"errors"
	"testing"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/harness/stub"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := harness.NewRegistry()
	s := stub.New()
	s.NameStr = "stub"
	r.Register(s)

	got, err := r.Get("stub")
	if err != nil {
		t.Fatalf("Get(stub) error: %v", err)
	}
	if got != s {
		t.Fatalf("Get returned %v, want %v", got, s)
	}

	if _, err := r.Get("missing"); !errors.Is(err, harness.ErrUnknownHarness) {
		t.Fatalf("Get(missing) error = %v, want ErrUnknownHarness", err)
	}
}

func TestRegistry_Names(t *testing.T) {
	r := harness.NewRegistry()
	a := stub.New()
	a.NameStr = "a"
	b := stub.New()
	b.NameStr = "b"
	r.Register(a)
	r.Register(b)

	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("Names len=%d, want 2 (%v)", len(names), names)
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("Names missing entries: %v", names)
	}
}

func TestRegistry_RegisterReplaces(t *testing.T) {
	r := harness.NewRegistry()
	first := stub.New()
	first.NameStr = "stub"
	second := stub.New()
	second.NameStr = "stub"
	r.Register(first)
	r.Register(second)

	got, err := r.Get("stub")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if got != second {
		t.Fatalf("replacement failed: got %v, want %v", got, second)
	}
}

func TestRegistry_Resolve_K8sImageWinsWhenRegistered(t *testing.T) {
	r := harness.NewRegistry()
	k8s := stub.New()
	k8s.NameStr = "k8sjob"
	web := stub.New()
	web.NameStr = "webhook"
	r.Register(k8s)
	r.Register(web)

	a := &agents.Agent{Name: "a", K8sImage: "ghcr.io/foo/bar:v1"}
	got, err := r.Resolve(a)
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got != k8s {
		t.Fatalf("Resolve picked %v, want k8sjob", got.Name())
	}
}

func TestRegistry_Resolve_FallsBackToWebhook(t *testing.T) {
	r := harness.NewRegistry()
	web := stub.New()
	web.NameStr = "webhook"
	r.Register(web)

	a := &agents.Agent{Name: "a"}
	got, err := r.Resolve(a)
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got != web {
		t.Fatalf("Resolve picked %v, want webhook", got.Name())
	}
}

func TestRegistry_Resolve_FallsBackToSubprocess(t *testing.T) {
	r := harness.NewRegistry()
	sub := stub.New()
	sub.NameStr = "subprocess"
	r.Register(sub)

	a := &agents.Agent{Name: "a"}
	got, err := r.Resolve(a)
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got != sub {
		t.Fatalf("Resolve picked %v, want subprocess", got.Name())
	}
}

func TestRegistry_Resolve_ErrNoBackend(t *testing.T) {
	r := harness.NewRegistry()
	a := &agents.Agent{Name: "a"}
	if _, err := r.Resolve(a); !errors.Is(err, harness.ErrNoBackend) {
		t.Fatalf("err = %v, want ErrNoBackend", err)
	}
}

func TestRegistry_Resolve_K8sImageSetButNoK8sBackend_FallsThrough(t *testing.T) {
	// Agent has k8s_image but no k8sjob backend is registered — should
	// fall through to webhook/subprocess, not fail.
	r := harness.NewRegistry()
	web := stub.New()
	web.NameStr = "webhook"
	r.Register(web)

	a := &agents.Agent{Name: "a", K8sImage: "foo:v1"}
	got, err := r.Resolve(a)
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got != web {
		t.Fatalf("Resolve picked %v, want webhook", got.Name())
	}
}

func TestRegistry_Resolve_CustomResolveFn(t *testing.T) {
	r := harness.NewRegistry()
	a := stub.New()
	a.NameStr = "a"
	b := stub.New()
	b.NameStr = "b"
	r.Register(a)
	r.Register(b)

	r.ResolveFn = func(r *harness.Registry, agent *agents.Agent) (harness.Harness, error) {
		return r.Get("b")
	}

	got, err := r.Resolve(&agents.Agent{Name: "x", K8sImage: "foo:v1"})
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got != b {
		t.Fatalf("Resolve picked %v, want b", got.Name())
	}
}

func TestRegistry_Execute_DelegatesToResolvedHarness(t *testing.T) {
	r := harness.NewRegistry()
	s := stub.New()
	s.NameStr = "subprocess"
	r.Register(s)

	req := &harness.ExecRequest{RunID: "run-1", AgentName: "a"}
	res, err := r.Execute(context.Background(), &agents.Agent{Name: "a"}, req)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res == nil {
		t.Fatal("Execute returned nil result")
	}
	if len(s.Calls) != 1 {
		t.Fatalf("stub got %d calls, want 1", len(s.Calls))
	}
	if s.Calls[0].RunID != "run-1" {
		t.Fatalf("stub call RunID = %q, want run-1", s.Calls[0].RunID)
	}
}

func TestRegistry_Execute_PropagatesError(t *testing.T) {
	r := harness.NewRegistry()
	s := stub.New()
	s.NameStr = "subprocess"
	wantErr := errors.New("boom")
	s.Err = wantErr
	r.Register(s)

	_, err := r.Execute(context.Background(), &agents.Agent{Name: "a"}, &harness.ExecRequest{RunID: "r"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestRegistry_Execute_NoBackend(t *testing.T) {
	r := harness.NewRegistry()
	_, err := r.Execute(context.Background(), &agents.Agent{Name: "a"}, &harness.ExecRequest{RunID: "r"})
	if !errors.Is(err, harness.ErrNoBackend) {
		t.Fatalf("err = %v, want ErrNoBackend", err)
	}
}
