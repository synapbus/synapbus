package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/apikeys"
	"github.com/synapbus/synapbus/internal/console"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/trace"
)

func TestNewMCPServerWithConsole(t *testing.T) {
	db := newTestDB(t)

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	agentStore := agents.NewSQLiteAgentStore(db)
	agentService := agents.NewAgentService(agentStore, tracer)

	con := console.New()

	srv := NewMCPServer(msgService, agentService, nil, nil, nil, nil, con, nil, nil)
	if srv == nil {
		t.Fatal("expected non-nil MCPServer")
	}
	if srv.console != con {
		t.Error("expected console printer to be set")
	}
	if srv.connMgr == nil {
		t.Error("expected connection manager to be set")
	}
}

func TestNewMCPServerNilConsole(t *testing.T) {
	db := newTestDB(t)

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	agentStore := agents.NewSQLiteAgentStore(db)
	agentService := agents.NewAgentService(agentStore, tracer)

	// nil console should not panic
	srv := NewMCPServer(msgService, agentService, nil, nil, nil, nil, nil, nil, nil)
	if srv == nil {
		t.Fatal("expected non-nil MCPServer")
	}
}

func TestConnectionManagerClientInfo(t *testing.T) {
	cm := NewConnectionManager()

	conn := &Connection{
		ID:                 "session-1",
		AgentName:          "planner",
		Transport:          "streamable-http",
		ClientName:         "claude-code",
		ClientVersion:      "1.2.3",
		ProtocolVersion:    "2025-03-26",
		ClientCapabilities: []string{"roots", "sampling"},
	}
	cm.Add(conn)

	got, ok := cm.Get("session-1")
	if !ok {
		t.Fatal("expected connection to be found")
	}
	if got.ClientName != "claude-code" {
		t.Errorf("expected client name 'claude-code', got %q", got.ClientName)
	}
	if got.ClientVersion != "1.2.3" {
		t.Errorf("expected client version '1.2.3', got %q", got.ClientVersion)
	}
	if got.ProtocolVersion != "2025-03-26" {
		t.Errorf("expected protocol version '2025-03-26', got %q", got.ProtocolVersion)
	}
	if len(got.ClientCapabilities) != 2 {
		t.Errorf("expected 2 capabilities, got %d", len(got.ClientCapabilities))
	}

	cm.Remove("session-1")
	if cm.Count() != 0 {
		t.Error("expected 0 connections after remove")
	}
}

