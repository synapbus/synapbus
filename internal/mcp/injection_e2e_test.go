package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/search"
	"github.com/synapbus/synapbus/internal/trace"
)

// TestInjection_CrossOwner_NoLeak is the SC-008 adversarial test: two
// owners (H1, H2) each have an agent + memories in #open-brain. When
// each agent invokes the same wrapped tool with the same query, their
// `relevant_context.memories` are disjoint along owner boundaries.
func TestInjection_CrossOwner_NoLeak(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Two human owners.
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO users (id, username, password_hash, display_name)
		 VALUES (1, 'h1', 'hash', 'H1'), (2, 'h2', 'hash', 'H2')`,
	); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	// One agent per owner.
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status)
		 VALUES ('a-h1', 'A1', 'ai', 1, 'k1', 'active'),
		        ('a-h2', 'A2', 'ai', 2, 'k2', 'active')`,
	); err != nil {
		t.Fatalf("seed agents: %v", err)
	}
	// Both join an open-brain channel — broadly readable.
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO channels (id, name, description, type, created_by)
		 VALUES (1, 'open-brain', 'shared', 'standard', 'system')`,
	); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO channel_members (channel_id, agent_name)
		 VALUES (1, 'a-h1'), (1, 'a-h2')`,
	); err != nil {
		t.Fatalf("seed members: %v", err)
	}
	// One memory per owner, both about the same topic.
	seedMemory(t, db, 1, "a-h1", "Kuzu graph DB is in H1's research notes")
	seedMemory(t, db, 1, "a-h2", "Kuzu graph DB also appears in H2's separate research")

	// Stand up a real search.Service (FTS-only, no embeddings).
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })
	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)
	searchSvc := search.NewService(db, nil, nil, msgService)

	// Configure wrap: low score floor so the test isn't flaky on FTS.
	cfg := WrapConfig{
		Cfg: messaging.MemoryConfig{
			InjectionEnabled:      true,
			InjectionBudgetTokens: 500,
			InjectionMaxItems:     5,
			InjectionMinScore:     0.0,
		},
		SearchSvc: searchSvc,
		QuerySource: func(_ context.Context, _ string, _ map[string]any, _ map[string]any) string {
			return "Kuzu"
		},
	}
	wrapped := WrapInjection(stubHandler(map[string]any{"ok": true}), "search_messages", cfg)

	// Call as H1.
	h1Ctx := agents.ContextWithAgent(ctx, &agents.Agent{Name: "a-h1", OwnerID: 1})
	h1Res, err := wrapped(h1Ctx, mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("h1 wrapped: %v", err)
	}
	h1Body := unmarshalText(t, h1Res)

	// Call as H2.
	h2Ctx := agents.ContextWithAgent(ctx, &agents.Agent{Name: "a-h2", OwnerID: 2})
	h2Res, err := wrapped(h2Ctx, mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("h2 wrapped: %v", err)
	}
	h2Body := unmarshalText(t, h2Res)

	h1Memories := extractMemoryFromAgents(h1Body)
	h2Memories := extractMemoryFromAgents(h2Body)

	for _, fromAgent := range h1Memories {
		if fromAgent != "a-h1" {
			t.Errorf("H1 saw memory from %q (cross-owner leak)", fromAgent)
		}
	}
	for _, fromAgent := range h2Memories {
		if fromAgent != "a-h2" {
			t.Errorf("H2 saw memory from %q (cross-owner leak)", fromAgent)
		}
	}

	// Disjoint sets: no h1 memory id may appear in h2's response.
	h1IDs := extractMemoryIDs(h1Body)
	h2IDs := extractMemoryIDs(h2Body)
	for id := range h1IDs {
		if _, dup := h2IDs[id]; dup {
			t.Errorf("memory id %d leaked across owners", id)
		}
	}
}

func seedMemory(t *testing.T, db *sql.DB, channelID int64, fromAgent, body string) {
	t.Helper()
	convRes, err := db.Exec(
		`INSERT INTO conversations (created_by, channel_id) VALUES (?, ?)`,
		fromAgent, channelID,
	)
	if err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	convID, _ := convRes.LastInsertId()
	if _, err := db.Exec(
		`INSERT INTO messages (conversation_id, from_agent, channel_id, body, priority, status, metadata)
		 VALUES (?, ?, ?, ?, 5, 'pending', '{}')`,
		convID, fromAgent, channelID, body,
	); err != nil {
		t.Fatalf("seed message: %v", err)
	}
}

func unmarshalText(t *testing.T, res *mcplib.CallToolResult) map[string]any {
	t.Helper()
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	tc, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("not text content: %T", res.Content[0])
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &m); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, tc.Text)
	}
	return m
}

func extractMemoryFromAgents(body map[string]any) []string {
	rc, ok := body["relevant_context"].(map[string]any)
	if !ok {
		return nil
	}
	mems, ok := rc["memories"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(mems))
	for _, raw := range mems {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if fa, ok := m["from_agent"].(string); ok {
			out = append(out, fa)
		}
	}
	return out
}

func extractMemoryIDs(body map[string]any) map[int64]struct{} {
	rc, ok := body["relevant_context"].(map[string]any)
	if !ok {
		return map[int64]struct{}{}
	}
	mems, ok := rc["memories"].([]any)
	if !ok {
		return map[int64]struct{}{}
	}
	out := map[int64]struct{}{}
	for _, raw := range mems {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := m["id"].(float64); ok {
			out[int64(id)] = struct{}{}
		}
	}
	return out
}
