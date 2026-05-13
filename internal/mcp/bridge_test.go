package mcp

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/reactions"
	"github.com/synapbus/synapbus/internal/storage"
	"github.com/synapbus/synapbus/internal/trace"
)

func newTestBridge(t *testing.T) (*ServiceBridge, *messaging.MessagingService, *agents.AgentService, *channels.Service) {
	t.Helper()
	db := newTestDB(t)

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

	// Seed test agents
	agentService.Register(context.Background(), "agent-a", "Agent A", "ai", nil, 1)
	agentService.Register(context.Background(), "agent-b", "Agent B", "ai", nil, 1)

	bridge := NewServiceBridge(
		msgService,
		agentService,
		channelService,
		swarmService,
		nil, // attachmentService
		nil, // searchService
		nil, // reactionService
		nil, // trustService
		nil, // wikiService
		"agent-a",
	)
	return bridge, msgService, agentService, channelService
}

func TestBridge_SendMessage(t *testing.T) {
	bridge, _, _, _ := newTestBridge(t)
	ctx := context.Background()

	result, err := bridge.Call(ctx, "send_message", map[string]any{
		"to":   "agent-b",
		"body": "hello from bridge",
	})
	if err != nil {
		t.Fatalf("Call send_message: %v", err)
	}

	r := result.(map[string]any)
	if r["message_id"] == nil {
		t.Error("expected message_id")
	}
}

func TestBridge_ReadInbox(t *testing.T) {
	bridge, msgService, _, _ := newTestBridge(t)
	ctx := context.Background()

	// Send a message to agent-a
	msgService.SendMessage(ctx, "agent-b", "agent-a", "test inbox msg", messaging.SendOptions{})

	result, err := bridge.Call(ctx, "read_inbox", map[string]any{
		"limit": 10,
	})
	if err != nil {
		t.Fatalf("Call read_inbox: %v", err)
	}

	r := result.(map[string]any)
	if r["count"].(int) != 1 {
		t.Errorf("count = %v, want 1", r["count"])
	}
}

func TestBridge_ClaimMessages(t *testing.T) {
	bridge, msgService, _, _ := newTestBridge(t)
	ctx := context.Background()

	msgService.SendMessage(ctx, "agent-b", "agent-a", "claim me", messaging.SendOptions{})

	result, err := bridge.Call(ctx, "claim_messages", map[string]any{
		"limit": 1,
	})
	if err != nil {
		t.Fatalf("Call claim_messages: %v", err)
	}

	r := result.(map[string]any)
	count := r["count"].(int)
	if count != 1 {
		t.Errorf("count = %v, want 1", count)
	}
}

func TestBridge_MarkDone(t *testing.T) {
	bridge, msgService, _, _ := newTestBridge(t)
	ctx := context.Background()

	msg, _ := msgService.SendMessage(ctx, "agent-b", "agent-a", "done me", messaging.SendOptions{})
	msgService.ClaimMessages(ctx, "agent-a", 1)

	result, err := bridge.Call(ctx, "mark_done", map[string]any{
		"message_id": int(msg.ID),
		"status":     "done",
	})
	if err != nil {
		t.Fatalf("Call mark_done: %v", err)
	}

	r := result.(map[string]any)
	if r["status"] != "done" {
		t.Errorf("status = %v, want done", r["status"])
	}
}

func TestBridge_MarkDone_Missing(t *testing.T) {
	bridge, _, _, _ := newTestBridge(t)
	ctx := context.Background()

	_, err := bridge.Call(ctx, "mark_done", map[string]any{})
	if err == nil {
		t.Error("expected error for missing message_id")
	}
}

func TestBridge_DiscoverAgents(t *testing.T) {
	bridge, _, _, _ := newTestBridge(t)
	ctx := context.Background()

	result, err := bridge.Call(ctx, "discover_agents", map[string]any{})
	if err != nil {
		t.Fatalf("Call discover_agents: %v", err)
	}

	r := result.(map[string]any)
	count := r["count"].(int)
	if count < 2 {
		t.Errorf("expected at least 2 agents, got %v", count)
	}
}

