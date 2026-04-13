// Package stub provides an in-memory Harness implementation used by tests
// and by development modes where no real execution backend is wanted.
package stub

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
)

// Harness is a canned, in-memory implementation of harness.Harness.
// Callers set Result / Err directly (under Mu) to control what Execute
// returns. Every call is recorded in Calls for assertions.
type Harness struct {
	Mu           sync.Mutex
	NameStr      string
	Caps         harness.Capabilities
	PreflightErr error
	ProvisionErr error
	Result       *harness.ExecResult
	Err          error
	ExecDelay    time.Duration
	Calls        []harness.ExecRequest
	CancelCalls  []string
}

// New returns a stub harness ready to use. The default result is a
// successful zero-exit-code run with empty logs.
func New() *Harness {
	return &Harness{
		NameStr: "stub",
		Caps:    harness.Capabilities{OTelNative: false, MaxConcurrency: 1},
		Result: &harness.ExecResult{
			ExitCode:   0,
			Logs:       "",
			ResultJSON: json.RawMessage(`{"ok":true}`),
		},
	}
}

func (h *Harness) Name() string                       { return h.NameStr }
func (h *Harness) Capabilities() harness.Capabilities { return h.Caps }

func (h *Harness) TestEnvironment(ctx context.Context) error {
	return h.PreflightErr
}

func (h *Harness) Provision(ctx context.Context, agent *agents.Agent) error {
	return h.ProvisionErr
}

func (h *Harness) Execute(ctx context.Context, req *harness.ExecRequest) (*harness.ExecResult, error) {
	h.Mu.Lock()
	// Copy the request value so later caller mutation cannot race with
	// test assertions reading Calls.
	reqCopy := *req
	h.Calls = append(h.Calls, reqCopy)
	delay := h.ExecDelay
	result := h.Result
	err := h.Err
	h.Mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (h *Harness) Cancel(ctx context.Context, runID string) error {
	h.Mu.Lock()
	h.CancelCalls = append(h.CancelCalls, runID)
	h.Mu.Unlock()
	return nil
}
