// goals_tools.go registers the spec-018 (dynamic agent spawning) MCP
// tools. All tools require an authenticated agent context (the caller
// is identified via extractAgentName). They wrap the goals/goal_tasks/
// secrets services so LLM agents can drive the full
// goal → task tree → spawn → verify flow through MCP.
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/goals"
	"github.com/synapbus/synapbus/internal/goaltasks"
	"github.com/synapbus/synapbus/internal/secrets"
)

// GoalsToolRegistrar wires the spec-018 primitives into MCP tool calls.
type GoalsToolRegistrar struct {
	goals     *goals.Service
	tasks     *goaltasks.Service
	agents    *agents.AgentService
	secrets   *secrets.Store
	db        *sql.DB
	logger    *slog.Logger
}

// NewGoalsToolRegistrar builds a registrar. All dependencies are
// optional — handlers return a clear error when they need something
// that wasn't wired.
func NewGoalsToolRegistrar(
	g *goals.Service,
	t *goaltasks.Service,
	a *agents.AgentService,
	sec *secrets.Store,
	db *sql.DB,
) *GoalsToolRegistrar {
	return &GoalsToolRegistrar{
		goals:   g,
		tasks:   t,
		agents:  a,
		secrets: sec,
		db:      db,
		logger:  slog.Default().With("component", "mcp-goals-tools"),
	}
}

// RegisterAllOnServer attaches create_goal, propose_task_tree,
// propose_agent, claim_task, request_resource, list_resources to the
// MCP server.
func (r *GoalsToolRegistrar) RegisterAllOnServer(s *server.MCPServer) {
	s.AddTool(r.createGoalTool(), r.handleCreateGoal)
	s.AddTool(r.proposeTaskTreeTool(), r.handleProposeTaskTree)
	s.AddTool(r.proposeAgentTool(), r.handleProposeAgent)
	s.AddTool(r.claimTaskTool(), r.handleClaimTask)
	s.AddTool(r.requestResourceTool(), r.handleRequestResource)
	s.AddTool(r.listResourcesTool(), r.handleListResources)
	r.logger.Info("spec-018 MCP tools registered", "count", 6)
}

// --- Tool Definitions ---

func (r *GoalsToolRegistrar) createGoalTool() mcplib.Tool {
	return mcplib.NewTool("create_goal",
		mcplib.WithDescription("Create a new top-level goal owned by the calling agent's human owner. Returns the goal id + backing channel id. The goal starts in draft status — call propose_task_tree to populate it and then use the execute tool to transition it to active."),
		mcplib.WithString("title", mcplib.Description("Short goal title"), mcplib.Required()),
		mcplib.WithString("description", mcplib.Description("Full goal brief (the 'blob' from the human)"), mcplib.Required()),
		mcplib.WithNumber("budget_dollars_cents", mcplib.Description("Optional dollar budget in cents (e.g. 5000 = $50)")),
		mcplib.WithNumber("budget_tokens", mcplib.Description("Optional token budget")),
		mcplib.WithNumber("max_spawn_depth", mcplib.Description("Max agent spawn depth (default 3)")),
	)
}

func (r *GoalsToolRegistrar) proposeTaskTreeTool() mcplib.Tool {
	return mcplib.NewTool("propose_task_tree",
		mcplib.WithDescription("Materialize a task tree under a goal. The tree field is a JSON TreeNode with {title, description, acceptance_criteria, billing_code, children[]}. Tasks are inserted in 'approved' status so specialists can claim them immediately."),
		mcplib.WithNumber("goal_id", mcplib.Description("Goal id from create_goal"), mcplib.Required()),
		mcplib.WithString("tree", mcplib.Description("JSON-encoded TreeNode"), mcplib.Required()),
	)
}

func (r *GoalsToolRegistrar) proposeAgentTool() mcplib.Tool {
	return mcplib.NewTool("propose_agent",
		mcplib.WithDescription("Propose creating a new specialist agent. Writes an agent_proposals row so a human can approve it via the #approvals channel. Returns the proposal id."),
		mcplib.WithString("name", mcplib.Description("Desired agent name"), mcplib.Required()),
		mcplib.WithString("display_name", mcplib.Description("Human-readable display name")),
		mcplib.WithString("system_prompt", mcplib.Description("System prompt for the spawned agent"), mcplib.Required()),
		mcplib.WithString("tool_scope", mcplib.Description("Comma-separated scope (e.g. 'messages:read,messages:send')")),
		mcplib.WithNumber("parent_task_id", mcplib.Description("The task this agent will work on")),
		mcplib.WithString("autonomy_tier", mcplib.Description("supervised | assisted | autonomous (default assisted)")),
	)
}

