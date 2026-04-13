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

	// Reactive trigger methods
	UpdateTriggerConfig(ctx context.Context, name string, mode string, cooldown, budget, maxDepth int) error
	UpdateK8sImage(ctx context.Context, name, image, envJSON, preset string) error
	SetPendingWork(ctx context.Context, name string, pending bool) error
	ListReactiveAgents(ctx context.Context) ([]*Agent, error)
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

	// Default trigger values
	triggerMode := agent.TriggerMode
	if triggerMode == "" {
		triggerMode = TriggerModePassive
	}
	cooldown := agent.CooldownSeconds
	if cooldown == 0 {
		cooldown = 600
	}
	budget := agent.DailyTriggerBudget
	if budget == 0 {
		budget = 8
	}
	maxDepth := agent.MaxTriggerDepth
	if maxDepth == 0 {
		maxDepth = 5
	}
	preset := agent.K8sResourcePreset
	if preset == "" {
		preset = "default"
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
	agent.TriggerMode = triggerMode
	agent.CooldownSeconds = cooldown
	agent.DailyTriggerBudget = budget
	agent.MaxTriggerDepth = maxDepth
	agent.K8sResourcePreset = preset
	return nil
}

// UpdateTriggerConfig updates the reactive trigger configuration for an agent.
func (s *SQLiteAgentStore) UpdateTriggerConfig(ctx context.Context, name string, mode string, cooldown, budget, maxDepth int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET trigger_mode = ?, cooldown_seconds = ?, daily_trigger_budget = ?, max_trigger_depth = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE name = ? AND status = 'active'`,
		mode, cooldown, budget, maxDepth, name,
	)
	return err
}

// UpdateK8sImage updates the K8s container image and env config for an agent.
func (s *SQLiteAgentStore) UpdateK8sImage(ctx context.Context, name, image, envJSON, preset string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET k8s_image = ?, k8s_env_json = ?, k8s_resource_preset = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE name = ? AND status = 'active'`,
		image, envJSON, preset, name,
	)
	return err
}

// SetPendingWork sets the pending_work flag for an agent.
func (s *SQLiteAgentStore) SetPendingWork(ctx context.Context, name string, pending bool) error {
	val := 0
	if pending {
		val = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET pending_work = ? WHERE name = ? AND status = 'active'`,
		val, name,
	)
	return err
}

// ListReactiveAgents returns all active agents with trigger_mode='reactive'.
func (s *SQLiteAgentStore) ListReactiveAgents(ctx context.Context) ([]*Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		agentSelectSQL()+` WHERE status = 'active' AND trigger_mode = 'reactive' ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanAgents(rows)
}

// agentSelectSQL returns the base SELECT clause for agent queries.
func agentSelectSQL() string {
	return `SELECT id, name, display_name, type, capabilities, owner_id, api_key_hash, status, created_at, updated_at,
		 trigger_mode, cooldown_seconds, daily_trigger_budget, max_trigger_depth, k8s_image, k8s_env_json, k8s_resource_preset, pending_work,
		 harness_name, local_command, harness_config_json
		 FROM agents`
}

func (s *SQLiteAgentStore) GetAgentByName(ctx context.Context, name string) (*Agent, error) {
	return s.scanAgent(s.db.QueryRowContext(ctx,
		agentSelectSQL()+` WHERE name = ? AND status = 'active'`, name,
	))
}

func (s *SQLiteAgentStore) GetAgentByID(ctx context.Context, id int64) (*Agent, error) {
	return s.scanAgent(s.db.QueryRowContext(ctx,
		agentSelectSQL()+` WHERE id = ? AND status = 'active'`, id,
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
		agentSelectSQL()+` WHERE status = 'active' ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanAgents(rows)
}

func (s *SQLiteAgentStore) ListAllActiveAgents(ctx context.Context) ([]*Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		agentSelectSQL()+` WHERE status = 'active' AND type != 'human' ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanAgents(rows)
}

func (s *SQLiteAgentStore) ListAgentsByOwner(ctx context.Context, ownerID int64) ([]*Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		agentSelectSQL()+` WHERE owner_id = ? AND status = 'active' ORDER BY name`,
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
		agentSelectSQL()+` WHERE status = 'active' AND capabilities LIKE ? ORDER BY name`,
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
		agentSelectSQL()+` WHERE owner_id = ? AND type = 'human' AND status = 'active' LIMIT 1`, ownerID,
	))
}

func (s *SQLiteAgentStore) scanAgent(row *sql.Row) (*Agent, error) {
	var agent Agent
	var caps string
	var k8sImage, k8sEnvJSON sql.NullString
	var harnessName, localCommand, harnessConfigJSON sql.NullString
	var pendingWork int
	err := row.Scan(
		&agent.ID, &agent.Name, &agent.DisplayName, &agent.Type,
		&caps, &agent.OwnerID, &agent.APIKeyHash, &agent.Status,
		&agent.CreatedAt, &agent.UpdatedAt,
		&agent.TriggerMode, &agent.CooldownSeconds, &agent.DailyTriggerBudget,
		&agent.MaxTriggerDepth, &k8sImage, &k8sEnvJSON,
		&agent.K8sResourcePreset, &pendingWork,
		&harnessName, &localCommand, &harnessConfigJSON,
	)
	if err != nil {
		return nil, err
	}
	agent.Capabilities = json.RawMessage(caps)
	agent.K8sImage = k8sImage.String
	agent.K8sEnvJSON = k8sEnvJSON.String
	agent.PendingWork = pendingWork != 0
	agent.HarnessName = harnessName.String
	agent.LocalCommand = localCommand.String
	agent.HarnessConfigJSON = harnessConfigJSON.String
	return &agent, nil
}

func (s *SQLiteAgentStore) scanAgents(rows *sql.Rows) ([]*Agent, error) {
	var agents []*Agent
	for rows.Next() {
		var agent Agent
		var caps string
		var k8sImage, k8sEnvJSON sql.NullString
		var harnessName, localCommand, harnessConfigJSON sql.NullString
		var pendingWork int
		err := rows.Scan(
			&agent.ID, &agent.Name, &agent.DisplayName, &agent.Type,
			&caps, &agent.OwnerID, &agent.APIKeyHash, &agent.Status,
			&agent.CreatedAt, &agent.UpdatedAt,
			&agent.TriggerMode, &agent.CooldownSeconds, &agent.DailyTriggerBudget,
			&agent.MaxTriggerDepth, &k8sImage, &k8sEnvJSON,
			&agent.K8sResourcePreset, &pendingWork,
			&harnessName, &localCommand, &harnessConfigJSON,
		)
		if err != nil {
			return nil, err
		}
		agent.Capabilities = json.RawMessage(caps)
		agent.K8sImage = k8sImage.String
		agent.K8sEnvJSON = k8sEnvJSON.String
		agent.PendingWork = pendingWork != 0
		agent.HarnessName = harnessName.String
		agent.LocalCommand = localCommand.String
		agent.HarnessConfigJSON = harnessConfigJSON.String
		agents = append(agents, &agent)
	}
	if agents == nil {
		agents = []*Agent{}
	}
	return agents, rows.Err()
}
