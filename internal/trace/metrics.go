package trace

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
)

// Metrics provides Prometheus-compatible metrics for SynapBus.
// It uses a hand-rolled implementation to avoid CGO dependencies
// from prometheus/client_golang.
type Metrics struct {
	tracesTotal  atomic.Int64
	errorsTotal  atomic.Int64
	activeAgents atomic.Int64

	mu            sync.RWMutex
	tracesByAction map[string]*atomic.Int64
}

// NewMetrics creates a new Metrics instance.
func NewMetrics() *Metrics {
	return &Metrics{
		tracesByAction: make(map[string]*atomic.Int64),
	}
}

// IncTrace increments the total trace counter and the per-action counter.
func (m *Metrics) IncTrace(action string) {
	m.tracesTotal.Add(1)
	m.getOrCreateActionCounter(action).Add(1)
}

// IncError increments the error counter.
func (m *Metrics) IncError() {
	m.errorsTotal.Add(1)
}

// SetActiveAgents sets the active agents gauge.
func (m *Metrics) SetActiveAgents(n int) {
	m.activeAgents.Store(int64(n))
}

func (m *Metrics) getOrCreateActionCounter(action string) *atomic.Int64 {
	m.mu.RLock()
	counter, ok := m.tracesByAction[action]
	m.mu.RUnlock()
	if ok {
		return counter
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Double-check
	if counter, ok := m.tracesByAction[action]; ok {
		return counter
	}
	counter = &atomic.Int64{}
	m.tracesByAction[action] = counter
	return counter
}

// WritePrometheus writes all metrics in Prometheus exposition format to the writer.
func (m *Metrics) WritePrometheus(w io.Writer) {
	fmt.Fprintf(w, "# HELP synapbus_traces_total Total number of trace entries recorded.\n")
	fmt.Fprintf(w, "# TYPE synapbus_traces_total counter\n")
	fmt.Fprintf(w, "synapbus_traces_total %d\n", m.tracesTotal.Load())
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP synapbus_traces_by_action Total traces by action type.\n")
	fmt.Fprintf(w, "# TYPE synapbus_traces_by_action counter\n")
	m.mu.RLock()
	// Sort actions for deterministic output
	actions := make([]string, 0, len(m.tracesByAction))
	for action := range m.tracesByAction {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		counter := m.tracesByAction[action]
		fmt.Fprintf(w, "synapbus_traces_by_action{action=%q} %d\n", action, counter.Load())
	}
	m.mu.RUnlock()
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP synapbus_errors_total Total number of errors recorded.\n")
	fmt.Fprintf(w, "# TYPE synapbus_errors_total counter\n")
	fmt.Fprintf(w, "synapbus_errors_total %d\n", m.errorsTotal.Load())
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP synapbus_active_agents Number of currently active agents.\n")
	fmt.Fprintf(w, "# TYPE synapbus_active_agents gauge\n")
	fmt.Fprintf(w, "synapbus_active_agents %d\n", m.activeAgents.Load())
}

// NullMetrics is a no-op metrics implementation for when metrics are disabled.
type NullMetrics struct{}

func (NullMetrics) IncTrace(action string) {}
func (NullMetrics) IncError()              {}
func (NullMetrics) SetActiveAgents(n int)  {}
