// Package integration provides end-to-end tests for SynapBus that exercise the
// real MCP protocol over HTTP. Each test starts an in-process server with an
// in-memory SQLite database, registers agents, and makes JSON-RPC 2.0 calls
// to /mcp exactly as a real MCP client would.
package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/apikeys"
	"github.com/synapbus/synapbus/internal/attachments"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/console"
	mcpserver "github.com/synapbus/synapbus/internal/mcp"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/search"
	"github.com/synapbus/synapbus/internal/storage"
	"github.com/synapbus/synapbus/internal/trace"
)

// ---------------------------------------------------------------------------
// Test harness
// ---------------------------------------------------------------------------

// testEnv holds a fully wired test server and helpers.
type testEnv struct {
	server       *httptest.Server
	agentService *agents.AgentService
	t            *testing.T
}

// agentCreds stores an agent name and its raw API key.
type agentCreds struct {
	Name   string
	APIKey string
}

// setupEnv creates an in-memory SynapBus server with all services wired up
// and returns a testEnv ready for MCP calls. The server is closed automatically
// when the test finishes.
func setupEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()

	// Quiet logging during tests
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// In-memory SQLite with a unique name per test to avoid shared-cache collisions.
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	if err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Seed a test user (owner_id=1) so agent registration works.
	if _, err := db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name)
		VALUES (1, 'testowner', 'hash', 'Test Owner')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Create services
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	agentStore := agents.NewSQLiteAgentStore(db)
	agentService := agents.NewAgentService(agentStore, tracer)

	channelStore := channels.NewSQLiteChannelStore(db)
	channelService := channels.NewService(channelStore, msgService, tracer)

	taskStore := channels.NewSQLiteTaskStore(db)
	swarmService := channels.NewSwarmService(taskStore, channelStore, tracer)

	// Attachment service needs a temp dir for CAS.
	tmpDir := t.TempDir()
	cas, err := attachments.NewCAS(filepath.Join(tmpDir, "attachments"), slog.Default())
	if err != nil {
		t.Fatalf("create CAS: %v", err)
	}
	attStore := attachments.NewSQLiteStore(db, slog.Default())
	attService := attachments.NewService(attStore, cas, slog.Default())

	// Search service (FTS-only, no embedding provider).
	searchService := search.NewService(db, nil, nil, msgService)

	// API key service
	apiKeyStore := apikeys.NewSQLiteStore(db)
	apiKeyService := apikeys.NewService(apiKeyStore)

	// Console printer (discard output during tests)
	con := console.NewWithWriter(io.Discard)

	// Create MCP server
	mcpSrv := mcpserver.NewMCPServer(msgService, agentService, channelService, swarmService, attService, searchService, con, nil, nil)
	t.Cleanup(func() {
		mcpSrv.Shutdown(context.Background())
	})

	// Wire chi router — same middleware as production
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(agents.OptionalAuthMiddlewareWithAPIKeys(agentService, apiKeyService))
		r.Mount("/mcp", mcpSrv.Handler())
	})

	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	return &testEnv{
		server:       ts,
		agentService: agentService,
		t:            t,
	}
}

// registerAgent creates an agent via the service layer and returns its credentials.
func (e *testEnv) registerAgent(name, displayName string) agentCreds {
	e.t.Helper()
	_, apiKey, err := e.agentService.Register(context.Background(), name, displayName, "ai", nil, 1)
	if err != nil {
		e.t.Fatalf("register agent %q: %v", name, err)
	}
	return agentCreds{Name: name, APIKey: apiKey}
}

// ---------------------------------------------------------------------------
// MCP JSON-RPC 2.0 client helpers
// ---------------------------------------------------------------------------

// jsonRPCRequest is a JSON-RPC 2.0 request envelope.
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonRPCResponse is a JSON-RPC 2.0 response envelope.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpClient wraps an HTTP client with MCP protocol helpers.
type mcpClient struct {
	baseURL   string
	apiKey    string
	sessionID string
	nextID    int
	t         *testing.T
}

// newMCPClient creates a client that speaks JSON-RPC 2.0 to the MCP endpoint.
func newMCPClient(t *testing.T, baseURL, apiKey string) *mcpClient {
	t.Helper()
	return &mcpClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		nextID:  1,
		t:       t,
	}
}

