package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

// MessagingService provides business logic for messaging operations.
type MessagingService struct {
	store  MessageStore
	tracer *trace.Tracer
	logger *slog.Logger
}

// NewMessagingService creates a new messaging service.
func NewMessagingService(store MessageStore, tracer *trace.Tracer) *MessagingService {
	return &MessagingService{
		store:  store,
		tracer: tracer,
		logger: slog.Default().With("component", "messaging"),
	}
}

// SendMessage creates a message, auto-creating conversations as needed.
func (s *MessagingService) SendMessage(ctx context.Context, from, to, body string, opts SendOptions) (*Message, error) {
	// Validate inputs
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("message body cannot be empty")
	}

	if to == "" && opts.ChannelID == nil {
		return nil, fmt.Errorf("either to_agent or channel_id must be specified")
	}

	if to != "" && opts.ChannelID != nil {
		return nil, fmt.Errorf("cannot specify both to_agent and channel_id")
	}

	priority := opts.Priority
	if priority == 0 {
		priority = 5
	}
	if priority < 1 || priority > 10 {
		return nil, fmt.Errorf("priority must be between 1 and 10")
	}

	var metadata json.RawMessage
	if opts.Metadata != "" {
		if !json.Valid([]byte(opts.Metadata)) {
			return nil, fmt.Errorf("metadata must be valid JSON")
		}
		metadata = json.RawMessage(opts.Metadata)
	} else {
		metadata = json.RawMessage("{}")
	}

	// For DMs, verify recipient agent exists
	if to != "" {
		exists, err := s.store.AgentExists(ctx, to)
		if err != nil {
			return nil, fmt.Errorf("check agent existence: %w", err)
		}
		if !exists {
			return nil, fmt.Errorf("agent not found: %s", to)
		}
	}

	// Find or create conversation
	var conv *Conversation
	var err error

	if opts.ConversationID != nil {
		conv, err = s.store.GetConversation(ctx, *opts.ConversationID)
		if err != nil {
			return nil, fmt.Errorf("get conversation: %w", err)
		}
	} else if opts.Subject != "" && to != "" {
		conv, err = s.store.FindConversation(ctx, opts.Subject, from, to)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("find conversation: %w", err)
		}
	}

	if conv == nil {
		conv = &Conversation{
			Subject:   opts.Subject,
			CreatedBy: from,
			ChannelID: opts.ChannelID,
		}
		if err := s.store.InsertConversation(ctx, conv); err != nil {
			return nil, fmt.Errorf("create conversation: %w", err)
		}
	}

	msg := &Message{
		ConversationID: conv.ID,
		FromAgent:      from,
		ToAgent:        to,
		ChannelID:      opts.ChannelID,
		ReplyTo:        opts.ReplyTo,
		Body:           body,
		Priority:       priority,
		Status:         StatusPending,
		Metadata:       metadata,
	}

	if err := s.store.InsertMessage(ctx, msg); err != nil {
		return nil, fmt.Errorf("insert message: %w", err)
	}

	s.logger.Info("message sent",
		"from", from,
		"to", to,
		"message_id", msg.ID,
		"conversation_id", conv.ID,
		"priority", priority,
	)

	// Record trace
	if s.tracer != nil {
		s.tracer.Record(ctx, from, "send_message", map[string]any{
			"message_id":      msg.ID,
			"conversation_id": conv.ID,
			"to":              to,
			"priority":        priority,
		})
	}

	return msg, nil
}

// ReadInbox returns messages for an agent and advances the read position.
func (s *MessagingService) ReadInbox(ctx context.Context, agentName string, opts ReadOptions) ([]*Message, error) {
	messages, err := s.store.GetInboxMessages(ctx, agentName, opts)
	if err != nil {
		return nil, fmt.Errorf("get inbox messages: %w", err)
	}

	// Advance inbox state for each conversation
	conversationMaxID := make(map[int64]int64)
	for _, msg := range messages {
		if msg.ID > conversationMaxID[msg.ConversationID] {
			conversationMaxID[msg.ConversationID] = msg.ID
		}
	}

	for convID, maxMsgID := range conversationMaxID {
		if err := s.store.UpdateInboxState(ctx, agentName, convID, maxMsgID); err != nil {
			s.logger.Error("failed to update inbox state",
				"agent", agentName,
				"conversation_id", convID,
				"error", err,
			)
		}
	}

	s.logger.Info("inbox read",
		"agent", agentName,
		"message_count", len(messages),
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "read_inbox", map[string]any{
			"message_count": len(messages),
			"include_read":  opts.IncludeRead,
		})
	}

	return messages, nil
}

