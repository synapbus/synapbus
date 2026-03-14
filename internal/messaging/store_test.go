package messaging

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/storage"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Use file::memory: with shared cache so all connections see the same database
	// Each test gets a unique name to avoid cross-test interference
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	// Run migrations
	ctx := context.Background()
	if err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	return db
}

// seedAgent inserts a test agent and its owner.
func seedAgent(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	// Ensure a user exists for the owner_id
	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testowner', 'hash', 'Test Owner')`)
	_, err := db.Exec(
		`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES (?, ?, 'ai', '{}', 1, 'testhash', 'active')`,
		name, name,
	)
	if err != nil {
		t.Fatalf("seed agent %s: %v", name, err)
	}
}

func TestSQLiteMessageStore_InsertConversation(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	conv := &Conversation{
		Subject:   "Test Subject",
		CreatedBy: "agent-a",
	}

	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	if conv.ID == 0 {
		t.Error("conversation ID should not be 0")
	}

	// Verify conversation exists
	got, err := store.GetConversation(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if got.Subject != "Test Subject" {
		t.Errorf("Subject = %q, want %q", got.Subject, "Test Subject")
	}
}

func TestSQLiteMessageStore_InsertAndGetMessage(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "receiver")

	conv := &Conversation{Subject: "test", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	msg := &Message{
		ConversationID: conv.ID,
		FromAgent:      "sender",
		ToAgent:        "receiver",
		Body:           "Hello!",
		Priority:       5,
		Status:         StatusPending,
	}

	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	if msg.ID == 0 {
		t.Error("message ID should not be 0")
	}

	got, err := store.GetMessageByID(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetMessageByID: %v", err)
	}

	if got.Body != "Hello!" {
		t.Errorf("Body = %q, want %q", got.Body, "Hello!")
	}
	if got.FromAgent != "sender" {
		t.Errorf("FromAgent = %q, want %q", got.FromAgent, "sender")
	}
	if got.ToAgent != "receiver" {
		t.Errorf("ToAgent = %q, want %q", got.ToAgent, "receiver")
	}
}

func TestSQLiteMessageStore_FindConversation(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "agent-a")
	seedAgent(t, db, "agent-b")

	// Create conversation and add a message
	conv := &Conversation{Subject: "Topic X", CreatedBy: "agent-a"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	msg := &Message{
		ConversationID: conv.ID,
		FromAgent:      "agent-a",
		ToAgent:        "agent-b",
		Body:           "test",
		Priority:       5,
		Status:         StatusPending,
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	tests := []struct {
		name     string
		subject  string
		from     string
		to       string
		wantErr  bool
	}{
		{
			name:    "find existing conversation",
			subject: "Topic X",
			from:    "agent-a",
			to:      "agent-b",
			wantErr: false,
		},
		{
			name:    "find reverse direction",
			subject: "Topic X",
			from:    "agent-b",
			to:      "agent-a",
			wantErr: false,
		},
		{
			name:    "no match for different subject",
			subject: "Topic Y",
			from:    "agent-a",
			to:      "agent-b",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, err := store.FindConversation(ctx, tt.subject, tt.from, tt.to)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("FindConversation: %v", err)
			}
			if found.ID != conv.ID {
				t.Errorf("found conversation ID = %d, want %d", found.ID, conv.ID)
			}
		})
	}
}

