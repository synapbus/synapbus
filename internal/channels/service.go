package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/trace"
)

// ChannelSummary holds a joined channel with its unread message count.
type ChannelSummary struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	UnreadCount   int        `json:"unread"`
	LastMessageID int64      `json:"last_message_id"`
	LastMessageAt *time.Time `json:"last_message_at"`
}

// Service provides business logic for channel operations.
type Service struct {
	store      ChannelStore
	msgService *messaging.MessagingService
	tracer     *trace.Tracer
	logger     *slog.Logger
}

// NewService creates a new channel service.
func NewService(store ChannelStore, msgService *messaging.MessagingService, tracer *trace.Tracer) *Service {
	return &Service{
		store:      store,
		msgService: msgService,
		tracer:     tracer,
		logger:     slog.Default().With("component", "channels"),
	}
}

// CreateChannel creates a new channel and adds the creator as the owner.
func (s *Service) CreateChannel(ctx context.Context, req CreateChannelRequest) (*Channel, error) {
	// Validate name
	if err := ValidateChannelName(req.Name); err != nil {
		return nil, err
	}

	name := NormalizeChannelName(req.Name)

	// Set default type
	chType := req.Type
	if chType == "" {
		chType = TypeStandard
	}

	ch := &Channel{
		Name:        name,
		Description: req.Description,
		Topic:       req.Topic,
		Type:        chType,
		IsPrivate:   req.IsPrivate,
		IsSystem:    req.IsSystem,
		CreatedBy:   req.CreatedBy,
	}

	if err := s.store.CreateChannel(ctx, ch); err != nil {
		return nil, err
	}

	// Auto-add creator as owner
	member := &Membership{
		ChannelID: ch.ID,
		AgentName: req.CreatedBy,
		Role:      RoleOwner,
	}
	if err := s.store.AddMember(ctx, member); err != nil {
		return nil, fmt.Errorf("add creator as owner: %w", err)
	}

	s.logger.Info("channel created",
		"id", ch.ID,
		"name", ch.Name,
		"type", ch.Type,
		"is_private", ch.IsPrivate,
		"created_by", req.CreatedBy,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, req.CreatedBy, "channel.create", map[string]any{
			"channel_id":   ch.ID,
			"channel_name": ch.Name,
			"type":         ch.Type,
			"is_private":   ch.IsPrivate,
		})
	}

	return ch, nil
}

// JoinChannel adds an agent to a channel.
func (s *Service) JoinChannel(ctx context.Context, channelID int64, agentName string) error {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}

	// Check if already a member (idempotent)
	isMember, err := s.store.IsMember(ctx, channelID, agentName)
	if err != nil {
		return fmt.Errorf("check membership: %w", err)
	}
	if isMember {
		return nil // idempotent
	}

	// If private, check for a pending invite
	if ch.IsPrivate {
		hasInvite, err := s.store.HasPendingInvite(ctx, channelID, agentName)
		if err != nil {
			return fmt.Errorf("check invite: %w", err)
		}
		if !hasInvite {
			return ErrNotInvited
		}
		// Accept the invite
		if err := s.store.AcceptInvite(ctx, channelID, agentName); err != nil {
			return fmt.Errorf("accept invite: %w", err)
		}
	}

	member := &Membership{
		ChannelID: channelID,
		AgentName: agentName,
		Role:      RoleMember,
	}
	if err := s.store.AddMember(ctx, member); err != nil {
		return fmt.Errorf("add member: %w", err)
	}

	s.logger.Info("agent joined channel",
		"channel_id", channelID,
		"agent", agentName,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "channel.join", map[string]any{
			"channel_id":   channelID,
			"channel_name": ch.Name,
		})
	}

	return nil
}

// LeaveChannel removes an agent from a channel.
func (s *Service) LeaveChannel(ctx context.Context, channelID int64, agentName string) error {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}

	// System channels cannot be left
	if ch.IsSystem {
		return ErrSystemChannel
	}

	// Check membership and role
	member, err := s.store.GetMember(ctx, channelID, agentName)
	if err != nil {
		return err
	}

	if member.Role == RoleOwner {
		return ErrOwnerCannotLeave
	}

	if err := s.store.RemoveMember(ctx, channelID, agentName); err != nil {
		return err
	}

	s.logger.Info("agent left channel",
		"channel_id", channelID,
		"agent", agentName,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "channel.leave", map[string]any{
			"channel_id":   channelID,
			"channel_name": ch.Name,
		})
	}

	return nil
}

