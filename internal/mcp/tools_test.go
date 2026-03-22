package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/actions"
	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/jsruntime"
	"github.com/synapbus/synapbus/internal/messaging"
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

func newTestHybridRegistrar(t *testing.T) (*HybridToolRegistrar, *messaging.MessagingService, *agents.AgentService, *sql.DB) {
	t.Helper()
	db := newTestDB(t)

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	agentStore := agents.NewSQLiteAgentStore(db)
	agentService := agents.NewAgentService(agentStore, tracer)

	jsPool := jsruntime.NewPool(2)
	t.Cleanup(func() { jsPool.Close() })

	actionRegistry := actions.NewRegistry()
	actionIndex := actions.NewIndex(actionRegistry.List())

	registrar := NewHybridToolRegistrar(
		msgService,
		agentService,
		nil, // channelService
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
	return registrar, msgService, agentService, db
}

func makeRequest(args map[string]any) mcplib.CallToolRequest {
	return mcplib.CallToolRequest{
		Params: mcplib.CallToolParams{
			Arguments: args,
		},
	}
}

func TestHybridTool_MyStatus(t *testing.T) {
	h, _, agentSvc, _ := newTestHybridRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "test-agent", "Test Agent", "ai", nil, 1)

	authCtx := ContextWithAgentName(ctx, "test-agent")

	t.Run("returns status with usage instructions", func(t *testing.T) {
		req := makeRequest(map[string]any{})

		result, err := h.handleMyStatus(authCtx, req)
		if err != nil {
			t.Fatalf("handleMyStatus: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		var resp map[string]any
		text := result.Content[0].(mcplib.TextContent).Text
		json.Unmarshal([]byte(text), &resp)

		// Check agent info
		agentInfo := resp["agent"].(map[string]any)
		if agentInfo["name"] != "test-agent" {
			t.Errorf("agent name = %v, want test-agent", agentInfo["name"])
		}

		// Check usage instructions
		usage := resp["usage"].(string)
		if usage == "" {
			t.Error("expected usage instructions in response")
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		req := makeRequest(map[string]any{})
		result, _ := h.handleMyStatus(ctx, req)
		if !result.IsError {
			t.Error("expected error for unauthenticated request")
		}
	})
}

func TestHybridTool_SendMessage_DM(t *testing.T) {
	h, _, agentSvc, _ := newTestHybridRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "sender", "Sender", "ai", nil, 1)
	agentSvc.Register(ctx, "receiver", "Receiver", "ai", nil, 1)

	authCtx := ContextWithAgentName(ctx, "sender")

	t.Run("successful DM", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"to":   "receiver",
			"body": "Hello from test",
		})

		result, err := h.handleSendMessage(authCtx, req)
		if err != nil {
			t.Fatalf("handleSendMessage: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		var resp map[string]any
		text := result.Content[0].(mcplib.TextContent).Text
		json.Unmarshal([]byte(text), &resp)
		if resp["message_id"] == nil {
			t.Error("expected message_id in response")
		}
	})

	t.Run("missing body", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"to": "receiver",
		})
		result, _ := h.handleSendMessage(authCtx, req)
		if !result.IsError {
			t.Error("expected error for missing body")
		}
	})

	t.Run("both to and channel rejected", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"to":      "receiver",
			"channel": "general",
			"body":    "test",
		})
		result, _ := h.handleSendMessage(authCtx, req)
		if !result.IsError {
			t.Error("expected error when both to and channel specified")
		}
	})

	t.Run("neither to nor channel rejected", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"body": "test",
		})
		result, _ := h.handleSendMessage(authCtx, req)
		if !result.IsError {
			t.Error("expected error when neither to nor channel specified")
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"to":   "receiver",
			"body": "should fail",
		})
		result, _ := h.handleSendMessage(ctx, req)
		if !result.IsError {
			t.Error("expected error for unauthenticated request")
		}
	})
}

