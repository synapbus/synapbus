package trace

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestSQLiteTraceStore_Insert(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteTraceStore(db)
	ctx := context.Background()

	tr := &Trace{
		OwnerID:   "1",
		AgentName: "test-agent",
		Action:    "send_message",
		Details:   json.RawMessage(`{"to":"other-agent"}`),
		Timestamp: time.Now().UTC(),
	}

	if err := store.Insert(ctx, tr); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if tr.ID == 0 {
		t.Error("expected non-zero ID after insert")
	}
}

func TestSQLiteTraceStore_Query(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteTraceStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Insert traces for two owners
	traces := []Trace{
		{OwnerID: "1", AgentName: "agent-a", Action: "send_message", Details: json.RawMessage(`{}`), Timestamp: now.Add(-5 * time.Minute)},
		{OwnerID: "1", AgentName: "agent-a", Action: "read_inbox", Details: json.RawMessage(`{}`), Timestamp: now.Add(-4 * time.Minute)},
		{OwnerID: "1", AgentName: "agent-b", Action: "send_message", Details: json.RawMessage(`{}`), Timestamp: now.Add(-3 * time.Minute)},
		{OwnerID: "2", AgentName: "agent-c", Action: "send_message", Details: json.RawMessage(`{}`), Timestamp: now.Add(-2 * time.Minute)},
		{OwnerID: "2", AgentName: "agent-c", Action: "error", Details: json.RawMessage(`{"err":"boom"}`), Timestamp: now.Add(-1 * time.Minute)},
	}

	for i := range traces {
		if err := store.Insert(ctx, &traces[i]); err != nil {
			t.Fatalf("Insert trace %d: %v", i, err)
		}
	}

	t.Run("owner isolation", func(t *testing.T) {
		result, total, err := store.Query(ctx, TraceFilter{OwnerID: "1"})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3", total)
		}
		if len(result) != 3 {
			t.Errorf("got %d traces, want 3", len(result))
		}
		// Verify no owner 2 traces leak through
		for _, tr := range result {
			if tr.OwnerID != "1" {
				t.Errorf("got trace with owner_id = %q, want %q", tr.OwnerID, "1")
			}
		}
	})

	t.Run("filter by agent_name", func(t *testing.T) {
		result, total, err := store.Query(ctx, TraceFilter{OwnerID: "1", AgentName: "agent-a"})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if total != 2 {
			t.Errorf("total = %d, want 2", total)
		}
		if len(result) != 2 {
			t.Errorf("got %d traces, want 2", len(result))
		}
	})

	t.Run("filter by action", func(t *testing.T) {
		result, total, err := store.Query(ctx, TraceFilter{OwnerID: "1", Action: "send_message"})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if total != 2 {
			t.Errorf("total = %d, want 2", total)
		}
		if len(result) != 2 {
			t.Errorf("got %d traces, want 2", len(result))
		}
	})

	t.Run("filter by time range", func(t *testing.T) {
		since := now.Add(-4 * time.Minute)
		until := now.Add(-2 * time.Minute)
		result, total, err := store.Query(ctx, TraceFilter{
			OwnerID: "1",
			Since:   &since,
			Until:   &until,
		})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if total != 2 {
			t.Errorf("total = %d, want 2", total)
		}
		if len(result) != 2 {
			t.Errorf("got %d traces, want 2", len(result))
		}
	})

	t.Run("combined filters", func(t *testing.T) {
		result, total, err := store.Query(ctx, TraceFilter{
			OwnerID:   "1",
			AgentName: "agent-a",
			Action:    "send_message",
		})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if total != 1 {
			t.Errorf("total = %d, want 1", total)
		}
		if len(result) != 1 {
			t.Errorf("got %d traces, want 1", len(result))
		}
	})

	t.Run("pagination", func(t *testing.T) {
		result, total, err := store.Query(ctx, TraceFilter{OwnerID: "1", PageSize: 2, Page: 1})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3", total)
		}
		if len(result) != 2 {
			t.Errorf("got %d traces on page 1, want 2", len(result))
		}

		// Page 2
		result2, total2, err := store.Query(ctx, TraceFilter{OwnerID: "1", PageSize: 2, Page: 2})
		if err != nil {
			t.Fatalf("Query page 2: %v", err)
		}
		if total2 != 3 {
			t.Errorf("total = %d, want 3", total2)
		}
		if len(result2) != 1 {
			t.Errorf("got %d traces on page 2, want 1", len(result2))
		}
	})

	t.Run("page_size capped at 200", func(t *testing.T) {
		_, _, err := store.Query(ctx, TraceFilter{OwnerID: "1", PageSize: 500})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		// Should not error — just cap silently
	})

	t.Run("reverse chronological order", func(t *testing.T) {
		result, _, err := store.Query(ctx, TraceFilter{OwnerID: "1"})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		for i := 1; i < len(result); i++ {
			if result[i].Timestamp.After(result[i-1].Timestamp) {
				t.Error("traces not in reverse chronological order")
			}
		}
	})
}

