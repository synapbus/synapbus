package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/actions"
	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/jsruntime"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/storage"
	"github.com/synapbus/synapbus/internal/trace"
)

func newTestHybridWithChannels(t *testing.T) (*HybridToolRegistrar, *channels.Service) {
	t.Helper()
	db := newTestDB(t)

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	channelStore := channels.NewSQLiteChannelStore(db)
	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)
	channelService := channels.NewService(channelStore, msgService, tracer)

	agentStore := agents.NewSQLiteAgentStore(db)
	agentService := agents.NewAgentService(agentStore, tracer)

	jsPool := jsruntime.NewPool(2)
	t.Cleanup(func() { jsPool.Close() })

	actionRegistry := actions.NewRegistry()
	actionIndex := actions.NewIndex(actionRegistry.List())

	// Seed test agents
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES ('agent-a', 'Agent A', 'ai', '{}', 1, 'hash', 'active')`)
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES ('agent-b', 'Agent B', 'ai', '{}', 1, 'hash', 'active')`)
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES ('agent-c', 'Agent C', 'ai', '{}', 1, 'hash', 'active')`)

	registrar := NewHybridToolRegistrar(
		msgService,
		agentService,
		channelService,
		nil, // swarmService
		nil, // attachmentService
		nil, // searchService
		nil, // reactionService
		nil, // trustService
		jsPool,
		actionRegistry,
		actionIndex,
		db,
	)
	return registrar, channelService
}

func parseResponse(t *testing.T, result *mcplib.CallToolResult) map[string]any {
	t.Helper()
	var resp map[string]any
	text := result.Content[0].(mcplib.TextContent).Text
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

// parseCallResult unwraps the execute response envelope and the call() wrapper
// to return the inner bridge result: { result: { ok, result: <bridge_data> }, calls, duration } → <bridge_data>
func parseCallResult(t *testing.T, result *mcplib.CallToolResult) map[string]any {
	t.Helper()
	resp := parseResponse(t, result)
	callEnvelope, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result to be map, got %T", resp["result"])
	}
	inner, ok := callEnvelope["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected call result to be map, got %T (ok=%v)", callEnvelope["result"], callEnvelope["ok"])
	}
	return inner
}

func TestHybridTool_SendMessage_Channel(t *testing.T) {
	h, svc := newTestHybridWithChannels(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "msg-test", Type: "standard", CreatedBy: "agent-a",
	})
	svc.JoinChannel(ctx, ch.ID, "agent-b")

	authCtx := ContextWithAgentName(ctx, "agent-a")

	t.Run("send to channel by name", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"channel": "msg-test",
			"body":    "Hello channel!",
		})

		result, err := h.handleSendMessage(authCtx, req)
		if err != nil {
			t.Fatalf("handleSendMessage: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resp := parseResponse(t, result)
		if resp["status"] != "sent" {
			t.Errorf("status = %v, want sent", resp["status"])
		}
		if resp["message_id"].(float64) == 0 {
			t.Error("message_id should be non-zero")
		}
	})

	t.Run("missing body for channel", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"channel": "msg-test",
		})
		result, _ := h.handleSendMessage(authCtx, req)
		if !result.IsError {
			t.Error("expected error for missing body")
		}
	})
}

func TestBridge_ChannelOperations(t *testing.T) {
	h, svc := newTestHybridWithChannels(t)
	ctx := context.Background()
	authCtx := ContextWithAgentName(ctx, "agent-a")

	t.Run("create_channel via execute", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"code": `call("create_channel", { name: "test-channel", description: "A test channel", type: "standard" })`,
		})

		result, err := h.handleExecute(authCtx, req)
		if err != nil {
			t.Fatalf("handleExecute: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resultData := parseCallResult(t, result)
		if resultData["name"] != "test-channel" {
			t.Errorf("name = %v, want test-channel", resultData["name"])
		}
	})

	t.Run("join_channel via execute", func(t *testing.T) {
		svc.CreateChannel(ctx, channels.CreateChannelRequest{
			Name: "join-test", Type: "standard", CreatedBy: "agent-a",
		})

		bCtx := ContextWithAgentName(ctx, "agent-b")
		req := makeRequest(map[string]any{
			"code": `call("join_channel", { channel_name: "join-test" })`,
		})

		result, err := h.handleExecute(bCtx, req)
		if err != nil {
			t.Fatalf("handleExecute: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resultData := parseCallResult(t, result)
		if resultData["status"] != "joined" {
			t.Errorf("status = %v, want joined", resultData["status"])
		}
	})

	t.Run("list_channels via execute", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"code": `call("list_channels", {})`,
		})

		result, err := h.handleExecute(authCtx, req)
		if err != nil {
			t.Fatalf("handleExecute: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resultData := parseCallResult(t, result)
		count := resultData["count"].(float64)
		if count < 1 {
			t.Errorf("expected at least 1 channel, got %v", count)
		}
	})

	t.Run("update_channel via execute", func(t *testing.T) {
		svc.CreateChannel(ctx, channels.CreateChannelRequest{
			Name: "update-test", Type: "standard", Topic: "Original", CreatedBy: "agent-a",
		})

		req := makeRequest(map[string]any{
			"code": `call("update_channel", { channel_name: "update-test", topic: "Updated topic" })`,
		})

		result, err := h.handleExecute(authCtx, req)
		if err != nil {
			t.Fatalf("handleExecute: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resultData := parseCallResult(t, result)
		if resultData["topic"] != "Updated topic" {
			t.Errorf("topic = %v, want 'Updated topic'", resultData["topic"])
		}
	})
}

// suppress unused import
var _ = storage.RunMigrations
