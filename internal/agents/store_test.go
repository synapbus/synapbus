package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/smart-mcp-proxy/synapbus/internal/storage"
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

	// Seed a test user for owner_id FK
	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testowner', 'hash', 'Test Owner')`)

	return db
}

func TestSQLiteAgentStore_CreateAndGet(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteAgentStore(db)
	ctx := context.Background()

	agent := &Agent{
		Name:         "test-bot",
		DisplayName:  "Test Bot",
		Type:         "ai",
		Capabilities: json.RawMessage(`{"skills":["testing"]}`),
		OwnerID:      1,
		APIKeyHash:   "somehash",
	}

	if err := store.CreateAgent(ctx, agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if agent.ID == 0 {
		t.Error("agent ID should not be 0")
	}

	// Get by name
	got, err := store.GetAgentByName(ctx, "test-bot")
	if err != nil {
		t.Fatalf("GetAgentByName: %v", err)
	}
	if got.DisplayName != "Test Bot" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Test Bot")
	}

	// Get by ID
	got2, err := store.GetAgentByID(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if got2.Name != "test-bot" {
		t.Errorf("Name = %q, want %q", got2.Name, "test-bot")
	}
}

func TestSQLiteAgentStore_DuplicateName(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteAgentStore(db)
	ctx := context.Background()

	agent := &Agent{
		Name:         "dup-bot",
		DisplayName:  "Dup Bot",
		Type:         "ai",
		Capabilities: json.RawMessage("{}"),
		OwnerID:      1,
		APIKeyHash:   "hash1",
	}

	if err := store.CreateAgent(ctx, agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	agent2 := &Agent{
		Name:         "dup-bot",
		DisplayName:  "Dup Bot 2",
		Type:         "ai",
		Capabilities: json.RawMessage("{}"),
		OwnerID:      1,
		APIKeyHash:   "hash2",
	}

	err := store.CreateAgent(ctx, agent2)
	if err == nil {
		t.Error("expected error for duplicate name")
	}
}

func TestSQLiteAgentStore_Update(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteAgentStore(db)
	ctx := context.Background()

	agent := &Agent{
		Name:         "update-bot",
		DisplayName:  "Update Bot",
		Type:         "ai",
		Capabilities: json.RawMessage(`{"skills":["v1"]}`),
		OwnerID:      1,
		APIKeyHash:   "hash",
	}

	store.CreateAgent(ctx, agent)

	agent.DisplayName = "Updated Bot"
	agent.Capabilities = json.RawMessage(`{"skills":["v1","v2"]}`)

	if err := store.UpdateAgent(ctx, agent); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	got, err := store.GetAgentByName(ctx, "update-bot")
	if err != nil {
		t.Fatalf("GetAgentByName: %v", err)
	}
	if got.DisplayName != "Updated Bot" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Updated Bot")
	}
}

func TestSQLiteAgentStore_Deactivate(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteAgentStore(db)
	ctx := context.Background()

	agent := &Agent{
		Name:         "deactivate-bot",
		DisplayName:  "Deactivate Bot",
		Type:         "ai",
		Capabilities: json.RawMessage("{}"),
		OwnerID:      1,
		APIKeyHash:   "hash",
	}

	store.CreateAgent(ctx, agent)

	if err := store.DeactivateAgent(ctx, "deactivate-bot"); err != nil {
		t.Fatalf("DeactivateAgent: %v", err)
	}

	// Should not be found (GetByName filters active only)
	_, err := store.GetAgentByName(ctx, "deactivate-bot")
	if err == nil {
		t.Error("expected error for deactivated agent")
	}

	// Deactivate non-existent
	err = store.DeactivateAgent(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent agent")
	}
}

func TestSQLiteAgentStore_ListActive(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteAgentStore(db)
	ctx := context.Background()

	for _, name := range []string{"bot-a", "bot-b", "bot-c"} {
		store.CreateAgent(ctx, &Agent{
			Name:         name,
			DisplayName:  name,
			Type:         "ai",
			Capabilities: json.RawMessage("{}"),
			OwnerID:      1,
			APIKeyHash:   "hash",
		})
	}

	agents, err := store.ListActiveAgents(ctx)
	if err != nil {
		t.Fatalf("ListActiveAgents: %v", err)
	}
	if len(agents) != 3 {
		t.Errorf("got %d agents, want 3", len(agents))
	}
}

func TestSQLiteAgentStore_SearchByCapability(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteAgentStore(db)
	ctx := context.Background()

	store.CreateAgent(ctx, &Agent{
		Name:         "searcher",
		DisplayName:  "Searcher",
		Type:         "ai",
		Capabilities: json.RawMessage(`{"skills":["web-search","summarization"]}`),
		OwnerID:      1,
		APIKeyHash:   "hash",
	})

	store.CreateAgent(ctx, &Agent{
		Name:         "analyzer",
		DisplayName:  "Analyzer",
		Type:         "ai",
		Capabilities: json.RawMessage(`{"skills":["sentiment-analysis"]}`),
		OwnerID:      1,
		APIKeyHash:   "hash",
	})

	t.Run("match found", func(t *testing.T) {
		results, err := store.SearchAgentsByCapability(ctx, "web-search")
		if err != nil {
			t.Fatalf("SearchAgentsByCapability: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("got %d results, want 1", len(results))
		}
		if len(results) > 0 && results[0].Name != "searcher" {
			t.Errorf("Name = %s, want searcher", results[0].Name)
		}
	})

	t.Run("no match", func(t *testing.T) {
		results, err := store.SearchAgentsByCapability(ctx, "quantum-computing")
		if err != nil {
			t.Fatalf("SearchAgentsByCapability: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("got %d results, want 0", len(results))
		}
	})
}

var _ = storage.RunMigrations
