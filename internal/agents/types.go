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

// Trigger mode constants.
const (
	TriggerModePassive  = "passive"
	TriggerModeReactive = "reactive"
	TriggerModeDisabled = "disabled"
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

	// Reactive trigger fields
	TriggerMode        string `json:"trigger_mode"`
	CooldownSeconds    int    `json:"cooldown_seconds"`
	DailyTriggerBudget int    `json:"daily_trigger_budget"`
	MaxTriggerDepth    int    `json:"max_trigger_depth"`
	K8sImage           string `json:"k8s_image,omitempty"`
	K8sEnvJSON         string `json:"k8s_env_json,omitempty"`
	K8sResourcePreset  string `json:"k8s_resource_preset"`
	PendingWork        bool   `json:"pending_work"`

	// Harness-agnostic execution fields (migration 019).
	HarnessName       string `json:"harness_name,omitempty"`        // explicit backend; empty = auto-resolve
	LocalCommand      string `json:"local_command,omitempty"`       // JSON-encoded argv for subprocess backend
	HarnessConfigJSON string `json:"harness_config_json,omitempty"` // opaque per-backend config
}