// Initialize performs the MCP initialize handshake and stores the session ID.
func (c *mcpClient) Initialize() {
	c.t.Helper()
	resp := c.rawCall("initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "integration-test",
			"version": "1.0.0",
		},
	})
	if resp.Error != nil {
		c.t.Fatalf("initialize failed: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}
}

// CallTool calls an MCP tool and returns the parsed text content.
func (c *mcpClient) CallTool(toolName string, args map[string]any) map[string]any {
	c.t.Helper()
	resp := c.rawCall("tools/call", map[string]any{
		"name":      toolName,
		"arguments": args,
	})
	if resp.Error != nil {
		c.t.Fatalf("tools/call %s: JSON-RPC error code=%d message=%s", toolName, resp.Error.Code, resp.Error.Message)
	}

	return c.parseToolResult(toolName, resp.Result)
}

// CallToolExpectError calls an MCP tool and expects an error result (isError=true).
func (c *mcpClient) CallToolExpectError(toolName string, args map[string]any) string {
	c.t.Helper()
	resp := c.rawCall("tools/call", map[string]any{
		"name":      toolName,
		"arguments": args,
	})
	if resp.Error != nil {
		c.t.Fatalf("tools/call %s: unexpected JSON-RPC error code=%d message=%s",
			toolName, resp.Error.Code, resp.Error.Message)
	}

	// Parse result to see isError flag
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		c.t.Fatalf("unmarshal tool result: %v", err)
	}
	if !result.IsError {
		c.t.Fatalf("expected tool error from %s, got success", toolName)
	}
	if len(result.Content) > 0 {
		return result.Content[0].Text
	}
	return ""
}

// ListTools calls tools/list and returns the tool names.
func (c *mcpClient) ListTools() []string {
	c.t.Helper()
	resp := c.rawCall("tools/list", map[string]any{})
	if resp.Error != nil {
		c.t.Fatalf("tools/list: JSON-RPC error code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		c.t.Fatalf("unmarshal tools/list: %v", err)
	}

	names := make([]string, len(result.Tools))
	for i, t := range result.Tools {
		names[i] = t.Name
	}
	return names
}

// rawCall sends a JSON-RPC 2.0 request and returns the response.
func (c *mcpClient) rawCall(method string, params any) jsonRPCResponse {
	c.t.Helper()

	reqID := c.nextID
	c.nextID++

	body, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		c.t.Fatalf("marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		c.t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("HTTP %s: %v", method, err)
	}
	defer httpResp.Body.Close()

	// Store session ID from response header
	if sid := httpResp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		c.t.Fatalf("read response: %v", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		c.t.Fatalf("HTTP %s status %d: %s", method, httpResp.StatusCode, string(respBody))
	}

	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		c.t.Fatalf("unmarshal response for %s: %v\nbody: %s", method, err, string(respBody))
	}

	return rpcResp
}

// parseToolResult extracts the JSON content from an MCP tool result.
func (c *mcpClient) parseToolResult(toolName string, raw json.RawMessage) map[string]any {
	c.t.Helper()

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		c.t.Fatalf("unmarshal tool result for %s: %v", toolName, err)
	}

	if result.IsError {
		msg := ""
		if len(result.Content) > 0 {
			msg = result.Content[0].Text
		}
		c.t.Fatalf("tool %s returned error: %s", toolName, msg)
	}

	if len(result.Content) == 0 {
		c.t.Fatalf("tool %s returned empty content", toolName)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &data); err != nil {
		c.t.Fatalf("unmarshal tool content for %s: %v\nraw: %s", toolName, err, result.Content[0].Text)
	}
	return data
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestE2E_DirectMessage(t *testing.T) {
	env := setupEnv(t)
	alice := env.registerAgent("alice", "Alice")
	bob := env.registerAgent("bob", "Bob")

	// Alice connects via MCP and sends a DM to Bob.
	aliceClient := newMCPClient(t, env.server.URL, alice.APIKey)
	aliceClient.Initialize()

	sendResult := aliceClient.CallTool("send_message", map[string]any{
		"to":   "bob",
		"body": "Hey Bob, how are you?",
	})

	msgID := sendResult["message_id"]
	if msgID == nil || msgID.(float64) == 0 {
		t.Fatal("expected non-zero message_id")
	}

	// Bob connects and reads his inbox.
	bobClient := newMCPClient(t, env.server.URL, bob.APIKey)
	bobClient.Initialize()

	inbox := bobClient.CallTool("read_inbox", map[string]any{})
	count := inbox["count"].(float64)
	if count != 1 {
		t.Fatalf("Bob's inbox count = %v, want 1", count)
	}

	messages := inbox["messages"].([]any)
	firstMsg := messages[0].(map[string]any)
	if firstMsg["from_agent"] != "alice" {
		t.Errorf("from_agent = %v, want alice", firstMsg["from_agent"])
	}
	if firstMsg["body"] != "Hey Bob, how are you?" {
		t.Errorf("body = %v, want 'Hey Bob, how are you?'", firstMsg["body"])
	}
}