func TestBridge_CreateChannel(t *testing.T) {
	bridge, _, _, _ := newTestBridge(t)
	ctx := context.Background()

	result, err := bridge.Call(ctx, "create_channel", map[string]any{
		"name": "bridge-test-ch",
		"type": "standard",
	})
	if err != nil {
		t.Fatalf("Call create_channel: %v", err)
	}

	r := result.(map[string]any)
	if r["name"] != "bridge-test-ch" {
		t.Errorf("name = %v, want bridge-test-ch", r["name"])
	}
}

func TestBridge_JoinChannel(t *testing.T) {
	bridge, _, _, channelService := newTestBridge(t)
	ctx := context.Background()

	channelService.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "join-bridge", Type: "standard", CreatedBy: "agent-a",
	})

	// Create a bridge for agent-b to join
	bridgeB := NewServiceBridge(
		bridge.msgService,
		bridge.agentService,
		bridge.channelService,
		bridge.swarmService,
		nil, nil, nil, nil, nil,
		"agent-b",
	)

	result, err := bridgeB.Call(ctx, "join_channel", map[string]any{
		"channel_name": "join-bridge",
	})
	if err != nil {
		t.Fatalf("Call join_channel: %v", err)
	}

	r := result.(map[string]any)
	if r["status"] != "joined" {
		t.Errorf("status = %v, want joined", r["status"])
	}
}

func TestBridge_ListChannels(t *testing.T) {
	bridge, _, _, channelService := newTestBridge(t)
	ctx := context.Background()

	channelService.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "list-ch-1", Type: "standard", CreatedBy: "agent-a",
	})
	channelService.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "list-ch-2", Type: "standard", CreatedBy: "agent-a",
	})

	result, err := bridge.Call(ctx, "list_channels", map[string]any{})
	if err != nil {
		t.Fatalf("Call list_channels: %v", err)
	}

	r := result.(map[string]any)
	count := r["count"].(int)
	if count < 2 {
		t.Errorf("expected at least 2 channels, got %v", count)
	}
}

func TestBridge_SendChannelMessage(t *testing.T) {
	bridge, _, _, channelService := newTestBridge(t)
	ctx := context.Background()

	channelService.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "msg-bridge", Type: "standard", CreatedBy: "agent-a",
	})

	result, err := bridge.Call(ctx, "send_channel_message", map[string]any{
		"channel_name": "msg-bridge",
		"body":         "hello from bridge",
	})
	if err != nil {
		t.Fatalf("Call send_channel_message: %v", err)
	}

	r := result.(map[string]any)
	if r["status"] != "sent" {
		t.Errorf("status = %v, want sent", r["status"])
	}
}

