package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/trace"
)

func newTestPromptRegistrar(t *testing.T) (*PromptRegistrar, *messaging.MessagingService, *agents.AgentService, *channels.Service) {
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

	traceStore := trace.NewSQLiteTraceStore(db)

	registrar := NewPromptRegistrar(db, agentService, channelService, traceStore)
	return registrar, msgService, agentService, channelService
}

// extractPromptText extracts the text content from a GetPromptResult.
func extractPromptText(t *testing.T, result *mcplib.GetPromptResult) string {
	t.Helper()
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one prompt message")
	}
	tc, ok := result.Messages[0].Content.(mcplib.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Messages[0].Content)
	}
	return tc.Text
}

func TestPrompt_DailyDigest(t *testing.T) {
	p, msgService, agentService, _ := newTestPromptRegistrar(t)
	ctx := context.Background()

	// Register agents
	agentService.Register(ctx, "agent-a", "Agent A", "ai", nil, 1)
	agentService.Register(ctx, "agent-b", "Agent B", "ai", nil, 1)

	t.Run("empty system", func(t *testing.T) {
		req := mcplib.GetPromptRequest{}
		result, err := p.handleDailyDigest(ctx, req)
		if err != nil {
			t.Fatalf("handleDailyDigest: %v", err)
		}

		text := extractPromptText(t, result)
		if !strings.Contains(text, "Daily Digest") {
			t.Error("expected 'Daily Digest' in output")
		}
		if !strings.Contains(text, "Total messages sent") {
			t.Error("expected 'Total messages sent' in output")
		}
		if result.Messages[0].Role != mcplib.RoleUser {
			t.Errorf("expected role=user, got %s", result.Messages[0].Role)
		}
	})

	t.Run("with messages", func(t *testing.T) {
		// Send some messages
		msgService.SendMessage(ctx, "agent-a", "agent-b", "hello", messaging.SendOptions{})
		msgService.SendMessage(ctx, "agent-b", "agent-a", "hi back", messaging.SendOptions{})
		msgService.SendMessage(ctx, "agent-a", "agent-b", "urgent!", messaging.SendOptions{Priority: 8})

		req := mcplib.GetPromptRequest{}
		result, err := p.handleDailyDigest(ctx, req)
		if err != nil {
			t.Fatalf("handleDailyDigest: %v", err)
		}

		text := extractPromptText(t, result)
		if !strings.Contains(text, "agent-a") {
			t.Error("expected 'agent-a' in active agents")
		}
		if !strings.Contains(text, "agent-b") {
			t.Error("expected 'agent-b' in active agents")
		}
		// Should have the high priority message
		if !strings.Contains(text, "High-Priority") {
			t.Error("expected 'High-Priority' section in output")
		}
	})
}

func TestPrompt_AgentHealthCheck(t *testing.T) {
	p, msgService, agentService, _ := newTestPromptRegistrar(t)
	ctx := context.Background()

	t.Run("no agents", func(t *testing.T) {
		req := mcplib.GetPromptRequest{}
		result, err := p.handleAgentHealthCheck(ctx, req)
		if err != nil {
			t.Fatalf("handleAgentHealthCheck: %v", err)
		}

		text := extractPromptText(t, result)
		if !strings.Contains(text, "Agent Health Check") {
			t.Error("expected 'Agent Health Check' header")
		}
	})

	t.Run("with agents and pending DMs", func(t *testing.T) {
		agentService.Register(ctx, "healthy-agent", "Healthy", "ai", nil, 1)
		agentService.Register(ctx, "overloaded-agent", "Overloaded", "ai", nil, 1)

		// Send 12 messages to overloaded-agent to trigger "NEEDS ATTENTION"
		for i := 0; i < 12; i++ {
			msgService.SendMessage(ctx, "healthy-agent", "overloaded-agent", "msg", messaging.SendOptions{})
		}

		req := mcplib.GetPromptRequest{}
		result, err := p.handleAgentHealthCheck(ctx, req)
		if err != nil {
			t.Fatalf("handleAgentHealthCheck: %v", err)
		}

		text := extractPromptText(t, result)
		if !strings.Contains(text, "healthy-agent") {
			t.Error("expected 'healthy-agent' in output")
		}
		if !strings.Contains(text, "overloaded-agent") {
			t.Error("expected 'overloaded-agent' in output")
		}
		if !strings.Contains(text, "NEEDS ATTENTION") {
			t.Error("expected 'NEEDS ATTENTION' flag for overloaded agent")
		}
	})
}

func TestPrompt_ChannelOverview(t *testing.T) {
	p, _, agentService, channelService := newTestPromptRegistrar(t)
	ctx := context.Background()

	t.Run("no channels", func(t *testing.T) {
		req := mcplib.GetPromptRequest{}
		result, err := p.handleChannelOverview(ctx, req)
		if err != nil {
			t.Fatalf("handleChannelOverview: %v", err)
		}

		text := extractPromptText(t, result)
		if !strings.Contains(text, "Channel Overview") {
			t.Error("expected 'Channel Overview' header")
		}
		if !strings.Contains(text, "Total channels: 0") {
			t.Error("expected 'Total channels: 0' with no channels")
		}
	})

	t.Run("with channels", func(t *testing.T) {
		agentService.Register(ctx, "chan-creator", "Chan Creator", "ai", nil, 1)

		channelService.CreateChannel(ctx, channels.CreateChannelRequest{
			Name:      "general",
			Type:      "standard",
			CreatedBy: "chan-creator",
		})
		channelService.CreateChannel(ctx, channels.CreateChannelRequest{
			Name:      "private-ops",
			Type:      "standard",
			IsPrivate: true,
			CreatedBy: "chan-creator",
		})

		req := mcplib.GetPromptRequest{}
		result, err := p.handleChannelOverview(ctx, req)
		if err != nil {
			t.Fatalf("handleChannelOverview: %v", err)
		}

		text := extractPromptText(t, result)
		if !strings.Contains(text, "general") {
			t.Error("expected 'general' channel in output")
		}
		if !strings.Contains(text, "private-ops") {
			t.Error("expected 'private-ops' channel in output")
		}
	})
}