func TestSQLiteTraceStore_QueryStream(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteTraceStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		tr := &Trace{
			OwnerID:   "1",
			AgentName: "stream-agent",
			Action:    "action",
			Details:   json.RawMessage(`{}`),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}
		if err := store.Insert(ctx, tr); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	var count int
	err := store.QueryStream(ctx, TraceFilter{OwnerID: "1"}, func(tr Trace) error {
		count++
		if tr.AgentName != "stream-agent" {
			t.Errorf("unexpected agent: %s", tr.AgentName)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("QueryStream: %v", err)
	}
	if count != 5 {
		t.Errorf("streamed %d traces, want 5", count)
	}
}

func TestSQLiteTraceStore_CountByAction(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteTraceStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	actions := []string{"send_message", "send_message", "read_inbox", "error"}
	for _, action := range actions {
		tr := &Trace{
			OwnerID:   "1",
			AgentName: "agent",
			Action:    action,
			Details:   json.RawMessage(`{}`),
			Timestamp: now,
		}
		if err := store.Insert(ctx, tr); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	counts, err := store.CountByAction(ctx, "1")
	if err != nil {
		t.Fatalf("CountByAction: %v", err)
	}
	if counts["send_message"] != 2 {
		t.Errorf("send_message count = %d, want 2", counts["send_message"])
	}
	if counts["read_inbox"] != 1 {
		t.Errorf("read_inbox count = %d, want 1", counts["read_inbox"])
	}
	if counts["error"] != 1 {
		t.Errorf("error count = %d, want 1", counts["error"])
	}
}

func TestSQLiteTraceStore_DeleteOlderThan(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteTraceStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Insert old and new traces
	old := &Trace{
		OwnerID:   "1",
		AgentName: "agent",
		Action:    "old_action",
		Details:   json.RawMessage(`{}`),
		Timestamp: now.Add(-48 * time.Hour),
	}
	recent := &Trace{
		OwnerID:   "1",
		AgentName: "agent",
		Action:    "new_action",
		Details:   json.RawMessage(`{}`),
		Timestamp: now,
	}

	if err := store.Insert(ctx, old); err != nil {
		t.Fatalf("Insert old: %v", err)
	}
	if err := store.Insert(ctx, recent); err != nil {
		t.Fatalf("Insert recent: %v", err)
	}

	// Delete traces older than 24 hours
	cutoff := now.Add(-24 * time.Hour)
	deleted, err := store.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	// Verify the recent one still exists
	traces, total, err := store.Query(ctx, TraceFilter{OwnerID: "1"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 1 {
		t.Errorf("remaining = %d, want 1", total)
	}
	if len(traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(traces))
	}
	if traces[0].Action != "new_action" {
		t.Errorf("remaining trace action = %q, want %q", traces[0].Action, "new_action")
	}
}

func TestSQLiteTraceStore_OwnerIsolation(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteTraceStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Three owners
	for _, ownerID := range []string{"1", "2", "3"} {
		for i := 0; i < 5; i++ {
			tr := &Trace{
				OwnerID:   ownerID,
				AgentName: "agent-" + ownerID,
				Action:    "action",
				Details:   json.RawMessage(`{}`),
				Timestamp: now,
			}
			if err := store.Insert(ctx, tr); err != nil {
				t.Fatalf("Insert: %v", err)
			}
		}
	}

	for _, ownerID := range []string{"1", "2", "3"} {
		traces, total, err := store.Query(ctx, TraceFilter{OwnerID: ownerID})
		if err != nil {
			t.Fatalf("Query owner %s: %v", ownerID, err)
		}
		if total != 5 {
			t.Errorf("owner %s: total = %d, want 5", ownerID, total)
		}
		for _, tr := range traces {
			if tr.OwnerID != ownerID {
				t.Errorf("owner %s got trace with owner_id = %q", ownerID, tr.OwnerID)
			}
		}
	}
}