func TestBridge_UnknownAction(t *testing.T) {
	bridge, _, _, _ := newTestBridge(t)
	ctx := context.Background()

	_, err := bridge.Call(ctx, "totally_unknown", map[string]any{})
	if err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestBridge_ParamHelpers(t *testing.T) {
	args := map[string]any{
		"str_val":  "hello",
		"int_val":  float64(42),
		"bool_val": true,
		"nil_val":  nil,
	}

	t.Run("getString", func(t *testing.T) {
		if v := getString(args, "str_val", ""); v != "hello" {
			t.Errorf("got %q, want hello", v)
		}
		if v := getString(args, "missing", "default"); v != "default" {
			t.Errorf("got %q, want default", v)
		}
	})

	t.Run("getInt", func(t *testing.T) {
		if v := getInt(args, "int_val", 0); v != 42 {
			t.Errorf("got %d, want 42", v)
		}
		if v := getInt(args, "missing", 99); v != 99 {
			t.Errorf("got %d, want 99", v)
		}
	})

	t.Run("getBool", func(t *testing.T) {
		if v := getBool(args, "bool_val", false); v != true {
			t.Errorf("got %v, want true", v)
		}
		if v := getBool(args, "missing", true); v != true {
			t.Errorf("got %v, want true", v)
		}
	})
}

func newTestBridgeWithReactions(t *testing.T) (*ServiceBridge, *channels.Service) {
	t.Helper()
	db := newTestDB(t)

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

	reactionStore := reactions.NewSQLiteStore(db)
	reactionService := reactions.NewService(reactionStore, slog.Default())

	agentService.Register(context.Background(), "agent-a", "Agent A", "ai", nil, 1)
	agentService.Register(context.Background(), "agent-b", "Agent B", "ai", nil, 1)

	bridge := NewServiceBridge(
		msgService,
		agentService,
		channelService,
		swarmService,
		nil, // attachmentService
		nil, // searchService
		reactionService,
		nil, // trustService
		nil, // wikiService
		"agent-a",
	)
	return bridge, channelService
}

func TestBridge_React_WorkflowState(t *testing.T) {
	tests := []struct {
		name              string
		reaction          string
		wantAction        string
		wantWorkflowState string
	}{
		{
			name:              "approve sets approved state",
			reaction:          "approve",
			wantAction:        "added",
			wantWorkflowState: "approved",
		},
		{
			name:              "in_progress sets in_progress state",
			reaction:          "in_progress",
			wantAction:        "added",
			wantWorkflowState: "in_progress",
		},
		{
			name:              "done sets done state",
			reaction:          "done",
			wantAction:        "added",
			wantWorkflowState: "done",
		},
		{
			name:              "published sets published state",
			reaction:          "published",
			wantAction:        "added",
			wantWorkflowState: "published",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bridge, channelService := newTestBridgeWithReactions(t)
			ctx := context.Background()

			// Create a channel and send a message to react to
			ch, err := channelService.CreateChannel(ctx, channels.CreateChannelRequest{
				Name: "react-test", Type: "standard", CreatedBy: "agent-a",
			})
			if err != nil {
				t.Fatalf("create channel: %v", err)
			}
			channelService.JoinChannel(ctx, ch.ID, "agent-a")

			msg, err := bridge.Call(ctx, "send_channel_message", map[string]any{
				"channel_name": "react-test",
				"body":         "test message",
			})
			if err != nil {
				t.Fatalf("send_channel_message: %v", err)
			}
			msgMap := msg.(map[string]any)
			msgID := msgMap["message_id"]

			// React to the message
			result, err := bridge.Call(ctx, "react", map[string]any{
				"message_id": msgID,
				"reaction":   tt.reaction,
			})
			if err != nil {
				t.Fatalf("react: %v", err)
			}

			resp := result.(map[string]any)

			if resp["action"] != tt.wantAction {
				t.Errorf("action = %v, want %v", resp["action"], tt.wantAction)
			}

			state, ok := resp["workflow_state"]
			if !ok {
				t.Fatal("response missing workflow_state field")
			}
			if state != tt.wantWorkflowState {
				t.Errorf("workflow_state = %v, want %v", state, tt.wantWorkflowState)
			}

			rxns, ok := resp["reactions"]
			if !ok {
				t.Fatal("response missing reactions field")
			}
			rxnSlice, ok := rxns.([]*reactions.Reaction)
			if !ok {
				t.Fatalf("reactions has unexpected type %T", rxns)
			}
			if len(rxnSlice) == 0 {
				t.Error("expected at least one reaction")
			}
		})
	}
}

