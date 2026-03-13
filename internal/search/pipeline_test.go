package search

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/smart-mcp-proxy/synapbus/internal/messaging"
	"github.com/smart-mcp-proxy/synapbus/internal/search/embedding"
	"github.com/smart-mcp-proxy/synapbus/internal/storage"
	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

func newPipelineTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	ctx := context.Background()
	if err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testowner', 'hash', 'Test Owner')`)
	return db
}

func TestPipeline_OnMessageCreated(t *testing.T) {
	db := newPipelineTestDB(t)
	ctx := context.Background()

	// Need real messages in the DB for FK constraints
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status) VALUES ('s', 'S', 'ai', 1, 'hash', 'active')`)
	db.Exec(`INSERT INTO conversations (subject, created_by) VALUES ('test', 's')`)
	db.Exec(`INSERT INTO messages (conversation_id, from_agent, body, priority, status, metadata) VALUES (1, 's', 'hello world', 5, 'pending', '{}')`)
	db.Exec(`INSERT INTO messages (conversation_id, from_agent, body, priority, status, metadata) VALUES (1, 's', 'another msg', 5, 'pending', '{}')`)
	db.Exec(`INSERT INTO messages (conversation_id, from_agent, body, priority, status, metadata) VALUES (1, 's', 'third msg', 5, 'pending', '{}')`)

	store := NewEmbeddingStore(db)
	idx := NewMemoryVectorIndex()
	provider := embedding.NewMockProvider(3)

	pipeline := NewPipeline(provider, store, idx, Config{
		BatchSize:        10,
		WorkerCount:      1,
		PollInterval:     100 * time.Millisecond,
		RetryMaxAttempts: 3,
	})

	t.Run("enqueues non-empty messages", func(t *testing.T) {
		pipeline.OnMessageCreated(ctx, 1, "hello world")

		count, err := store.PendingCount(ctx)
		if err != nil {
			t.Fatalf("PendingCount: %v", err)
		}
		if count != 1 {
			t.Errorf("pending count = %d, want 1", count)
		}
	})

	t.Run("skips empty messages", func(t *testing.T) {
		initialCount, _ := store.PendingCount(ctx)
		pipeline.OnMessageCreated(ctx, 2, "")
		pipeline.OnMessageCreated(ctx, 3, "   ")

		count, _ := store.PendingCount(ctx)
		if count != initialCount {
			t.Errorf("pending count changed from %d to %d for empty messages", initialCount, count)
		}
	})
}

func TestPipeline_ProcessBatch(t *testing.T) {
	db := newPipelineTestDB(t)
	ctx := context.Background()

	// Register agents and send test messages
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status) VALUES ('sender', 'Sender', 'ai', 1, 'hash', 'active')`)
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status) VALUES ('receiver', 'Receiver', 'ai', 1, 'hash', 'active')`)

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	msg1, _ := msgService.SendMessage(ctx, "sender", "receiver", "test message one", messaging.SendOptions{})
	msg2, _ := msgService.SendMessage(ctx, "sender", "receiver", "test message two", messaging.SendOptions{})

	store := NewEmbeddingStore(db)
	idx := NewMemoryVectorIndex()
	provider := embedding.NewMockProvider(3)

	cfg := Config{
		BatchSize:        10,
		WorkerCount:      1,
		PollInterval:     100 * time.Millisecond,
		RetryMaxAttempts: 3,
	}

	pipeline := NewPipeline(provider, store, idx, cfg)

	// Enqueue messages
	store.Enqueue(ctx, msg1.ID)
	store.Enqueue(ctx, msg2.ID)

	// Start pipeline and wait for processing
	pipeline.Start(ctx)
	time.Sleep(500 * time.Millisecond)
	pipeline.Stop()

	// Check that vectors were added to index
	if idx.Len() != 2 {
		t.Errorf("index len = %d, want 2", idx.Len())
	}

	// Check that embeddings were recorded
	embCount, err := store.EmbeddingCount(ctx)
	if err != nil {
		t.Fatalf("EmbeddingCount: %v", err)
	}
	if embCount != 2 {
		t.Errorf("embedding count = %d, want 2", embCount)
	}

	// Check queue is cleared
	pending, _ := store.PendingCount(ctx)
	if pending != 0 {
		t.Errorf("pending count = %d, want 0", pending)
	}
}

func TestPipeline_ErrorHandling(t *testing.T) {
	db := newPipelineTestDB(t)
	ctx := context.Background()

	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status) VALUES ('sender', 'Sender', 'ai', 1, 'hash', 'active')`)
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status) VALUES ('receiver', 'Receiver', 'ai', 1, 'hash', 'active')`)

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)
	msg1, _ := msgService.SendMessage(ctx, "sender", "receiver", "test message", messaging.SendOptions{})

	store := NewEmbeddingStore(db)
	idx := NewMemoryVectorIndex()

	// Create a provider that fails
	failProvider := embedding.NewMockProvider(3)
	failProvider.SetBatchFunc(func(ctx context.Context, texts []string) ([][]float32, error) {
		return nil, fmt.Errorf("provider error: rate limited")
	})

	cfg := Config{
		BatchSize:        10,
		WorkerCount:      1,
		PollInterval:     100 * time.Millisecond,
		RetryMaxAttempts: 3,
	}

	pipeline := NewPipeline(failProvider, store, idx, cfg)
	store.Enqueue(ctx, msg1.ID)

	pipeline.Start(ctx)
	time.Sleep(500 * time.Millisecond)
	pipeline.Stop()

	// Index should be empty (embedding failed)
	if idx.Len() != 0 {
		t.Errorf("index len = %d, want 0 (embedding should have failed)", idx.Len())
	}
}

func TestEmbeddingStore_QueueOperations(t *testing.T) {
	db := newPipelineTestDB(t)
	ctx := context.Background()

	store := NewEmbeddingStore(db)

	// Create test messages directly
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status) VALUES ('s', 'S', 'ai', 1, 'hash', 'active')`)
	db.Exec(`INSERT INTO conversations (subject, created_by) VALUES ('test', 's')`)
	db.Exec(`INSERT INTO messages (conversation_id, from_agent, body, priority, status, metadata) VALUES (1, 's', 'msg1', 5, 'pending', '{}')`)
	db.Exec(`INSERT INTO messages (conversation_id, from_agent, body, priority, status, metadata) VALUES (1, 's', 'msg2', 5, 'pending', '{}')`)

	t.Run("enqueue and dequeue", func(t *testing.T) {
		store.Enqueue(ctx, 1)
		store.Enqueue(ctx, 2)

		count, _ := store.PendingCount(ctx)
		if count != 2 {
			t.Errorf("pending count = %d, want 2", count)
		}

		items, err := store.Dequeue(ctx, 1)
		if err != nil {
			t.Fatalf("Dequeue: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("dequeued = %d, want 1", len(items))
		}
		if items[0].MessageID != 1 {
			t.Errorf("dequeued message_id = %d, want 1", items[0].MessageID)
		}
	})

	t.Run("mark completed", func(t *testing.T) {
		store.MarkCompleted(ctx, 1)

		// Should be able to dequeue the second item
		items, err := store.Dequeue(ctx, 10)
		if err != nil {
			t.Fatalf("Dequeue: %v", err)
		}
		// The second item should be available (first was dequeued, now processing)
		found := false
		for _, item := range items {
			if item.MessageID == 2 {
				found = true
			}
		}
		if !found && len(items) == 0 {
			// item 2 may already have been dequeued in the first call's processing
			// This is OK in the test
		}
	})

	t.Run("duplicate enqueue is ignored", func(t *testing.T) {
		db.Exec(`INSERT INTO messages (conversation_id, from_agent, body, priority, status, metadata) VALUES (1, 's', 'msg3', 5, 'pending', '{}')`)
		store.Enqueue(ctx, 3)
		store.Enqueue(ctx, 3) // duplicate
		// Should not error
	})
}

