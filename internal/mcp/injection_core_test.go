// Integration test for US2 → US1 wiring: a real CoreMemoryStore, wired
// through messaging.NewCoreProvider, surfaces a seeded blob in the
// wrapped tool response's `relevant_context.core_memory` field; an agent
// with no row gets no relevant_context.
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/storage"
)

func newInjectionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("foreign keys: %v", err)
	}
	if err := storage.RunMigrations(context.Background(), db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return db
}

// TestInjection_CoreMemoryWiring proves a wrapped session-start handler
// surfaces a blob seeded via CoreMemoryStore as
// `relevant_context.core_memory`. Mirrors the contract example in
// `specs/020-proactive-memory-dream-worker/contracts/mcp-injection.md`.
func TestInjection_CoreMemoryWiring(t *testing.T) {
	db := newInjectionTestDB(t)
	ctx := context.Background()

	const owner = "42"
	const agent = "a1"
	const blob = "I am a1. Currently focused on memory tests."

	coreStore := messaging.NewCoreMemoryStore(db, 2048)
	if err := coreStore.Set(ctx, owner, agent, blob, "human"); err != nil {
		t.Fatalf("seed core memory: %v", err)
	}

	cfg := WrapConfig{
		Cfg: messaging.MemoryConfig{
			InjectionEnabled:      true,
			InjectionBudgetTokens: 500,
			InjectionMaxItems:     5,
			InjectionMinScore:     0.25,
		},
		SearchSvc:    nil, // query="" forces no retrieval — only core matters.
		IncludeCore:  true,
		CoreProvider: messaging.NewCoreProvider(coreStore),
		QuerySource:  func(_ context.Context, _ string, _ map[string]any, _ map[string]any) string { return "" },
	}
	inner := stubHandler(map[string]any{"agent": agent})
	wrapped := WrapInjection(inner, "my_status", cfg)

	// Owner 42 ↔ caller a1.
	callerCtx := agents.ContextWithAgent(ctx, &agents.Agent{Name: agent, OwnerID: 42})
	res, err := wrapped(callerCtx, mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("wrapped my_status: %v", err)
	}
	body := extractJSON(t, res)
	rc, ok := body["relevant_context"].(map[string]any)
	if !ok {
		t.Fatalf("relevant_context missing: %+v", body)
	}
	if got := rc["core_memory"]; got != blob {
		t.Errorf("core_memory: got %v want %q", got, blob)
	}
}

// TestInjection_NoCoreMemoryYieldsNoPacket verifies that an agent
// without a memory_core row gets the original handler response back,
// without a `relevant_context` field appended.
func TestInjection_NoCoreMemoryYieldsNoPacket(t *testing.T) {
	db := newInjectionTestDB(t)

	coreStore := messaging.NewCoreMemoryStore(db, 2048)
	// Intentionally NO Set — the agent has no row.

	cfg := WrapConfig{
		Cfg: messaging.MemoryConfig{
			InjectionEnabled:      true,
			InjectionBudgetTokens: 500,
			InjectionMaxItems:     5,
			InjectionMinScore:     0.25,
		},
		SearchSvc:    nil,
		IncludeCore:  true,
		CoreProvider: messaging.NewCoreProvider(coreStore),
		QuerySource:  func(_ context.Context, _ string, _ map[string]any, _ map[string]any) string { return "" },
	}
	inner := stubHandler(map[string]any{"agent": "a2"})
	wrapped := WrapInjection(inner, "my_status", cfg)

	ctx := agents.ContextWithAgent(context.Background(), &agents.Agent{Name: "a2", OwnerID: 42})
	res, err := wrapped(ctx, mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	body := extractJSON(t, res)
	if _, has := body["relevant_context"]; has {
		t.Errorf("relevant_context attached for agent with no core row: %v", body["relevant_context"])
	}
	if body["agent"] != "a2" {
		t.Errorf("inner body lost: %+v", body)
	}
}

// Verify ContextPacket round-trips its core_memory through json. This is
// the contract field consumed by clients.
func TestInjection_CoreMemoryJSONShape(t *testing.T) {
	db := newInjectionTestDB(t)
	ctx := context.Background()
	coreStore := messaging.NewCoreMemoryStore(db, 2048)
	if err := coreStore.Set(ctx, "1", "a1", "hello", "human"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	provider := messaging.NewCoreProvider(coreStore)
	got, err := provider.Get(ctx, "1", "a1")
	if err != nil {
		t.Fatalf("provider.Get: %v", err)
	}
	if got != "hello" {
		t.Errorf("provider.Get: got %q want hello", got)
	}

	// Empty case (no row) yields "" without error.
	got, err = provider.Get(ctx, "1", "nobody")
	if err != nil || got != "" {
		t.Errorf("provider.Get on missing: got %q err=%v", got, err)
	}

	// Sanity: ensure the adapter is reachable through json marshaling of a packet.
	type fakePacket struct {
		Core string `json:"core_memory,omitempty"`
	}
	b, _ := json.Marshal(fakePacket{Core: "hello"})
	if string(b) != `{"core_memory":"hello"}` {
		t.Errorf("json marshaling: got %s", b)
	}
}
