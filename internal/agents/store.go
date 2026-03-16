package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// AgentStore defines the storage interface for agent operations.
type AgentStore interface {
	CreateAgent(ctx context.Context, agent *Agent) error
	GetAgentByName(ctx context.Context, name string) (*Agent, error)
	GetAgentByID(ctx context.Context, id int64) (*Agent, error)
	UpdateAgent(ctx context.Context, agent *Agent) error
	DeactivateAgent(ctx context.Context, name string) error
	ListActiveAgents(ctx context.Context) ([]*Agent, error)
	ListAllActiveAgents(ctx context.Context) ([]*Agent, error)
	ListAgentsByOwner(ctx context.Context, ownerID int64) ([]*Agent, error)
	SearchAgentsByCapability(ctx context.Context, query string) ([]*Agent, error)
	GetHumanAgentByOwner(ctx context.Context, ownerID int64) (*Agent, error)
}

// SQLiteAgentStore implements AgentStore using SQLite.
type SQLiteAgentStore struct {
	db *sql.DB
}

// NewSQLiteAgentStore creates a new SQLite-backed agent store.
func NewSQLiteAgentStore(db *sql.DB) *SQLiteAgentStore {
	return &SQLiteAgentStore{db: db}
}

func (s *SQLiteAgentStore) CreateAgent(ctx context.Context, agent *Agent) error {
	caps := string(agent.Capabilities)
	if caps == "" {
		caps = "{}"
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		agent.Name, agent.DisplayName, agent.Type, caps, agent.OwnerID, agent.APIKeyHash, AgentStatusActive,
	)
	if err != nil {
		return fmt.Errorf("insert agent: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get agent id: %w", err)
	}
	agent.ID = id
	agent.Status = AgentStatusActive
	return nil
}

func (s *SQLiteAgentStore) GetAgentByName(ctx context.Context, name string) (*Agent, error) {
	return s.scanAgent(s.db.QueryRowContext(ctx,
		`SELECT id, name, display_name, type, capabilities, owner_id, api_key_hash, status, created_at, updated_at
		 FROM agents WHERE name = ? AND status = 'active'`, name,
	))
}

func (s *SQLiteAgentStore) GetAgentByID(ctx context.Context, id int64) (*Agent, error) {
	return s.scanAgent(s.db.QueryRowContext(ctx,
		`SELECT id, name, display_name, type, capabilities, owner_id, api_key_hash, status, created_at, updated_at
		 FROM agents WHERE id = ? AND status = 'active'`, id,
	))
}

func (s *SQLiteAgentStore) UpdateAgent(ctx context.Context, agent *Agent) error {
	caps := string(agent.Capabilities)
	if caps == "" {
		caps = "{}"
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET display_name = ?, capabilities = ?, api_key_hash = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE name = ? AND status = 'active'`,
		agent.DisplayName, caps, agent.APIKeyHash, agent.Name,
	)
	return err
}

func (s *SQLiteAgentStore) DeactivateAgent(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE agents SET status = 'inactive', updated_at = CURRENT_TIMESTAMP
		 WHERE name = ? AND status = 'active'`,
		name,
	)
	if err != nil {
		return fmt.Errorf("deactivate agent: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("agent not found: %s", name)
	}
	return nil
}

func (s *SQLiteAgentStore) ListActiveAgents(ctx context.Context) ([]*Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, display_name, type, capabilities, owner_id, api_key_hash, status, created_at, updated_at
		 FROM agents WHERE status = 'active' ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanAgents(rows)
}

func (s *SQLiteAgentStore) ListAllActiveAgents(ctx context.Context) ([]*Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, display_name, type, capabilities, owner_id, api_key_hash, status, created_at, updated_at
		 FROM agents WHERE status = 'active' AND type != 'human' ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanAgents(rows)
}

func (s *SQLiteAgentStore) ListAgentsByOwner(ctx context.Context, ownerID int64) ([]*Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, display_name, type, capabilities, owner_id, api_key_hash, status, created_at, updated_at
		 FROM agents WHERE owner_id = ? AND status = 'active' ORDER BY name`,
		ownerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanAgents(rows)
}

func (s *SQLiteAgentStore) SearchAgentsByCapability(ctx context.Context, query string) ([]*Agent, error) {
	// Simple LIKE search on the capabilities JSON field
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, display_name, type, capabilities, owner_id, api_key_hash, status, created_at, updated_at
		 FROM agents WHERE status = 'active' AND capabilities LIKE ? ORDER BY name`,
		"%"+query+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanAgents(rows)
}

func (s *SQLiteAgentStore) GetHumanAgentByOwner(ctx context.Context, ownerID int64) (*Agent, error) {
	return s.scanAgent(s.db.QueryRowContext(ctx,
		`SELECT id, name, display_name, type, capabilities, owner_id, api_key_hash, status, created_at, updated_at
		 FROM agents WHERE owner_id = ? AND type = 'human' AND status = 'active' LIMIT 1`, ownerID,
	))
}

func (s *SQLiteAgentStore) scanAgent(row *sql.Row) (*Agent, error) {
	var agent Agent
	var caps string
	err := row.Scan(
		&agent.ID, &agent.Name, &agent.DisplayName, &agent.Type,
		&caps, &agent.OwnerID, &agent.APIKeyHash, &agent.Status,
		&agent.CreatedAt, &agent.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	agent.Capabilities = json.RawMessage(caps)
	return &agent, nil
}

func (s *SQLiteAgentStore) scanAgents(rows *sql.Rows) ([]*Agent, error) {
	var agents []*Agent
	for rows.Next() {
		var agent Agent
		var caps string
		err := rows.Scan(
			&agent.ID, &agent.Name, &agent.DisplayName, &agent.Type,
			&caps, &agent.OwnerID, &agent.APIKeyHash, &agent.Status,
			&agent.CreatedAt, &agent.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		agent.Capabilities = json.RawMessage(caps)
		agents = append(agents, &agent)
	}
	if agents == nil {
		agents = []*Agent{}
	}
	return agents, rows.Err()
}