func TestE2E_ChannelMessaging(t *testing.T) {
	env := setupEnv(t)
	alice := env.registerAgent("alice", "Alice")
	bob := env.registerAgent("bob", "Bob")

	aliceClient := newMCPClient(t, env.server.URL, alice.APIKey)
	aliceClient.Initialize()

	bobClient := newMCPClient(t, env.server.URL, bob.APIKey)
	bobClient.Initialize()

	// Alice creates a channel.
	createResult := aliceClient.CallTool("create_channel", map[string]any{
		"name":        "project-x",
		"description": "Channel for Project X",
	})
	channelID := createResult["channel_id"].(float64)
	if channelID == 0 {
		t.Fatal("expected non-zero channel_id")
	}
	if createResult["name"] != "project-x" {
		t.Errorf("channel name = %v, want project-x", createResult["name"])
	}

	// Bob joins the channel.
	joinResult := bobClient.CallTool("join_channel", map[string]any{
		"channel_name": "project-x",
	})
	if joinResult["status"] != "joined" {
		t.Errorf("join status = %v, want joined", joinResult["status"])
	}

	// Alice sends a message to the channel.
	sendResult := aliceClient.CallTool("send_channel_message", map[string]any{
		"channel_name": "project-x",
		"body":         "Welcome to Project X!",
	})
	if sendResult["status"] != "sent" {
		t.Errorf("send status = %v, want sent", sendResult["status"])
	}
	if sendResult["message_id"].(float64) == 0 {
		t.Error("expected non-zero message_id")
	}

	// Bob reads his inbox and should see the channel message.
	inbox := bobClient.CallTool("read_inbox", map[string]any{})
	count := inbox["count"].(float64)
	if count < 1 {
		t.Fatalf("Bob's inbox count = %v, want >= 1", count)
	}

	messages := inbox["messages"].([]any)
	found := false
	for _, m := range messages {
		msg := m.(map[string]any)
		if msg["body"] == "Welcome to Project X!" {
			found = true
			if msg["from_agent"] != "alice" {
				t.Errorf("from_agent = %v, want alice", msg["from_agent"])
			}
			break
		}
	}
	if !found {
		t.Error("Bob did not receive the channel message")
	}

	// Alice lists channels.
	listResult := aliceClient.CallTool("list_channels", map[string]any{})
	chList := listResult["channels"].([]any)
	foundChannel := false
	for _, ch := range chList {
		chMap := ch.(map[string]any)
		if chMap["name"] == "project-x" {
			foundChannel = true
			if chMap["member_count"].(float64) != 2 {
				t.Errorf("member_count = %v, want 2", chMap["member_count"])
			}
			break
		}
	}
	if !foundChannel {
		t.Error("project-x channel not found in list")
	}
}

func TestE2E_SearchMessages(t *testing.T) {
	env := setupEnv(t)
	alice := env.registerAgent("alice", "Alice")
	bob := env.registerAgent("bob", "Bob")

	aliceClient := newMCPClient(t, env.server.URL, alice.APIKey)
	aliceClient.Initialize()

	bobClient := newMCPClient(t, env.server.URL, bob.APIKey)
	bobClient.Initialize()

	// Alice sends several messages to Bob.
	aliceClient.CallTool("send_message", map[string]any{
		"to":   "bob",
		"body": "The deployment pipeline is broken",
	})
	aliceClient.CallTool("send_message", map[string]any{
		"to":   "bob",
		"body": "Database backup completed successfully",
	})
	aliceClient.CallTool("send_message", map[string]any{
		"to":   "bob",
		"body": "Please review the pull request",
	})

	// Bob searches for "deployment" — should find exactly one.
	searchResult := bobClient.CallTool("search_messages", map[string]any{
		"query": "deployment",
	})
	count := searchResult["count"].(float64)
	if count != 1 {
		t.Errorf("search count for 'deployment' = %v, want 1", count)
	}

	// Bob searches for "database" — should find exactly one.
	searchResult2 := bobClient.CallTool("search_messages", map[string]any{
		"query": "database",
	})
	count2 := searchResult2["count"].(float64)
	if count2 != 1 {
		t.Errorf("search count for 'database' = %v, want 1", count2)
	}

	// Verify search mode is fulltext (no embedding provider configured).
	if mode := searchResult["search_mode"]; mode != "fulltext" {
		t.Errorf("search_mode = %v, want fulltext", mode)
	}
}