// DeleteChannel deletes a channel by ID. System channels cannot be deleted.
func (s *Service) DeleteChannel(ctx context.Context, channelID int64) error {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}

	if ch.IsSystem {
		return ErrSystemChannel
	}

	if err := s.store.DeleteChannel(ctx, channelID); err != nil {
		return err
	}

	s.logger.Info("channel deleted",
		"channel_id", channelID,
		"channel_name", ch.Name,
	)

	return nil
}

// InviteToChannel invites an agent to a private channel. Only the owner can invite.
func (s *Service) InviteToChannel(ctx context.Context, channelID int64, agentName, inviterAgent string) error {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}

	// Verify inviter is the owner
	inviterMember, err := s.store.GetMember(ctx, channelID, inviterAgent)
	if err != nil {
		return err
	}
	if inviterMember.Role != RoleOwner {
		return ErrNotChannelOwner
	}

	// Check if already a member (idempotent)
	isMember, err := s.store.IsMember(ctx, channelID, agentName)
	if err != nil {
		return fmt.Errorf("check membership: %w", err)
	}
	if isMember {
		return nil // already a member, no-op
	}

	inv := &ChannelInvite{
		ChannelID: channelID,
		AgentName: agentName,
		InvitedBy: inviterAgent,
	}
	if err := s.store.CreateInvite(ctx, inv); err != nil {
		return fmt.Errorf("create invite: %w", err)
	}

	s.logger.Info("agent invited to channel",
		"channel_id", channelID,
		"agent", agentName,
		"invited_by", inviterAgent,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, inviterAgent, "channel.invite", map[string]any{
			"channel_id":   channelID,
			"channel_name": ch.Name,
			"invitee":      agentName,
		})
	}

	return nil
}

// KickFromChannel removes an agent from a channel. Only the owner can kick.
func (s *Service) KickFromChannel(ctx context.Context, channelID int64, agentName, kickerAgent string) error {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}

	// Verify kicker is the owner
	kickerMember, err := s.store.GetMember(ctx, channelID, kickerAgent)
	if err != nil {
		return err
	}
	if kickerMember.Role != RoleOwner {
		return ErrNotChannelOwner
	}

	// Cannot kick yourself
	if agentName == kickerAgent {
		return fmt.Errorf("cannot kick yourself from the channel")
	}

	// Verify target is a member
	if err := s.store.RemoveMember(ctx, channelID, agentName); err != nil {
		return err
	}

	s.logger.Info("agent kicked from channel",
		"channel_id", channelID,
		"agent", agentName,
		"kicked_by", kickerAgent,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, kickerAgent, "channel.kick", map[string]any{
			"channel_id":   channelID,
			"channel_name": ch.Name,
			"kicked":       agentName,
		})
	}

	return nil
}

// ListChannels returns channels visible to the agent.
func (s *Service) ListChannels(ctx context.Context, agentName string) ([]*ChannelWithCount, error) {
	chList, err := s.store.ListChannels(ctx, agentName)
	if err != nil {
		return nil, err
	}

	result := make([]*ChannelWithCount, len(chList))
	for i, ch := range chList {
		count, err := s.store.CountMembers(ctx, ch.ID)
		if err != nil {
			return nil, fmt.Errorf("count members for channel %d: %w", ch.ID, err)
		}
		result[i] = &ChannelWithCount{
			Channel:     *ch,
			MemberCount: count,
		}
	}

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "channel.list", map[string]any{
			"count": len(result),
		})
	}

	return result, nil
}

// ListChannelsPaginated returns channels visible to the agent with pagination.
func (s *Service) ListChannelsPaginated(ctx context.Context, agentName string, opts ListChannelsOptions) (*PaginatedChannels, error) {
	allChannels, err := s.store.ListChannels(ctx, agentName)
	if err != nil {
		return nil, err
	}

	total := len(allChannels)

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	// Apply pagination
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := allChannels[start:end]

	result := make([]*ChannelWithCount, len(page))
	for i, ch := range page {
		count, err := s.store.CountMembers(ctx, ch.ID)
		if err != nil {
			return nil, fmt.Errorf("count members for channel %d: %w", ch.ID, err)
		}
		result[i] = &ChannelWithCount{
			Channel:     *ch,
			MemberCount: count,
		}
	}

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "channel.list", map[string]any{
			"count": len(result),
			"total": total,
		})
	}

	return &PaginatedChannels{
		Channels: result,
		Total:    total,
		Offset:   offset,
		Limit:    limit,
	}, nil
}

// GetChannel returns a channel by ID.
func (s *Service) GetChannel(ctx context.Context, id int64) (*Channel, error) {
	return s.store.GetChannel(ctx, id)
}

