package harness_test

import (
	"context"
	"errors"
	"testing"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/harness/stub"
	"github.com/synapbus/synapbus/internal/observability"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func withTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	otel.SetTracerProvider(tp)
	// Install propagator so InjectTraceContext emits TRACEPARENT.
	_, _ = observability.Init(context.Background(), observability.Config{Enabled: false}, nil)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return exp
}

func TestRegistry_Execute_EmitsSpan(t *testing.T) {
	exp := withTracer(t)

	r := harness.NewRegistry()
	s := stub.New()
	s.NameStr = "subprocess"
	r.Register(s)

	_, err := r.Execute(context.Background(), &agents.Agent{Name: "a"}, &harness.ExecRequest{RunID: "run-x"})
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	found := false
	for _, sp := range spans {
		if sp.Name == "harness.execute" {
			found = true
			attrs := map[string]string{}
			for _, a := range sp.Attributes {
				attrs[string(a.Key)] = a.Value.Emit()
			}
			if attrs["agent.name"] != "a" {
				t.Errorf("agent.name attr = %q", attrs["agent.name"])
			}
			if attrs["run.id"] != "run-x" {
				t.Errorf("run.id attr = %q", attrs["run.id"])
			}
			if attrs["harness.name"] != "subprocess" {
				t.Errorf("harness.name attr = %q", attrs["harness.name"])
			}
		}
	}
	if !found {
		t.Fatalf("span harness.execute not found in %v", spans)
	}
}

func TestRegistry_Execute_InjectsTraceContextIntoEnv(t *testing.T) {
	_ = withTracer(t)

	r := harness.NewRegistry()
	s := stub.New()
	s.NameStr = "subprocess"
	r.Register(s)

	_, err := r.Execute(context.Background(), &agents.Agent{Name: "a"}, &harness.ExecRequest{RunID: "run-x"})
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if len(s.Calls) != 1 {
		t.Fatalf("stub calls = %d", len(s.Calls))
	}
	env := s.Calls[0].Env
	if _, ok := env["TRACEPARENT"]; !ok {
		t.Errorf("TRACEPARENT not injected into req.Env: %v", env)
	}
}

func TestRegistry_Execute_SpanRecordsError(t *testing.T) {
	exp := withTracer(t)

	r := harness.NewRegistry()
	s := stub.New()
	s.NameStr = "subprocess"
	s.Err = errors.New("boom")
	r.Register(s)

	_, err := r.Execute(context.Background(), &agents.Agent{Name: "a"}, &harness.ExecRequest{RunID: "run-x"})
	if err == nil {
		t.Fatal("expected error")
	}
	spans := exp.GetSpans()
	var statuses []string
	for _, sp := range spans {
		if sp.Name == "harness.execute" {
			statuses = append(statuses, sp.Status.Code.String())
		}
	}
	if len(statuses) == 0 || statuses[0] != "Error" {
		t.Errorf("span statuses = %v, want [Error]", statuses)
	}
}

func TestRegistry_Execute_PopulatesResultTraceID(t *testing.T) {
	_ = withTracer(t)

	r := harness.NewRegistry()
	s := stub.New()
	s.NameStr = "subprocess"
	r.Register(s)

	res, err := r.Execute(context.Background(), &agents.Agent{Name: "a"}, &harness.ExecRequest{RunID: "run-x"})
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if res.TraceID == "" {
		t.Error("ExecResult.TraceID empty after registry execute")
	}
}
