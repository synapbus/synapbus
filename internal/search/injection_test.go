package search

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/messaging"
)

// stubCoreProvider lets a test override the core-memory blob returned to
// BuildContextPacket.
type stubCoreProvider struct {
	blob string
	err  error
}

func (s *stubCoreProvider) Get(ctx context.Context, ownerID, agentName string) (string, error) {
	return s.blob, s.err
}

// seedOwnedAgent inserts a users row + an agents row tied to that owner.
func seedOwnedAgent(t *testing.T, db *sql.DB, ownerID int64, ownerName, agentName string) *agents.Agent {
	t.Helper()
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO users (id, username, password_hash, display_name)
		 VALUES (?, ?, 'hash', ?)`, ownerID, ownerName, ownerName,
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status)
		 VALUES (?, ?, 'ai', ?, ?, 'active')`,
		agentName, agentName, ownerID, agentName+"hash",
	); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return &agents.Agent{Name: agentName, OwnerID: ownerID}
}

// seedChannel inserts an open-brain channel with members `agentNames`.
func seedChannel(t *testing.T, db *sql.DB, channelID int64, name string, agentNames ...string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO channels (id, name, description, type, created_by)
		 VALUES (?, ?, '', 'standard', 'system')`, channelID, name,
	); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	for _, a := range agentNames {
		if _, err := db.Exec(
			`INSERT OR IGNORE INTO channel_members (channel_id, agent_name) VALUES (?, ?)`,
			channelID, a,
		); err != nil {
			t.Fatalf("seed channel_member: %v", err)
		}
	}
}

// seedChannelMessage inserts a message directly so we don't need the
// channels.Service stack in this test.
func seedChannelMessage(t *testing.T, db *sql.DB, channelID int64, fromAgent, body string) int64 {
	t.Helper()
	convRes, err := db.Exec(
		`INSERT INTO conversations (created_by, channel_id) VALUES (?, ?)`,
		fromAgent, channelID,
	)
	if err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	convID, _ := convRes.LastInsertId()

	res, err := db.Exec(
		`INSERT INTO messages (conversation_id, from_agent, channel_id, body, priority, status, metadata)
		 VALUES (?, ?, ?, ?, 5, 'pending', '{}')`,
		convID, fromAgent, channelID, body,
	)
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestBuildContextPacket_BudgetZeroDisables(t *testing.T) {
	svc, _, db := newTestServices(t)
	a := seedOwnedAgent(t, db, 1, "alice", "a1")

	pkt, err := BuildContextPacket(context.Background(), svc, a, "query", InjectionOpts{
		BudgetTokens: 0,
	})
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt != nil {
		t.Errorf("BudgetTokens=0 should return nil packet, got %+v", pkt)
	}
}

func TestBuildContextPacket_OwnerScoping(t *testing.T) {
	svc, _, db := newTestServices(t)
	ctx := context.Background()

	h1Agent := seedOwnedAgent(t, db, 1, "alice", "a1")
	_ = seedOwnedAgent(t, db, 2, "bob", "b1")

	seedChannel(t, db, 1, "open-brain", "a1", "b1")
	seedChannelMessage(t, db, 1, "a1", "Kuzu graph DB archived 2025")
	seedChannelMessage(t, db, 1, "b1", "Kuzu graph DB looks promising")

	pkt, err := BuildContextPacket(ctx, svc, h1Agent, "Kuzu", InjectionOpts{
		BudgetTokens: 500,
		MaxItems:     5,
		MinScore:     0.0,
	})
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt == nil {
		t.Fatal("expected non-nil packet")
	}
	for _, m := range pkt.Memories {
		if m.FromAgent != "a1" {
			t.Errorf("leaked memory from %q (owner != caller)", m.FromAgent)
		}
		if !strings.Contains(m.Body, "Kuzu") {
			t.Errorf("unexpected body: %q", m.Body)
		}
	}
}

func TestBuildContextPacket_TokenBudgetGreedyFillAndTruncate(t *testing.T) {
	items := []MemoryItem{
		{ID: 1, Body: strings.Repeat("a", 200), Score: 0.9},
		{ID: 2, Body: strings.Repeat("b", 200), Score: 0.8},
		{ID: 3, Body: strings.Repeat("c", 200), Score: 0.7},
	}
	// Budget 100 tokens => 400 chars total. Each item costs ~232 chars
	// → ~58 tokens. First fits cleanly; second must truncate.
	out := applyTokenBudget(items, 5, 100)

	if len(out) == 0 {
		t.Fatal("expected at least one admitted item")
	}
	if out[0].ID != 1 {
		t.Errorf("first admitted item ID = %d, want 1", out[0].ID)
	}
	total := 0
	for _, it := range out {
		total += EstimateTokens(itemChars(it))
	}
	if total > 100 {
		t.Errorf("total tokens admitted = %d, exceeds budget 100", total)
	}
	sawTruncated := false
	for _, it := range out {
		if it.Truncated {
			sawTruncated = true
		}
	}
	if len(out) > 1 && !sawTruncated {
		t.Errorf("expected truncation when admitting a second item under tight budget")
	}
}

func TestBuildContextPacket_ScoreFloorDrops(t *testing.T) {
	svc, _, db := newTestServices(t)
	ctx := context.Background()
	_ = seedOwnedAgent(t, db, 1, "alice", "a1")
	seedChannel(t, db, 1, "open-brain", "a1")

	cidPtr := int64(1)
	low := &SearchResult{
		Message: &messaging.Message{
			ID:        100,
			FromAgent: "a1",
			Body:      "low score body",
			ChannelID: &cidPtr,
		},
		SimilarityScore: 0.1,
		MatchType:       ModeSemantic,
	}
	high := &SearchResult{
		Message: &messaging.Message{
			ID:        101,
			FromAgent: "a1",
			Body:      "high score body",
			ChannelID: &cidPtr,
		},
		SimilarityScore: 0.8,
		MatchType:       ModeSemantic,
	}

	items, err := filterAndScore(ctx, svc, []*SearchResult{low, high}, "1", InjectionOpts{
		MinScore: 0.5,
	})
	if err != nil {
		t.Fatalf("filterAndScore: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 admitted item, got %d", len(items))
	}
	if items[0].ID != 101 {
		t.Errorf("admitted ID = %d, want 101 (high score)", items[0].ID)
	}
}

func TestBuildContextPacket_CoreMemoryIncluded(t *testing.T) {
	svc, _, db := newTestServices(t)
	ctx := context.Background()
	a := seedOwnedAgent(t, db, 1, "alice", "a1")

	provider := &stubCoreProvider{blob: "I am alice's research agent."}
	pkt, err := BuildContextPacket(ctx, svc, a, "", InjectionOpts{
		BudgetTokens: 500,
		MaxItems:     5,
		IncludeCore:  true,
		CoreProvider: provider,
	})
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt == nil {
		t.Fatal("expected non-nil packet when core memory is present")
	}
	if pkt.CoreMemory != provider.blob {
		t.Errorf("CoreMemory = %q, want %q", pkt.CoreMemory, provider.blob)
	}
	if pkt.PacketChars < len(provider.blob) {
		t.Errorf("PacketChars = %d, want >= %d", pkt.PacketChars, len(provider.blob))
	}
	if pkt.PacketTokenEstimate != EstimateTokens(pkt.PacketChars) {
		t.Errorf("PacketTokenEstimate inconsistent with PacketChars")
	}
}

func TestBuildContextPacket_EmptyReturnsNil(t *testing.T) {
	svc, _, db := newTestServices(t)
	ctx := context.Background()
	a := seedOwnedAgent(t, db, 1, "alice", "a1")

	// No memories, no core provider → should return (nil, nil) so the
	// wrapper omits the relevant_context field entirely.
	pkt, err := BuildContextPacket(ctx, svc, a, "", InjectionOpts{
		BudgetTokens: 500,
		MaxItems:     5,
	})
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt != nil {
		t.Errorf("expected nil packet, got %+v", pkt)
	}
}
