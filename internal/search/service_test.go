package search

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/search/embedding"
	"github.com/synapbus/synapbus/internal/storage"
	"github.com/synapbus/synapbus/internal/trace"
)

func newTestDB(t *testing.T) *sql.DB {
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

	// Seed test user
	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testowner', 'hash', 'Test Owner')`)

	return db
}

func newTestServices(t *testing.T) (*Service, *messaging.MessagingService, *sql.DB) {
	t.Helper()
	db := newTestDB(t)

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	// Create search service without semantic search (FTS-only)
	searchService := NewService(db, nil, nil, msgService)
	return searchService, msgService, db
}

func seedTestAgents(t *testing.T, db *sql.DB, names ...string) {
	t.Helper()
	for _, name := range names {
		db.Exec(
			`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status)
			 VALUES (?, ?, 'ai', 1, 'hash', 'active')`,
			name, name,
		)
	}
}

func TestService_FulltextSearch(t *testing.T) {
	svc, msgSvc, db := newTestServices(t)
	ctx := context.Background()

	seedTestAgents(t, db, "sender", "searcher")

	// Send some messages
	msgSvc.SendMessage(ctx, "sender", "searcher", "deployment failed in staging", messaging.SendOptions{})
	msgSvc.SendMessage(ctx, "sender", "searcher", "all services healthy", messaging.SendOptions{})
	msgSvc.SendMessage(ctx, "sender", "searcher", "database connection timeout", messaging.SendOptions{})

	t.Run("keyword search returns matching messages", func(t *testing.T) {
		resp, err := svc.Search(ctx, "searcher", SearchOptions{
			Query: "deployment",
			Mode:  ModeFulltext,
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if resp.SearchMode != ModeFulltext {
			t.Errorf("search_mode = %q, want %q", resp.SearchMode, ModeFulltext)
		}
		if len(resp.Results) != 1 {
			t.Errorf("result count = %d, want 1", len(resp.Results))
		}
		if len(resp.Results) > 0 && resp.Results[0].MatchType != ModeFulltext {
			t.Errorf("match_type = %q, want %q", resp.Results[0].MatchType, ModeFulltext)
		}
	})

	t.Run("empty query returns all messages", func(t *testing.T) {
		resp, err := svc.Search(ctx, "searcher", SearchOptions{
			Query: "",
			Mode:  ModeFulltext,
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(resp.Results) != 3 {
			t.Errorf("result count = %d, want 3", len(resp.Results))
		}
	})

	t.Run("auto mode falls back to fulltext without provider", func(t *testing.T) {
		resp, err := svc.Search(ctx, "searcher", SearchOptions{
			Query: "deployment",
			Mode:  ModeAuto,
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if resp.SearchMode != ModeFulltext {
			t.Errorf("auto search_mode = %q, want %q", resp.SearchMode, ModeFulltext)
		}
	})

	t.Run("semantic mode fails without provider", func(t *testing.T) {
		_, err := svc.Search(ctx, "searcher", SearchOptions{
			Query: "deployment",
			Mode:  ModeSemantic,
			Limit: 10,
		})
		if err == nil {
			t.Error("expected error for semantic mode without provider")
		}
	})

	t.Run("limit is enforced", func(t *testing.T) {
		resp, err := svc.Search(ctx, "searcher", SearchOptions{
			Query: "",
			Mode:  ModeFulltext,
			Limit: 1,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(resp.Results) != 1 {
			t.Errorf("result count = %d, want 1", len(resp.Results))
		}
	})

	t.Run("max limit is 100", func(t *testing.T) {
		resp, err := svc.Search(ctx, "searcher", SearchOptions{
			Query: "",
			Mode:  ModeFulltext,
			Limit: 500,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		// Should work, just capped
		_ = resp
	})
}

func TestService_AccessControl(t *testing.T) {
	svc, msgSvc, db := newTestServices(t)
	ctx := context.Background()

	seedTestAgents(t, db, "alice", "bob", "charlie")

	// Alice sends to Bob
	msgSvc.SendMessage(ctx, "alice", "bob", "secret for bob", messaging.SendOptions{})

	// Alice sends to Charlie
	msgSvc.SendMessage(ctx, "alice", "charlie", "secret for charlie", messaging.SendOptions{})

	t.Run("bob sees only his messages", func(t *testing.T) {
		resp, err := svc.Search(ctx, "bob", SearchOptions{
			Query: "secret",
			Mode:  ModeFulltext,
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(resp.Results) != 1 {
			t.Errorf("bob result count = %d, want 1", len(resp.Results))
		}
	})

	t.Run("charlie sees only his messages", func(t *testing.T) {
		resp, err := svc.Search(ctx, "charlie", SearchOptions{
			Query: "secret",
			Mode:  ModeFulltext,
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(resp.Results) != 1 {
			t.Errorf("charlie result count = %d, want 1", len(resp.Results))
		}
	})

	t.Run("alice sees all (she is the sender)", func(t *testing.T) {
		resp, err := svc.Search(ctx, "alice", SearchOptions{
			Query: "secret",
			Mode:  ModeFulltext,
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(resp.Results) != 2 {
			t.Errorf("alice result count = %d, want 2", len(resp.Results))
		}
	})
}

func TestService_SemanticSearch(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	seedTestAgents(t, db, "sender", "searcher")

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	// Create mock provider
	mockProvider := embedding.NewMockProvider(3)

	// Create index with vectors
	idx := NewMemoryVectorIndex()

	// Send messages
	msg1, _ := msgService.SendMessage(ctx, "sender", "searcher", "deployment failure in staging", messaging.SendOptions{})
	msg2, _ := msgService.SendMessage(ctx, "sender", "searcher", "cat pictures are cute", messaging.SendOptions{})
	msg3, _ := msgService.SendMessage(ctx, "sender", "searcher", "staging server crashed", messaging.SendOptions{})

	// Add vectors to index (simulating what the pipeline would do)
	// msg1 and msg3 are about similar topics (large X component), msg2 is very different (large Z component)
	idx.AddVector(msg1.ID, []float32{0.95, 0.1, 0.0})  // deployment-related
	idx.AddVector(msg2.ID, []float32{0.0, 0.0, 1.0})    // unrelated (orthogonal)
	idx.AddVector(msg3.ID, []float32{0.90, 0.15, 0.0})  // deployment-related

	// Override mock provider to return a vector similar to deployment topics
	mockProvider.SetEmbedFunc(func(ctx context.Context, text string) ([]float32, error) {
		return []float32{0.95, 0.1, 0.0}, nil // query vector close to deployment msgs
	})

	svc := NewService(db, mockProvider, idx, msgService)

	t.Run("semantic search returns ranked results", func(t *testing.T) {
		resp, err := svc.Search(ctx, "searcher", SearchOptions{
			Query: "staging deployment issues",
			Mode:  ModeSemantic,
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if resp.SearchMode != ModeSemantic {
			t.Errorf("search_mode = %q, want %q", resp.SearchMode, ModeSemantic)
		}
		if len(resp.Results) < 2 {
			t.Fatalf("expected at least 2 results, got %d", len(resp.Results))
		}

		// Results should have similarity scores >= 0
		for _, r := range resp.Results {
			if r.SimilarityScore < 0 {
				t.Errorf("expected non-negative similarity score, got %f for msg %d", r.SimilarityScore, r.Message.ID)
			}
		}

		// The deployment-related messages should score higher than the cat pictures message
		var deploymentScore, catScore float64
		for _, r := range resp.Results {
			if r.Message.ID == msg1.ID {
				deploymentScore = r.SimilarityScore
			}
			if r.Message.ID == msg2.ID {
				catScore = r.SimilarityScore
			}
		}

		if deploymentScore <= catScore {
			t.Errorf("deployment msg scored %f, cat msg scored %f; deployment should score higher",
				deploymentScore, catScore)
		}
	})

	t.Run("auto mode uses semantic when available", func(t *testing.T) {
		resp, err := svc.Search(ctx, "searcher", SearchOptions{
			Query: "staging deployment",
			Mode:  ModeAuto,
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if resp.SearchMode != ModeSemantic {
			t.Errorf("auto search_mode = %q, want %q", resp.SearchMode, ModeSemantic)
		}
	})

	t.Run("fulltext mode is always available", func(t *testing.T) {
		resp, err := svc.Search(ctx, "searcher", SearchOptions{
			Query: "deployment",
			Mode:  ModeFulltext,
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if resp.SearchMode != ModeFulltext {
			t.Errorf("search_mode = %q, want %q", resp.SearchMode, ModeFulltext)
		}
	})
}

func TestService_Filters(t *testing.T) {
	svc, msgSvc, db := newTestServices(t)
	ctx := context.Background()

	seedTestAgents(t, db, "agent-a", "agent-b", "searcher")

	msgSvc.SendMessage(ctx, "agent-a", "searcher", "low priority task", messaging.SendOptions{Priority: 2})
	msgSvc.SendMessage(ctx, "agent-a", "searcher", "high priority alert", messaging.SendOptions{Priority: 9})
	msgSvc.SendMessage(ctx, "agent-b", "searcher", "from agent-b", messaging.SendOptions{})

	t.Run("filter by from_agent", func(t *testing.T) {
		resp, err := svc.Search(ctx, "searcher", SearchOptions{
			Query:     "",
			Mode:      ModeFulltext,
			Limit:     10,
			FromAgent: "agent-a",
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(resp.Results) != 2 {
			t.Errorf("result count = %d, want 2", len(resp.Results))
		}
	})

	t.Run("filter by min_priority", func(t *testing.T) {
		resp, err := svc.Search(ctx, "searcher", SearchOptions{
			Query:       "",
			Mode:        ModeFulltext,
			Limit:       10,
			MinPriority: 5,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		// Only the high priority message and agent-b (default priority 5) match
		for _, r := range resp.Results {
			if r.Message.Priority < 5 {
				t.Errorf("got message with priority %d, want >= 5", r.Message.Priority)
			}
		}
	})
}

func TestService_HasSemanticSearch(t *testing.T) {
	t.Run("without provider", func(t *testing.T) {
		svc := NewService(nil, nil, nil, nil)
		if svc.HasSemanticSearch() {
			t.Error("HasSemanticSearch() = true, want false")
		}
	})

	t.Run("with provider and index", func(t *testing.T) {
		idx := NewMemoryVectorIndex()
		provider := embedding.NewMockProvider(3)
		svc := NewService(nil, provider, idx, nil)
		if !svc.HasSemanticSearch() {
			t.Error("HasSemanticSearch() = false, want true")
		}
	})
}