func TestHybridTool_Search(t *testing.T) {
	h, _, agentSvc, _ := newTestHybridRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "test-agent", "Test Agent", "ai", nil, 1)
	authCtx := ContextWithAgentName(ctx, "test-agent")

	t.Run("search for messaging actions", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"query": "read inbox messages",
		})

		result, err := h.handleSearch(authCtx, req)
		if err != nil {
			t.Fatalf("handleSearch: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		var resp map[string]any
		text := result.Content[0].(mcplib.TextContent).Text
		json.Unmarshal([]byte(text), &resp)

		count := resp["count"].(float64)
		if count == 0 {
			t.Error("expected at least one result")
		}

		actionsList := resp["actions"].([]any)
		firstAction := actionsList[0].(map[string]any)
		if firstAction["name"] == nil {
			t.Error("expected name in action result")
		}
	})

	t.Run("empty query returns all actions", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"limit": float64(20),
		})

		result, err := h.handleSearch(authCtx, req)
		if err != nil {
			t.Fatalf("handleSearch: %v", err)
		}

		var resp map[string]any
		text := result.Content[0].(mcplib.TextContent).Text
		json.Unmarshal([]byte(text), &resp)

		count := resp["count"].(float64)
		if count < 5 {
			t.Errorf("expected at least 5 actions in browse mode, got %v", count)
		}
	})
}

func TestHybridTool_Execute(t *testing.T) {
	h, msgSvc, agentSvc, _ := newTestHybridRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "executor", "Executor", "ai", nil, 1)
	agentSvc.Register(ctx, "target", "Target", "ai", nil, 1)

	// Send a message so the executor has something to read
	msgSvc.SendMessage(ctx, "target", "executor", "hello executor", messaging.SendOptions{})

	authCtx := ContextWithAgentName(ctx, "executor")

	t.Run("read_inbox via execute", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"code": `call("read_inbox", { limit: 10 })`,
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
		if count != 1 {
			t.Errorf("expected 1 message, got %v", count)
		}
	})

	t.Run("send_message via execute", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"code": `call("send_message", { to: "target", body: "hello from execute" })`,
		})

		result, err := h.handleExecute(authCtx, req)
		if err != nil {
			t.Fatalf("handleExecute: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resultData := parseCallResult(t, result)
		if resultData["message_id"] == nil {
			t.Error("expected message_id in execute result")
		}
	})

	t.Run("discover_agents via execute", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"code": `call("discover_agents", {})`,
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
		if count < 2 {
			t.Errorf("expected at least 2 agents, got %v", count)
		}
	})

	t.Run("unknown action returns error", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"code": `call("nonexistent_action", {})`,
		})

		result, _ := h.handleExecute(authCtx, req)
		// call() returns { ok: false, error: {...} } inside a successful execution result.
		resp := parseResponse(t, result)
		callResult := resp["result"].(map[string]any)
		if callResult["ok"] != false {
			t.Error("expected ok=false for unknown action")
		}
	})

	t.Run("empty code rejected", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"code": "",
		})

		result, _ := h.handleExecute(authCtx, req)
		if !result.IsError {
			t.Error("expected error for empty code")
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"code": `call("read_inbox", {})`,
		})
		result, _ := h.handleExecute(ctx, req)
		if !result.IsError {
			t.Error("expected error for unauthenticated request")
		}
	})

	t.Run("auth propagation to bridge", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"code": `call("read_inbox", {})`,
		})

		// Execute as "executor" - should see executor's inbox
		result, err := h.handleExecute(authCtx, req)
		if err != nil {
			t.Fatalf("handleExecute: %v", err)
		}

		var resp map[string]any
		text := result.Content[0].(mcplib.TextContent).Text
		json.Unmarshal([]byte(text), &resp)

		// The bridge should use "executor" as the agent name
		if resp["calls"].(float64) != 1 {
			t.Errorf("expected 1 call, got %v", resp["calls"])
		}
	})
}

