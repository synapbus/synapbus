// Package harness provides a runtime-agnostic interface for dispatching agent
// work to a concrete execution backend (Kubernetes Job, local subprocess,
// outbound webhook, or an in-memory stub for tests). It is the single seam
// between SynapBus's reactor / webhook / MCP entry points and whatever
// actually runs an agent.
//
// Inspired by GoogleCloudPlatform/scion's api.Harness interface. See
// docs/harness-otel-design.md for the full design rationale.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/messaging"
)

// ErrNoBackend is returned by Registry.Resolve when no registered harness
// can handle the given agent.
var ErrNoBackend = errors.New("harness: no backend available for agent")

// ErrUnknownHarness is returned when a harness name is requested but is
// not registered.
var ErrUnknownHarness = errors.New("harness: unknown harness name")

// Capabilities advertises what a given harness backend supports so the
// caller can degrade gracefully (e.g. fall back from a system prompt to
// inline instructions when the backend cannot carry one).
type Capabilities struct {
	// SystemPrompt means the backend can carry a dedicated system prompt
	// separate from the user task.
	SystemPrompt bool

	// SessionResume means the backend can resume a prior conversation by
	// session id. When false, every Execute is a cold start.
	SessionResume bool

	// Skills means the backend can materialise a set of skill files into
	// the agent workspace.
	Skills bool

	// OTelNative means the child process honours standard OTEL_* env vars
	// (OTEL_EXPORTER_OTLP_ENDPOINT, TRACEPARENT, etc.). If true, the
	// dispatcher will inject trace context via environment variables.
	OTelNative bool

	// MaxConcurrency is an advisory upper bound on simultaneous Execute
	// calls the backend can handle. Zero means "no explicit limit".
	MaxConcurrency int
}

// Budget bounds a single Execute call. Zero values mean "no limit".
type Budget struct {
	MaxWallClock time.Duration
	MaxTokensIn  int64
	MaxTokensOut int64
	MaxCostUSD   float64
}

// Usage records resource consumption for a completed run.
type Usage struct {
	TokensIn     int64   `json:"tokens_in"`
	TokensOut    int64   `json:"tokens_out"`
	TokensCached int64   `json:"tokens_cached"`
	CostUSD      float64 `json:"cost_usd"`
}

// ExecRequest is the single input to a harness Execute call.
type ExecRequest struct {
	// RunID is a stable caller-generated id (UUID-ish). Propagated into
	// the child as SYNAPBUS_RUN_ID so logs and traces correlate.
	RunID string

	// AgentName is the target agent's SynapBus name. The harness may use
	// it for logging and for selecting per-agent configuration.
	AgentName string

	// Agent is the full agent record. Backends read per-agent config
	// from it (K8sImage, LocalCommand, etc.). Nil is allowed only for
	// the stub backend used in tests.
	Agent *agents.Agent

	// Message is the triggering message, if any. Nil for on-demand runs
	// such as admin CLI invocations.
	Message *messaging.Message

	// Context is an optional conversation window the caller wants the
	// child to see. Backends that support SessionResume may ignore this
	// in favour of their own session state.
	Context []*messaging.Message

	// SessionID, when non-empty and Capabilities.SessionResume is true,
	// asks the backend to resume a prior conversation.
	SessionID string

	// Budget bounds wall-clock, tokens, and cost.
	Budget Budget

	// Env is a set of caller-provided environment overrides merged on
	// top of whatever the backend normally injects (last write wins).
	Env map[string]string

	// Skills lists skill names the backend should materialise if
	// Capabilities.Skills is true.
	Skills []string
}

// ExecResult is the single output of a harness Execute call.
type ExecResult struct {
	// ExitCode follows Unix convention: 0 success, non-zero failure.
	// For backends without a true exit code (e.g. webhook), this is a
	// synthetic value: 0 on HTTP 2xx, 1 otherwise.
	ExitCode int

	// Logs is a bounded excerpt of stdout+stderr (or HTTP response body
	// for the webhook backend). Full logs live on disk / remote storage.
	Logs string

	// ResultJSON is an optional structured output the agent emitted.
	// Subprocess agents write this to a well-known path; K8s agents
	// write it to stdout or a shared volume; webhook agents return it
	// in the response body.
	ResultJSON json.RawMessage

	// Usage captures token / cost accounting when the backend can
	// report it. Zero values mean "not reported".
	Usage Usage

	// SessionID is the backend-specific session identifier for resume.
	// Empty when the backend does not support SessionResume.
	SessionID string

	// TraceID is the W3C trace id (hex) the run was observed under, so
	// callers can link their row to a distributed trace.
	TraceID string
}

// Harness is the interface every execution backend implements.
type Harness interface {
	// Name is the short stable identifier used in config (e.g. "k8sjob",
	// "subprocess", "webhook", "stub").
	Name() string

	// Capabilities advertises backend features.
	Capabilities() Capabilities

	// TestEnvironment is a cheap preflight: is the required binary
	// installed, is auth valid, can we reach the model? Called by the
	// admin CLI and by Registry.Resolve as a health gate.
	TestEnvironment(ctx context.Context) error

	// Provision performs one-shot setup for a given agent (writing
	// config files, pre-approving tool fingerprints, materialising
	// skills). Idempotent — safe to call repeatedly.
	Provision(ctx context.Context, agent *agents.Agent) error

	// Execute dispatches a single request and blocks until the run
	// terminates, the context is cancelled, or the Budget is exhausted.
	// Implementations must always return a non-nil *ExecResult when
	// they return nil error.
	Execute(ctx context.Context, req *ExecRequest) (*ExecResult, error)

	// Cancel asks the backend to abort an in-flight run with the given
	// RunID. Best-effort; returns nil if the run is unknown or already
	// terminated.
	Cancel(ctx context.Context, runID string) error
}