func TestE2E_ThreadReply(t *testing.T) {
	env := setupEnv(t)
	alice := env.registerAgent("alice", "Alice")
	bob := env.registerAgent("bob", "Bob")

	aliceClient := newMCPClient(t, env.server.URL, alice.APIKey)
	aliceClient.Initialize()

	bobClient := newMCPClient(t, env.server.URL, bob.APIKey)
	bobClient.Initialize()

	// Alice sends a message to Bob.
	sendResult := aliceClient.CallTool("send_message", map[string]any{
		"to":      "bob",
		"body":    "Can you check the logs?",
		"subject": "Log investigation",
	})
	originalMsgID := sendResult["message_id"].(float64)

	// Bob replies to Alice's message using reply_to.
	replyResult := bobClient.CallTool("send_message", map[string]any{
		"to":       "alice",
		"body":     "Sure, I found an error in the logs.",
		"reply_to": originalMsgID,
	})
	replyMsgID := replyResult["message_id"].(float64)
	if replyMsgID == 0 {
		t.Fatal("expected non-zero reply message_id")
	}

	// Alice reads her inbox and should see Bob's reply.
	inbox := aliceClient.CallTool("read_inbox", map[string]any{})
	messages := inbox["messages"].([]any)
	foundReply := false
	for _, m := range messages {
		msg := m.(map[string]any)
		if msg["body"] == "Sure, I found an error in the logs." {
			foundReply = true
			if msg["from_agent"] != "bob" {
				t.Errorf("from_agent = %v, want bob", msg["from_agent"])
			}
			// Verify reply_to is set
			if rt, ok := msg["reply_to"]; ok && rt != nil {
				if rt.(float64) != originalMsgID {
					t.Errorf("reply_to = %v, want %v", rt, originalMsgID)
				}
			} else {
				t.Error("expected reply_to to be set on the reply message")
			}
			break
		}
	}
	if !foundReply {
		t.Error("Alice did not receive Bob's reply")
	}
}

func TestE2E_AgentDiscovery(t *testing.T) {
	env := setupEnv(t)
	_ = env.registerAgent("search-bot", "Search Bot")
	_ = env.registerAgent("code-bot", "Code Bot")
	charlie := env.registerAgent("charlie", "Charlie")

	// Register agents with specific capabilities via the service directly.
	ctx := context.Background()
	env.agentService.UpdateAgent(ctx, "search-bot", "", json.RawMessage(`{"skills":["web-search","summarize"]}`))
	env.agentService.UpdateAgent(ctx, "code-bot", "", json.RawMessage(`{"skills":["code-review","testing"]}`))

	charlieClient := newMCPClient(t, env.server.URL, charlie.APIKey)
	charlieClient.Initialize()

	// Discover all agents (no query filter).
	allAgents := charlieClient.CallTool("discover_agents", map[string]any{})
	allCount := allAgents["count"].(float64)
	if allCount < 3 {
		t.Errorf("discover_agents count = %v, want >= 3", allCount)
	}

	// Verify agent details are present.
	agentsList := allAgents["agents"].([]any)
	names := make(map[string]bool)
	for _, a := range agentsList {
		agent := a.(map[string]any)
		names[agent["name"].(string)] = true
	}
	for _, expected := range []string{"search-bot", "code-bot", "charlie"} {
		if !names[expected] {
			t.Errorf("expected agent %q in discover_agents result", expected)
		}
	}

	// Discover agents by capability keyword.
	searchBots := charlieClient.CallTool("discover_agents", map[string]any{
		"query": "web-search",
	})
	searchCount := searchBots["count"].(float64)
	if searchCount != 1 {
		t.Errorf("discover_agents(web-search) count = %v, want 1", searchCount)
	}
	searchAgents := searchBots["agents"].([]any)
	if searchAgents[0].(map[string]any)["name"] != "search-bot" {
		t.Errorf("expected search-bot, got %v", searchAgents[0].(map[string]any)["name"])
	}
}

