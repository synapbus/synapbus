package agents

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	"golang.org/x/crypto/bcrypt"

	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/trace"
)

// AgentService provides business logic for agent registry operations.
type AgentService struct {
	store           AgentStore
	tracer          *trace.Tracer
	deadLetterStore *messaging.DeadLetterStore
	logger          *slog.Logger
}

// NewAgentService creates a new agent service.
func NewAgentService(store AgentStore, tracer *trace.Tracer) *AgentService {
	return &AgentService{
		store:  store,
		tracer: tracer,
		logger: slog.Default().With("component", "agents"),
	}
}

// SetDeadLetterStore sets the dead letter store for capturing messages on agent deregistration.
func (s *AgentService) SetDeadLetterStore(dls *messaging.DeadLetterStore) {
	s.deadLetterStore = dls
}

// Register creates a new agent with a generated API key.
// Returns the agent and the raw API key (shown once).
func (s *AgentService) Register(ctx context.Context, name, displayName, agentType string, capabilities json.RawMessage, ownerID int64) (*Agent, string, error) {
	if name == "" {
		return nil, "", fmt.Errorf("agent name is required")
	}

	if agentType == "" {
		agentType = "ai"
	}

	if agentType != "ai" && agentType != "human" {
		return nil, "", fmt.Errorf("agent type must be 'ai' or 'human'")
	}

	if capabilities == nil || len(capabilities) == 0 {
		capabilities = json.RawMessage("{}")
	}

	if !json.Valid(capabilities) {
		return nil, "", fmt.Errorf("capabilities must be valid JSON")
	}

	// Generate API key
	apiKey, err := generateAPIKey()
	if err != nil {
		return nil, "", fmt.Errorf("generate API key: %w", err)
	}

	// Hash the key with bcrypt
	hash, err := bcrypt.GenerateFromPassword([]byte(apiKey), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash API key: %w", err)
	}

	agent := &Agent{
		Name:         name,
		DisplayName:  displayName,
		Type:         agentType,
		Capabilities: capabilities,
		OwnerID:      ownerID,
		APIKeyHash:   string(hash),
	}

	if err := s.store.CreateAgent(ctx, agent); err != nil {
		return nil, "", fmt.Errorf("create agent: %w", err)
	}

	s.logger.Info("agent registered",
		"name", name,
		"type", agentType,
		"owner_id", ownerID,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, name, "register_agent", map[string]any{
			"agent_id":   agent.ID,
			"agent_type": agentType,
			"owner_id":   ownerID,
		})
	}

	return agent, apiKey, nil
}

// Authenticate verifies an API key and returns the associated agent.
func (s *AgentService) Authenticate(ctx context.Context, apiKey string) (*Agent, error) {
	// Get all active agents and check the key against each.
	// In production, this would use a prefix-based lookup or cache.
	agents, err := s.store.ListActiveAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}

	for _, agent := range agents {
		if err := bcrypt.CompareHashAndPassword([]byte(agent.APIKeyHash), []byte(apiKey)); err == nil {
			return agent, nil
		}
	}

	return nil, fmt.Errorf("invalid API key")
}

// GetAgent returns an agent by name.
func (s *AgentService) GetAgent(ctx context.Context, name string) (*Agent, error) {
	agent, err := s.store.GetAgentByName(ctx, name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("agent not found: %s", name)
		}
		return nil, err
	}
	return agent, nil
}

// GetAgentByID returns an agent by ID.
func (s *AgentService) GetAgentByID(ctx context.Context, id int64) (*Agent, error) {
	agent, err := s.store.GetAgentByID(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("agent not found: %d", id)
		}
		return nil, err
	}
	return agent, nil
}

// UpdateAgent updates an agent's display name and/or capabilities.
func (s *AgentService) UpdateAgent(ctx context.Context, name string, displayName string, capabilities json.RawMessage) (*Agent, error) {
	agent, err := s.store.GetAgentByName(ctx, name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("agent not found: %s", name)
		}
		return nil, err
	}

	if displayName != "" {
		agent.DisplayName = displayName
	}

	if capabilities != nil && len(capabilities) > 0 {
		if !json.Valid(capabilities) {
			return nil, fmt.Errorf("capabilities must be valid JSON")
		}
		agent.Capabilities = capabilities
	}

	if err := s.store.UpdateAgent(ctx, agent); err != nil {
		return nil, fmt.Errorf("update agent: %w", err)
	}

	s.logger.Info("agent updated",
		"name", name,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, name, "update_agent", map[string]any{
			"agent_id": agent.ID,
		})
	}

	return agent, nil
}

// Deregister soft-deletes an agent. Only the owner can deregister.
// Pending/processing messages are captured as dead letters before deactivation.
func (s *AgentService) Deregister(ctx context.Context, name string, ownerID int64) error {
	agent, err := s.store.GetAgentByName(ctx, name)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("agent not found: %s", name)
		}
		return err
	}

	if agent.OwnerID != ownerID {
		return fmt.Errorf("only the agent's owner can deregister it")
	}

	// Capture pending/processing messages as dead letters before deactivation
	var deadLetterCount int
	if s.deadLetterStore != nil {
		captured, err := s.deadLetterStore.CaptureDeadLetters(ctx, agent.OwnerID, name)
		if err != nil {
			s.logger.Warn("failed to capture dead letters",
				"agent", name,
				"error", err,
			)
		} else {
			deadLetterCount = captured
		}
	}

	if err := s.store.DeactivateAgent(ctx, name); err != nil {
		return fmt.Errorf("deactivate agent: %w", err)
	}

	s.logger.Info("agent deregistered",
		"name", name,
		"owner_id", ownerID,
		"dead_letters_captured", deadLetterCount,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, name, "deregister_agent", map[string]any{
			"agent_id":              agent.ID,
			"owner_id":              ownerID,
			"dead_letters_captured": deadLetterCount,
		})
	}

	return nil
}

