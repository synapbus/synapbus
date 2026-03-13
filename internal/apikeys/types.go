// Package apikeys provides API key management with permissions for SynapBus.
package apikeys

import "time"

// APIKey represents a managed API key with permissions.
type APIKey struct {
	ID              int64       `json:"id"`
	UserID          int64       `json:"user_id"`
	AgentID         *int64      `json:"agent_id,omitempty"` // nil = user-level
	Name            string      `json:"name"`
	KeyPrefix       string      `json:"key_prefix"`
	Permissions     Permissions `json:"permissions"`
	AllowedChannels []string    `json:"allowed_channels"`
	ReadOnly        bool        `json:"read_only"`
	ExpiresAt       *time.Time  `json:"expires_at,omitempty"`
	LastUsedAt      *time.Time  `json:"last_used_at,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	RevokedAt       *time.Time  `json:"revoked_at,omitempty"`
}

// Permissions defines the access level for an API key.
type Permissions struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
	Admin bool `json:"admin"`
}

// CreateKeyRequest holds parameters for creating a new API key.
type CreateKeyRequest struct {
	UserID          int64       `json:"user_id"`
	AgentID         *int64      `json:"agent_id,omitempty"`
	Name            string      `json:"name"`
	Permissions     Permissions `json:"permissions"`
	AllowedChannels []string    `json:"allowed_channels"`
	ReadOnly        bool        `json:"read_only"`
	ExpiresAt       *time.Time  `json:"expires_at,omitempty"`
}
