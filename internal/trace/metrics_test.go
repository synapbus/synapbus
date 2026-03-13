package trace

import (
	"bytes"
	"strings"
	"testing"
)

func TestMetrics_IncTrace(t *testing.T) {
	m := NewMetrics()

	m.IncTrace("send_message")
	m.IncTrace("send_message")
	m.IncTrace("read_inbox")

	if got := m.tracesTotal.Load(); got != 3 {
		t.Errorf("tracesTotal = %d, want 3", got)
	}

	m.mu.RLock()
	if got := m.tracesByAction["send_message"].Load(); got != 2 {
		t.Errorf("tracesByAction[send_message] = %d, want 2", got)
	}
	if got := m.tracesByAction["read_inbox"].Load(); got != 1 {
		t.Errorf("tracesByAction[read_inbox] = %d, want 1", got)
	}
	m.mu.RUnlock()
}

func TestMetrics_IncError(t *testing.T) {
	m := NewMetrics()

	m.IncError()
	m.IncError()

	if got := m.errorsTotal.Load(); got != 2 {
		t.Errorf("errorsTotal = %d, want 2", got)
	}
}

func TestMetrics_SetActiveAgents(t *testing.T) {
	m := NewMetrics()

	m.SetActiveAgents(5)
	if got := m.activeAgents.Load(); got != 5 {
		t.Errorf("activeAgents = %d, want 5", got)
	}

	m.SetActiveAgents(3)
	if got := m.activeAgents.Load(); got != 3 {
		t.Errorf("activeAgents = %d, want 3", got)
	}
}

func TestMetrics_WritePrometheus(t *testing.T) {
	m := NewMetrics()

	m.IncTrace("send_message")
	m.IncTrace("send_message")
	m.IncTrace("read_inbox")
	m.IncError()
	m.SetActiveAgents(7)

	var buf bytes.Buffer
	m.WritePrometheus(&buf)
	output := buf.String()

	expected := []string{
		"# HELP synapbus_traces_total",
		"# TYPE synapbus_traces_total counter",
		"synapbus_traces_total 3",
		"# HELP synapbus_traces_by_action",
		"# TYPE synapbus_traces_by_action counter",
		`synapbus_traces_by_action{action="read_inbox"} 1`,
		`synapbus_traces_by_action{action="send_message"} 2`,
		"# HELP synapbus_errors_total",
		"# TYPE synapbus_errors_total counter",
		"synapbus_errors_total 1",
		"# HELP synapbus_active_agents",
		"# TYPE synapbus_active_agents gauge",
		"synapbus_active_agents 7",
	}

	for _, exp := range expected {
		if !strings.Contains(output, exp) {
			t.Errorf("missing expected output: %q", exp)
		}
	}
}

func TestNullMetrics_DoesNotPanic(t *testing.T) {
	var m NullMetrics
	// These should not panic
	m.IncTrace("action")
	m.IncError()
	m.SetActiveAgents(5)
}

func TestMetrics_ConcurrentAccess(t *testing.T) {
	m := NewMetrics()

	// Concurrent writes should not race
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				m.IncTrace("action")
				m.IncError()
				m.SetActiveAgents(j)
			}
			done <- struct{}{}
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	if got := m.tracesTotal.Load(); got != 1000 {
		t.Errorf("tracesTotal = %d, want 1000", got)
	}
}
