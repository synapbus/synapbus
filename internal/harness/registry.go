package harness

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Observer is called on every Registry.Execute so callers can persist
// harness_runs rows, emit metrics, or drive side-effects without
// coupling the core registry to storage. OnStart runs after the backend
// has been resolved but before Execute, so the observer knows the
// backend name. OnFinish runs after Execute returns, whether it
// succeeded or failed.
type Observer interface {
	OnStart(ctx context.Context, agent *agents.Agent, harnessName string, req *ExecRequest)
	OnFinish(ctx context.Context, agent *agents.Agent, harnessName string, req *ExecRequest, res *ExecResult, err error)
}

// Registry holds the set of available Harness implementations and resolves
// the right backend for a given agent. It is safe for concurrent use.
type Registry struct {
	mu     sync.RWMutex
	byName map[string]Harness

	// ResolveFn, if non-nil, overrides the default resolution policy.
	// The default picks by agent.HarnessName → k8sjob → webhook →
	// subprocess in that order.
	ResolveFn func(r *Registry, agent *agents.Agent) (Harness, error)

	// Observer, if non-nil, is notified on every Execute.
	Observer Observer
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]Harness{}}
}

// Register adds a harness under its Name(). A second Register with the
// same name replaces the first — tests rely on this to swap in a stub.
func (r *Registry) Register(h Harness) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byName[h.Name()] = h
}

// Get returns the harness registered under name, or ErrUnknownHarness.
func (r *Registry) Get(name string) (Harness, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownHarness, name)
	}
	return h, nil
}

// Names returns the registered harness names in no particular order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byName))
	for k := range r.byName {
		out = append(out, k)
	}
	return out
}

// Resolve picks the right backend for an agent.
//
// Default policy (in order):
//  1. If ResolveFn is set, delegate to it.
//  2. Else if agent.HarnessName is set AND registered, use it
//     (explicit wins over inference — matches the reactor's
//     agentBackendKind policy).
//  3. Else if agent has K8sImage set and "k8sjob" is registered.
//  4. Else if agent has LocalCommand set and "subprocess" is registered.
//  5. Else if agent has a webhook URL in HarnessConfigJSON and
//     "webhook" is registered.
//  6. Else try "k8sjob", "subprocess", "webhook" in that order as a
//     last-resort fallback.
//  7. Else ErrNoBackend.
func (r *Registry) Resolve(agent *agents.Agent) (Harness, error) {
	if r.ResolveFn != nil {
		return r.ResolveFn(r, agent)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	if agent != nil && agent.HarnessName != "" {
		if h, ok := r.byName[agent.HarnessName]; ok {
			return h, nil
		}
	}
	if agent != nil && agent.K8sImage != "" {
		if h, ok := r.byName["k8sjob"]; ok {
			return h, nil
		}
	}
	if agent != nil && agent.LocalCommand != "" {
		if h, ok := r.byName["subprocess"]; ok {
			return h, nil
		}
	}
	if agent != nil && agent.HarnessConfigJSON != "" &&
		strings.Contains(agent.HarnessConfigJSON, "\"url\"") {
		if h, ok := r.byName["webhook"]; ok {
			return h, nil
		}
	}
	// Last-resort fallback chain. Matches older behaviour for tests
	// that register just one harness without setting any hint fields.
	for _, name := range []string{"k8sjob", "subprocess", "webhook"} {
		if h, ok := r.byName[name]; ok {
			return h, nil
		}
	}
	return nil, fmt.Errorf("%w: agent=%q", ErrNoBackend, agentNameOf(agent))
}

func agentNameOf(a *agents.Agent) string {
	if a == nil {
		return ""
	}
	return a.Name
}

// Execute is the single entry point the reactor uses: resolve a backend,
// call its Execute, return the result. Trace spans, env injection, and
// harness_runs rows are layered on top of this by higher packages so the
// core registry stays a thin dispatcher.
func (r *Registry) Execute(ctx context.Context, agent *agents.Agent, req *ExecRequest) (*ExecResult, error) {
	tracer := otel.Tracer(observability.TracerName)
	ctx, span := tracer.Start(ctx, "harness.execute",
		trace.WithAttributes(
			attribute.String("agent.name", agentNameOf(agent)),
			attribute.String("run.id", req.RunID),
		),
	)
	defer span.End()

	h, err := r.Resolve(agent)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		if r.Observer != nil {
			r.Observer.OnFinish(ctx, agent, "", req, nil, err)
		}
		return nil, err
	}
	span.SetAttributes(attribute.String("harness.name", h.Name()))

	if req.Agent == nil {
		req.Agent = agent
	}
	if req.Env == nil {
		req.Env = map[string]string{}
	}
	observability.InjectTraceContext(ctx, req.Env)

	if r.Observer != nil {
		r.Observer.OnStart(ctx, agent, h.Name(), req)
	}

	res, err := h.Execute(ctx, req)

	if r.Observer != nil {
		r.Observer.OnFinish(ctx, agent, h.Name(), req, res, err)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return res, err
	}
	if res != nil {
		span.SetAttributes(
			attribute.Int("exit.code", res.ExitCode),
			attribute.Int64("usage.tokens_in", res.Usage.TokensIn),
			attribute.Int64("usage.tokens_out", res.Usage.TokensOut),
			attribute.Float64("usage.cost_usd", res.Usage.CostUSD),
		)
		if res.TraceID == "" {
			res.TraceID = observability.TraceIDFromContext(ctx)
		}
		if res.ExitCode != 0 {
			span.SetStatus(codes.Error, fmt.Sprintf("exit code %d", res.ExitCode))
		} else {
			span.SetStatus(codes.Ok, "")
		}
	}
	return res, nil
}