func (r *GoalsToolRegistrar) claimTaskTool() mcplib.Tool {
	return mcplib.NewTool("claim_task",
		mcplib.WithDescription("Atomically claim an approved task. Returns the claimed task or an error if another agent got it first."),
		mcplib.WithNumber("task_id", mcplib.Description("Task id to claim"), mcplib.Required()),
	)
}

func (r *GoalsToolRegistrar) requestResourceTool() mcplib.Tool {
	return mcplib.NewTool("request_resource",
		mcplib.WithDescription("Post a structured resource request to the #requests channel when a task needs a secret the agent doesn't currently have. The human reads it and provisions the secret via `synapbus secrets set`."),
		mcplib.WithString("resource_name", mcplib.Description("Uppercase env var name, e.g. BREVO_API_KEY"), mcplib.Required()),
		mcplib.WithString("reason", mcplib.Description("Why this resource is needed"), mcplib.Required()),
		mcplib.WithNumber("task_id", mcplib.Description("Task this request is attached to"), mcplib.Required()),
	)
}

func (r *GoalsToolRegistrar) listResourcesTool() mcplib.Tool {
	return mcplib.NewTool("list_resources",
		mcplib.WithDescription("List the names of secrets currently available to the calling agent. Returns names only — never values. Use this to check whether a secret has been provisioned after calling request_resource."),
	)
}

// --- Handlers ---

func (r *GoalsToolRegistrar) handleCreateGoal(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}
	if r.goals == nil {
		return mcplib.NewToolResultError("goals service not configured"), nil
	}
	title := req.GetString("title", "")
	description := req.GetString("description", "")
	if title == "" || description == "" {
		return mcplib.NewToolResultError("title and description are required"), nil
	}
	agent, err := r.agents.GetAgent(ctx, agentName)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve caller: %s", err)), nil
	}
	var ownerUsername string
	_ = r.db.QueryRowContext(ctx, `SELECT username FROM users WHERE id=?`, agent.OwnerID).Scan(&ownerUsername)

	var budgetTokens, budgetDollars *int64
	if v := req.GetInt("budget_tokens", 0); v > 0 {
		t := int64(v)
		budgetTokens = &t
	}
	if v := req.GetInt("budget_dollars_cents", 0); v > 0 {
		t := int64(v)
		budgetDollars = &t
	}
	maxDepth := req.GetInt("max_spawn_depth", 3)

	g, err := r.goals.CreateGoal(ctx, goals.CreateGoalInput{
		Title:              title,
		Description:        description,
		OwnerUserID:        agent.OwnerID,
		OwnerUsername:      ownerUsername,
		CoordinatorAgentID: &agent.ID,
		BudgetTokens:       budgetTokens,
		BudgetDollarsCents: budgetDollars,
		MaxSpawnDepth:      maxDepth,
	})
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("create_goal: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"goal_id":    g.ID,
		"slug":       g.Slug,
		"channel_id": g.ChannelID,
		"status":     g.Status,
	})
}

func (r *GoalsToolRegistrar) handleProposeTaskTree(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}
	if r.tasks == nil {
		return mcplib.NewToolResultError("goal_tasks service not configured"), nil
	}
	goalID := int64(req.GetInt("goal_id", 0))
	if goalID <= 0 {
		return mcplib.NewToolResultError("goal_id is required"), nil
	}
	treeJSON := req.GetString("tree", "")
	if treeJSON == "" {
		return mcplib.NewToolResultError("tree is required"), nil
	}
	var tree goaltasks.TreeNode
	if err := json.Unmarshal([]byte(treeJSON), &tree); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("parse tree: %s", err)), nil
	}

	agent, err := r.agents.GetAgent(ctx, agentName)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve caller: %s", err)), nil
	}

	rootID, allIDs, err := r.tasks.CreateTree(ctx, goaltasks.CreateTreeInput{
		GoalID:         goalID,
		CreatedByAgent: &agent.ID,
		Root:           tree,
		InitialStatus:  goaltasks.StatusApproved,
		DefaultBilling: "mcp",
	})
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("create tree: %s", err)), nil
	}

	// Also set the goal's root_task_id.
	_, _ = r.db.ExecContext(ctx, `UPDATE goals SET root_task_id=? WHERE id=?`, rootID, goalID)

	return resultJSON(map[string]any{
		"root_task_id": rootID,
		"task_count":   len(allIDs),
	})
}

