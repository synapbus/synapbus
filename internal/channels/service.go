package channels

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/smart-mcp-proxy/synapbus/internal/messaging"
	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

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
	channels, err := s.store.ListChannels(ctx, agentName)
	if err != nil {
		return nil, err
	}

	result := make([]*ChannelWithCount, len(channels))
	for i, ch := range channels {
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

// GetChannel returns a channel by ID.
func (s *Service) GetChannel(ctx context.Context, id int64) (*Channel, error) {
	return s.store.GetChannel(ctx, id)
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

// BroadcastMessage sends a message to all channel members except the sender.
func (s *Service) BroadcastMessage(ctx context.Context, channelID int64, fromAgent, body string, priority int, metadata string) ([]*messaging.Message, error) {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}

	// Verify sender is a member
	isMember, err := s.store.IsMember(ctx, channelID, fromAgent)
	if err != nil {
		return nil, fmt.Errorf("check membership: %w", err)
	}
	if !isMember {
		return nil, ErrNotChannelMember
	}

	// Get all members
	members, err := s.store.GetMembers(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("get members: %w", err)
	}

	var messages []*messaging.Message
	for _, m := range members {
		if m.AgentName == fromAgent {
			continue // skip sender
		}

		// Build metadata with channel info
		channelMeta := fmt.Sprintf(`{"channel_id":%d,"channel_name":%q}`, channelID, ch.Name)
		if metadata != "" {
			// Merge user metadata with channel metadata
			channelMeta = fmt.Sprintf(`{"channel_id":%d,"channel_name":%q,"user_metadata":%s}`, channelID, ch.Name, metadata)
		}

		opts := messaging.SendOptions{
			Subject:  fmt.Sprintf("channel:%s", ch.Name),
			Priority: priority,
			Metadata: channelMeta,
		}

		msg, err := s.msgService.SendMessage(ctx, fromAgent, m.AgentName, body, opts)
		if err != nil {
			s.logger.Error("failed to send channel message",
				"channel_id", channelID,
				"from", fromAgent,
				"to", m.AgentName,
				"error", err,
			)
			continue // best effort — don't fail the whole broadcast
		}
		messages = append(messages, msg)
	}

	s.logger.Info("channel message broadcast",
		"channel_id", channelID,
		"from", fromAgent,
		"recipients", len(messages),
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, fromAgent, "channel.broadcast", map[string]any{
			"channel_id":   channelID,
			"channel_name": ch.Name,
			"recipients":   len(messages),
		})
	}

	if messages == nil {
		messages = []*messaging.Message{}
	}
	return messages, nil
}

// GetMembers returns all members of a channel.
func (s *Service) GetMembers(ctx context.Context, channelID int64) ([]*Membership, error) {
	return s.store.GetMembers(ctx, channelID)
}