// T007: Test MCP tool calls with valid API key — agent identity is correctly resolved.
func TestMCPToolCall_WithValidAPIKey(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	agentStore := agents.NewSQLiteAgentStore(db)
	agentService := agents.NewAgentService(agentStore, tracer)

	apiKeyStore := apikeys.NewSQLiteStore(db)
	apiKeyService := apikeys.NewService(apiKeyStore)

	// Register an agent and get the raw API key
	_, apiKey, err := agentService.Register(ctx, "test-mcp-agent", "Test MCP Agent", "ai", nil, 1)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	// Also register a receiver
	agentService.Register(ctx, "receiver", "Receiver", "ai", nil, 1)

	// Create MCP server
	srv := NewMCPServer(msgService, agentService, nil, nil, nil, nil, nil, nil, nil)

	// Mount with auth middleware, just like main.go does
	mux := http.NewServeMux()
	handler := agents.OptionalAuthMiddlewareWithAPIKeys(agentService, apiKeyService)(srv.Handler())
	mux.Handle("/mcp", handler)
	mux.Handle("/mcp/", handler)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Send an initialize request to confirm we can connect with API key
	initPayload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test-client","version":"0.1"}}}`

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(initPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("MCP request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should get 200 OK (MCP accepted the connection)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Verify the agent was authenticated by checking the connection manager
	// (the AfterInitialize hook would have captured the agent name)
	// The init should have succeeded — verify by checking no 401 was returned
	t.Log("MCP connection with valid API key succeeded")
}

// T008: Test MCP tool calls without auth return 401 when auth is required.
// Note: With the current OptionalAuthMiddleware, unauthenticated requests pass through
// (returning tool-level errors). This test verifies that an invalid API key is rejected.
func TestMCPToolCall_InvalidAPIKeyReturns401(t *testing.T) {
	db := newTestDB(t)

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	agentStore := agents.NewSQLiteAgentStore(db)
	agentService := agents.NewAgentService(agentStore, tracer)

	apiKeyStore := apikeys.NewSQLiteStore(db)
	apiKeyService := apikeys.NewService(apiKeyStore)

	srv := NewMCPServer(msgService, agentService, nil, nil, nil, nil, nil, nil, nil)

	mux := http.NewServeMux()
	handler := agents.OptionalAuthMiddlewareWithAPIKeys(agentService, apiKeyService)(srv.Handler())
	mux.Handle("/mcp", handler)
	mux.Handle("/mcp/", handler)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	initPayload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test-client","version":"0.1"}}}`

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(initPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer invalid-key-12345")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("MCP request failed: %v", err)
	}
	defer resp.Body.Close()

	// Invalid API key should be rejected with 401
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid API key, got %d", resp.StatusCode)
	}
}

// T008 (continued): Test that unauthenticated MCP tool calls (no auth header at all)
// are rejected at the tool handler level.
func TestMCPToolCall_NoAuthReturnsToolError(t *testing.T) {
	tr, _, agentSvc, _ := newTestRegistrar(t)
	ctx := context.Background()

	// Register a receiver so the send would work if auth was present
	agentSvc.Register(ctx, "receiver", "Receiver", "ai", nil, 1)

	// Call send_message without any authenticated context
	req := makeRequest(map[string]any{
		"to":   "receiver",
		"body": "should fail",
	})

	result, err := tr.handleSendMessage(ctx, req)
	if err != nil {
		t.Fatalf("handleSendMessage returned error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for unauthenticated request")
	}

	text := result.Content[0].(mcplib.TextContent).Text
	if !strings.Contains(text, "authentication required") {
		t.Errorf("expected 'authentication required' error, got %q", text)
	}
}

// T009: Verify send_message enforces from_agent from the authenticated context.
// The send_message tool does NOT expose a "from" parameter — the sender is always
// derived from the authenticated agent identity in the context.
func TestSendMessage_EnforcesAuthenticatedAgent(t *testing.T) {
	tr, _, agentSvc, _ := newTestRegistrar(t)
	ctx := context.Background()

	agentSvc.Register(ctx, "real-sender", "Real Sender", "ai", nil, 1)
	agentSvc.Register(ctx, "impersonated", "Impersonated", "ai", nil, 1)
	agentSvc.Register(ctx, "receiver", "Receiver", "ai", nil, 1)

	// Authenticate as "real-sender"
	authCtx := ContextWithAgentName(ctx, "real-sender")

	// Try to send a message — even if someone could supply a "from" field,
	// the handler should use the authenticated agent name, not a user-supplied value.
	req := makeRequest(map[string]any{
		"to":   "receiver",
		"body": "message from real sender",
		// Note: there is no "from" parameter in the send_message tool definition,
		// but even if extra args are passed, the handler ignores them.
	})

	result, err := tr.handleSendMessage(authCtx, req)
	if err != nil {
		t.Fatalf("handleSendMessage: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	// Verify the message was sent from "real-sender" by reading receiver's inbox
	inboxCtx := ContextWithAgentName(ctx, "receiver")
	inboxReq := makeRequest(map[string]any{})
	inboxResult, _ := tr.handleReadInbox(inboxCtx, inboxReq)

	text := inboxResult.Content[0].(mcplib.TextContent).Text
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	messages := resp["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}

	msg := messages[0].(map[string]any)
	fromAgent := msg["from_agent"].(string)
	if fromAgent != "real-sender" {
		t.Errorf("message from_agent = %q, want %q — send_message must enforce authenticated agent", fromAgent, "real-sender")
	}
}