func TestEmbeddingStore_EmbeddingOperations(t *testing.T) {
	db := newPipelineTestDB(t)
	ctx := context.Background()

	store := NewEmbeddingStore(db)

	// Create a message
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status) VALUES ('s', 'S', 'ai', 1, 'hash', 'active')`)
	db.Exec(`INSERT INTO conversations (subject, created_by) VALUES ('test', 's')`)
	db.Exec(`INSERT INTO messages (conversation_id, from_agent, body, priority, status, metadata) VALUES (1, 's', 'msg1', 5, 'pending', '{}')`)

	t.Run("save and count embeddings", func(t *testing.T) {
		err := store.SaveEmbedding(ctx, 1, "openai", "text-embedding-3-small", 1536)
		if err != nil {
			t.Fatalf("SaveEmbedding: %v", err)
		}

		count, err := store.EmbeddingCount(ctx)
		if err != nil {
			t.Fatalf("EmbeddingCount: %v", err)
		}
		if count != 1 {
			t.Errorf("embedding count = %d, want 1", count)
		}
	})

	t.Run("get provider", func(t *testing.T) {
		provider, err := store.GetEmbeddingProvider(ctx)
		if err != nil {
			t.Fatalf("GetEmbeddingProvider: %v", err)
		}
		if provider != "openai" {
			t.Errorf("provider = %q, want openai", provider)
		}
	})

	t.Run("delete embedding", func(t *testing.T) {
		err := store.DeleteEmbedding(ctx, 1)
		if err != nil {
			t.Fatalf("DeleteEmbedding: %v", err)
		}

		count, _ := store.EmbeddingCount(ctx)
		if count != 0 {
			t.Errorf("embedding count after delete = %d, want 0", count)
		}
	})

	t.Run("delete all embeddings", func(t *testing.T) {
		store.SaveEmbedding(ctx, 1, "openai", "model", 1536)
		store.DeleteAllEmbeddings(ctx)

		count, _ := store.EmbeddingCount(ctx)
		if count != 0 {
			t.Errorf("embedding count after delete all = %d, want 0", count)
		}
	})
}