func TestE2E_ReadInbox(t *testing.T) {
	env := setupEnv(t)
	alice := env.registerAgent("alice", "Alice")
	bob := env.registerAgent("bob", "Bob")
	carol := env.registerAgent("carol", "Carol")

	aliceClient := newMCPClient(t, env.server.URL, alice.APIKey)
	aliceClient.Initialize()

	bobClient := newMCPClient(t, env.server.URL, bob.APIKey)
	bobClient.Initialize()

	carolClient := newMCPClient(t, env.server.URL, carol.APIKey)
	carolClient.Initialize()

	// Alice and Carol both send messages to Bob.
	aliceClient.CallTool("send_message", map[string]any{
		"to":       "bob",
		"body":     "Priority task from Alice",
		"priority": 8,
	})
	aliceClient.CallTool("send_message", map[string]any{
		"to":       "bob",
		"body":     "Low priority note from Alice",
		"priority": 2,
	})
	carolClient.CallTool("send_message", map[string]any{
		"to":   "bob",
		"body": "Message from Carol",
	})

	// Bob reads all inbox messages.
	t.Run("ReadAll", func(t *testing.T) {
		inbox := bobClient.CallTool("read_inbox", map[string]any{
			"include_read": true,
		})
		count := inbox["count"].(float64)
		if count != 3 {
			t.Errorf("inbox count = %v, want 3", count)
		}
	})

	// Bob reads with from_agent filter.
	t.Run("FilterByAgent", func(t *testing.T) {
		inbox := bobClient.CallTool("read_inbox", map[string]any{
			"from_agent":   "carol",
			"include_read": true,
		})
		count := inbox["count"].(float64)
		if count != 1 {
			t.Errorf("inbox count (from carol) = %v, want 1", count)
		}
		messages := inbox["messages"].([]any)
		if len(messages) > 0 {
			msg := messages[0].(map[string]any)
			if msg["from_agent"] != "carol" {
				t.Errorf("from_agent = %v, want carol", msg["from_agent"])
			}
		}
	})

	// Bob reads with min_priority filter.
	t.Run("FilterByPriority", func(t *testing.T) {
		inbox := bobClient.CallTool("read_inbox", map[string]any{
			"min_priority": 5,
			"include_read": true,
		})
		count := inbox["count"].(float64)
		if count != 2 {
			// carol's message has priority 5 (default), alice's has priority 8
			t.Errorf("inbox count (min_priority=5) = %v, want 2", count)
		}
	})
}

func TestE2E_ClaimAndMarkDone(t *testing.T) {
	env := setupEnv(t)
	alice := env.registerAgent("alice", "Alice")
	bob := env.registerAgent("bob", "Bob")

	aliceClient := newMCPClient(t, env.server.URL, alice.APIKey)
	aliceClient.Initialize()

	bobClient := newMCPClient(t, env.server.URL, bob.APIKey)
	bobClient.Initialize()

	// Alice sends a task to Bob.
	sendResult := aliceClient.CallTool("send_message", map[string]any{
		"to":   "bob",
		"body": "Process this report",
	})
	msgID := sendResult["message_id"].(float64)

	// Bob claims the message.
	claimResult := bobClient.CallTool("claim_messages", map[string]any{
		"limit": 1,
	})
	claimCount := claimResult["count"].(float64)
	if claimCount != 1 {
		t.Fatalf("claimed count = %v, want 1", claimCount)
	}

	// Bob marks the message as done.
	doneResult := bobClient.CallTool("mark_done", map[string]any{
		"message_id": msgID,
		"status":     "done",
	})
	if doneResult["status"] != "done" {
		t.Errorf("mark_done status = %v, want done", doneResult["status"])
	}
}

