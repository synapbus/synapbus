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

	// Dynamic-spawning trust fields (migration 023).
	ConfigHash       string     `json:"config_hash,omitempty"`
	ParentAgentID    *int64     `json:"parent_agent_id,omitempty"`
	SpawnDepth       int        `json:"spawn_depth"`
	SystemPrompt     string     `json:"system_prompt,omitempty"`
	AutonomyTier     string     `json:"autonomy_tier,omitempty"`
	ToolScopeJSON    string     `json:"tool_scope_json,omitempty"`
	QuarantinedAt    *time.Time `json:"quarantined_at,omitempty"`
	QuarantineReason string     `json:"quarantine_reason,omitempty"`
}