// IsMember checks if an agent is a member of a channel.
func (s *Service) IsMember(ctx context.Context, channelID int64, agentName string) (bool, error) {
	return s.store.IsMember(ctx, channelID, agentName)
}

// GetChannelByName returns a channel by name.
func (s *Service) GetChannelByName(ctx context.Context, name string) (*Channel, error) {
	return s.store.GetChannelByName(ctx, NormalizeChannelName(name))
}

// UpdateChannel updates a channel's metadata. Only the owner can update.
func (s *Service) UpdateChannel(ctx context.Context, channelID int64, req UpdateChannelRequest, agentName string) (*Channel, error) {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}

	// Verify caller is the owner
	member, err := s.store.GetMember(ctx, channelID, agentName)
	if err != nil {
		return nil, err
	}
	if member.Role != RoleOwner {
		return nil, ErrNotChannelOwner
	}

	// Apply updates
	if req.Description != nil {
		ch.Description = *req.Description
	}
	if req.Topic != nil {
		ch.Topic = *req.Topic
	}

	if err := s.store.UpdateChannel(ctx, ch); err != nil {
		return nil, err
	}

	// Reload to get updated timestamps
	ch, err = s.store.GetChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}

	s.logger.Info("channel updated",
		"channel_id", channelID,
		"agent", agentName,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "channel.update", map[string]any{
			"channel_id":   channelID,
			"channel_name": ch.Name,
		})
	}

	return ch, nil
}

// BroadcastMessage sends a message to a channel. It creates a single channel
// message (visible in the channel timeline via GetChannelMessages) and also
// delivers individual DM notifications to each member's inbox.
// If the message body contains @mentions, mentioned members receive a
// "mention":true flag in their inbox notification metadata, and the channel
// message metadata includes "mentioned_agents".
func (s *Service) BroadcastMessage(ctx context.Context, channelID int64, fromAgent, body string, priority int, metadata string, replyTo *int64, attachments []string) ([]*messaging.Message, error) {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}

	// Verify sender is a member; auto-join public channels on first send.
	isMember, err := s.store.IsMember(ctx, channelID, fromAgent)
	if err != nil {
		return nil, fmt.Errorf("check membership: %w", err)
	}
	if !isMember {
		if ch.IsPrivate {
			return nil, ErrNotChannelMember
		}
		if err := s.JoinChannel(ctx, channelID, fromAgent); err != nil {
			return nil, fmt.Errorf("auto-join public channel: %w", err)
		}
	}

	// Get members for mentions and inbox notifications
	members, err := s.store.GetMembers(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("get members: %w", err)
	}

	// Parse @mentions from the message body
	mentionedNames := messaging.ParseMentions(body)
	memberSet := make(map[string]bool, len(members))
	for _, m := range members {
		memberSet[m.AgentName] = true
	}

	// Filter mentions to channel members only, exclude sender
	mentionedMembers := make(map[string]bool)
	var mentionedAgentsList []string
	for _, name := range mentionedNames {
		if name == fromAgent || !memberSet[name] {
			continue
		}
		if !mentionedMembers[name] {
			mentionedMembers[name] = true
			mentionedAgentsList = append(mentionedAgentsList, name)
		}
	}

	// 1. Create the canonical channel message (no "to" — this is a channel post).
	channelMetaObj := map[string]any{"channel_name": ch.Name}
	if metadata != "" {
		channelMetaObj["user_metadata"] = json.RawMessage(metadata)
	}
	if len(mentionedAgentsList) > 0 {
		channelMetaObj["mentioned_agents"] = mentionedAgentsList
	}
	channelMetaBytes, _ := json.Marshal(channelMetaObj)

	channelMsg, err := s.msgService.SendMessage(ctx, fromAgent, "", body, messaging.SendOptions{
		Subject:     fmt.Sprintf("channel:%s", ch.Name),
		Priority:    priority,
		Metadata:    string(channelMetaBytes),
		ChannelID:   &channelID,
		ReplyTo:     replyTo,
		Attachments: attachments,
	})
	if err != nil {
		return nil, fmt.Errorf("create channel message: %w", err)
	}

	// 2. Deliver inbox notifications only to @mentioned members.
	recipientCount := 0
	for _, m := range members {
		if m.AgentName == fromAgent || !mentionedMembers[m.AgentName] {
			continue
		}

		inboxMetaObj := map[string]any{
			"channel_id":         channelID,
			"channel_name":       ch.Name,
			"channel_message_id": channelMsg.ID,
			"mention":            true,
		}
		inboxMetaBytes, _ := json.Marshal(inboxMetaObj)

		_, err := s.msgService.SendMessage(ctx, fromAgent, m.AgentName, body, messaging.SendOptions{
			Subject:  fmt.Sprintf("channel:%s", ch.Name),
			Priority: priority,
			Metadata: string(inboxMetaBytes),
		})
		if err != nil {
			s.logger.Error("failed to send mention notification",
				"channel_id", channelID,
				"from", fromAgent,
				"to", m.AgentName,
				"error", err,
			)
			continue
		}
		recipientCount++
	}

	s.logger.Info("channel message broadcast",
		"channel_id", channelID,
		"from", fromAgent,
		"message_id", channelMsg.ID,
		"recipients", recipientCount,
		"mentions", len(mentionedAgentsList),
	)

	if s.tracer != nil {
		traceData := map[string]any{
			"channel_id":   channelID,
			"channel_name": ch.Name,
			"message_id":   channelMsg.ID,
			"recipients":   recipientCount,
		}
		if len(mentionedAgentsList) > 0 {
			traceData["mentioned_agents"] = mentionedAgentsList
		}
		s.tracer.Record(ctx, fromAgent, "channel.broadcast", traceData)
	}

	return []*messaging.Message{channelMsg}, nil
}

