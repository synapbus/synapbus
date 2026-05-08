package messaging

// SendOptions configures message sending behavior.
type SendOptions struct {
	Subject        string   `json:"subject,omitempty"`
	Priority       int      `json:"priority,omitempty"`
	Metadata       string   `json:"metadata,omitempty"`
	ChannelID      *int64   `json:"channel_id,omitempty"`
	ConversationID *int64   `json:"conversation_id,omitempty"`
	ReplyTo        *int64   `json:"reply_to,omitempty"`
	Attachments    []string `json:"attachments,omitempty"` // attachment hashes to link
}

// ReadOptions configures inbox reading behavior.
//
// Read state is now an explicit, opt-in side effect. Callers that want the
// historical "fetch unread + mark them read" worker-queue behavior must set
// both IncludeRead=false (filter to unread) and MarkRead=true (advance the
// per-conversation read pointer). The default behavior is a pure peek that
// never mutates inbox state — this keeps read_inbox idempotent under retries
// and prevents it from racing with the claim/process/done loop and the
// StalemateWorker (see #bugs-synapbus message 30674).
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
	// MarkRead, when true, advances the per-conversation read pointer for the
	// returned messages. When false (the default) ReadInbox is a pure peek and
	// does not mutate any state.
	MarkRead bool `json:"mark_read,omitempty"`
}

// SearchOptions configures message search behavior.
type SearchOptions struct {
	FromAgent       string   `json:"from_agent,omitempty"`
	ToAgent         string   `json:"to_agent,omitempty"`
	ChannelID       *int64   `json:"channel_id,omitempty"`
	MinPriority     int      `json:"min_priority,omitempty"`
	Status          string   `json:"status,omitempty"`
	Limit           int      `json:"limit,omitempty"`
	Offset          int      `json:"offset,omitempty"`
	After           string   `json:"after,omitempty"`
	Before          string   `json:"before,omitempty"`
	Channel         string   `json:"channel,omitempty"`
	Channels        []string `json:"channels,omitempty"`         // include messages in these channels
	ExcludeChannels []string `json:"exclude_channels,omitempty"` // exclude messages in these channels
	Agents          []string `json:"agents,omitempty"`           // include messages from/to these agents
	ExcludeAgents   []string `json:"exclude_agents,omitempty"`   // exclude messages from/to these agents
}