// ClaimMessages atomically claims pending messages for processing.
func (s *MessagingService) ClaimMessages(ctx context.Context, agentName string, limit int) ([]*Message, error) {
	if limit <= 0 {
		limit = 10
	}

	messages, err := s.store.ClaimMessages(ctx, agentName, limit)
	if err != nil {
		return nil, fmt.Errorf("claim messages: %w", err)
	}

	s.logger.Info("messages claimed",
		"agent", agentName,
		"count", len(messages),
	)

	if s.tracer != nil {
		ids := make([]int64, len(messages))
		for i, m := range messages {
			ids[i] = m.ID
		}
		s.tracer.Record(ctx, agentName, "claim_messages", map[string]any{
			"claimed_count": len(messages),
			"message_ids":   ids,
		})
	}

	return messages, nil
}

// MarkDone marks a message as done. Only the claiming agent can do this.
func (s *MessagingService) MarkDone(ctx context.Context, messageID int64, agentName string) error {
	msg, err := s.store.GetMessageByID(ctx, messageID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("message not found: %d", messageID)
		}
		return fmt.Errorf("get message: %w", err)
	}

	if msg.Status != StatusProcessing {
		return fmt.Errorf("message %d is not in processing status (current: %s)", messageID, msg.Status)
	}

	if msg.ClaimedBy != agentName {
		return fmt.Errorf("message %d is not claimed by agent %s", messageID, agentName)
	}

	if err := s.store.UpdateMessageStatus(ctx, messageID, StatusDone, agentName, nil); err != nil {
		return fmt.Errorf("mark done: %w", err)
	}

	s.logger.Info("message marked done",
		"agent", agentName,
		"message_id", messageID,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "mark_done", map[string]any{
			"message_id": messageID,
			"status":     StatusDone,
		})
	}

	return nil
}

// MarkFailed marks a message as failed with a reason.
func (s *MessagingService) MarkFailed(ctx context.Context, messageID int64, agentName, reason string) error {
	msg, err := s.store.GetMessageByID(ctx, messageID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("message not found: %d", messageID)
		}
		return fmt.Errorf("get message: %w", err)
	}

	if msg.Status != StatusProcessing {
		return fmt.Errorf("message %d is not in processing status (current: %s)", messageID, msg.Status)
	}

	if msg.ClaimedBy != agentName {
		return fmt.Errorf("message %d is not claimed by agent %s", messageID, agentName)
	}

	// Merge failure reason into metadata
	var existingMeta map[string]any
	if err := json.Unmarshal(msg.Metadata, &existingMeta); err != nil {
		existingMeta = make(map[string]any)
	}
	existingMeta["error"] = reason
	mergedMeta, err := json.Marshal(existingMeta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	if err := s.store.UpdateMessageStatus(ctx, messageID, StatusFailed, agentName, mergedMeta); err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}

	s.logger.Info("message marked failed",
		"agent", agentName,
		"message_id", messageID,
		"reason", reason,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "mark_failed", map[string]any{
			"message_id": messageID,
			"status":     StatusFailed,
			"reason":     reason,
		})
	}

	return nil
}

// SearchMessages performs full-text search on messages.
func (s *MessagingService) SearchMessages(ctx context.Context, agentName, query string, opts SearchOptions) ([]*Message, error) {
	messages, err := s.store.SearchMessages(ctx, agentName, query, opts)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}

	s.logger.Info("messages searched",
		"agent", agentName,
		"query", query,
		"result_count", len(messages),
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "search_messages", map[string]any{
			"query":        query,
			"result_count": len(messages),
		})
	}

	return messages, nil
}

// GetMessageByID returns a single message by its ID.
func (s *MessagingService) GetMessageByID(ctx context.Context, id int64) (*Message, error) {
	msg, err := s.store.GetMessageByID(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("message not found: %d", id)
		}
		return nil, fmt.Errorf("get message: %w", err)
	}
	return msg, nil
}

// GetReplies returns all messages that are replies to the given message.
func (s *MessagingService) GetReplies(ctx context.Context, messageID int64) ([]*Message, error) {
	replies, err := s.store.GetReplies(ctx, messageID)
	if err != nil {
		return nil, fmt.Errorf("get replies: %w", err)
	}
	return replies, nil
}

// GetConversation returns a conversation and its messages.
func (s *MessagingService) GetConversation(ctx context.Context, id int64) (*Conversation, []*Message, error) {
	conv, err := s.store.GetConversation(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("get conversation: %w", err)
	}

	messages, err := s.store.GetConversationMessages(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("get conversation messages: %w", err)
	}

	return conv, messages, nil
}
