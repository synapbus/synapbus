package reactions

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

func TestService_ListByState_FiltersCorrectly(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewService(store, logger)
	ctx := context.Background()

	// Ensure user and agents exist
	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testowner', 'hash', 'Test Owner')`)
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES ('agent-a', 'agent-a', 'ai', '{}', 1, 'testhash', 'active')`)
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES ('agent-b', 'agent-b', 'ai', '{}', 1, 'testhash2', 'active')`)

	// Create a channel
	result, err := db.Exec(`INSERT INTO channels (name, description, created_by, workflow_enabled) VALUES ('test-channel', 'test', 'agent-a', 1)`)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID, _ := result.LastInsertId()

	// Helper to create a message in the channel
	createMsg := func(agent, body string) int64 {
		t.Helper()
		r, err := db.Exec(
			`INSERT INTO conversations (subject, created_by, created_at, updated_at) VALUES ('test', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			agent,
		)
		if err != nil {
			t.Fatalf("create conversation: %v", err)
		}
		convID, _ := r.LastInsertId()

		r, err = db.Exec(
			`INSERT INTO messages (conversation_id, from_agent, channel_id, body, priority, status, created_at) VALUES (?, ?, ?, ?, 5, 'pending', CURRENT_TIMESTAMP)`,
			convID, agent, channelID, body,
		)
		if err != nil {
			t.Fatalf("create message: %v", err)
		}
		id, _ := r.LastInsertId()
		return id
	}

	// Scenario: msg1 has approve + reject (should be "rejected" since reject has higher priority)
	msg1 := createMsg("agent-a", "msg with approve and reject")
	store.Insert(ctx, &Reaction{MessageID: msg1, AgentName: "agent-a", Reaction: ReactionApprove})
	store.Insert(ctx, &Reaction{MessageID: msg1, AgentName: "agent-b", Reaction: ReactionReject})

	// Scenario: msg2 has only approve (should be "approved")
	msg2 := createMsg("agent-a", "msg with only approve")
	store.Insert(ctx, &Reaction{MessageID: msg2, AgentName: "agent-a", Reaction: ReactionApprove})

	// Scenario: msg3 has no reactions (should be "proposed")
	msg3 := createMsg("agent-a", "msg with no reactions")

	// Scenario: msg4 has approve + in_progress + done (should be "done")
	msg4 := createMsg("agent-a", "msg with approve, in_progress, done")
	store.Insert(ctx, &Reaction{MessageID: msg4, AgentName: "agent-a", Reaction: ReactionApprove})
	store.Insert(ctx, &Reaction{MessageID: msg4, AgentName: "agent-b", Reaction: ReactionInProgress})
	store.Insert(ctx, &Reaction{MessageID: msg4, AgentName: "agent-b", Reaction: ReactionDone})

	// Test: list "approved" should only return msg2 (NOT msg1 which also has approve but its state is rejected)
	t.Run("approved returns only truly approved", func(t *testing.T) {
		ids, err := svc.ListByState(ctx, channelID, StateApproved)
		if err != nil {
			t.Fatalf("ListByState(approved): %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 approved message, got %d: %v", len(ids), ids)
		}
		if ids[0] != msg2 {
			t.Errorf("expected msg2 (id=%d), got id=%d", msg2, ids[0])
		}
	})

	// Test: list "rejected" should only return msg1
	t.Run("rejected returns only truly rejected", func(t *testing.T) {
		ids, err := svc.ListByState(ctx, channelID, StateRejected)
		if err != nil {
			t.Fatalf("ListByState(rejected): %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 rejected message, got %d: %v", len(ids), ids)
		}
		if ids[0] != msg1 {
			t.Errorf("expected msg1 (id=%d), got id=%d", msg1, ids[0])
		}
	})

	// Test: list "proposed" should only return msg3
	t.Run("proposed returns only messages with no reactions", func(t *testing.T) {
		ids, err := svc.ListByState(ctx, channelID, StateProposed)
		if err != nil {
			t.Fatalf("ListByState(proposed): %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 proposed message, got %d: %v", len(ids), ids)
		}
		if ids[0] != msg3 {
			t.Errorf("expected msg3 (id=%d), got id=%d", msg3, ids[0])
		}
	})

	// Test: list "done" should only return msg4
	t.Run("done returns only truly done", func(t *testing.T) {
		ids, err := svc.ListByState(ctx, channelID, StateDone)
		if err != nil {
			t.Fatalf("ListByState(done): %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 done message, got %d: %v", len(ids), ids)
		}
		if ids[0] != msg4 {
			t.Errorf("expected msg4 (id=%d), got id=%d", msg4, ids[0])
		}
	})

	// Test: list "in_progress" should return nothing (msg4 has in_progress but done overrides it)
	t.Run("in_progress excludes messages that have progressed to done", func(t *testing.T) {
		ids, err := svc.ListByState(ctx, channelID, StateInProgress)
		if err != nil {
			t.Fatalf("ListByState(in_progress): %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("expected 0 in_progress messages, got %d: %v", len(ids), ids)
		}
	})
}

func TestService_ListByState_EmptyChannel(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewService(store, logger)
	ctx := context.Background()

	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testowner', 'hash', 'Test Owner')`)

	result, err := db.Exec(`INSERT INTO channels (name, description, created_by, workflow_enabled) VALUES ('empty-channel', 'empty', 'testowner', 1)`)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID, _ := result.LastInsertId()

	ids, err := svc.ListByState(ctx, channelID, StateProposed)
	if err != nil {
		t.Fatalf("ListByState: %v", err)
	}
	if ids != nil && len(ids) != 0 {
		t.Errorf("expected nil or empty slice, got %v", ids)
	}
}
