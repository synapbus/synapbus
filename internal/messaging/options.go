package messaging

// SendOptions configures message sending behavior.
type SendOptions struct {
	Subject        string `json:"subject,omitempty"`
	Priority       int    `json:"priority,omitempty"`
	Metadata       string `json:"metadata,omitempty"`
	ChannelID      *int64 `json:"channel_id,omitempty"`
	ConversationID *int64 `json:"conversation_id,omitempty"`
	ReplyTo        *int64 `json:"reply_to,omitempty"`
}

// ReadOptions configures inbox reading behavior.
type ReadOptions struct {
	Status         string `json:"status,omitempty"`
	FromAgent      string `json:"from_agent,omitempty"`
	ConversationID *int64 `json:"conversation_id,omitempty"`
	MinPriority    int    `json:"min_priority,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Offset         int    `json:"offset,omitempty"`
	After          string `json:"after,omitempty"`
	Before         string `json:"before,omitempty"`
	IncludeRead    bool   `json:"include_read,omitempty"`
}

// SearchOptions configures message search behavior.
type SearchOptions struct {
	FromAgent   string `json:"from_agent,omitempty"`
	ToAgent     string `json:"to_agent,omitempty"`
	ChannelID   *int64 `json:"channel_id,omitempty"`
	MinPriority int    `json:"min_priority,omitempty"`
	Status      string `json:"status,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Offset      int    `json:"offset,omitempty"`
	After       string `json:"after,omitempty"`
	Before      string `json:"before,omitempty"`
	Channel     string `json:"channel,omitempty"`
}
