package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"

	"github.com/smart-mcp-proxy/synapbus/internal/agents"
	"github.com/smart-mcp-proxy/synapbus/internal/messaging"
	"github.com/smart-mcp-proxy/synapbus/internal/storage"
	"github.com/smart-mcp-proxy/synapbus/internal/trace"
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

func newTestRegistrar(t *testing.T) (*ToolRegistrar, *messaging.MessagingService, *agents.AgentService, *sql.DB) {
	t.Helper()
	db := newTestDB(t)

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	agentStore := agents.NewSQLiteAgentStore(db)
	agentService := agents.NewAgentService(agentStore, tracer)

	registrar := NewToolRegistrar(msgService, agentService)
	return registrar, msgService, agentService, db
}

func makeRequest(args map[string]any) mcplib.CallToolRequest {
	return mcplib.CallToolRequest{
		Params: mcplib.CallToolParams{
			Arguments: args,
		},
	}
}

func TestToolHandler_RegisterAgent(t *testing.T) {
	tr, _, _, _ := newTestRegistrar(t)
	ctx := context.Background()

	t.Run("successful registration", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"name":         "test-agent",
			"display_name": "Test Agent",
			"type":         "ai",
		})

		result, err := tr.handleRegisterAgent(ctx, req)
		if err != nil {
			t.Fatalf("handleRegisterAgent: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		// Parse response
		var resp map[string]any
		text := result.Content[0].(mcplib.TextContent).Text
		if err := json.Unmarshal([]byte(text), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if resp["api_key"] == nil || resp["api_key"] == "" {
			t.Error("expected api_key in response")
		}
		if resp["name"] != "test-agent" {
			t.Errorf("name = %v, want test-agent", resp["name"])
		}
	})

	t.Run("missing name", func(t *testing.T) {
		req := makeRequest(map[string]any{})

		result, err := tr.handleRegisterAgent(ctx, req)
		if err != nil {
			t.Fatalf("handleRegisterAgent: %v", err)
		}
		if !result.IsError {
			t.Error("expected error for missing name")
		}
	})
}

func TestToolHandler_SendMessage(t *testing.T) {
	tr, _, agentSvc, _ := newTestRegistrar(t)
	ctx := context.Background()

	// Register agents
	agentSvc.Register(ctx, "sender", "Sender", "ai", nil, 1)
	agentSvc.Register(ctx, "receiver", "Receiver", "ai", nil, 1)

	// Set up authenticated context
	authCtx := ContextWithAgentName(ctx, "sender")

	t.Run("successful send", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"to":   "receiver",
			"body": "Hello from test",
		})

		result, err := tr.handleSendMessage(authCtx, req)
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

	t.Run("missing to", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"body": "no recipient",
		})

		result, _ := tr.handleSendMessage(authCtx, req)
		if !result.IsError {
			t.Error("expected error for missing 'to'")
		}
	})

	t.Run("missing body", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"to": "receiver",
		})

		result, _ := tr.handleSendMessage(authCtx, req)
		if !result.IsError {
			t.Error("expected error for missing body")
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"to":   "receiver",
			"body": "should fail",
		})

		result, _ := tr.handleSendMessage(ctx, req)
		if !result.IsError {
			t.Error("expected error for unauthenticated request")
		}
	})
}

