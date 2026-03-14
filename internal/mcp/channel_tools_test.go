package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/storage"
	"github.com/synapbus/synapbus/internal/trace"
)

func newTestChannelRegistrar(t *testing.T) (*ChannelToolRegistrar, *channels.Service) {
	t.Helper()
	db := newTestDB(t)

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	channelStore := channels.NewSQLiteChannelStore(db)
	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)
	channelService := channels.NewService(channelStore, msgService, tracer)

	// Seed test agents
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES ('agent-a', 'Agent A', 'ai', '{}', 1, 'hash', 'active')`)
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES ('agent-b', 'Agent B', 'ai', '{}', 1, 'hash', 'active')`)
	db.Exec(`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES ('agent-c', 'Agent C', 'ai', '{}', 1, 'hash', 'active')`)

	registrar := NewChannelToolRegistrar(channelService)
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

func TestChannelToolHandler_CreateChannel(t *testing.T) {
	ctr, _ := newTestChannelRegistrar(t)
	authCtx := ContextWithAgentName(context.Background(), "agent-a")

	t.Run("successful creation", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"name":        "test-channel",
			"description": "A test channel",
			"type":        "standard",
		})

		result, err := ctr.handleCreateChannel(authCtx, req)
		if err != nil {
			t.Fatalf("handleCreateChannel: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resp := parseResponse(t, result)
		if resp["name"] != "test-channel" {
			t.Errorf("name = %v, want test-channel", resp["name"])
		}
		if resp["channel_id"] == nil || resp["channel_id"].(float64) == 0 {
			t.Error("expected non-zero channel_id")
		}
	})

	t.Run("create private channel", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"name":       "private-test",
			"is_private": true,
		})

		result, err := ctr.handleCreateChannel(authCtx, req)
		if err != nil {
			t.Fatalf("handleCreateChannel: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resp := parseResponse(t, result)
		if resp["is_private"] != true {
			t.Errorf("is_private = %v, want true", resp["is_private"])
		}
	})

	t.Run("missing name", func(t *testing.T) {
		req := makeRequest(map[string]any{})
		result, _ := ctr.handleCreateChannel(authCtx, req)
		if !result.IsError {
			t.Error("expected error for missing name")
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		req := makeRequest(map[string]any{"name": "fail"})
		result, _ := ctr.handleCreateChannel(context.Background(), req)
		if !result.IsError {
			t.Error("expected error for unauthenticated request")
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		req := makeRequest(map[string]any{"name": "test-channel"})
		result, _ := ctr.handleCreateChannel(authCtx, req)
		if !result.IsError {
			t.Error("expected error for duplicate name")
		}
	})
}

func TestChannelToolHandler_JoinChannel(t *testing.T) {
	ctr, svc := newTestChannelRegistrar(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "join-test", Type: "standard", CreatedBy: "agent-a",
	})

	authCtx := ContextWithAgentName(ctx, "agent-b")

	t.Run("join by channel_id", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"channel_id": float64(ch.ID),
		})

		result, err := ctr.handleJoinChannel(authCtx, req)
		if err != nil {
			t.Fatalf("handleJoinChannel: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resp := parseResponse(t, result)
		if resp["status"] != "joined" {
			t.Errorf("status = %v, want joined", resp["status"])
		}
	})

	t.Run("join by channel_name", func(t *testing.T) {
		authCtxC := ContextWithAgentName(ctx, "agent-c")
		req := makeRequest(map[string]any{
			"channel_name": "join-test",
		})

		result, err := ctr.handleJoinChannel(authCtxC, req)
		if err != nil {
			t.Fatalf("handleJoinChannel: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}
	})

	t.Run("no channel identifier", func(t *testing.T) {
		req := makeRequest(map[string]any{})
		result, _ := ctr.handleJoinChannel(authCtx, req)
		if !result.IsError {
			t.Error("expected error when no channel identifier provided")
		}
	})
}

func TestChannelToolHandler_LeaveChannel(t *testing.T) {
	ctr, svc := newTestChannelRegistrar(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "leave-test", Type: "standard", CreatedBy: "agent-a",
	})
	svc.JoinChannel(ctx, ch.ID, "agent-b")

	authCtx := ContextWithAgentName(ctx, "agent-b")

	t.Run("successful leave", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"channel_id": float64(ch.ID),
		})

		result, err := ctr.handleLeaveChannel(authCtx, req)
		if err != nil {
			t.Fatalf("handleLeaveChannel: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}
	})

	t.Run("owner cannot leave", func(t *testing.T) {
		ownerCtx := ContextWithAgentName(ctx, "agent-a")
		req := makeRequest(map[string]any{
			"channel_id": float64(ch.ID),
		})

		result, _ := ctr.handleLeaveChannel(ownerCtx, req)
		if !result.IsError {
			t.Error("expected error for owner leaving")
		}
	})
}

