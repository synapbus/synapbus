package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/search"
)

// stubBuilder lets us bypass the real search service and short-circuit
// BuildContextPacket to whatever ContextPacket we want for the test.
// The wrap layer doesn't expose a builder seam (it calls
// search.BuildContextPacket directly), so we instead drive the wrap end
// to end with a real (empty) *search.Service and a stub CoreProvider
// that emits a packet when IncludeCore is true.

type stubCoreProvider struct{ blob string }

func (s *stubCoreProvider) Get(_ context.Context, _, _ string) (string, error) {
	return s.blob, nil
}

func stubHandler(body map[string]any) ToolHandler {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		b, _ := json.Marshal(body)
		return mcplib.NewToolResultText(string(b)), nil
	}
}

func extractJSON(t *testing.T, res *mcplib.CallToolResult) map[string]any {
	t.Helper()
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected 1 content, got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("content not TextContent: %T", res.Content[0])
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &m); err != nil {
		t.Fatalf("non-JSON content: %v: %q", err, tc.Text)
	}
	return m
}

func TestWrapInjection_DisabledReturnsHandlerUnchanged(t *testing.T) {
	inner := stubHandler(map[string]any{"hello": "world"})
	cfg := WrapConfig{
		Cfg: messaging.MemoryConfig{InjectionEnabled: false},
	}
	wrapped := WrapInjection(inner, "my_status", cfg)

	res, err := wrapped(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	body := extractJSON(t, res)
	if _, has := body["relevant_context"]; has {
		t.Error("relevant_context attached despite disabled config")
	}
	if body["hello"] != "world" {
		t.Errorf("inner body mutated: %v", body)
	}
}

func TestWrapInjection_EmptyMemoriesAndNoCore_OmitsField(t *testing.T) {
	// IncludeCore=false and no memories in the DB → packet is nil → no
	// relevant_context field on the response.
	cfg := WrapConfig{
		Cfg: messaging.MemoryConfig{
			InjectionEnabled:      true,
			InjectionBudgetTokens: 500,
			InjectionMaxItems:     5,
			InjectionMinScore:     0.25,
		},
		SearchSvc:   nil, // BuildContextPacket short-circuits when query=="" → returns nil
		QuerySource: func(_ context.Context, _ string, _ map[string]any, _ map[string]any) string { return "" },
	}
	// SearchSvc nil + empty query forces BuildContextPacket through the
	// "no retrieval" path. But it'll still try a core fetch (skipped:
	// IncludeCore=false). With no provider and no retrieval → nil packet.
	inner := stubHandler(map[string]any{"ok": true})
	wrapped := WrapInjection(inner, "send_message", cfg)

	// Inject a caller agent into the context so the wrapper does not
	// bail out at the identity check.
	ctx := agents.ContextWithAgent(context.Background(), &agents.Agent{Name: "a1", OwnerID: 1})

	res, err := wrapped(ctx, mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	body := extractJSON(t, res)
	if _, has := body["relevant_context"]; has {
		t.Errorf("relevant_context attached when memories+core empty: %v", body["relevant_context"])
	}
}

func TestWrapInjection_AppendsRelevantContext_CoreOnly(t *testing.T) {
	cfg := WrapConfig{
		Cfg: messaging.MemoryConfig{
			InjectionEnabled:      true,
			InjectionBudgetTokens: 500,
			InjectionMaxItems:     5,
			InjectionMinScore:     0.25,
		},
		SearchSvc:    nil, // query="" guarantees no retrieval attempt
		IncludeCore:  true,
		CoreProvider: &stubCoreProvider{blob: "I am a1."},
		QuerySource:  func(_ context.Context, _ string, _ map[string]any, _ map[string]any) string { return "" },
	}
	inner := stubHandler(map[string]any{"agent": "a1"})
	wrapped := WrapInjection(inner, "my_status", cfg)

	ctx := agents.ContextWithAgent(context.Background(), &agents.Agent{Name: "a1", OwnerID: 1})
	res, err := wrapped(ctx, mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	body := extractJSON(t, res)
	rc, has := body["relevant_context"].(map[string]any)
	if !has {
		t.Fatalf("relevant_context missing: %+v", body)
	}
	if rc["core_memory"] != "I am a1." {
		t.Errorf("core_memory wrong: %v", rc["core_memory"])
	}
	if mems, ok := rc["memories"].([]any); !ok || len(mems) != 0 {
		t.Errorf("memories should be empty slice when only core is set: %v", rc["memories"])
	}
	// PacketChars at minimum the length of the core blob.
	if got, ok := rc["packet_chars"].(float64); !ok || int(got) < len("I am a1.") {
		t.Errorf("packet_chars looks wrong: %v", rc["packet_chars"])
	}
}

func TestWrapInjection_NonJSONResultPassesThrough(t *testing.T) {
	inner := func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultText("not json"), nil
	}
	cfg := WrapConfig{
		Cfg: messaging.MemoryConfig{
			InjectionEnabled:      true,
			InjectionBudgetTokens: 500,
			InjectionMaxItems:     5,
		},
		QuerySource: func(_ context.Context, _ string, _ map[string]any, _ map[string]any) string { return "" },
	}
	wrapped := WrapInjection(inner, "execute", cfg)
	ctx := agents.ContextWithAgent(context.Background(), &agents.Agent{Name: "a1", OwnerID: 1})
	res, err := wrapped(ctx, mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("unexpected result shape: %+v", res)
	}
	tc, ok := res.Content[0].(mcplib.TextContent)
	if !ok || tc.Text != "not json" {
		t.Errorf("non-JSON result mutated: %+v", res.Content[0])
	}
}

func TestWrapInjection_NoAgentInContext_PassesThrough(t *testing.T) {
	cfg := WrapConfig{
		Cfg: messaging.MemoryConfig{
			InjectionEnabled:      true,
			InjectionBudgetTokens: 500,
			InjectionMaxItems:     5,
		},
		IncludeCore:  true,
		CoreProvider: &stubCoreProvider{blob: "blob"},
		QuerySource:  func(_ context.Context, _ string, _ map[string]any, _ map[string]any) string { return "" },
	}
	wrapped := WrapInjection(stubHandler(map[string]any{"x": 1}), "my_status", cfg)
	res, err := wrapped(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	body := extractJSON(t, res)
	if _, has := body["relevant_context"]; has {
		t.Error("relevant_context attached despite no caller agent")
	}
}

// Ensure ContextPacket as the value carries through json round-trip
// (it's used directly as a map entry via body["relevant_context"] = pkt).
func TestWrapInjection_ContextPacketJSONShape(t *testing.T) {
	pkt := &search.ContextPacket{
		Memories:            []search.MemoryItem{},
		CoreMemory:          "core",
		PacketChars:         4,
		PacketTokenEstimate: 1,
		RetrievalQuery:      "",
		SearchMode:          "auto",
	}
	b, err := json.Marshal(pkt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rt map[string]any
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"memories", "core_memory", "packet_chars", "packet_token_estimate", "retrieval_query", "search_mode"} {
		if _, has := rt[k]; !has {
			t.Errorf("missing JSON key %q", k)
		}
	}
}