func TestHybridTool_GetReplies(t *testing.T) {
	h, msgSvc, agentSvc, _ := newTestHybridRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "alice", "Alice", "ai", nil, 1)
	agentSvc.Register(ctx, "bob", "Bob", "ai", nil, 1)

	authCtx := ContextWithAgentName(ctx, "alice")

	// Send a parent message from bob to alice.
	parentMsg, err := msgSvc.SendMessage(ctx, "bob", "alice", "parent message", messaging.SendOptions{})
	if err != nil {
		t.Fatalf("send parent message: %v", err)
	}

	// Send two replies to the parent message.
	replyTo := parentMsg.ID
	_, err = msgSvc.SendMessage(ctx, "alice", "bob", "reply one", messaging.SendOptions{ReplyTo: &replyTo})
	if err != nil {
		t.Fatalf("send reply 1: %v", err)
	}
	_, err = msgSvc.SendMessage(ctx, "bob", "alice", "reply two", messaging.SendOptions{ReplyTo: &replyTo})
	if err != nil {
		t.Fatalf("send reply 2: %v", err)
	}

	t.Run("returns replies for message", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"message_id": float64(parentMsg.ID),
		})

		result, err := h.handleGetReplies(authCtx, req)
		if err != nil {
			t.Fatalf("handleGetReplies: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		var resp map[string]any
		text := result.Content[0].(mcplib.TextContent).Text
		json.Unmarshal([]byte(text), &resp)

		count := resp["count"].(float64)
		if count != 2 {
			t.Errorf("expected 2 replies, got %v", count)
		}

		replies := resp["replies"].([]any)
		if len(replies) != 2 {
			t.Errorf("expected 2 replies in array, got %d", len(replies))
		}

		if resp["message_id"].(float64) != float64(parentMsg.ID) {
			t.Errorf("expected message_id %d, got %v", parentMsg.ID, resp["message_id"])
		}
	})

	t.Run("returns empty for message with no replies", func(t *testing.T) {
		// Send a message with no replies.
		noReplyMsg, err := msgSvc.SendMessage(ctx, "bob", "alice", "no replies here", messaging.SendOptions{})
		if err != nil {
			t.Fatalf("send message: %v", err)
		}

		req := makeRequest(map[string]any{
			"message_id": float64(noReplyMsg.ID),
		})

		result, err := h.handleGetReplies(authCtx, req)
		if err != nil {
			t.Fatalf("handleGetReplies: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		var resp map[string]any
		text := result.Content[0].(mcplib.TextContent).Text
		json.Unmarshal([]byte(text), &resp)

		count := resp["count"].(float64)
		if count != 0 {
			t.Errorf("expected 0 replies, got %v", count)
		}
	})

	t.Run("missing message_id", func(t *testing.T) {
		req := makeRequest(map[string]any{})
		result, _ := h.handleGetReplies(authCtx, req)
		if !result.IsError {
			t.Error("expected error for missing message_id")
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"message_id": float64(1),
		})
		result, _ := h.handleGetReplies(ctx, req)
		if !result.IsError {
			t.Error("expected error for unauthenticated request")
		}
	})

	t.Run("get_replies via execute", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"code": fmt.Sprintf(`call("get_replies", {"message_id": %d})`, parentMsg.ID),
		})

		result, err := h.handleExecute(authCtx, req)
		if err != nil {
			t.Fatalf("handleExecute: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		// Parse the execute envelope to get the bridge result.
		var resp map[string]any
		text := result.Content[0].(mcplib.TextContent).Text
		json.Unmarshal([]byte(text), &resp)

		callEnvelope := resp["result"].(map[string]any)
		inner := callEnvelope["result"].(map[string]any)
		count := inner["count"].(float64)
		if count != 2 {
			t.Errorf("expected 2 replies via execute, got %v", count)
		}
	})
}

var _ = storage.RunMigrations