func TestToolHandler_ReadInbox(t *testing.T) {
	tr, msgSvc, agentSvc, _ := newTestRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "sender", "Sender", "ai", nil, 1)
	agentSvc.Register(ctx, "reader", "Reader", "ai", nil, 1)

	msgSvc.SendMessage(ctx, "sender", "reader", "test message", messaging.SendOptions{})

	authCtx := ContextWithAgentName(ctx, "reader")

	t.Run("read messages", func(t *testing.T) {
		req := makeRequest(map[string]any{})

		result, err := tr.handleReadInbox(authCtx, req)
		if err != nil {
			t.Fatalf("handleReadInbox: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		var resp map[string]any
		text := result.Content[0].(mcplib.TextContent).Text
		json.Unmarshal([]byte(text), &resp)
		count := resp["count"].(float64)
		if count != 1 {
			t.Errorf("count = %v, want 1", count)
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		req := makeRequest(map[string]any{})
		result, _ := tr.handleReadInbox(ctx, req)
		if !result.IsError {
			t.Error("expected error for unauthenticated request")
		}
	})
}

func TestToolHandler_ClaimMessages(t *testing.T) {
	tr, msgSvc, agentSvc, _ := newTestRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "sender", "Sender", "ai", nil, 1)
	agentSvc.Register(ctx, "claimer", "Claimer", "ai", nil, 1)

	msgSvc.SendMessage(ctx, "sender", "claimer", "task 1", messaging.SendOptions{})
	msgSvc.SendMessage(ctx, "sender", "claimer", "task 2", messaging.SendOptions{})

	authCtx := ContextWithAgentName(ctx, "claimer")

	req := makeRequest(map[string]any{"limit": float64(1)})
	result, err := tr.handleClaimMessages(authCtx, req)
	if err != nil {
		t.Fatalf("handleClaimMessages: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	var resp map[string]any
	text := result.Content[0].(mcplib.TextContent).Text
	json.Unmarshal([]byte(text), &resp)
	count := resp["count"].(float64)
	if count != 1 {
		t.Errorf("count = %v, want 1", count)
	}
}

func TestToolHandler_MarkDone(t *testing.T) {
	tr, msgSvc, agentSvc, _ := newTestRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "sender", "Sender", "ai", nil, 1)
	agentSvc.Register(ctx, "worker", "Worker", "ai", nil, 1)

	msg, _ := msgSvc.SendMessage(ctx, "sender", "worker", "do this", messaging.SendOptions{})
	msgSvc.ClaimMessages(ctx, "worker", 1)

	authCtx := ContextWithAgentName(ctx, "worker")

	req := makeRequest(map[string]any{
		"message_id": float64(msg.ID),
	})

	result, err := tr.handleMarkDone(authCtx, req)
	if err != nil {
		t.Fatalf("handleMarkDone: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestToolHandler_SearchMessages(t *testing.T) {
	tr, msgSvc, agentSvc, _ := newTestRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "sender", "Sender", "ai", nil, 1)
	agentSvc.Register(ctx, "searcher", "Searcher", "ai", nil, 1)

	msgSvc.SendMessage(ctx, "sender", "searcher", "deployment failed", messaging.SendOptions{})
	msgSvc.SendMessage(ctx, "sender", "searcher", "all clear", messaging.SendOptions{})

	authCtx := ContextWithAgentName(ctx, "searcher")

	t.Run("keyword search", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"query": "deployment",
		})

		result, err := tr.handleSearchMessages(authCtx, req)
		if err != nil {
			t.Fatalf("handleSearchMessages: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		var resp map[string]any
		text := result.Content[0].(mcplib.TextContent).Text
		json.Unmarshal([]byte(text), &resp)
		count := resp["count"].(float64)
		if count != 1 {
			t.Errorf("count = %v, want 1", count)
		}
	})
}

func TestToolHandler_DiscoverAgents(t *testing.T) {
	tr, _, agentSvc, _ := newTestRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "bot-a", "Bot A", "ai", json.RawMessage(`{"skills":["search"]}`), 1)
	agentSvc.Register(ctx, "bot-b", "Bot B", "ai", json.RawMessage(`{"skills":["analyze"]}`), 1)

	authCtx := ContextWithAgentName(ctx, "bot-a")

	req := makeRequest(map[string]any{
		"query": "search",
	})

	result, err := tr.handleDiscoverAgents(authCtx, req)
	if err != nil {
		t.Fatalf("handleDiscoverAgents: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	var resp map[string]any
	text := result.Content[0].(mcplib.TextContent).Text
	json.Unmarshal([]byte(text), &resp)
	count := resp["count"].(float64)
	if count != 1 {
		t.Errorf("count = %v, want 1", count)
	}
}

func TestToolHandler_UpdateAgent(t *testing.T) {
	tr, _, agentSvc, _ := newTestRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "update-me", "Update Me", "ai", nil, 1)

	authCtx := ContextWithAgentName(ctx, "update-me")

	req := makeRequest(map[string]any{
		"display_name": "Updated Name",
		"capabilities": `{"skills":["new-skill"]}`,
	})

	result, err := tr.handleUpdateAgent(authCtx, req)
	if err != nil {
		t.Fatalf("handleUpdateAgent: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	var resp map[string]any
	text := result.Content[0].(mcplib.TextContent).Text
	json.Unmarshal([]byte(text), &resp)
	if resp["display_name"] != "Updated Name" {
		t.Errorf("display_name = %v, want Updated Name", resp["display_name"])
	}
}

func TestToolHandler_DeregisterAgent(t *testing.T) {
	tr, _, agentSvc, _ := newTestRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "bye-bot", "Bye Bot", "ai", nil, 1)

	authCtx := ContextWithAgentName(ctx, "bye-bot")

	req := makeRequest(map[string]any{})

	result, err := tr.handleDeregisterAgent(authCtx, req)
	if err != nil {
		t.Fatalf("handleDeregisterAgent: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

var _ = storage.RunMigrations
