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

// InboxState tracks per-agent, per-conversation read position.
type InboxState struct {
	AgentName         string    `json:"agent_name"`
	ConversationID    int64     `json:"conversation_id"`
	LastReadMessageID int64     `json:"last_read_message_id"`
	UpdatedAt         time.Time `json:"updated_at"`
}