func (r *GoalsToolRegistrar) handleProposeAgent(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}
	if r.db == nil {
		return mcplib.NewToolResultError("db not configured"), nil
	}
	name := req.GetString("name", "")
	if name == "" {
		return mcplib.NewToolResultError("name is required"), nil
	}
	systemPrompt := req.GetString("system_prompt", "")
	if systemPrompt == "" {
		return mcplib.NewToolResultError("system_prompt is required"), nil
	}
	toolScope := req.GetString("tool_scope", "")
	if toolScope == "" {
		toolScope = "[]"
	} else if !strings.HasPrefix(toolScope, "[") {
		// Accept comma-separated convenience form.
		parts := strings.Split(toolScope, ",")
		for i := range parts {
			parts[i] = `"` + strings.TrimSpace(parts[i]) + `"`
		}
		toolScope = "[" + strings.Join(parts, ",") + "]"
	}
	tier := req.GetString("autonomy_tier", "assisted")
	parentTaskID := int64(req.GetInt("parent_task_id", 0))
	if parentTaskID <= 0 {
		return mcplib.NewToolResultError("parent_task_id is required (proposals must attach to a task)"), nil
	}

	agent, err := r.agents.GetAgent(ctx, agentName)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve caller: %s", err)), nil
	}

	// Resolve goal_id from the parent task.
	var goalID int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT goal_id FROM goal_tasks WHERE id=?`, parentTaskID).Scan(&goalID); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve goal from task %d: %s", parentTaskID, err)), nil
	}

	res, err := r.db.ExecContext(ctx, `
		INSERT INTO agent_proposals (
			proposer_agent_id, goal_id, parent_task_id, proposed_name,
			proposed_model, proposed_system_prompt, proposed_tool_scope_json,
			proposed_autonomy_tier, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending')`,
		agent.ID, goalID, parentTaskID, name,
		"gemini-2.5-flash", systemPrompt, toolScope, tier)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("insert proposal: %s", err)), nil
	}
	id, _ := res.LastInsertId()

	return resultJSON(map[string]any{
		"proposal_id": id,
		"status":      "pending",
		"name":        name,
	})
}

func (r *GoalsToolRegistrar) handleClaimTask(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}
	if r.tasks == nil {
		return mcplib.NewToolResultError("goal_tasks service not configured"), nil
	}
	taskID := int64(req.GetInt("task_id", 0))
	if taskID <= 0 {
		return mcplib.NewToolResultError("task_id is required"), nil
	}
	agent, err := r.agents.GetAgent(ctx, agentName)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve caller: %s", err)), nil
	}
	if err := r.tasks.Claim(ctx, taskID, agent.ID, nil); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("claim_task: %s", err)), nil
	}
	_ = r.tasks.Transition(ctx, taskID, goaltasks.StatusInProgress, goaltasks.Extras{})
	return resultJSON(map[string]any{
		"task_id": taskID,
		"status":  "claimed",
	})
}

func (r *GoalsToolRegistrar) handleRequestResource(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}
	resourceName := strings.ToUpper(strings.TrimSpace(req.GetString("resource_name", "")))
	reason := req.GetString("reason", "")
	taskID := int64(req.GetInt("task_id", 0))
	if resourceName == "" || reason == "" || taskID <= 0 {
		return mcplib.NewToolResultError("resource_name, reason, and task_id are all required"), nil
	}
	agent, err := r.agents.GetAgent(ctx, agentName)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve caller: %s", err)), nil
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO resource_requests (requester_agent_id, task_id, resource_name, resource_type, reason, status)
		VALUES (?, ?, ?, 'env_var', ?, 'pending')`,
		agent.ID, taskID, resourceName, reason)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("insert resource_request: %s", err)), nil
	}
	return resultJSON(map[string]any{
		"status":       "pending",
		"resource":     resourceName,
		"next_action":  fmt.Sprintf("synapbus secrets set %s <value> --scope agent:%s", resourceName, agentName),
	})
}

func (r *GoalsToolRegistrar) handleListResources(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}
	if r.secrets == nil {
		return resultJSON(map[string]any{"resources": []any{}})
	}
	agent, err := r.agents.GetAgent(ctx, agentName)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve caller: %s", err)), nil
	}
	infos, err := r.secrets.List(ctx, []secrets.Scope{
		{Type: secrets.ScopeUser, ID: agent.OwnerID},
		{Type: secrets.ScopeAgent, ID: agent.ID},
	})
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("list secrets: %s", err)), nil
	}
	names := make([]string, 0, len(infos))
	for _, i := range infos {
		names = append(names, i.Name)
	}
	return resultJSON(map[string]any{
		"resources": names,
		"count":     len(names),
	})
}
