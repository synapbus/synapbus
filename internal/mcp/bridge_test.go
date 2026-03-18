package mcp

import (
	"context"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
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
		nil, nil, nil, nil,
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

var _ = storage.RunMigrations
