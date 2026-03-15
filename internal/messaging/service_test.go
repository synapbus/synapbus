package messaging

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/storage"
	"github.com/synapbus/synapbus/internal/trace"
)

func newTestService(t *testing.T) (*MessagingService, *sql.DB) {
	t.Helper()
	db := newTestDB(t)

	// Seed test agents
	seedAgent(t, db, "sender")
	seedAgent(t, db, "receiver")
	seedAgent(t, db, "worker")

	store := NewSQLiteMessageStore(db)
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	svc := NewMessagingService(store, tracer)
	return svc, db
}

func TestMessagingService_SendMessage(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		body    string
		opts    SendOptions
		wantErr bool
	}{
		{
			name: "successful send",
			from: "sender",
			to:   "receiver",
			body: "Hello, world!",
			opts: SendOptions{Subject: "Test", Priority: 5},
		},
		{
			name:    "empty body fails",
			from:    "sender",
			to:      "receiver",
			body:    "",
			opts:    SendOptions{},
			wantErr: true,
		},
		{
			name:    "whitespace body fails",
			from:    "sender",
			to:      "receiver",
			body:    "   ",
			opts:    SendOptions{},
			wantErr: true,
		},
		{
			name:    "invalid priority",
			from:    "sender",
			to:      "receiver",
			body:    "test",
			opts:    SendOptions{Priority: 15},
			wantErr: true,
		},
		{
			name:    "priority too low",
			from:    "sender",
			to:      "receiver",
			body:    "test",
			opts:    SendOptions{Priority: -1},
			wantErr: true,
		},
		{
			name:    "invalid metadata JSON",
			from:    "sender",
			to:      "receiver",
			body:    "test",
			opts:    SendOptions{Metadata: "not json{"},
			wantErr: true,
		},
		{
			name:    "non-existent recipient",
			from:    "sender",
			to:      "ghost-agent",
			body:    "test",
			opts:    SendOptions{},
			wantErr: true,
		},
		{
			name:    "no recipient specified",
			from:    "sender",
			to:      "",
			body:    "test",
			opts:    SendOptions{},
			wantErr: true,
		},
		{
			name: "valid metadata",
			from: "sender",
			to:   "receiver",
			body: "with metadata",
			opts: SendOptions{Metadata: `{"key":"value"}`},
		},
		{
			name: "default priority",
			from: "sender",
			to:   "receiver",
			body: "default priority",
			opts: SendOptions{},
		},
		{
			name: "priority 1 is valid",
			from: "sender",
			to:   "receiver",
			body: "low priority",
			opts: SendOptions{Priority: 1},
		},
		{
			name: "priority 10 is valid",
			from: "sender",
			to:   "receiver",
			body: "high priority",
			opts: SendOptions{Priority: 10},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			ctx := context.Background()

			msg, err := svc.SendMessage(ctx, tt.from, tt.to, tt.body, tt.opts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SendMessage() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}

			if msg.ID == 0 {
				t.Error("message ID should not be 0")
			}
			if msg.Status != StatusPending {
				t.Errorf("status = %s, want %s", msg.Status, StatusPending)
			}
			if msg.FromAgent != tt.from {
				t.Errorf("from = %s, want %s", msg.FromAgent, tt.from)
			}
			if msg.ToAgent != tt.to {
				t.Errorf("to = %s, want %s", msg.ToAgent, tt.to)
			}
		})
	}
}

func TestMessagingService_ConversationReuse(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	msg1, err := svc.SendMessage(ctx, "sender", "receiver", "first message", SendOptions{Subject: "Reuse Topic"})
	if err != nil {
		t.Fatalf("SendMessage 1: %v", err)
	}

	msg2, err := svc.SendMessage(ctx, "sender", "receiver", "second message", SendOptions{Subject: "Reuse Topic"})
	if err != nil {
		t.Fatalf("SendMessage 2: %v", err)
	}

	if msg1.ConversationID != msg2.ConversationID {
		t.Errorf("conversations should be reused: msg1=%d, msg2=%d", msg1.ConversationID, msg2.ConversationID)
	}
}

func TestMessagingService_ReadInbox(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.SendMessage(ctx, "sender", "receiver", "msg 1", SendOptions{Priority: 3})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	_, err = svc.SendMessage(ctx, "sender", "receiver", "msg 2", SendOptions{Priority: 8})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	t.Run("returns messages ordered by priority desc", func(t *testing.T) {
		result, err := svc.ReadInbox(ctx, "receiver", ReadOptions{IncludeRead: true})
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		if len(result.Messages) != 2 {
			t.Fatalf("got %d messages, want 2", len(result.Messages))
		}
		if result.Messages[0].Priority < result.Messages[1].Priority {
			t.Errorf("messages not ordered by priority desc: %d, %d", result.Messages[0].Priority, result.Messages[1].Priority)
		}
		if result.Total != 2 {
			t.Errorf("total = %d, want 2", result.Total)
		}
	})
}

