package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/smart-mcp-proxy/synapbus/internal/channels"
)

// SwarmToolRegistrar registers swarm-pattern MCP tools on the server.
type SwarmToolRegistrar struct {
	swarmService   *channels.SwarmService
	channelService *channels.Service
	logger         *slog.Logger
}

// NewSwarmToolRegistrar creates a new swarm tool registrar.
func NewSwarmToolRegistrar(swarmService *channels.SwarmService, channelService *channels.Service) *SwarmToolRegistrar {
	return &SwarmToolRegistrar{
		swarmService:   swarmService,
		channelService: channelService,
		logger:         slog.Default().With("component", "mcp-swarm-tools"),
	}
}

// RegisterAll registers all swarm tools on the MCP server.
func (str *SwarmToolRegistrar) RegisterAll(s *server.MCPServer) {
	s.AddTool(str.postTaskTool(), str.handlePostTask)
	s.AddTool(str.bidTaskTool(), str.handleBidTask)
	s.AddTool(str.acceptBidTool(), str.handleAcceptBid)
	s.AddTool(str.completeTaskTool(), str.handleCompleteTask)
	s.AddTool(str.listTasksTool(), str.handleListTasks)

	str.logger.Info("swarm MCP tools registered", "count", 5)
}

// --- Tool Definitions ---

func (str *SwarmToolRegistrar) postTaskTool() mcp.Tool {
	return mcp.NewTool("post_task",
		mcp.WithDescription("Post a task to an auction channel for agents to bid on"),
		mcp.WithString("channel_name", mcp.Description("Name of the auction channel"), mcp.Required()),
		mcp.WithString("title", mcp.Description("Task title"), mcp.Required()),
		mcp.WithString("description", mcp.Description("Task description")),
		mcp.WithString("requirements", mcp.Description("JSON object of task requirements")),
		mcp.WithString("deadline", mcp.Description("Task deadline in ISO 8601 format (e.g. 2026-03-13T15:00:00Z)")),
	)
}

func (str *SwarmToolRegistrar) bidTaskTool() mcp.Tool {
	return mcp.NewTool("bid_task",
		mcp.WithDescription("Submit a bid on an open task in an auction channel"),
		mcp.WithNumber("task_id", mcp.Description("ID of the task to bid on"), mcp.Required()),
		mcp.WithString("capabilities", mcp.Description("JSON object describing your relevant capabilities")),
		mcp.WithString("time_estimate", mcp.Description("Estimated time to complete the task")),
		mcp.WithString("message", mcp.Description("Message to the task poster explaining your bid")),
	)
}

func (str *SwarmToolRegistrar) acceptBidTool() mcp.Tool {
	return mcp.NewTool("accept_bid",
		mcp.WithDescription("Accept a bid on a task you posted, assigning the task to the bidding agent"),
		mcp.WithNumber("task_id", mcp.Description("ID of the task"), mcp.Required()),
		mcp.WithNumber("bid_id", mcp.Description("ID of the bid to accept"), mcp.Required()),
	)
}

func (str *SwarmToolRegistrar) completeTaskTool() mcp.Tool {
	return mcp.NewTool("complete_task",
		mcp.WithDescription("Mark a task as completed (only the assigned agent can do this)"),
		mcp.WithNumber("task_id", mcp.Description("ID of the task to complete"), mcp.Required()),
	)
}

func (str *SwarmToolRegistrar) listTasksTool() mcp.Tool {
	return mcp.NewTool("list_tasks",
		mcp.WithDescription("List tasks in an auction channel, optionally filtered by status"),
		mcp.WithString("channel_name", mcp.Description("Name of the auction channel"), mcp.Required()),
		mcp.WithString("status", mcp.Description("Filter by task status: open, assigned, completed, cancelled")),
	)
}

// --- Tool Handlers ---

