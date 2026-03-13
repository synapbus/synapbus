// Package agents provides agent registry and authentication for SynapBus.
package agents

import (
	"encoding/json"
	"time"
)

// Agent status constants.
const (
	AgentStatusActive   = "active"
	AgentStatusInactive = "inactive"
)

// Agent represents a registered entity that can send/receive messages.
type Agent struct {
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	DisplayName    string          `json:"display_name"`
	Type           string          `json:"type"`
	Capabilities   json.RawMessage `json:"capabilities"`
	OwnerID        int64           `json:"owner_id"`
	APIKeyHash     string          `json:"-"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}