func TestBridge_React_Toggle_Removes_WorkflowState(t *testing.T) {
	bridge, channelService := newTestBridgeWithReactions(t)
	ctx := context.Background()

	ch, err := channelService.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "toggle-test", Type: "standard", CreatedBy: "agent-a",
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelService.JoinChannel(ctx, ch.ID, "agent-a")

	msg, err := bridge.Call(ctx, "send_channel_message", map[string]any{
		"channel_name": "toggle-test",
		"body":         "toggle message",
	})
	if err != nil {
		t.Fatalf("send_channel_message: %v", err)
	}
	msgMap := msg.(map[string]any)
	msgID := msgMap["message_id"]

	// Add reaction
	bridge.Call(ctx, "react", map[string]any{
		"message_id": msgID,
		"reaction":   "approve",
	})

	// Toggle off (remove)
	result, err := bridge.Call(ctx, "react", map[string]any{
		"message_id": msgID,
		"reaction":   "approve",
	})
	if err != nil {
		t.Fatalf("react toggle off: %v", err)
	}

	resp := result.(map[string]any)
	if resp["action"] != "removed" {
		t.Errorf("action = %v, want removed", resp["action"])
	}

	// After removing the only reaction, workflow_state should be "proposed"
	state, ok := resp["workflow_state"]
	if !ok {
		t.Fatal("response missing workflow_state after removal")
	}
	if state != "proposed" {
		t.Errorf("workflow_state = %v, want proposed", state)
	}
}

var _ = storage.RunMigrations

// TestBridge_ActionAliases verifies that each observed wrong action name from
// production logs resolves to the correct real action. We don't validate the
// full result payload — only that the call succeeds (i.e. dispatch reached the
// real handler) rather than returning "unknown action".
func TestBridge_ActionAliases(t *testing.T) {
	tests := []struct {
		alias     string
		realName  string
		args      map[string]any
		setupCh   string // optional: channel name to create+join before the call
		expectErr string // optional: substring of expected error (when call reaches real handler but fails for unrelated reasons)
	}{
		{
			alias:    "search",
			realName: "search_messages",
			args:     map[string]any{"query": "anything"},
		},
		{
			alias:    "my_status",
			realName: "read_inbox",
			args:     map[string]any{"limit": 5},
		},
		{
			alias:    "read_dm",
			realName: "read_inbox",
			args:     map[string]any{"limit": 5},
		},
		{
			alias:    "read_channel",
			realName: "get_channel_messages",
			args:     map[string]any{"channel_name": "alias-ch"},
			setupCh:  "alias-ch",
		},
		{
			alias:     "read_article",
			realName:  "get_article",
			args:      map[string]any{"slug": "anything"},
			expectErr: "wiki not available", // bridge has no wikiService → dispatch reached real handler
		},
	}

	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			bridge, _, _, channelService := newTestBridge(t)
			ctx := context.Background()

			if tt.setupCh != "" {
				ch, err := channelService.CreateChannel(ctx, channels.CreateChannelRequest{
					Name: tt.setupCh, Type: "standard", CreatedBy: "agent-a",
				})
				if err != nil {
					t.Fatalf("create channel: %v", err)
				}
				if err := channelService.JoinChannel(ctx, ch.ID, "agent-a"); err != nil {
					t.Fatalf("join channel: %v", err)
				}
			}

			_, err := bridge.Call(ctx, tt.alias, tt.args)
			if tt.expectErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.expectErr)
				}
				if !strings.Contains(err.Error(), tt.expectErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.expectErr)
				}
				if strings.Contains(err.Error(), "unknown action") {
					t.Fatalf("alias %q should have been resolved, got unknown-action error: %v", tt.alias, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("alias %q (real=%s) failed: %v", tt.alias, tt.realName, err)
			}
		})
	}
}