func (str *SwarmToolRegistrar) handlePostTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	channelName := req.GetString("channel_name", "")
	if channelName == "" {
		return mcp.NewToolResultError("'channel_name' parameter is required"), nil
	}

	title := req.GetString("title", "")
	if title == "" {
		return mcp.NewToolResultError("'title' parameter is required"), nil
	}

	description := req.GetString("description", "")
	requirementsStr := req.GetString("requirements", "{}")
	deadlineStr := req.GetString("deadline", "")

	// Parse requirements JSON
	var requirements json.RawMessage
	if requirementsStr != "" {
		if !json.Valid([]byte(requirementsStr)) {
			return mcp.NewToolResultError("requirements must be valid JSON"), nil
		}
		requirements = json.RawMessage(requirementsStr)
	}

	// Parse deadline
	var deadline *time.Time
	if deadlineStr != "" {
		t, err := time.Parse(time.RFC3339, deadlineStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("deadline must be ISO 8601 format: %s", err)), nil
		}
		deadline = &t
	}

	// Resolve channel
	ch, err := str.channelService.GetChannelByName(ctx, channelName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("post_task failed: %s", err)), nil
	}

	task, err := str.swarmService.PostTask(ctx, ch.ID, agentName, title, description, requirements, deadline)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("post_task failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"task_id":     task.ID,
		"channel_id":  task.ChannelID,
		"title":       task.Title,
		"status":      task.Status,
		"posted_by":   task.PostedBy,
		"deadline":    task.Deadline,
		"created_at":  task.CreatedAt,
	})
}

func (str *SwarmToolRegistrar) handleBidTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	taskID, err := req.RequireInt("task_id")
	if err != nil {
		return mcp.NewToolResultError("'task_id' parameter is required"), nil
	}

	capabilitiesStr := req.GetString("capabilities", "{}")
	timeEstimate := req.GetString("time_estimate", "")
	message := req.GetString("message", "")

	var capabilities json.RawMessage
	if capabilitiesStr != "" {
		if !json.Valid([]byte(capabilitiesStr)) {
			return mcp.NewToolResultError("capabilities must be valid JSON"), nil
		}
		capabilities = json.RawMessage(capabilitiesStr)
	}

	bid, err := str.swarmService.BidOnTask(ctx, int64(taskID), agentName, capabilities, timeEstimate, message)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("bid_task failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"bid_id":        bid.ID,
		"task_id":       bid.TaskID,
		"agent_name":    bid.AgentName,
		"time_estimate": bid.TimeEstimate,
		"status":        bid.Status,
	})
}

func (str *SwarmToolRegistrar) handleAcceptBid(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	taskID, err := req.RequireInt("task_id")
	if err != nil {
		return mcp.NewToolResultError("'task_id' parameter is required"), nil
	}

	bidID, err := req.RequireInt("bid_id")
	if err != nil {
		return mcp.NewToolResultError("'bid_id' parameter is required"), nil
	}

	if err := str.swarmService.AcceptBid(ctx, int64(taskID), int64(bidID), agentName); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("accept_bid failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"task_id": taskID,
		"bid_id":  bidID,
		"status":  "accepted",
	})
}

func (str *SwarmToolRegistrar) handleCompleteTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	taskID, err := req.RequireInt("task_id")
	if err != nil {
		return mcp.NewToolResultError("'task_id' parameter is required"), nil
	}

	if err := str.swarmService.CompleteTask(ctx, int64(taskID), agentName); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("complete_task failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"task_id": taskID,
		"status":  "completed",
	})
}

func (str *SwarmToolRegistrar) handleListTasks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}
	_ = agentName // just verifying auth

	channelName := req.GetString("channel_name", "")
	if channelName == "" {
		return mcp.NewToolResultError("'channel_name' parameter is required"), nil
	}

	statusFilter := req.GetString("status", "")

	// Resolve channel
	ch, err := str.channelService.GetChannelByName(ctx, channelName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list_tasks failed: %s", err)), nil
	}

	// Verify channel is auction type
	if ch.Type != channels.TypeAuction {
		return mcp.NewToolResultError(fmt.Sprintf("list_tasks requires a channel of type 'auction', got '%s'", ch.Type)), nil
	}

	tasks, err := str.swarmService.ListTasks(ctx, ch.ID, statusFilter)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list_tasks failed: %s", err)), nil
	}

	result := make([]map[string]any, len(tasks))
	for i, task := range tasks {
		result[i] = map[string]any{
			"id":          task.ID,
			"title":       task.Title,
			"description": task.Description,
			"status":      task.Status,
			"posted_by":   task.PostedBy,
			"assigned_to": task.AssignedTo,
			"deadline":    task.Deadline,
			"created_at":  task.CreatedAt,
		}
	}

	return resultJSON(map[string]any{
		"tasks": result,
		"count": len(result),
	})
}
