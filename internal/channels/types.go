// Package channels provides channel management for SynapBus.
package channels

import "time"

// ChannelType constants.
const (
	TypeStandard   = "standard"
	TypeBlackboard = "blackboard"
	TypeAuction    = "auction"
)

// MemberRole constants.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// InviteStatus constants.
const (
	InviteStatusPending  = "pending"
	InviteStatusAccepted = "accepted"
	InviteStatusDeclined = "declined"
)

// Channel represents a named group communication space.
type Channel struct {
	ID                     int64     `json:"id"`
	Name                   string    `json:"name"`
	Description            string    `json:"description"`
	Topic                  string    `json:"topic"`
	Type                   string    `json:"type"`
	IsPrivate              bool      `json:"is_private"`
	IsSystem               bool      `json:"is_system"`
	CreatedBy              string    `json:"created_by"`
	WorkflowEnabled        bool      `json:"workflow_enabled"`
	AutoApprove            bool      `json:"auto_approve"`
	StalemateRemindAfter   string    `json:"stalemate_remind_after"`
	StalemateEscalateAfter string    `json:"stalemate_escalate_after"`
	PublishThreshold       float64   `json:"publish_threshold"`
	ApproveThreshold       float64   `json:"approve_threshold"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// ChannelWithCount embeds Channel and adds a member count.
type ChannelWithCount struct {
	Channel
	MemberCount int `json:"member_count"`
}

// PaginatedChannels holds a page of channels with total count.
type PaginatedChannels struct {
	Channels []*ChannelWithCount `json:"channels"`
	Total    int                 `json:"total"`
	Offset   int                 `json:"offset"`
	Limit    int                 `json:"limit"`
}

// ListChannelsOptions configures channel listing behavior.
type ListChannelsOptions struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// PaginatedTasks holds a page of tasks with total count.
type PaginatedTasks struct {
	Tasks  []*Task `json:"tasks"`
	Total  int     `json:"total"`
	Offset int     `json:"offset"`
	Limit  int     `json:"limit"`
}

// Membership represents the relationship between an agent and a channel.
type Membership struct {
	ID        int64     `json:"id"`
	ChannelID int64     `json:"channel_id"`
	AgentName string    `json:"agent_name"`
	Role      string    `json:"role"`
	JoinedAt  time.Time `json:"joined_at"`
}

// ChannelInvite represents a pending invitation for an agent to join a private channel.
type ChannelInvite struct {
	ID        int64     `json:"id"`
	ChannelID int64     `json:"channel_id"`
	AgentName string    `json:"agent_name"`
	InvitedBy string    `json:"invited_by"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status"`
}

// CreateChannelRequest is the input for creating a channel.
type CreateChannelRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Topic       string `json:"topic"`
	Type        string `json:"type"`
	IsPrivate   bool   `json:"is_private"`
	IsSystem    bool   `json:"is_system"`
	CreatedBy   string `json:"created_by"`
}

// UpdateChannelRequest is the input for updating a channel.
type UpdateChannelRequest struct {
	Description *string `json:"description,omitempty"`
	Topic       *string `json:"topic,omitempty"`
}

// JoinChannelRequest is the input for joining a channel.
type JoinChannelRequest struct {
	ChannelID int64  `json:"channel_id"`
	AgentName string `json:"agent_name"`
}

// ChannelSettings holds workflow-related settings for a channel.
type ChannelSettings struct {
	WorkflowEnabled        bool    `json:"workflow_enabled"`
	AutoApprove            bool    `json:"auto_approve"`
	StalemateRemindAfter   string  `json:"stalemate_remind_after"`
	StalemateEscalateAfter string  `json:"stalemate_escalate_after"`
	PublishThreshold       float64 `json:"publish_threshold"`
	ApproveThreshold       float64 `json:"approve_threshold"`
}

// InviteRequest is the input for inviting an agent to a channel.
type InviteRequest struct {
	ChannelID    int64  `json:"channel_id"`
	AgentName    string `json:"agent_name"`
	InviterAgent string `json:"inviter_agent"`
}
