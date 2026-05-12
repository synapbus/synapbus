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

// RegisterAllOnServer attaches create_goal, propose_task_tree, claim_task,
// request_resource, list_resources, and complete_goal to the MCP server.
//
// propose_agent was removed when SynapBus moved to internal-only mode: it
// wrote a pending row to agent_proposals for human approval via #approvals,
// and that approval surface no longer exists. Agents are created via the
// admin CLI directly. See migration 027_remove_approval_noise.sql.
func (r *GoalsToolRegistrar) RegisterAllOnServer(s *server.MCPServer) {
	s.AddTool(r.createGoalTool(), r.handleCreateGoal)
	s.AddTool(r.proposeTaskTreeTool(), r.handleProposeTaskTree)
	s.AddTool(r.claimTaskTool(), r.handleClaimTask)
	s.AddTool(r.requestResourceTool(), r.handleRequestResource)
	s.AddTool(r.listResourcesTool(), r.handleListResources)
	s.AddTool(r.completeGoalTool(), r.handleCompleteGoal)
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

func (r *GoalsToolRegistrar) completeGoalTool() mcplib.Tool {
	return mcplib.NewTool("complete_goal",
		mcplib.WithDescription("Mark a goal as terminally done. The critic (or coordinator) calls this after the FINAL verdict: status=\"completed\" on success, \"stuck\" when the goal couldn't be finished, \"cancelled\" when the human aborted. Records a summary paragraph for /goals/<id> and links it to the DM that carried the FINAL message. Idempotent when called with the current status."),
		mcplib.WithNumber("goal_id", mcplib.Description("Goal id from create_goal"), mcplib.Required()),
		mcplib.WithString("status", mcplib.Description("Terminal status: completed | stuck | cancelled"), mcplib.Required()),
		mcplib.WithString("summary", mcplib.Description("One-paragraph human-readable completion summary (what was done, top findings, recommended next step). Goes straight into /goals/<id>."), mcplib.Required()),
		mcplib.WithNumber("completion_message_id", mcplib.Description("Optional id of the message that carried the verdict. When set, /goals/<id> can deep-link to the full findings JSON in that DM.")),
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

	// Auto-transition draft → active. The coordinator would otherwise
	// have to call a separate tool just to flip state, which is friction
	// we don't need — if there's a task tree, the goal is active. Ignore
	// the legal-transition error when the goal isn't in draft (operators
	// may have already moved it manually).
	_ = r.goals.TransitionStatus(ctx, goalID, goals.StatusActive)

	return resultJSON(map[string]any{
		"root_task_id": rootID,
		"task_count":   len(allIDs),
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

func (r *GoalsToolRegistrar) handleCompleteGoal(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}
	if r.goals == nil {
		return mcplib.NewToolResultError("goals service not configured"), nil
	}
	goalID := int64(req.GetInt("goal_id", 0))
	status := req.GetString("status", "")
	summary := req.GetString("summary", "")
	messageID := int64(req.GetInt("completion_message_id", 0))
	if goalID <= 0 || status == "" || summary == "" {
		return mcplib.NewToolResultError("goal_id, status, and summary are all required"), nil
	}
	switch status {
	case goals.StatusCompleted, goals.StatusStuck, goals.StatusCancelled:
		// ok
	default:
		return mcplib.NewToolResultError(fmt.Sprintf("invalid status %q — must be completed|stuck|cancelled", status)), nil
	}

	// Authorization: caller must own the goal (same human owner).
	// Coordinator + critic both run as ai agents owned by the same
	// user in the doc-gardener example; this check keeps one user's
	// agents from closing another user's goals.
	callerAgent, err := r.agents.GetAgent(ctx, agentName)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve caller: %s", err)), nil
	}
	g, err := r.goals.GetGoal(ctx, goalID)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("get goal: %s", err)), nil
	}
	if callerAgent.OwnerID != g.OwnerUserID {
		return mcplib.NewToolResultError("goal is owned by a different user — refusing to transition"), nil
	}

	if err := r.goals.Complete(ctx, goalID, status, summary, messageID); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("complete_goal: %s", err)), nil
	}

	r.logger.Info("goal completed via MCP",
		"goal_id", goalID,
		"status", status,
		"by_agent", agentName,
	)

	return resultJSON(map[string]any{
		"goal_id": goalID,
		"status":  status,
	})
}
