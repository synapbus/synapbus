package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/synapbus/synapbus/internal/dispatcher"
	"github.com/synapbus/synapbus/internal/trace"
)

// EmbeddingNotifier is called when messages are created or deleted so the
// embedding pipeline can enqueue them without a direct import dependency.
type EmbeddingNotifier interface {
	OnMessageCreated(ctx context.Context, messageID int64, body string)
}

// MessageListener is notified after every message is persisted.
// Implementations must not block — use goroutines for slow work.
type MessageListener interface {
	OnMessageSent(ctx context.Context, msg *Message)
}

// AttachmentLinker links attachment hashes to message IDs. This avoids
// importing the attachments package directly. Set via SetAttachmentLinker.
type AttachmentLinker interface {
	AttachToMessage(ctx context.Context, hash string, messageID int64) error
	GetByMessageID(ctx context.Context, messageID int64) ([]AttachmentInfo, error)
}

// ReactionEnricher loads reactions for messages. This avoids importing the
// reactions package directly. Set via SetReactionEnricher.
type ReactionEnricher interface {
	GetByMessageIDs(ctx context.Context, messageIDs []int64) (map[int64][]ReactionInfo, error)
}

// MessagingService provides business logic for messaging operations.
type MessagingService struct {
	store      MessageStore
	tracer     *trace.Tracer
	dispatcher dispatcher.EventDispatcher
	embeddings EmbeddingNotifier
	attLinker  AttachmentLinker
	rxEnricher ReactionEnricher
	listeners  []MessageListener
	logger     *slog.Logger
}

// NewMessagingService creates a new messaging service.
func NewMessagingService(store MessageStore, tracer *trace.Tracer) *MessagingService {
	return &MessagingService{
		store:  store,
		tracer: tracer,
		logger: slog.Default().With("component", "messaging"),
	}
}

// SetDispatcher sets the event dispatcher for webhook/K8s delivery.
func (s *MessagingService) SetDispatcher(d dispatcher.EventDispatcher) {
	s.dispatcher = d
}

// SetEmbeddingNotifier sets the embedding pipeline callback for new messages.
func (s *MessagingService) SetEmbeddingNotifier(n EmbeddingNotifier) {
	s.embeddings = n
}

// SetAttachmentLinker sets the attachment linker for message-attachment binding.
func (s *MessagingService) SetAttachmentLinker(l AttachmentLinker) {
	s.attLinker = l
}

// SetReactionEnricher sets the reaction enricher for message reaction loading.
func (s *MessagingService) SetReactionEnricher(e ReactionEnricher) {
	s.rxEnricher = e
}