func TestE2E_ListTools(t *testing.T) {
	env := setupEnv(t)
	alice := env.registerAgent("alice", "Alice")

	aliceClient := newMCPClient(t, env.server.URL, alice.APIKey)
	aliceClient.Initialize()

	tools := aliceClient.ListTools()
	if len(tools) == 0 {
		t.Fatal("expected at least one tool from tools/list")
	}

	// Verify core tools are present.
	toolSet := make(map[string]bool)
	for _, name := range tools {
		toolSet[name] = true
	}
	expectedTools := []string{
		"send_message",
		"read_inbox",
		"claim_messages",
		"mark_done",
		"search_messages",
		"discover_agents",
		"get_channel_messages",
		"create_channel",
		"join_channel",
		"list_channels",
		"send_channel_message",
	}
	for _, name := range expectedTools {
		if !toolSet[name] {
			t.Errorf("expected tool %q in tools/list", name)
		}
	}
}

func TestE2E_UnauthenticatedAccess(t *testing.T) {
	env := setupEnv(t)

	// A client with no API key.
	anonClient := newMCPClient(t, env.server.URL, "")
	anonClient.Initialize()

	// Calling a tool that requires auth should return an error result.
	errMsg := anonClient.CallToolExpectError("send_message", map[string]any{
		"to":   "nobody",
		"body": "should fail",
	})
	if errMsg == "" {
		t.Error("expected error message for unauthenticated send_message")
	}

	// discover_agents should also require auth.
	errMsg2 := anonClient.CallToolExpectError("discover_agents", map[string]any{})
	if errMsg2 == "" {
		t.Error("expected error message for unauthenticated discover_agents")
	}
}

func TestE2E_ChannelPrivateInvite(t *testing.T) {
	env := setupEnv(t)
	alice := env.registerAgent("alice", "Alice")
	bob := env.registerAgent("bob", "Bob")

	aliceClient := newMCPClient(t, env.server.URL, alice.APIKey)
	aliceClient.Initialize()

	bobClient := newMCPClient(t, env.server.URL, bob.APIKey)
	bobClient.Initialize()

	// Alice creates a private channel.
	createResult := aliceClient.CallTool("create_channel", map[string]any{
		"name":       "secret-ops",
		"is_private": true,
	})
	if createResult["is_private"] != true {
		t.Errorf("is_private = %v, want true", createResult["is_private"])
	}

	// Bob tries to join without an invite — should fail.
	errMsg := bobClient.CallToolExpectError("join_channel", map[string]any{
		"channel_name": "secret-ops",
	})
	if errMsg == "" {
		t.Error("expected error when joining private channel without invite")
	}

	// Alice invites Bob.
	inviteResult := aliceClient.CallTool("invite_to_channel", map[string]any{
		"channel_name": "secret-ops",
		"agent_name":   "bob",
	})
	if inviteResult["status"] != "invited" {
		t.Errorf("invite status = %v, want invited", inviteResult["status"])
	}

	// Now Bob can join.
	joinResult := bobClient.CallTool("join_channel", map[string]any{
		"channel_name": "secret-ops",
	})
	if joinResult["status"] != "joined" {
		t.Errorf("join status = %v, want joined", joinResult["status"])
	}
}

func TestE2E_MultipleMessagesAndReadState(t *testing.T) {
	env := setupEnv(t)
	alice := env.registerAgent("alice", "Alice")
	bob := env.registerAgent("bob", "Bob")

	aliceClient := newMCPClient(t, env.server.URL, alice.APIKey)
	aliceClient.Initialize()

	bobClient := newMCPClient(t, env.server.URL, bob.APIKey)
	bobClient.Initialize()

	// Alice sends 3 messages to Bob.
	for i := 1; i <= 3; i++ {
		aliceClient.CallTool("send_message", map[string]any{
			"to":   "bob",
			"body": fmt.Sprintf("message %d", i),
		})
	}

	// Bob reads inbox — gets 3 messages.
	inbox1 := bobClient.CallTool("read_inbox", map[string]any{})
	if inbox1["count"].(float64) != 3 {
		t.Fatalf("first read count = %v, want 3", inbox1["count"])
	}

	// Bob reads inbox again without include_read — should be 0 (already read).
	inbox2 := bobClient.CallTool("read_inbox", map[string]any{})
	if inbox2["count"].(float64) != 0 {
		t.Errorf("second read count = %v, want 0 (messages already read)", inbox2["count"])
	}

	// With include_read, all 3 should come back.
	inbox3 := bobClient.CallTool("read_inbox", map[string]any{
		"include_read": true,
	})
	if inbox3["count"].(float64) != 3 {
		t.Errorf("read with include_read count = %v, want 3", inbox3["count"])
	}
}