// DiscoverAgents searches for agents by capability keywords.
func (s *AgentService) DiscoverAgents(ctx context.Context, query string) ([]*Agent, error) {
	if query == "" {
		return s.store.ListActiveAgents(ctx)
	}
	return s.store.SearchAgentsByCapability(ctx, query)
}

// ListAgents returns all agents owned by the given owner.
func (s *AgentService) ListAgents(ctx context.Context, ownerID int64) ([]*Agent, error) {
	return s.store.ListAgentsByOwner(ctx, ownerID)
}

// ListAllActiveAgents returns all active non-human agents across all owners.
// Used for the A2A Agent Card discovery endpoint.
func (s *AgentService) ListAllActiveAgents(ctx context.Context) ([]*Agent, error) {
	return s.store.ListAllActiveAgents(ctx)
}

// RevokeKey generates a new API key for an agent. Only the owner can do this.
// Returns the agent and the new raw API key (shown once).
func (s *AgentService) RevokeKey(ctx context.Context, name string, ownerID int64) (*Agent, string, error) {
	agent, err := s.store.GetAgentByName(ctx, name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, "", fmt.Errorf("agent not found: %s", name)
		}
		return nil, "", err
	}

	if agent.OwnerID != ownerID {
		return nil, "", fmt.Errorf("only the agent's owner can revoke its API key")
	}

	// Generate new API key
	apiKey, err := generateAPIKey()
	if err != nil {
		return nil, "", fmt.Errorf("generate API key: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(apiKey), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash API key: %w", err)
	}

	agent.APIKeyHash = string(hash)
	if err := s.store.UpdateAgent(ctx, agent); err != nil {
		return nil, "", fmt.Errorf("update agent: %w", err)
	}

	s.logger.Info("agent API key revoked",
		"name", name,
		"owner_id", ownerID,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, name, "revoke_api_key", map[string]any{
			"agent_id": agent.ID,
			"owner_id": ownerID,
		})
	}

	return agent, apiKey, nil
}

// GetHumanAgentForUser returns the human-type agent for a given owner.
// Returns nil, nil if no human agent exists for this user.
func (s *AgentService) GetHumanAgentForUser(ctx context.Context, ownerID int64) (*Agent, error) {
	agent, err := s.store.GetHumanAgentByOwner(ctx, ownerID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get human agent: %w", err)
	}
	return agent, nil
}

// EnsureHumanAgent creates a human-type agent matching the username if one doesn't already exist.
// This is called on login to ensure every user has a human identity for sending messages from the UI.
func (s *AgentService) EnsureHumanAgent(ctx context.Context, username, displayName string, ownerID int64) (*Agent, error) {
	// Check if user already has a human agent
	agents, err := s.store.ListAgentsByOwner(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}

	for _, a := range agents {
		if a.Type == "human" && a.Status == "active" {
			return a, nil
		}
	}

	// Create human agent matching the username
	if displayName == "" {
		displayName = username
	}

	// Try username first, then username-human if taken
	agentName := username
	agent, _, err := s.Register(ctx, agentName, displayName, "human", nil, ownerID)
	if err != nil {
		// Name collision — try with suffix
		agentName = username + "-human"
		agent, _, err = s.Register(ctx, agentName, displayName, "human", nil, ownerID)
		if err != nil {
			s.logger.Warn("could not auto-create human agent", "username", username, "error", err)
			return nil, nil
		}
	}

	s.logger.Info("auto-created human agent for user",
		"username", username,
		"agent_name", agent.Name,
		"owner_id", ownerID,
	)
	return agent, nil
}

// EnsureSystemAgent creates the "system" agent if it doesn't already exist.
// The system agent is used for retention warnings and other system notifications.
func (s *AgentService) EnsureSystemAgent(ctx context.Context, ownerID int64) (*Agent, error) {
	agent, err := s.store.GetAgentByName(ctx, "system")
	if err == nil && agent != nil && agent.Status == "active" {
		return agent, nil
	}

	agent, _, err = s.Register(ctx, "system", "System", "ai", json.RawMessage(`{"role":"system-notifications"}`), ownerID)
	if err != nil {
		// May already exist from a concurrent call
		agent, err2 := s.store.GetAgentByName(ctx, "system")
		if err2 == nil && agent != nil {
			return agent, nil
		}
		return nil, fmt.Errorf("create system agent: %w", err)
	}

	s.logger.Info("created system agent", "owner_id", ownerID)
	return agent, nil
}

// GetAgentWithOwner returns agent details along with the owner's display name.
func (s *AgentService) GetAgentWithOwner(ctx context.Context, name string) (*Agent, string, error) {
	agent, err := s.store.GetAgentByName(ctx, name)
	if err != nil {
		return nil, "", fmt.Errorf("get agent: %w", err)
	}
	return agent, "", nil // Owner name resolved by caller with access to user store
}

// generateAPIKey creates a cryptographically random API key (32 bytes, hex encoded).
func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
