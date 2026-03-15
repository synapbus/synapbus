// Package messaging provides core messaging types and services for SynapBus.
package messaging

import (
	"encoding/json"
	"time"
)

// Message status constants.
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusDone       = "done"
	StatusFailed     = "failed"
)

// Message represents a single message in the system.
type Message struct {
	ID             int64            `json:"id"`
	ConversationID int64            `json:"conversation_id"`
	FromAgent      string           `json:"from_agent"`
	ToAgent        string           `json:"to_agent,omitempty"`
	ChannelID      *int64           `json:"channel_id,omitempty"`
	ReplyTo        *int64           `json:"reply_to,omitempty"`
	Body           string           `json:"body"`
	Priority       int              `json:"priority"`
	Status         string           `json:"status"`
	Metadata       json.RawMessage  `json:"metadata"`
	ClaimedBy      string           `json:"claimed_by,omitempty"`
	ClaimedAt      *time.Time       `json:"claimed_at,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

// Conversation groups related messages into a thread.
type Conversation struct {
	ID        int64      `json:"id"`
	Subject   string     `json:"subject"`
	CreatedBy string     `json:"created_by"`
	ChannelID *int64     `json:"channel_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// DeadLetter represents an undeliverable message captured when an agent is deleted.
type DeadLetter struct {
	ID                int64           `json:"id"`
	OwnerID           int64           `json:"owner_id"`
	OriginalMessageID int64           `json:"original_message_id"`
	ToAgent           string          `json:"to_agent"`
	FromAgent         string          `json:"from_agent"`
	Body              string          `json:"body"`
	Subject           string          `json:"subject"`
	Priority          int             `json:"priority"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
	Acknowledged      bool            `json:"acknowledged"`
	CreatedAt         time.Time       `json:"created_at"`
}

// PaginatedMessages holds a page of messages with total count.
type PaginatedMessages struct {
	Messages []*Message `json:"messages"`
	Total    int        `json:"total"`
	Offset   int        `json:"offset"`
	Limit    int        `json:"limit"`
}

// InboxState tracks per-agent, per-conversation read position.
type InboxState struct {
	AgentName         string    `json:"agent_name"`
	ConversationID    int64     `json:"conversation_id"`
	LastReadMessageID int64     `json:"last_read_message_id"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// DMUnreadCount holds the unread DM count for a specific peer agent.
type DMUnreadCount struct {
	Agent         string `json:"agent"`
	UnreadCount   int    `json:"unread_count"`
	LastMessageID int64  `json:"last_message_id"`
}
