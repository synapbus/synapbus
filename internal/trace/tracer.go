// Package trace provides agent activity trace recording for SynapBus.
package trace

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
)

// TraceEntry represents a single trace record.
type TraceEntry struct {
	AgentName string
	Action    string
	Details   any
	Error     string
}

// Tracer records agent actions to the traces table.
// It uses a buffered channel for async recording.
type Tracer struct {
	db     *sql.DB
	logger *slog.Logger
	ch     chan TraceEntry
	done   chan struct{}
}

// NewTracer creates a new Tracer with a buffered channel.
func NewTracer(db *sql.DB) *Tracer {
	t := &Tracer{
		db:     db,
		logger: slog.Default().With("component", "tracer"),
		ch:     make(chan TraceEntry, 256),
		done:   make(chan struct{}),
	}
	go t.processLoop()
	return t
}

// Record enqueues a trace entry for async storage.
func (t *Tracer) Record(ctx context.Context, agentName, action string, details any) {
	entry := TraceEntry{
		AgentName: agentName,
		Action:    action,
		Details:   details,
	}

	select {
	case t.ch <- entry:
	default:
		t.logger.Warn("trace channel full, dropping entry",
			"agent", agentName,
			"action", action,
		)
	}

	t.logger.Info("trace recorded",
		"agent", agentName,
		"action", action,
	)
}

// RecordError enqueues a trace entry with an error.
func (t *Tracer) RecordError(ctx context.Context, agentName, action string, details any, traceErr error) {
	entry := TraceEntry{
		AgentName: agentName,
		Action:    action,
		Details:   details,
	}
	if traceErr != nil {
		entry.Error = traceErr.Error()
	}

	select {
	case t.ch <- entry:
	default:
		t.logger.Warn("trace channel full, dropping entry",
			"agent", agentName,
			"action", action,
		)
	}
}

// Close stops the tracer and flushes remaining entries.
func (t *Tracer) Close() {
	close(t.ch)
	<-t.done
}

func (t *Tracer) processLoop() {
	defer close(t.done)
	for entry := range t.ch {
		t.writeEntry(entry)
	}
}

func (t *Tracer) writeEntry(entry TraceEntry) {
	detailsJSON, err := json.Marshal(entry.Details)
	if err != nil {
		t.logger.Error("failed to marshal trace details",
			"error", err,
			"agent", entry.AgentName,
			"action", entry.Action,
		)
		detailsJSON = []byte("{}")
	}

	var traceErr sql.NullString
	if entry.Error != "" {
		traceErr = sql.NullString{String: entry.Error, Valid: true}
	}

	_, err = t.db.Exec(
		`INSERT INTO traces (agent_name, action, details, error, created_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		entry.AgentName, entry.Action, string(detailsJSON), traceErr,
	)
	if err != nil {
		t.logger.Error("failed to write trace entry",
			"error", err,
			"agent", entry.AgentName,
			"action", entry.Action,
		)
	}
}
