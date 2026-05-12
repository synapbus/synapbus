package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/synapbus/synapbus/internal/messaging"
)

// memToolHarness bundles deps + helpers for the memory tools tests.
type memToolHarness struct {
	db       *sql.DB
	reg      *MemoryToolRegistrar
	tokens   *messaging.DispatchTokenStore
	jobs     *messaging.JobsStore
	links    *messaging.LinkStore
	pins     *messaging.PinStore
	core     *messaging.CoreMemoryStore
	jobID    int64
	tokenStr string
	ownerID  string
}

func newMemToolHarness(t *testing.T) *memToolHarness {
	t.Helper()
	db := newTestDB(t)

	// Seed two owners + their agents (used for owner mismatch tests).
	_, _ = db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (2, 'otheruser', 'hash', 'Other')`)
	_, _ = db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status) VALUES ('a-h1', 'a-h1', 'ai', 1, 'h1', 'active')`)
	_, _ = db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, owner_id, api_key_hash, status) VALUES ('a-h2', 'a-h2', 'ai', 2, 'h2', 'active')`)

	tokens := messaging.NewDispatchTokenStore(db)
	jobs := messaging.NewJobsStore(db)
	links := messaging.NewLinkStore(db)
	pins := messaging.NewPinStore(db)
	core := messaging.NewCoreMemoryStore(db, 64)

	jobID, err := jobs.Create(context.Background(), "1", "reflection", "manual:test")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	tok, _, err := tokens.Issue(context.Background(), "1", jobID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	reg := NewMemoryToolRegistrar(MemoryToolDeps{
		DB:     db,
		Core:   core,
		Links:  links,
		Pins:   pins,
		Jobs:   jobs,
		Tokens: tokens,
	})
	return &memToolHarness{
		db: db, reg: reg, tokens: tokens, jobs: jobs, links: links, pins: pins, core: core,
		jobID: jobID, tokenStr: tok, ownerID: "1",
	}
}

func (h *memToolHarness) ctxWithToken(tok string) context.Context {
	return WithDispatchToken(context.Background(), tok)
}

// seedMemoryChannel inserts an open-brain channel and one message
// belonging to `agentName`.
func (h *memToolHarness) seedMessage(t *testing.T, agentName, body string) int64 {
	t.Helper()
	_, _ = h.db.Exec(`INSERT OR IGNORE INTO channels (id, name, description, type, created_by) VALUES (1, 'open-brain', '', 'standard', 'system')`)
	res, err := h.db.Exec(
		`INSERT INTO conversations (created_by, channel_id) VALUES (?, 1)`, agentName)
	if err != nil {
		t.Fatalf("seed conv: %v", err)
	}
	convID, _ := res.LastInsertId()
	res, err = h.db.Exec(
		`INSERT INTO messages (conversation_id, from_agent, channel_id, body, priority, status, metadata)
		 VALUES (?, ?, 1, ?, 5, 'pending', '{}')`,
		convID, agentName, body,
	)
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func callRequest(args map[string]any) mcplib.CallToolRequest {
	return mcplib.CallToolRequest{
		Params: mcplib.CallToolParams{Arguments: args},
	}
}

func resultText(t *testing.T, res *mcplib.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("nil/empty result")
	}
	tc, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("not TextContent: %T", res.Content[0])
	}
	return tc.Text
}

func resultIsError(res *mcplib.CallToolResult) bool {
	return res != nil && res.IsError
}

// --- Token error matrix ---

func TestMemoryTools_DispatchTokenMissing(t *testing.T) {
	h := newMemToolHarness(t)
	// No token in context.
	res, _ := h.reg.handleAddLink(context.Background(), callRequest(map[string]any{
		"owner_id": "1", "src_id": 1.0, "dst_id": 2.0, "relation_type": "refines",
	}))
	if !resultIsError(res) {
		t.Fatal("expected error result")
	}
	if !strings.Contains(resultText(t, res), "dispatch_token_missing") {
		t.Errorf("expected dispatch_token_missing, got %q", resultText(t, res))
	}
}

func TestMemoryTools_DispatchTokenExpired(t *testing.T) {
	h := newMemToolHarness(t)
	// Forcibly expire.
	if _, err := h.db.Exec(
		`UPDATE memory_dispatch_tokens SET expires_at = ? WHERE token = ?`,
		time.Now().Add(-1*time.Minute).UTC(), h.tokenStr,
	); err != nil {
		t.Fatalf("expire: %v", err)
	}
	res, _ := h.reg.handleAddLink(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id": "1", "src_id": 1.0, "dst_id": 2.0, "relation_type": "refines",
	}))
	if !resultIsError(res) || !strings.Contains(resultText(t, res), "dispatch_token_expired") {
		t.Errorf("expected dispatch_token_expired, got %q", resultText(t, res))
	}
}

func TestMemoryTools_DispatchTokenOwnerMismatch(t *testing.T) {
	h := newMemToolHarness(t)
	res, _ := h.reg.handleAddLink(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id": "2", "src_id": 1.0, "dst_id": 2.0, "relation_type": "refines",
	}))
	if !resultIsError(res) || !strings.Contains(resultText(t, res), "dispatch_token_owner_mismatch") {
		t.Errorf("expected dispatch_token_owner_mismatch, got %q", resultText(t, res))
	}
}

// --- memory_add_link ---

func TestMemoryAddLink_HappyPath(t *testing.T) {
	h := newMemToolHarness(t)
	a := h.seedMessage(t, "a-h1", "fact A")
	b := h.seedMessage(t, "a-h1", "fact B")

	res, err := h.reg.handleAddLink(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id":      "1",
		"src_id":        float64(a),
		"dst_id":        float64(b),
		"relation_type": "refines",
	}))
	if err != nil {
		t.Fatalf("handleAddLink: %v", err)
	}
	if resultIsError(res) {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if _, ok := body["link_id"]; !ok {
		t.Errorf("expected link_id in response: %v", body)
	}

	// And actions should have been appended.
	job, err := h.jobs.Get(context.Background(), h.jobID)
	if err != nil {
		t.Fatalf("Get job: %v", err)
	}
	if len(job.Actions) != 1 || job.Actions[0]["tool"] != "memory_add_link" {
		t.Errorf("expected one action for memory_add_link, got %v", job.Actions)
	}
}

func TestMemoryAddLink_RelationTypeReserved(t *testing.T) {
	h := newMemToolHarness(t)
	a := h.seedMessage(t, "a-h1", "x")
	b := h.seedMessage(t, "a-h1", "y")
	res, _ := h.reg.handleAddLink(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id": "1", "src_id": float64(a), "dst_id": float64(b),
		"relation_type": "mention",
	}))
	if !resultIsError(res) || !strings.Contains(resultText(t, res), "relation_type_reserved") {
		t.Errorf("expected relation_type_reserved, got %q", resultText(t, res))
	}
}

func TestMemoryAddLink_NotSameOwner(t *testing.T) {
	h := newMemToolHarness(t)
	src := h.seedMessage(t, "a-h1", "x")
	other := h.seedMessage(t, "a-h2", "y")
	res, _ := h.reg.handleAddLink(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id": "1", "src_id": float64(src), "dst_id": float64(other),
		"relation_type": "refines",
	}))
	if !resultIsError(res) || !strings.Contains(resultText(t, res), "not_same_owner") {
		t.Errorf("expected not_same_owner, got %q", resultText(t, res))
	}
}

func TestMemoryAddLink_SourceNotFound(t *testing.T) {
	h := newMemToolHarness(t)
	res, _ := h.reg.handleAddLink(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id": "1", "src_id": 9999.0, "dst_id": 8888.0, "relation_type": "refines",
	}))
	if !resultIsError(res) || !strings.Contains(resultText(t, res), "source_not_found") {
		t.Errorf("expected source_not_found, got %q", resultText(t, res))
	}
}

// --- memory_rewrite_core ---

func TestMemoryRewriteCore_HappyPath(t *testing.T) {
	h := newMemToolHarness(t)
	res, _ := h.reg.handleRewriteCore(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id": "1", "agent_name": "a-h1", "blob": "I am a-h1.",
	}))
	if resultIsError(res) {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
	blob, _, ok, _ := h.core.Get(context.Background(), "1", "a-h1")
	if !ok || blob != "I am a-h1." {
		t.Errorf("blob not stored: ok=%v blob=%q", ok, blob)
	}
}

func TestMemoryRewriteCore_TooLarge(t *testing.T) {
	h := newMemToolHarness(t)
	big := strings.Repeat("x", 65)
	res, _ := h.reg.handleRewriteCore(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id": "1", "agent_name": "a-h1", "blob": big,
	}))
	if !resultIsError(res) || !strings.Contains(resultText(t, res), "core_memory_too_large") {
		t.Errorf("expected core_memory_too_large, got %q", resultText(t, res))
	}
}

func TestMemoryRewriteCore_AgentNotSameOwner(t *testing.T) {
	h := newMemToolHarness(t)
	res, _ := h.reg.handleRewriteCore(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id": "1", "agent_name": "a-h2", "blob": "trying to overwrite",
	}))
	if !resultIsError(res) || !strings.Contains(resultText(t, res), "not_same_owner") {
		t.Errorf("expected not_same_owner, got %q", resultText(t, res))
	}
}

// --- memory_mark_duplicate ---

func TestMemoryMarkDuplicate_HappyPath(t *testing.T) {
	h := newMemToolHarness(t)
	a := h.seedMessage(t, "a-h1", "fact A")
	b := h.seedMessage(t, "a-h1", "fact A shorter")
	res, _ := h.reg.handleMarkDuplicate(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id": "1", "a_id": float64(a), "b_id": float64(b), "keep_id": float64(a),
		"reason": "shorter",
	}))
	if resultIsError(res) {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
	// Audit row appended on job.
	job, _ := h.jobs.Get(context.Background(), h.jobID)
	if len(job.Actions) != 1 || job.Actions[0]["tool"] != "memory_mark_duplicate" {
		t.Errorf("expected one mark_duplicate action: %v", job.Actions)
	}
}

// --- memory_supersede ---

func TestMemorySupersede_HappyPath(t *testing.T) {
	h := newMemToolHarness(t)
	a := h.seedMessage(t, "a-h1", "old fact")
	b := h.seedMessage(t, "a-h1", "new fact")
	res, _ := h.reg.handleSupersede(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id": "1", "a_id": float64(a), "b_id": float64(b), "reason": "newer",
	}))
	if resultIsError(res) {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
}

// --- memory_list_unprocessed ---

func TestMemoryListUnprocessed_ReturnsOwnerScopedMessages(t *testing.T) {
	h := newMemToolHarness(t)
	h.seedMessage(t, "a-h1", "memory 1")
	h.seedMessage(t, "a-h1", "memory 2")
	h.seedMessage(t, "a-h2", "other owner's memory")

	res, _ := h.reg.handleListUnprocessed(h.ctxWithToken(h.tokenStr), callRequest(map[string]any{
		"owner_id": "1",
	}))
	if resultIsError(res) {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(resultText(t, res)), &body)
	mems, _ := body["memories"].([]any)
	if len(mems) != 2 {
		t.Errorf("expected 2 owner-scoped memories, got %d (full body: %v)", len(mems), body)
	}
}

// --- memory_write_reflection ---

func TestMemoryWriteReflection_HappyPath(t *testing.T) {
	h := newMemToolHarness(t)
	a := h.seedMessage(t, "a-h1", "source 1")
	b := h.seedMessage(t, "a-h1", "source 2")

	args := map[string]any{
		"owner_id":           "1",
		"body":               "Across these I notice...",
		"source_message_ids": "" + intCSV(a, b),
	}
	res, _ := h.reg.handleWriteReflection(h.ctxWithToken(h.tokenStr), callRequest(args))
	if resultIsError(res) {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(resultText(t, res)), &body)
	if _, ok := body["memory_id"]; !ok {
		t.Errorf("expected memory_id: %v", body)
	}
	if v, _ := body["links_created"].(float64); int(v) != 2 {
		t.Errorf("expected links_created=2, got %v", body["links_created"])
	}
}

func intCSV(ids ...int64) string {
	var b strings.Builder
	for i, id := range ids {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(itoa(id))
	}
	return b.String()
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
