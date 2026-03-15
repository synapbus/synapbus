package api

import (
	"context"
	"log/slog"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
)

// NewMessageEvent is broadcast when a new message is sent.
type NewMessageEvent struct {
	Channel   string `json:"channel,omitempty"`   // set for channel messages
	FromAgent string `json:"from_agent,omitempty"` // set for DMs
	ToAgent   string `json:"to_agent,omitempty"`   // set for DMs
	MessageID int64  `json:"message_id"`
}

// UnreadUpdateEvent is broadcast when unread counts change (e.g. mark-read).
type UnreadUpdateEvent struct {
	Channel     string `json:"channel,omitempty"`
	Agent       string `json:"agent,omitempty"`
	UnreadCount int    `json:"unread_count"`
}

// EventBroadcaster broadcasts real-time events to connected SSE clients.
type EventBroadcaster interface {
	BroadcastNewMessage(ctx context.Context, ownerID int64, event NewMessageEvent)
	BroadcastUnreadUpdate(ctx context.Context, ownerID int64, event UnreadUpdateEvent)
}

// SSEBroadcaster implements EventBroadcaster using the SSEHub.
type SSEBroadcaster struct {
	hub            *SSEHub
	agentService   *agents.AgentService
	channelService *channels.Service
	logger         *slog.Logger
}

// NewSSEBroadcaster creates a broadcaster that sends events via SSE.
func NewSSEBroadcaster(hub *SSEHub, agentService *agents.AgentService, channelService *channels.Service) *SSEBroadcaster {
	return &SSEBroadcaster{
		hub:            hub,
		agentService:   agentService,
		channelService: channelService,
		logger:         slog.Default().With("component", "api.broadcaster"),
	}
}

// BroadcastNewMessage sends a new_message event to the given owner.
func (b *SSEBroadcaster) BroadcastNewMessage(_ context.Context, ownerID int64, event NewMessageEvent) {
	b.hub.Broadcast(ownerID, SSEEvent{
		Type: "new_message",
		Data: event,
	})
}

// BroadcastUnreadUpdate sends an unread_update event to the given owner.
func (b *SSEBroadcaster) BroadcastUnreadUpdate(_ context.Context, ownerID int64, event UnreadUpdateEvent) {
	b.hub.Broadcast(ownerID, SSEEvent{
		Type: "unread_update",
		Data: event,
	})
}

// BroadcastDM broadcasts a new_message event for a direct message.
// It resolves the recipient agent's owner and sends the event to them.
func (b *SSEBroadcaster) BroadcastDM(ctx context.Context, msg NewMessageEvent) {
	if msg.ToAgent == "" {
		return
	}

	agent, err := b.agentService.GetAgent(ctx, msg.ToAgent)
	if err != nil {
		b.logger.Debug("could not resolve recipient owner for SSE broadcast",
			"to_agent", msg.ToAgent, "error", err)
		return
	}

	b.BroadcastNewMessage(ctx, agent.OwnerID, msg)
}

// BroadcastChannelMessage broadcasts a new_message event for a channel message.
// It resolves all channel members' owners and sends the event to each unique owner.
func (b *SSEBroadcaster) BroadcastChannelMessage(ctx context.Context, channelID int64, msg NewMessageEvent) {
	if b.channelService == nil {
		return
	}

	members, err := b.channelService.GetMembers(ctx, channelID)
	if err != nil {
		b.logger.Debug("could not get channel members for SSE broadcast",
			"channel_id", channelID, "error", err)
		return
	}

	// Collect unique owner IDs to avoid duplicate broadcasts.
	seen := make(map[int64]bool)
	for _, m := range members {
		agent, err := b.agentService.GetAgent(ctx, m.AgentName)
		if err != nil {
			continue
		}
		if !seen[agent.OwnerID] {
			seen[agent.OwnerID] = true
			b.BroadcastNewMessage(ctx, agent.OwnerID, msg)
		}
	}
}
