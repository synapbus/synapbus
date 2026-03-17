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

func TestSQLiteMessageStore_GetInboxMessages_Offset(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "reader")

	conv := &Conversation{Subject: "offset test", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	// Insert 5 messages
	for i := 0; i < 5; i++ {
		msg := &Message{
			ConversationID: conv.ID,
			FromAgent:      "sender",
			ToAgent:        "reader",
			Body:           fmt.Sprintf("msg %d", i),
			Priority:       5,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	t.Run("offset=0 returns from beginning", func(t *testing.T) {
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{
			Limit:       2,
			Offset:      0,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 2 {
			t.Errorf("got %d messages, want 2", len(messages))
		}
	})

	t.Run("offset=2 skips first 2", func(t *testing.T) {
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{
			Limit:       2,
			Offset:      2,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 2 {
			t.Errorf("got %d messages, want 2", len(messages))
		}
	})

	t.Run("offset beyond total returns empty", func(t *testing.T) {
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{
			Limit:       10,
			Offset:      100,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 0 {
			t.Errorf("got %d messages, want 0", len(messages))
		}
	})
}

func TestSQLiteMessageStore_CountInboxMessages(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "counter")

	conv := &Conversation{Subject: "count test", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	for i := 0; i < 5; i++ {
		msg := &Message{
			ConversationID: conv.ID,
			FromAgent:      "sender",
			ToAgent:        "counter",
			Body:           "msg",
			Priority:       5,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	count, err := store.CountInboxMessages(ctx, "counter", ReadOptions{IncludeRead: true})
	if err != nil {
		t.Fatalf("CountInboxMessages: %v", err)
	}
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
}

func TestSQLiteMessageStore_SearchMessages_Offset(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "searcher")

	conv := &Conversation{Subject: "search offset test", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	for i := 0; i < 5; i++ {
		msg := &Message{
			ConversationID: conv.ID,
			FromAgent:      "sender",
			ToAgent:        "searcher",
			Body:           fmt.Sprintf("unique message %d", i),
			Priority:       5,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	t.Run("offset=0 limit=2 returns first 2", func(t *testing.T) {
		results, err := store.SearchMessages(ctx, "searcher", "", SearchOptions{
			Limit:  2,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("got %d results, want 2", len(results))
		}
	})

	t.Run("offset=3 returns remaining 2", func(t *testing.T) {
		results, err := store.SearchMessages(ctx, "searcher", "", SearchOptions{
			Limit:  10,
			Offset: 3,
		})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("got %d results, want 2", len(results))
		}
	})

	t.Run("offset beyond total returns empty", func(t *testing.T) {
		results, err := store.SearchMessages(ctx, "searcher", "", SearchOptions{
			Limit:  10,
			Offset: 100,
		})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("got %d results, want 0", len(results))
		}
	})
}

func TestSQLiteMessageStore_CountSearchMessages(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "searcher")

	conv := &Conversation{Subject: "count search", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	for i := 0; i < 3; i++ {
		msg := &Message{
			ConversationID: conv.ID,
			FromAgent:      "sender",
			ToAgent:        "searcher",
			Body:           fmt.Sprintf("deployment issue %d", i),
			Priority:       5,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	count, err := store.CountSearchMessages(ctx, "searcher", "deployment", SearchOptions{})
	if err != nil {
		t.Fatalf("CountSearchMessages: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}

	count, err = store.CountSearchMessages(ctx, "searcher", "", SearchOptions{})
	if err != nil {
		t.Fatalf("CountSearchMessages: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestSQLiteMessageStore_DateFiltering(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "reader")

	conv := &Conversation{Subject: "date test", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	// Insert messages (all at "now" since SQLite uses CURRENT_TIMESTAMP)
	for i := 0; i < 3; i++ {
		msg := &Message{
			ConversationID: conv.ID,
			FromAgent:      "sender",
			ToAgent:        "reader",
			Body:           fmt.Sprintf("dated msg %d", i),
			Priority:       5,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	t.Run("after in the past returns all", func(t *testing.T) {
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{
			IncludeRead: true,
			After:       "2020-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 3 {
			t.Errorf("got %d messages, want 3", len(messages))
		}
	})

	t.Run("after in the future returns none", func(t *testing.T) {
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{
			IncludeRead: true,
			After:       "2099-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 0 {
			t.Errorf("got %d messages, want 0", len(messages))
		}
	})

	t.Run("before in the past returns none", func(t *testing.T) {
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{
			IncludeRead: true,
			Before:      "2020-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 0 {
			t.Errorf("got %d messages, want 0", len(messages))
		}
	})

	t.Run("before in the future returns all", func(t *testing.T) {
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{
			IncludeRead: true,
			Before:      "2099-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 3 {
			t.Errorf("got %d messages, want 3", len(messages))
		}
	})

	t.Run("combined after+before", func(t *testing.T) {
		messages, err := store.GetInboxMessages(ctx, "reader", ReadOptions{
			IncludeRead: true,
			After:       "2020-01-01T00:00:00Z",
			Before:      "2099-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}
		if len(messages) != 3 {
			t.Errorf("got %d messages, want 3", len(messages))
		}
	})

	t.Run("search date filtering", func(t *testing.T) {
		results, err := store.SearchMessages(ctx, "reader", "", SearchOptions{
			After:  "2020-01-01T00:00:00Z",
			Before: "2099-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 3 {
			t.Errorf("got %d results, want 3", len(results))
		}
	})

	t.Run("search after in the future returns none", func(t *testing.T) {
		results, err := store.SearchMessages(ctx, "reader", "", SearchOptions{
			After: "2099-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("got %d results, want 0", len(results))
		}
	})
}

func TestSQLiteMessageStore_SearchMessages_ChannelFilter(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "agent-a")

	// Create a channel
	_, err := db.ExecContext(ctx,
		`INSERT INTO channels (id, name, description, topic, type, is_private, is_system, created_by, created_at, updated_at)
		 VALUES (1, 'test-channel', '', '', 'standard', 0, 0, 'agent-a', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// Add agent as member
	_, err = db.ExecContext(ctx,
		`INSERT INTO channel_members (channel_id, agent_name, role, joined_at)
		 VALUES (1, 'agent-a', 'owner', CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("add member: %v", err)
	}

	conv := &Conversation{Subject: "channel test", CreatedBy: "agent-a"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	chID := int64(1)
	// Insert a channel message
	msg := &Message{
		ConversationID: conv.ID,
		FromAgent:      "agent-a",
		ChannelID:      &chID,
		Body:           "channel message",
		Priority:       5,
		Status:         StatusPending,
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	// Insert a DM (not in channel)
	seedAgent(t, db, "agent-b")
	convDM := &Conversation{Subject: "dm test", CreatedBy: "agent-a"}
	if err := store.InsertConversation(ctx, convDM); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}
	dmMsg := &Message{
		ConversationID: convDM.ID,
		FromAgent:      "agent-a",
		ToAgent:        "agent-b",
		Body:           "dm message",
		Priority:       5,
		Status:         StatusPending,
	}
	if err := store.InsertMessage(ctx, dmMsg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	t.Run("filter by channel name", func(t *testing.T) {
		results, err := store.SearchMessages(ctx, "agent-a", "", SearchOptions{
			Channel: "test-channel",
		})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("got %d results, want 1", len(results))
		}
		if len(results) > 0 && results[0].Body != "channel message" {
			t.Errorf("body = %q, want %q", results[0].Body, "channel message")
		}
	})

	t.Run("non-existent channel returns empty", func(t *testing.T) {
		results, err := store.SearchMessages(ctx, "agent-a", "", SearchOptions{
			Channel: "no-such-channel",
		})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("got %d results, want 0", len(results))
		}
	})
}

func TestSQLiteMessageStore_GetChannelMessages_Offset(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "agent-a")

	// Create a channel
	_, err := db.ExecContext(ctx,
		`INSERT INTO channels (id, name, description, topic, type, is_private, is_system, created_by, created_at, updated_at)
		 VALUES (1, 'offset-channel', '', '', 'standard', 0, 0, 'agent-a', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	conv := &Conversation{Subject: "ch offset", CreatedBy: "agent-a"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	chID := int64(1)
	for i := 0; i < 5; i++ {
		msg := &Message{
			ConversationID: conv.ID,
			FromAgent:      "agent-a",
			ChannelID:      &chID,
			Body:           fmt.Sprintf("ch msg %d", i),
			Priority:       5,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	t.Run("offset=0 limit=3", func(t *testing.T) {
		messages, err := store.GetChannelMessages(ctx, 1, 3, 0)
		if err != nil {
			t.Fatalf("GetChannelMessages: %v", err)
		}
		if len(messages) != 3 {
			t.Errorf("got %d messages, want 3", len(messages))
		}
	})

	t.Run("offset=3 returns remaining", func(t *testing.T) {
		messages, err := store.GetChannelMessages(ctx, 1, 10, 3)
		if err != nil {
			t.Fatalf("GetChannelMessages: %v", err)
		}
		if len(messages) != 2 {
			t.Errorf("got %d messages, want 2", len(messages))
		}
	})

	t.Run("count channel messages", func(t *testing.T) {
		count, err := store.CountChannelMessages(ctx, 1)
		if err != nil {
			t.Fatalf("CountChannelMessages: %v", err)
		}
		if count != 5 {
			t.Errorf("count = %d, want 5", count)
		}
	})
}

func TestSQLiteMessageStore_GetReplyCounts(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "replier")

	conv := &Conversation{Subject: "reply counts", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	// Insert parent message
	parent := &Message{
		ConversationID: conv.ID,
		FromAgent:      "sender",
		ToAgent:        "replier",
		Body:           "parent message",
		Priority:       5,
		Status:         StatusPending,
	}
	if err := store.InsertMessage(ctx, parent); err != nil {
		t.Fatalf("InsertMessage (parent): %v", err)
	}

	// Insert 3 replies to parent
	for i := 0; i < 3; i++ {
		reply := &Message{
			ConversationID: conv.ID,
			FromAgent:      "replier",
			ToAgent:        "sender",
			ReplyTo:        &parent.ID,
			Body:           fmt.Sprintf("reply %d", i),
			Priority:       5,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, reply); err != nil {
			t.Fatalf("InsertMessage (reply %d): %v", i, err)
		}
	}

	t.Run("parent with 3 replies", func(t *testing.T) {
		counts, err := store.GetReplyCounts(ctx, []int64{parent.ID})
		if err != nil {
			t.Fatalf("GetReplyCounts: %v", err)
		}
		if counts[parent.ID] != 3 {
			t.Errorf("reply count for parent = %d, want 3", counts[parent.ID])
		}
	})

	t.Run("message with no replies returns 0", func(t *testing.T) {
		// Insert a message with no replies
		noReply := &Message{
			ConversationID: conv.ID,
			FromAgent:      "sender",
			ToAgent:        "replier",
			Body:           "no replies here",
			Priority:       5,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, noReply); err != nil {
			t.Fatalf("InsertMessage (noReply): %v", err)
		}

		counts, err := store.GetReplyCounts(ctx, []int64{noReply.ID})
		if err != nil {
			t.Fatalf("GetReplyCounts: %v", err)
		}
		if counts[noReply.ID] != 0 {
			t.Errorf("reply count for noReply = %d, want 0", counts[noReply.ID])
		}
	})

	t.Run("multiple parents", func(t *testing.T) {
		// Insert a second parent with 2 replies
		parent2 := &Message{
			ConversationID: conv.ID,
			FromAgent:      "sender",
			ToAgent:        "replier",
			Body:           "second parent",
			Priority:       5,
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, parent2); err != nil {
			t.Fatalf("InsertMessage (parent2): %v", err)
		}
		for i := 0; i < 2; i++ {
			reply := &Message{
				ConversationID: conv.ID,
				FromAgent:      "replier",
				ToAgent:        "sender",
				ReplyTo:        &parent2.ID,
				Body:           fmt.Sprintf("reply to parent2 %d", i),
				Priority:       5,
				Status:         StatusPending,
			}
			if err := store.InsertMessage(ctx, reply); err != nil {
				t.Fatalf("InsertMessage (parent2 reply %d): %v", i, err)
			}
		}

		counts, err := store.GetReplyCounts(ctx, []int64{parent.ID, parent2.ID})
		if err != nil {
			t.Fatalf("GetReplyCounts: %v", err)
		}
		if counts[parent.ID] != 3 {
			t.Errorf("reply count for parent = %d, want 3", counts[parent.ID])
		}
		if counts[parent2.ID] != 2 {
			t.Errorf("reply count for parent2 = %d, want 2", counts[parent2.ID])
		}
	})

	t.Run("empty slice returns empty map", func(t *testing.T) {
		counts, err := store.GetReplyCounts(ctx, []int64{})
		if err != nil {
			t.Fatalf("GetReplyCounts: %v", err)
		}
		if len(counts) != 0 {
			t.Errorf("expected empty map, got %v", counts)
		}
	})
}

func TestSQLiteMessageStore_CombinedFiltersAndPagination(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteMessageStore(db)
	ctx := context.Background()

	seedAgent(t, db, "sender")
	seedAgent(t, db, "reader")

	conv := &Conversation{Subject: "combined", CreatedBy: "sender"}
	if err := store.InsertConversation(ctx, conv); err != nil {
		t.Fatalf("InsertConversation: %v", err)
	}

	for i := 0; i < 10; i++ {
		msg := &Message{
			ConversationID: conv.ID,
			FromAgent:      "sender",
			ToAgent:        "reader",
			Body:           fmt.Sprintf("combined msg %d", i),
			Priority:       5 + (i % 3),
			Status:         StatusPending,
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	t.Run("filters + offset + limit", func(t *testing.T) {
		// Get all with min_priority=6 first to know expected count
		all, err := store.GetInboxMessages(ctx, "reader", ReadOptions{
			MinPriority: 6,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}

		// Now paginate through
		page1, err := store.GetInboxMessages(ctx, "reader", ReadOptions{
			MinPriority: 6,
			Limit:       2,
			Offset:      0,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("GetInboxMessages: %v", err)
		}

		count, err := store.CountInboxMessages(ctx, "reader", ReadOptions{
			MinPriority: 6,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("CountInboxMessages: %v", err)
		}

		if count != len(all) {
			t.Errorf("count = %d, want %d", count, len(all))
		}

		if len(page1) > 2 {
			t.Errorf("page1 got %d messages, want at most 2", len(page1))
		}
	})
}
