// Package trace provides agent activity trace recording for SynapBus.
package trace

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"
)

// TraceEntry represents a single trace record to be written.
type TraceEntry struct {
	OwnerID   string
	AgentName string
	Action    string
	Details   any
	Error     string
}

// MetricsRecorder is an optional interface for recording trace metrics.
type MetricsRecorder interface {
	IncTrace(action string)
	IncError()
}

// Tracer records agent actions to the traces table.
// It uses a buffered channel for async recording and batches writes.
type Tracer struct {
	db      *sql.DB
	logger  *slog.Logger
	ch      chan TraceEntry
	done    chan struct{}
	metrics MetricsRecorder
}

// NewTracer creates a new Tracer with a buffered channel and batch writing.
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

// SetMetrics sets the metrics recorder for the tracer.
func (t *Tracer) SetMetrics(m MetricsRecorder) {
	t.metrics = m
}

// Record enqueues a trace entry for async storage (no owner ID — legacy API).
func (t *Tracer) Record(ctx context.Context, agentName, action string, details any) {
	t.RecordWithOwner(ctx, "", agentName, action, details)
}

// RecordWithOwner enqueues a trace entry with an explicit owner ID.
func (t *Tracer) RecordWithOwner(ctx context.Context, ownerID, agentName, action string, details any) {
	entry := TraceEntry{
		OwnerID:   ownerID,
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

// RecordError enqueues a trace entry with an error (no owner ID — legacy API).
func (t *Tracer) RecordError(ctx context.Context, agentName, action string, details any, traceErr error) {
	t.RecordErrorWithOwner(ctx, "", agentName, action, details, traceErr)
}

// RecordErrorWithOwner enqueues a trace entry with an error and explicit owner ID.
func (t *Tracer) RecordErrorWithOwner(ctx context.Context, ownerID, agentName, action string, details any, traceErr error) {
	entry := TraceEntry{
		OwnerID:   ownerID,
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

	batch := make([]TraceEntry, 0, 64)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case entry, ok := <-t.ch:
			if !ok {
				// Channel closed, flush remaining
				if len(batch) > 0 {
					t.writeBatch(batch)
				}
				return
			}
			batch = append(batch, entry)
			if len(batch) >= 64 {
				t.writeBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				t.writeBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

func (t *Tracer) writeBatch(entries []TraceEntry) {
	tx, err := t.db.Begin()
	if err != nil {
		t.logger.Error("failed to begin trace batch transaction",
			"error", err,
			"batch_size", len(entries),
		)
		return
	}

	stmt, err := tx.Prepare(
		`INSERT INTO traces (owner_id, agent_name, action, details, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		t.logger.Error("failed to prepare trace insert",
			"error", err,
		)
		tx.Rollback()
		return
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, entry := range entries {
		detailsJSON, marshalErr := json.Marshal(entry.Details)
		if marshalErr != nil {
			t.logger.Error("failed to marshal trace details",
				"error", marshalErr,
				"agent", entry.AgentName,
				"action", entry.Action,
			)
			detailsJSON = []byte("{}")
		}

		var traceErr sql.NullString
		if entry.Error != "" {
			traceErr = sql.NullString{String: entry.Error, Valid: true}
		}

		_, execErr := stmt.Exec(
			entry.OwnerID, entry.AgentName, entry.Action, string(detailsJSON), traceErr, now,
		)
		if execErr != nil {
			t.logger.Error("failed to write trace entry",
				"error", execErr,
				"agent", entry.AgentName,
				"action", entry.Action,
			)
		}

		// Record metrics
		if t.metrics != nil {
			t.metrics.IncTrace(entry.Action)
			if entry.Error != "" {
				t.metrics.IncError()
			}
		}
	}

	if err := tx.Commit(); err != nil {
		t.logger.Error("failed to commit trace batch",
			"error", err,
			"batch_size", len(entries),
		)
	}
}
