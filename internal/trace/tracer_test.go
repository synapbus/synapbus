package trace

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Create traces table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS traces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_name TEXT NOT NULL,
			action TEXT NOT NULL,
			details TEXT NOT NULL DEFAULT '{}',
			error TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("create traces table: %v", err)
	}

	return db
}

func TestTracer_Record(t *testing.T) {
	tests := []struct {
		name      string
		agent     string
		action    string
		details   any
	}{
		{
			name:   "simple trace",
			agent:  "test-agent",
			action: "send_message",
			details: map[string]any{
				"to":      "other-agent",
				"message": "hello",
			},
		},
		{
			name:    "trace with nil details",
			agent:   "agent-a",
			action:  "read_inbox",
			details: nil,
		},
		{
			name:   "trace with string details",
			agent:  "agent-b",
			action: "search",
			details: "query string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			tracer := NewTracer(db)
			defer tracer.Close()

			ctx := context.Background()
			tracer.Record(ctx, tt.agent, tt.action, tt.details)

			// Give async writer time to process
			time.Sleep(50 * time.Millisecond)

			// Verify trace was written
			var count int
			err := db.QueryRow(
				"SELECT COUNT(*) FROM traces WHERE agent_name = ? AND action = ?",
				tt.agent, tt.action,
			).Scan(&count)
			if err != nil {
				t.Fatalf("query trace: %v", err)
			}
			if count != 1 {
				t.Errorf("trace count = %d, want 1", count)
			}
		})
	}
}

func TestTracer_RecordError(t *testing.T) {
	db := newTestDB(t)
	tracer := NewTracer(db)
	defer tracer.Close()

	ctx := context.Background()
	tracer.RecordError(ctx, "test-agent", "failed_action",
		map[string]any{"key": "value"},
		fmt.Errorf("something went wrong"),
	)

	time.Sleep(50 * time.Millisecond)

	var errorText sql.NullString
	err := db.QueryRow(
		"SELECT error FROM traces WHERE agent_name = 'test-agent' AND action = 'failed_action'",
	).Scan(&errorText)
	if err != nil {
		t.Fatalf("query trace: %v", err)
	}
	if !errorText.Valid || errorText.String != "something went wrong" {
		t.Errorf("error = %v, want 'something went wrong'", errorText)
	}
}

func TestTraceStore(t *testing.T) {
	db := newTestDB(t)
	tracer := NewTracer(db)

	ctx := context.Background()

	// Record some traces
	tracer.Record(ctx, "agent-a", "send_message", map[string]any{"to": "agent-b"})
	tracer.Record(ctx, "agent-a", "read_inbox", map[string]any{"count": 5})
	tracer.Record(ctx, "agent-b", "send_message", map[string]any{"to": "agent-a"})

	tracer.Close() // flush all entries

	store := NewSQLiteTraceStore(db)

	t.Run("get traces by agent", func(t *testing.T) {
		traces, err := store.GetTraces(ctx, "agent-a", 10)
		if err != nil {
			t.Fatalf("GetTraces: %v", err)
		}
		if len(traces) != 2 {
			t.Errorf("got %d traces, want 2", len(traces))
		}
	})

	t.Run("get traces by action", func(t *testing.T) {
		traces, err := store.GetTracesByAction(ctx, "send_message", 10)
		if err != nil {
			t.Fatalf("GetTracesByAction: %v", err)
		}
		if len(traces) != 2 {
			t.Errorf("got %d traces, want 2", len(traces))
		}
	})

	t.Run("limit results", func(t *testing.T) {
		traces, err := store.GetTraces(ctx, "agent-a", 1)
		if err != nil {
			t.Fatalf("GetTraces: %v", err)
		}
		if len(traces) != 1 {
			t.Errorf("got %d traces, want 1", len(traces))
		}
	})
}
