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

	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

// AgentService provides business logic for agent registry operations.
type AgentService struct {
	store  AgentStore
	tracer *trace.Tracer
	logger *slog.Logger
}

// NewAgentService creates a new agent service.
func NewAgentService(store AgentStore, tracer *trace.Tracer) *AgentService {
	return &AgentService{
		store:  store,
		tracer: tracer,
		logger: slog.Default().With("component", "agents"),
	}
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

	if err := s.store.DeactivateAgent(ctx, name); err != nil {
		return fmt.Errorf("deactivate agent: %w", err)
	}

	s.logger.Info("agent deregistered",
		"name", name,
		"owner_id", ownerID,
	)

	if s.tracer != nil {
		s.tracer.Record(ctx, name, "deregister_agent", map[string]any{
			"agent_id": agent.ID,
			"owner_id": ownerID,
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

// generateAPIKey creates a cryptographically random API key (32 bytes, hex encoded).
func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