func TestPrompt_DebugAgent(t *testing.T) {
	p, msgService, agentService, _ := newTestPromptRegistrar(t)
	ctx := context.Background()

	t.Run("missing agent_name", func(t *testing.T) {
		req := mcplib.GetPromptRequest{
			Params: mcplib.GetPromptParams{
				Name:      "debug-agent",
				Arguments: map[string]string{},
			},
		}
		result, err := p.handleDebugAgent(ctx, req)
		if err != nil {
			t.Fatalf("handleDebugAgent: %v", err)
		}

		text := extractPromptText(t, result)
		if !strings.Contains(text, "agent_name argument is required") {
			t.Error("expected error message about missing agent_name")
		}
	})

	t.Run("agent not found", func(t *testing.T) {
		req := mcplib.GetPromptRequest{
			Params: mcplib.GetPromptParams{
				Name: "debug-agent",
				Arguments: map[string]string{
					"agent_name": "nonexistent",
				},
			},
		}
		result, err := p.handleDebugAgent(ctx, req)
		if err != nil {
			t.Fatalf("handleDebugAgent: %v", err)
		}

		text := extractPromptText(t, result)
		if !strings.Contains(text, "agent not found") {
			t.Error("expected 'agent not found' error message")
		}
	})

	t.Run("agent exists", func(t *testing.T) {
		agentService.Register(ctx, "debug-target", "Debug Target", "ai", nil, 1)

		// Send some messages to create pending DMs
		agentService.Register(ctx, "sender-for-debug", "Sender", "ai", nil, 1)
		msgService.SendMessage(ctx, "sender-for-debug", "debug-target", "test message", messaging.SendOptions{})

		// Wait briefly for traces to flush
		time.Sleep(200 * time.Millisecond)

		req := mcplib.GetPromptRequest{
			Params: mcplib.GetPromptParams{
				Name: "debug-agent",
				Arguments: map[string]string{
					"agent_name": "debug-target",
				},
			},
		}
		result, err := p.handleDebugAgent(ctx, req)
		if err != nil {
			t.Fatalf("handleDebugAgent: %v", err)
		}

		text := extractPromptText(t, result)
		if !strings.Contains(text, "debug-target") {
			t.Error("expected 'debug-target' in output")
		}
		if !strings.Contains(text, "Identity") {
			t.Error("expected 'Identity' section in output")
		}
		if !strings.Contains(text, "Pending Messages") {
			t.Error("expected 'Pending Messages' section in output")
		}
		if !strings.Contains(text, "Recent Traces") {
			t.Error("expected 'Recent Traces' section in output")
		}
		if !strings.Contains(text, "Recent Errors") {
			t.Error("expected 'Recent Errors' section in output")
		}
		if !strings.Contains(text, "testowner") {
			t.Error("expected owner name 'testowner' in output")
		}
	})
}

func TestPrompt_Registration(t *testing.T) {
	p, _, _, _ := newTestPromptRegistrar(t)

	// Verify prompt definitions
	t.Run("daily-digest prompt definition", func(t *testing.T) {
		prompt := p.dailyDigestPrompt()
		if prompt.Name != "daily-digest" {
			t.Errorf("name = %q, want daily-digest", prompt.Name)
		}
		if prompt.Description == "" {
			t.Error("expected non-empty description")
		}
		if len(prompt.Arguments) != 0 {
			t.Errorf("expected 0 arguments, got %d", len(prompt.Arguments))
		}
	})

	t.Run("agent-health-check prompt definition", func(t *testing.T) {
		prompt := p.agentHealthCheckPrompt()
		if prompt.Name != "agent-health-check" {
			t.Errorf("name = %q, want agent-health-check", prompt.Name)
		}
		if len(prompt.Arguments) != 0 {
			t.Errorf("expected 0 arguments, got %d", len(prompt.Arguments))
		}
	})

	t.Run("channel-overview prompt definition", func(t *testing.T) {
		prompt := p.channelOverviewPrompt()
		if prompt.Name != "channel-overview" {
			t.Errorf("name = %q, want channel-overview", prompt.Name)
		}
	})

	t.Run("debug-agent prompt definition", func(t *testing.T) {
		prompt := p.debugAgentPrompt()
		if prompt.Name != "debug-agent" {
			t.Errorf("name = %q, want debug-agent", prompt.Name)
		}
		if len(prompt.Arguments) != 1 {
			t.Fatalf("expected 1 argument, got %d", len(prompt.Arguments))
		}
		arg := prompt.Arguments[0]
		if arg.Name != "agent_name" {
			t.Errorf("arg name = %q, want agent_name", arg.Name)
		}
		if !arg.Required {
			t.Error("expected agent_name to be required")
		}
	})
}