// AddMessageListener registers a listener that is notified after message creation.
func (s *MessagingService) AddMessageListener(l MessageListener) {
	s.listeners = append(s.listeners, l)
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

	// Link attachments if provided.
	if s.attLinker != nil && len(opts.Attachments) > 0 {
		for _, hash := range opts.Attachments {
			if err := s.attLinker.AttachToMessage(ctx, hash, msg.ID); err != nil {
				s.logger.Error("failed to link attachment",
					"hash", hash,
					"message_id", msg.ID,
					"error", err,
				)
			}
		}
	}

	// Enqueue for embedding (async, best-effort)
	if s.embeddings != nil {
		s.embeddings.OnMessageCreated(ctx, msg.ID, msg.Body)
	}

	// Notify listeners (SSE, etc.)
	for _, l := range s.listeners {
		l.OnMessageSent(ctx, msg)
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

	// Dispatch event to webhooks/K8s (async, best-effort)
	if s.dispatcher != nil {
		eventType := "message.received"
		channel := ""
		if opts.ChannelID != nil {
			eventType = "channel.message"
			channel = fmt.Sprintf("%d", *opts.ChannelID)
		}

		event := dispatcher.MessageEvent{
			EventType: eventType,
			MessageID: msg.ID,
			FromAgent: from,
			ToAgent:   to,
			Channel:   channel,
			Body:      body,
			Priority:  priority,
			Metadata:  string(metadata),
		}

		// Extract @mentions for mention webhook events
		event.MentionedAgents = dispatcher.ExtractMentions(body)

		go func() {
			if err := s.dispatcher.Dispatch(context.Background(), event); err != nil {
				s.logger.Error("event dispatch failed",
					"message_id", msg.ID,
					"error", err,
				)
			}
		}()
	}

	return msg, nil
}

// ReadInbox returns messages for an agent. By default this is a pure peek and
// does not mutate any state. When opts.MarkRead is true the per-conversation
// read pointer is advanced past the returned messages — this is the explicit
// opt-in for the historical "fetch unread + mark read" worker-queue flow.
//
// Splitting the read-state side effect from the fetch fixes a race against the
// claim/process/done loop and the StalemateWorker: a destructive default meant
// that `read_inbox` calls between `claim_messages` and `mark_done` could move
// the read pointer past messages that the StalemateWorker still needed to
// reason about, producing inconsistent views across `read_inbox`, `my_status`,
// and the worker's reminder/escalation queries (see bug 30674).
func (s *MessagingService) ReadInbox(ctx context.Context, agentName string, opts ReadOptions) (*PaginatedMessages, error) {
	messages, err := s.store.GetInboxMessages(ctx, agentName, opts)
	if err != nil {
		return nil, fmt.Errorf("get inbox messages: %w", err)
	}

	total, err := s.store.CountInboxMessages(ctx, agentName, opts)
	if err != nil {
		return nil, fmt.Errorf("count inbox messages: %w", err)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	if opts.MarkRead {
		// Advance inbox state for each conversation, taking the max returned
		// message ID per conversation as the new read watermark.
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
	}

	s.logger.Info("inbox read",
		"agent", agentName,
		"message_count", len(messages),
		"mark_read", opts.MarkRead,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "read_inbox", map[string]any{
			"message_count": len(messages),
			"include_read":  opts.IncludeRead,
			"mark_read":     opts.MarkRead,
		})
	}

	return &PaginatedMessages{
		Messages: messages,
		Total:    total,
		Offset:   opts.Offset,
		Limit:    limit,
	}, nil
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
func (s *MessagingService) SearchMessages(ctx context.Context, agentName, query string, opts SearchOptions) (*PaginatedMessages, error) {
	messages, err := s.store.SearchMessages(ctx, agentName, query, opts)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}

	total, err := s.store.CountSearchMessages(ctx, agentName, query, opts)
	if err != nil {
		return nil, fmt.Errorf("count search messages: %w", err)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
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

	return &PaginatedMessages{
		Messages: messages,
		Total:    total,
		Offset:   opts.Offset,
		Limit:    limit,
	}, nil
}

// GetPendingDMCount returns the total count of pending DMs for an agent.
func (s *MessagingService) GetPendingDMCount(ctx context.Context, agentName string) (int64, error) {
	return s.store.CountPendingDMs(ctx, agentName)
}

// GetPendingDMs returns up to limit pending DMs for an agent, newest first.
func (s *MessagingService) GetPendingDMs(ctx context.Context, agentName string, limit int) ([]*Message, error) {
	return s.store.GetPendingDMs(ctx, agentName, limit)
}

// GetRecentMentions returns recent channel messages mentioning the agent.
func (s *MessagingService) GetRecentMentions(ctx context.Context, agentName string, limit int) ([]*Message, error) {
	return s.store.GetRecentMentions(ctx, agentName, limit)
}

// GetSystemNotifications returns pending messages from the "system" agent.
func (s *MessagingService) GetSystemNotifications(ctx context.Context, agentName string, limit int) ([]*Message, error) {
	return s.store.GetSystemNotifications(ctx, agentName, limit)
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

// GetChannelMessages returns messages posted to a channel.
func (s *MessagingService) GetChannelMessages(ctx context.Context, channelID int64, limit, offset int) (*PaginatedMessages, error) {
	messages, err := s.store.GetChannelMessages(ctx, channelID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get channel messages: %w", err)
	}

	total, err := s.store.CountChannelMessages(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("count channel messages: %w", err)
	}

	if limit <= 0 {
		limit = 50
	}

	return &PaginatedMessages{
		Messages: messages,
		Total:    total,
		Offset:   offset,
		Limit:    limit,
	}, nil
}

// GetDMMessages returns direct messages between owned agents and a peer agent.
func (s *MessagingService) GetDMMessages(ctx context.Context, ownedAgents []string, peerAgent string, limit int) ([]*Message, error) {
	messages, err := s.store.GetDMMessages(ctx, ownedAgents, peerAgent, limit)
	if err != nil {
		return nil, fmt.Errorf("get dm messages: %w", err)
	}
	return messages, nil
}

// GetDMPartners returns all DM conversation partners for the owned agents.
func (s *MessagingService) GetDMPartners(ctx context.Context, ownedAgents []string) ([]DMPartner, error) {
	return s.store.GetDMPartners(ctx, ownedAgents)
}

// GetDMUnreadCounts returns unread DM counts grouped by peer agent.
func (s *MessagingService) GetDMUnreadCounts(ctx context.Context, agentName string) ([]DMUnreadCount, error) {
	return s.store.GetDMUnreadCounts(ctx, agentName)
}

// GetLastReadForChannel returns the last_read_message_id for an agent in a channel.
func (s *MessagingService) GetLastReadForChannel(ctx context.Context, agentName string, channelID int64) (int64, error) {
	return s.store.GetLastReadForChannel(ctx, agentName, channelID)
}

// GetLastReadForDM returns the last_read_message_id for owned agents in a DM with a peer.
func (s *MessagingService) GetLastReadForDM(ctx context.Context, agentNames []string, peerAgent string) (int64, error) {
	return s.store.GetLastReadForDM(ctx, agentNames, peerAgent)
}

// UpdateInboxState updates the read position for an agent in a conversation.
func (s *MessagingService) UpdateInboxState(ctx context.Context, agentName string, conversationID int64, lastReadMsgID int64) error {
	return s.store.UpdateInboxState(ctx, agentName, conversationID, lastReadMsgID)
}

// GetConversationIDsForChannel returns conversation IDs in a channel with messages up to lastMessageID.
func (s *MessagingService) GetConversationIDsForChannel(ctx context.Context, channelID int64, lastMessageID int64) ([]int64, error) {
	return s.store.GetConversationIDsForChannel(ctx, channelID, lastMessageID)
}

// GetConversationIDsForDM returns conversation IDs for DMs between owned agents and a peer.
func (s *MessagingService) GetConversationIDsForDM(ctx context.Context, agentNames []string, peerAgent string, lastMessageID int64) ([]int64, error) {
	return s.store.GetConversationIDsForDM(ctx, agentNames, peerAgent, lastMessageID)
}

// EnrichMessages populates ReplyCount and Attachments on a slice of messages.
func (s *MessagingService) EnrichMessages(ctx context.Context, msgs []*Message) {
	if len(msgs) == 0 {
		return
	}

	ids := make([]int64, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}

	// Batch-load reply counts.
	counts, err := s.store.GetReplyCounts(ctx, ids)
	if err != nil {
		s.logger.Error("failed to load reply counts", "error", err)
	} else {
		for _, m := range msgs {
			if c, ok := counts[m.ID]; ok {
				m.ReplyCount = c
			}
		}
	}

	// Batch-load attachments.
	if s.attLinker != nil {
		for _, m := range msgs {
			atts, err := s.attLinker.GetByMessageID(ctx, m.ID)
			if err != nil {
				s.logger.Error("failed to load attachments", "message_id", m.ID, "error", err)
				continue
			}
			if len(atts) > 0 {
				m.Attachments = atts
			}
		}
	}

	// Batch-load reactions and derive workflow state.
	if s.rxEnricher != nil {
		rxMap, err := s.rxEnricher.GetByMessageIDs(ctx, ids)
		if err != nil {
			s.logger.Error("failed to load reactions", "error", err)
		} else {
			for _, m := range msgs {
				if rxs, ok := rxMap[m.ID]; ok && len(rxs) > 0 {
					m.Reactions = rxs
					// Derive workflow state from reactions
					highestPriority := 0
					priorities := map[string]int{
						"approve": 2, "in_progress": 3, "reject": 4, "done": 5, "published": 6,
					}
					states := map[string]string{
						"approve": "approved", "in_progress": "in_progress", "reject": "rejected", "done": "done", "published": "published",
					}
					for _, rx := range rxs {
						if p, ok := priorities[rx.Reaction]; ok && p > highestPriority {
							highestPriority = p
							m.WorkflowState = states[rx.Reaction]
						}
					}
				}
				// Default to "proposed" for channel messages with no reactions
				if m.WorkflowState == "" && m.ChannelID != nil {
					m.WorkflowState = "proposed"
				}
			}
		}
	}
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