func TestSQLiteMessageStore_GetInboxMessages(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "reader")

	conv := &Conversation{Subject: "inbox test", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	// Insert messages with different priorities
	for _, p := range []int{3, 8, 5} {
		msg := &Message{
			ConversationID: conv.ID,
			FromAgent:      "sender",
			ToAgent:        "reader",
			Body:           "msg",
			Priority:       p,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	t.Run("returns messages ordered by priority desc", func(t *testing.T) {
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{IncludeRead: true})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 3 {
			t.Fatalf("got %d messages, want 3", len(messages))
		}
		if messages[0].Priority != 8 {
			t.Errorf("first message priority = %d, want 8", messages[0].Priority)
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{Limit: 1, IncludeRead: true})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 1 {
			t.Errorf("got %d messages, want 1", len(messages))
		}
	})

	t.Run("filters by status", func(t *testing.T) {
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{
			Status:      StatusProcessing,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 0 {
			t.Errorf("got %d messages, want 0 (no processing messages)", len(messages))
		}
	})

	t.Run("read/unread tracking", func(t *testing.T) {
		// Read all messages (unread only)
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 3 {
			t.Fatalf("got %d messages, want 3", len(messages))
		}

		// Advance inbox state
		maxID := messages[0].ID
		for _, m := range messages {
			if m.ID > maxID {
				maxID = m.ID
			}
		}
		if err := store.UpdateInboxState(ctx, "reader", conv.ID, maxID); err != nil {
			t.Fatalf("UpdateInboxState: %v", err)
		}

		// Read again without include_read — should be empty
		messages, err = store.GetInboxMessages(ctx, "reader", ReadOptions{})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 0 {
			t.Errorf("got %d messages after reading, want 0", len(messages))
		}

		// Read again with include_read — should return all
		messages, err = store.GetInboxMessages(ctx, "reader", ReadOptions{IncludeRead: true})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 3 {
			t.Errorf("got %d messages with include_read, want 3", len(messages))
		}
	})
}

func TestSQLiteMessageStore_ClaimMessages(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "worker")

	conv := &Conversation{Subject: "claim test", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	// Insert 5 pending messages
	for i := 0; i < 5; i++ {
		msg := &Message{
			ConversationID: conv.ID,
			FromAgent:      "sender",
			ToAgent:        "worker",
			Body:           "task",
			Priority:       5,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	t.Run("claim with limit", func(t *testing.T) {
		claimed, err := store.ClaimMessages(ctx, "worker", 3)
		if err != nil {
			t.Fatalf("ClaimMessages: %v", err)
		}
		if len(claimed) != 3 {
			t.Errorf("claimed %d messages, want 3", len(claimed))
		}
		for _, msg := range claimed {
			if msg.Status != StatusProcessing {
				t.Errorf("claimed message status = %s, want %s", msg.Status, StatusProcessing)
			}
			if msg.ClaimedBy != "worker" {
				t.Errorf("claimed_by = %s, want worker", msg.ClaimedBy)
			}
		}
	})

	t.Run("already claimed messages are skipped", func(t *testing.T) {
		// Only 2 remaining pending
		claimed, err := store.ClaimMessages(ctx, "worker", 10)
		if err != nil {
			t.Fatalf("ClaimMessages: %v", err)
		}
		if len(claimed) != 2 {
			t.Errorf("claimed %d messages, want 2", len(claimed))
		}
	})

	t.Run("no pending returns empty", func(t *testing.T) {
		claimed, err := store.ClaimMessages(ctx, "worker", 5)
		if err != nil {
			t.Fatalf("ClaimMessages: %v", err)
		}
		if len(claimed) != 0 {
			t.Errorf("claimed %d messages, want 0", len(claimed))
		}
	})
}

func TestSQLiteMessageStore_UpdateMessageStatus(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "worker")

	conv := &Conversation{Subject: "status test", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	msg := &Message{
		ConversationID: conv.ID,
		FromAgent:      "sender",
		ToAgent:        "worker",
		Body:           "test",
		Priority:       5,
		Status:         StatusPending,
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	// Claim the message first
	claimed, err := store.ClaimMessages(ctx, "worker", 1)
	if err != nil {
		t.Fatalf("ClaimMessages: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d, want 1", len(claimed))
	}

	t.Run("mark done by correct agent", func(t *testing.T) {
		err := store.UpdateMessageStatus(ctx, claimed[0].ID, StatusDone, "worker", nil)
		if err != nil {
			t.Fatalf("UpdateMessageStatus: %v", err)
		}

		got, err := store.GetMessageByID(ctx, claimed[0].ID)
		if err != nil {
			t.Fatalf("GetMessageByID: %v", err)
		}
		if got.Status != StatusDone {
			t.Errorf("status = %s, want %s", got.Status, StatusDone)
		}
	})

	t.Run("wrong agent cannot update", func(t *testing.T) {
		err := store.UpdateMessageStatus(ctx, claimed[0].ID, StatusDone, "other-agent", nil)
		if err == nil {
			t.Error("expected error for wrong agent, got nil")
		}
	})
}

func TestSQLiteMessageStore_SearchMessages(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "searcher")

	conv := &Conversation{Subject: "search test", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	msgs := []struct {
		body     string
		priority int
	}{
		{"deployment failure in production", 8},
		{"deployment succeeded on staging", 3},
		{"security alert: unauthorized access", 9},
	}

	for _, m := range msgs {
		msg := &Message{
			ConversationID: conv.ID,
			FromAgent:      "sender",
			ToAgent:        "searcher",
			Body:           m.body,
			Priority:       m.priority,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	t.Run("FTS keyword match", func(t *testing.T) {
		results, err := store.SearchMessages(ctx, "searcher", "deployment", SearchOptions{})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("got %d results, want 2", len(results))
		}
	})

	t.Run("empty query returns recent", func(t *testing.T) {
		results, err := store.SearchMessages(ctx, "searcher", "", SearchOptions{})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 3 {
			t.Errorf("got %d results, want 3", len(results))
		}
	})

	t.Run("min_priority filter", func(t *testing.T) {
		results, err := store.SearchMessages(ctx, "searcher", "", SearchOptions{MinPriority: 7})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("got %d results, want 2", len(results))
		}
	})

	t.Run("limit", func(t *testing.T) {
		results, err := store.SearchMessages(ctx, "searcher", "", SearchOptions{Limit: 1})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("got %d results, want 1", len(results))
		}
	})
}

func TestSQLiteMessageStore_AgentExists(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "exists-agent")

	tests := []struct {
		name   string
		agent  string
		exists bool
	}{
		{"existing agent", "exists-agent", true},
		{"non-existing agent", "ghost-agent", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, err := store.AgentExists(ctx, tt.agent)
			if err != nil {
				t.Fatalf("AgentExists: %v", err)
			}
			if exists != tt.exists {
				t.Errorf("AgentExists(%s) = %v, want %v", tt.agent, exists, tt.exists)
			}
		})
	}
}