func TestMessagingService_ReadInbox_ReadUnread(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.SendMessage(ctx, "sender", "receiver", "msg 1", SendOptions{Priority: 3})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	_, err = svc.SendMessage(ctx, "sender", "receiver", "msg 2", SendOptions{Priority: 8})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// First read
	result, err := svc.ReadInbox(ctx, "receiver", ReadOptions{})
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected messages on first read")
	}

	// Second read without include_read
	result, err = svc.ReadInbox(ctx, "receiver", ReadOptions{})
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(result.Messages) != 0 {
		t.Errorf("got %d messages on second read (no include_read), want 0", len(result.Messages))
	}

	// With include_read
	result, err = svc.ReadInbox(ctx, "receiver", ReadOptions{IncludeRead: true})
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Errorf("got %d messages with include_read, want 2", len(result.Messages))
	}
}

func TestMessagingService_ReadInbox_Filters(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	svc.SendMessage(ctx, "sender", "receiver", "low pri", SendOptions{Priority: 3})
	svc.SendMessage(ctx, "sender", "receiver", "high pri", SendOptions{Priority: 8})

	t.Run("filter by from_agent", func(t *testing.T) {
		result, err := svc.ReadInbox(ctx, "receiver", ReadOptions{
			FromAgent:   "sender",
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		if len(result.Messages) != 2 {
			t.Errorf("got %d messages from sender, want 2", len(result.Messages))
		}

		result, err = svc.ReadInbox(ctx, "receiver", ReadOptions{
			FromAgent:   "nobody",
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		if len(result.Messages) != 0 {
			t.Errorf("got %d messages from nobody, want 0", len(result.Messages))
		}
	})

	t.Run("filter by min_priority", func(t *testing.T) {
		result, err := svc.ReadInbox(ctx, "receiver", ReadOptions{
			MinPriority: 7,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		if len(result.Messages) != 1 {
			t.Errorf("got %d messages with min_priority=7, want 1", len(result.Messages))
		}
	})

	t.Run("limit", func(t *testing.T) {
		result, err := svc.ReadInbox(ctx, "receiver", ReadOptions{
			Limit:       1,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		if len(result.Messages) != 1 {
			t.Errorf("got %d messages with limit=1, want 1", len(result.Messages))
		}
	})
}

func TestMessagingService_ClaimMessages(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := svc.SendMessage(ctx, "sender", "worker", "task", SendOptions{Priority: 5})
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	}

	// Claim 3
	claimed, err := svc.ClaimMessages(ctx, "worker", 3)
	if err != nil {
		t.Fatalf("ClaimMessages: %v", err)
	}
	if len(claimed) != 3 {
		t.Errorf("claimed %d, want 3", len(claimed))
	}
	for _, m := range claimed {
		if m.Status != StatusProcessing {
			t.Errorf("status = %s, want %s", m.Status, StatusProcessing)
		}
	}

	// Claim remaining
	claimed2, err := svc.ClaimMessages(ctx, "worker", 10)
	if err != nil {
		t.Fatalf("ClaimMessages: %v", err)
	}
	if len(claimed2) != 2 {
		t.Errorf("claimed %d, want 2", len(claimed2))
	}

	// No more pending
	claimed3, err := svc.ClaimMessages(ctx, "worker", 5)
	if err != nil {
		t.Fatalf("ClaimMessages: %v", err)
	}
	if len(claimed3) != 0 {
		t.Errorf("claimed %d, want 0", len(claimed3))
	}
}

func TestMessagingService_MarkDone(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	msg, err := svc.SendMessage(ctx, "sender", "worker", "task", SendOptions{Priority: 5})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	claimed, err := svc.ClaimMessages(ctx, "worker", 1)
	if err != nil {
		t.Fatalf("ClaimMessages: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d, want 1", len(claimed))
	}

	// Mark done
	err = svc.MarkDone(ctx, msg.ID, "worker")
	if err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	// Verify status
	got, err := svc.store.GetMessageByID(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetMessageByID: %v", err)
	}
	if got.Status != StatusDone {
		t.Errorf("status = %s, want %s", got.Status, StatusDone)
	}
}

func TestMessagingService_MarkDone_AlreadyDone(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	msg, _ := svc.SendMessage(ctx, "sender", "worker", "task", SendOptions{})
	svc.ClaimMessages(ctx, "worker", 1)
	svc.MarkDone(ctx, msg.ID, "worker")

	// Try again
	err := svc.MarkDone(ctx, msg.ID, "worker")
	if err == nil {
		t.Error("expected error for marking already-done message")
	}
}

func TestMessagingService_MarkDone_WrongAgent(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	msg, _ := svc.SendMessage(ctx, "sender", "worker", "task", SendOptions{})
	svc.ClaimMessages(ctx, "worker", 1)

	err := svc.MarkDone(ctx, msg.ID, "sender")
	if err == nil {
		t.Error("expected error for wrong agent")
	}
}

func TestMessagingService_MarkDone_NonExistent(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	err := svc.MarkDone(ctx, 99999, "worker")
	if err == nil {
		t.Error("expected error for non-existent message")
	}
}

func TestMessagingService_MarkFailed(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	msg, err := svc.SendMessage(ctx, "sender", "worker", "failing task", SendOptions{})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	svc.ClaimMessages(ctx, "worker", 1)

	err = svc.MarkFailed(ctx, msg.ID, "worker", "timeout error")
	if err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	got, err := svc.store.GetMessageByID(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetMessageByID: %v", err)
	}
	if got.Status != StatusFailed {
		t.Errorf("status = %s, want %s", got.Status, StatusFailed)
	}
	if string(got.Metadata) == "{}" {
		t.Error("expected metadata to contain error info")
	}
}

func TestMessagingService_SearchMessages(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	svc.SendMessage(ctx, "sender", "receiver", "deployment failure in prod", SendOptions{Priority: 8})
	svc.SendMessage(ctx, "sender", "receiver", "deployment success in staging", SendOptions{Priority: 3})
	svc.SendMessage(ctx, "sender", "receiver", "security alert detected", SendOptions{Priority: 9})

	t.Run("keyword search", func(t *testing.T) {
		result, err := svc.SearchMessages(ctx, "receiver", "deployment", SearchOptions{})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(result.Messages) != 2 {
			t.Errorf("got %d results, want 2", len(result.Messages))
		}
		if result.Total != 2 {
			t.Errorf("total = %d, want 2", result.Total)
		}
	})

	t.Run("empty query returns recent", func(t *testing.T) {
		result, err := svc.SearchMessages(ctx, "receiver", "", SearchOptions{})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(result.Messages) != 3 {
			t.Errorf("got %d results, want 3", len(result.Messages))
		}
	})
}

func TestMessagingService_GetConversation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	msg1, _ := svc.SendMessage(ctx, "sender", "receiver", "first in thread", SendOptions{Subject: "Thread"})
	svc.SendMessage(ctx, "sender", "receiver", "second in thread", SendOptions{Subject: "Thread"})

	conv, messages, err := svc.GetConversation(ctx, msg1.ConversationID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}

	if conv.Subject != "Thread" {
		t.Errorf("subject = %s, want Thread", conv.Subject)
	}

	if len(messages) != 2 {
		t.Errorf("got %d messages, want 2", len(messages))
	}
}

func TestMessagingService_TracesRecorded(t *testing.T) {
	svc, db := newTestService(t)
	ctx := context.Background()

	svc.SendMessage(ctx, "sender", "receiver", "traced message", SendOptions{})
	svc.ReadInbox(ctx, "receiver", ReadOptions{IncludeRead: true})

	// Wait for async trace writes with retries
	var count int
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		err := db.QueryRow("SELECT COUNT(*) FROM traces").Scan(&count)
		if err != nil {
			t.Fatalf("count traces: %v", err)
		}
		if count >= 2 {
			break
		}
	}
	if count < 2 {
		t.Errorf("expected at least 2 traces, got %d", count)
	}
}

func TestMessagingService_ReadInbox_Pagination(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// Send 5 messages
	for i := 0; i < 5; i++ {
		_, err := svc.SendMessage(ctx, "sender", "receiver", "paginated msg", SendOptions{Priority: 5})
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	}

	t.Run("offset pagination returns correct page", func(t *testing.T) {
		result, err := svc.ReadInbox(ctx, "receiver", ReadOptions{
			Limit:       2,
			Offset:      0,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		if len(result.Messages) != 2 {
			t.Errorf("got %d messages, want 2", len(result.Messages))
		}
		if result.Total != 5 {
			t.Errorf("total = %d, want 5", result.Total)
		}
		if result.Offset != 0 {
			t.Errorf("offset = %d, want 0", result.Offset)
		}
		if result.Limit != 2 {
			t.Errorf("limit = %d, want 2", result.Limit)
		}
	})

	t.Run("offset=3 returns remaining 2", func(t *testing.T) {
		result, err := svc.ReadInbox(ctx, "receiver", ReadOptions{
			Limit:       10,
			Offset:      3,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		if len(result.Messages) != 2 {
			t.Errorf("got %d messages, want 2", len(result.Messages))
		}
		if result.Total != 5 {
			t.Errorf("total = %d, want 5", result.Total)
		}
	})

	t.Run("offset beyond total returns empty", func(t *testing.T) {
		result, err := svc.ReadInbox(ctx, "receiver", ReadOptions{
			Limit:       10,
			Offset:      100,
			IncludeRead: true,
		})
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		if len(result.Messages) != 0 {
			t.Errorf("got %d messages, want 0", len(result.Messages))
		}
		if result.Total != 5 {
			t.Errorf("total = %d, want 5", result.Total)
		}
	})
}

func TestMessagingService_SearchMessages_Pagination(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		svc.SendMessage(ctx, "sender", "receiver", "searchable item", SendOptions{Priority: 5})
	}

	t.Run("pagination with total count", func(t *testing.T) {
		result, err := svc.SearchMessages(ctx, "receiver", "searchable", SearchOptions{
			Limit:  2,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(result.Messages) != 2 {
			t.Errorf("got %d results, want 2", len(result.Messages))
		}
		if result.Total != 5 {
			t.Errorf("total = %d, want 5", result.Total)
		}
		if result.Offset != 0 {
			t.Errorf("offset = %d, want 0", result.Offset)
		}
		if result.Limit != 2 {
			t.Errorf("limit = %d, want 2", result.Limit)
		}
	})

	t.Run("second page", func(t *testing.T) {
		result, err := svc.SearchMessages(ctx, "receiver", "searchable", SearchOptions{
			Limit:  2,
			Offset: 2,
		})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(result.Messages) != 2 {
			t.Errorf("got %d results, want 2", len(result.Messages))
		}
		if result.Total != 5 {
			t.Errorf("total = %d, want 5", result.Total)
		}
	})
}

func TestMessagingService_GetChannelMessages_Pagination(t *testing.T) {
	svc, db := newTestService(t)
	ctx := context.Background()

	// Create a channel
	_, err := db.Exec(
		`INSERT INTO channels (id, name, description, topic, type, is_private, is_system, created_by, created_at, updated_at)
		 VALUES (1, 'pag-channel', '', '', 'standard', 0, 0, 'sender', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	chID := int64(1)
	for i := 0; i < 5; i++ {
		svc.SendMessage(ctx, "sender", "", "ch msg", SendOptions{
			ChannelID: &chID,
			Priority:  5,
		})
	}

	t.Run("pagination with total", func(t *testing.T) {
		result, err := svc.GetChannelMessages(ctx, 1, 2, 0)
		if err != nil {
			t.Fatalf("GetChannelMessages: %v", err)
		}
		if len(result.Messages) != 2 {
			t.Errorf("got %d messages, want 2", len(result.Messages))
		}
		if result.Total != 5 {
			t.Errorf("total = %d, want 5", result.Total)
		}
		if result.Offset != 0 {
			t.Errorf("offset = %d, want 0", result.Offset)
		}
	})

	t.Run("offset=3 returns remaining", func(t *testing.T) {
		result, err := svc.GetChannelMessages(ctx, 1, 10, 3)
		if err != nil {
			t.Fatalf("GetChannelMessages: %v", err)
		}
		if len(result.Messages) != 2 {
			t.Errorf("got %d messages, want 2", len(result.Messages))
		}
		if result.Total != 5 {
			t.Errorf("total = %d, want 5", result.Total)
		}
	})
}

func TestMessagingService_ReadInbox_DateFiltering(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	svc.SendMessage(ctx, "sender", "receiver", "dated msg", SendOptions{Priority: 5})

	t.Run("after in the past returns messages", func(t *testing.T) {
		result, err := svc.ReadInbox(ctx, "receiver", ReadOptions{
			IncludeRead: true,
			After:       "2020-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		if len(result.Messages) != 1 {
			t.Errorf("got %d messages, want 1", len(result.Messages))
		}
	})

	t.Run("after in the future returns empty", func(t *testing.T) {
		result, err := svc.ReadInbox(ctx, "receiver", ReadOptions{
			IncludeRead: true,
			After:       "2099-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		if len(result.Messages) != 0 {
			t.Errorf("got %d messages, want 0", len(result.Messages))
		}
	})
}

// suppress unused import warning for storage package
var _ = storage.RunMigrations