// TestBridge_TopLevelToolHint verifies that wrong-guess names which map to
// top-level MCP tools (not bridge actions) produce a targeted hint pointing
// at the real tool, instead of a generic "unknown action" error.
func TestBridge_TopLevelToolHint(t *testing.T) {
	bridge, _, _, _ := newTestBridge(t)
	ctx := context.Background()

	// The misspelled wrong guess hits the hint.
	_, err := bridge.Call(ctx, "rewrite_core_memory", map[string]any{})
	if err == nil {
		t.Fatal("expected error for top-level-tool wrong-guess")
	}
	msg := err.Error()
	if !strings.Contains(msg, "top-level MCP tool") {
		t.Errorf("error should mention top-level MCP tool, got: %v", err)
	}
	if !strings.Contains(msg, "memory_rewrite_core") {
		t.Errorf("error should name the real tool memory_rewrite_core, got: %v", err)
	}

	// The correct top-level tool name, when called via the bridge, also
	// returns the hint instead of plain "unknown action". (Agents learn the
	// real tool name from the first hint and then retry via call() — the
	// hint must catch both spellings.)
	_, err = bridge.Call(ctx, "memory_rewrite_core", map[string]any{})
	if err == nil {
		t.Fatal("expected error for memory_rewrite_core via bridge")
	}
	msg = err.Error()
	if !strings.Contains(msg, "top-level MCP tool") {
		t.Errorf("memory_rewrite_core via bridge should hint top-level MCP tool, got: %v", err)
	}
}

// TestBridge_UnknownAction_Suggestion verifies that an unknown action that is
// close to a real one (Levenshtein-wise) returns a "did you mean" suggestion.
func TestBridge_UnknownAction_Suggestion(t *testing.T) {
	bridge, _, _, _ := newTestBridge(t)
	ctx := context.Background()

	tests := []struct {
		input        string
		wantContains string
	}{
		// substring hit: "send" is contained in "send_message"
		{input: "send", wantContains: "send_message"},
		// Levenshtein within a shared verb: "list_channel" → "list_channels" (distance 1)
		{input: "list_channel", wantContains: "list_channels"},
		// Levenshtein within a shared verb: "get_channel_message" → "get_channel_messages"
		{input: "get_channel_message", wantContains: "get_channel_messages"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := bridge.Call(ctx, tt.input, map[string]any{})
			if err == nil {
				t.Fatalf("expected error for %q", tt.input)
			}
			if !strings.Contains(err.Error(), "did you mean") {
				t.Errorf("error should contain 'did you mean', got: %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantContains) {
				t.Errorf("error should suggest %q, got: %v", tt.wantContains, err)
			}
		})
	}
}

// TestBridge_UnknownAction_NoSuggestion verifies that a truly distant unknown
// action returns the plain "unknown action" error without a misleading
// suggestion.
func TestBridge_UnknownAction_NoSuggestion(t *testing.T) {
	bridge, _, _, _ := newTestBridge(t)
	ctx := context.Background()

	_, err := bridge.Call(ctx, "xyzzy_quux_frobnicate", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("distant action should not get a suggestion, got: %v", err)
	}
}

// TestBridge_UnknownAction_NoCrossVerbSuggestion guards against the suggester
// pairing actions that share a suffix but have opposite intent — e.g.
// `read_message` is two edits from `send_message`, but suggesting "send" to an
// agent that asked to read is actively misleading. The leading-verb gate in
// suggestBridgeAction must prevent this.
func TestBridge_UnknownAction_NoCrossVerbSuggestion(t *testing.T) {
	bridge, _, _, _ := newTestBridge(t)
	ctx := context.Background()

	cases := []string{
		"read_message",        // would have suggested send_message (distance 2)
		"delete_message",      // would have suggested send_message (distance 3)
		"fetch_channel",       // unrelated verb; must not suggest send/list/get
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := bridge.Call(ctx, in, map[string]any{})
			if err == nil {
				t.Fatalf("expected error for %q", in)
			}
			if strings.Contains(err.Error(), "did you mean") {
				t.Errorf("cross-verb suggestion leaked for %q: %v", in, err)
			}
		})
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
		{"send", "sned", 2},
		{"list_channel", "list_channels", 1},
	}
	for _, tt := range tests {
		got := levenshtein(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