func TestChannelToolHandler_ListChannels(t *testing.T) {
	ctr, svc := newTestChannelRegistrar(t)
	ctx := context.Background()

	svc.CreateChannel(ctx, channels.CreateChannelRequest{Name: "pub-1", Type: "standard", CreatedBy: "agent-a"})
	svc.CreateChannel(ctx, channels.CreateChannelRequest{Name: "pub-2", Type: "standard", CreatedBy: "agent-a"})

	authCtx := ContextWithAgentName(ctx, "agent-b")

	req := makeRequest(map[string]any{})
	result, err := ctr.handleListChannels(authCtx, req)
	if err != nil {
		t.Fatalf("handleListChannels: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	resp := parseResponse(t, result)
	count := resp["count"].(float64)
	if count != 2 {
		t.Errorf("count = %v, want 2", count)
	}

	chList := resp["channels"].([]any)
	ch0 := chList[0].(map[string]any)
	if ch0["name"] == nil {
		t.Error("expected name field in channel")
	}
	if ch0["member_count"] == nil {
		t.Error("expected member_count field in channel")
	}
}

func TestChannelToolHandler_InviteToChannel(t *testing.T) {
	ctr, svc := newTestChannelRegistrar(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "invite-test", Type: "standard", IsPrivate: true, CreatedBy: "agent-a",
	})

	ownerCtx := ContextWithAgentName(ctx, "agent-a")

	t.Run("owner can invite", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"channel_id": float64(ch.ID),
			"agent_name": "agent-b",
		})

		result, err := ctr.handleInviteToChannel(ownerCtx, req)
		if err != nil {
			t.Fatalf("handleInviteToChannel: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}
	})

	t.Run("missing agent_name", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"channel_id": float64(ch.ID),
		})
		result, _ := ctr.handleInviteToChannel(ownerCtx, req)
		if !result.IsError {
			t.Error("expected error for missing agent_name")
		}
	})
}

func TestChannelToolHandler_KickFromChannel(t *testing.T) {
	ctr, svc := newTestChannelRegistrar(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "kick-test", Type: "standard", CreatedBy: "agent-a",
	})
	svc.JoinChannel(ctx, ch.ID, "agent-b")

	ownerCtx := ContextWithAgentName(ctx, "agent-a")

	t.Run("owner can kick", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"channel_id": float64(ch.ID),
			"agent_name": "agent-b",
		})

		result, err := ctr.handleKickFromChannel(ownerCtx, req)
		if err != nil {
			t.Fatalf("handleKickFromChannel: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resp := parseResponse(t, result)
		if resp["status"] != "kicked" {
			t.Errorf("status = %v, want kicked", resp["status"])
		}
	})
}

func TestChannelToolHandler_SendChannelMessage(t *testing.T) {
	ctr, svc := newTestChannelRegistrar(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "msg-test", Type: "standard", CreatedBy: "agent-a",
	})
	svc.JoinChannel(ctx, ch.ID, "agent-b")

	authCtx := ContextWithAgentName(ctx, "agent-a")

	t.Run("send channel message", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"channel_name": "msg-test",
			"body":         "Hello channel!",
		})

		result, err := ctr.handleSendChannelMessage(authCtx, req)
		if err != nil {
			t.Fatalf("handleSendChannelMessage: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resp := parseResponse(t, result)
		recipients := resp["recipients"].(float64)
		if recipients != 1 {
			t.Errorf("recipients = %v, want 1", recipients)
		}
	})

	t.Run("missing body", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"channel_name": "msg-test",
		})
		result, _ := ctr.handleSendChannelMessage(authCtx, req)
		if !result.IsError {
			t.Error("expected error for missing body")
		}
	})
}

func TestChannelToolHandler_UpdateChannel(t *testing.T) {
	ctr, svc := newTestChannelRegistrar(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, channels.CreateChannelRequest{
		Name: "update-test", Type: "standard", Topic: "Original", CreatedBy: "agent-a",
	})

	ownerCtx := ContextWithAgentName(ctx, "agent-a")

	t.Run("update topic", func(t *testing.T) {
		req := makeRequest(map[string]any{
			"channel_id": float64(ch.ID),
			"topic":      "Updated topic",
		})

		result, err := ctr.handleUpdateChannel(ownerCtx, req)
		if err != nil {
			t.Fatalf("handleUpdateChannel: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}

		resp := parseResponse(t, result)
		if resp["topic"] != "Updated topic" {
			t.Errorf("topic = %v, want 'Updated topic'", resp["topic"])
		}
	})

	t.Run("non-owner cannot update", func(t *testing.T) {
		svc.JoinChannel(ctx, ch.ID, "agent-b")
		nonOwnerCtx := ContextWithAgentName(ctx, "agent-b")
		req := makeRequest(map[string]any{
			"channel_id": float64(ch.ID),
			"topic":      "Unauthorized",
		})
		result, _ := ctr.handleUpdateChannel(nonOwnerCtx, req)
		if !result.IsError {
			t.Error("expected error for non-owner update")
		}
	})
}

// suppress unused import
var _ = storage.RunMigrations