// GetChannelSummaries returns channels the agent has joined with unread message counts.
func (s *Service) GetChannelSummaries(ctx context.Context, agentName string) ([]ChannelSummary, error) {
	return s.store.GetChannelSummaries(ctx, agentName)
}

// GetMembers returns all members of a channel.
func (s *Service) GetMembers(ctx context.Context, channelID int64) ([]*Membership, error) {
	return s.store.GetMembers(ctx, channelID)
}

// MyAgentsChannelName returns the canonical my-agents channel name for a username.
func MyAgentsChannelName(username string) string {
	return NormalizeChannelName("my-agents-" + username)
}

// EnsureMyAgentsChannel creates the private system channel "my-agents-{username}"
// if it does not already exist, and ensures the human agent is the owner.
// This method is idempotent.
func (s *Service) EnsureMyAgentsChannel(ctx context.Context, username string, humanAgentName string) error {
	channelName := MyAgentsChannelName(username)

	ch, err := s.store.GetChannelByName(ctx, channelName)
	if err == nil {
		// Channel already exists — ensure human agent is a member
		isMember, err := s.store.IsMember(ctx, ch.ID, humanAgentName)
		if err != nil {
			return fmt.Errorf("check membership: %w", err)
		}
		if !isMember {
			member := &Membership{
				ChannelID: ch.ID,
				AgentName: humanAgentName,
				Role:      RoleOwner,
			}
			if err := s.store.AddMember(ctx, member); err != nil {
				return fmt.Errorf("add owner to my-agents channel: %w", err)
			}
		}
		return nil
	}
	if err != ErrChannelNotFound {
		return fmt.Errorf("check my-agents channel: %w", err)
	}

	// Channel does not exist — create it
	_, err = s.CreateChannel(ctx, CreateChannelRequest{
		Name:        channelName,
		Description: fmt.Sprintf("Private command channel for all agents owned by %s", username),
		Type:        TypeStandard,
		IsPrivate:   true,
		IsSystem:    true,
		CreatedBy:   humanAgentName,
	})
	if err != nil {
		// Another goroutine may have created it concurrently
		if err == ErrChannelNameConflict {
			return nil
		}
		return fmt.Errorf("create my-agents channel: %w", err)
	}

	s.logger.Info("my-agents channel created",
		"channel", channelName,
		"owner", humanAgentName,
	)

	return nil
}

// JoinMyAgentsChannel adds an agent to the owner's my-agents channel.
// If the channel does not exist, this is a no-op (best effort).
func (s *Service) JoinMyAgentsChannel(ctx context.Context, username string, agentName string) error {
	channelName := MyAgentsChannelName(username)

	ch, err := s.store.GetChannelByName(ctx, channelName)
	if err != nil {
		if err == ErrChannelNotFound {
			return nil // channel doesn't exist yet, no-op
		}
		return fmt.Errorf("get my-agents channel: %w", err)
	}

	// Check if already a member (idempotent)
	isMember, err := s.store.IsMember(ctx, ch.ID, agentName)
	if err != nil {
		return fmt.Errorf("check membership: %w", err)
	}
	if isMember {
		return nil
	}

	member := &Membership{
		ChannelID: ch.ID,
		AgentName: agentName,
		Role:      RoleMember,
	}
	if err := s.store.AddMember(ctx, member); err != nil {
		return fmt.Errorf("add agent to my-agents channel: %w", err)
	}

	s.logger.Info("agent joined my-agents channel",
		"channel", channelName,
		"agent", agentName,
	)

	return nil
}
