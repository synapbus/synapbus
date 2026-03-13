package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/smart-mcp-proxy/synapbus/internal/agents"
	"github.com/smart-mcp-proxy/synapbus/internal/messaging"
	"github.com/smart-mcp-proxy/synapbus/internal/search"
)

// ToolRegistrar registers all SynapBus MCP tools on the given server.
type ToolRegistrar struct {
	msgService    *messaging.MessagingService
	agentService  *agents.AgentService
	searchService *search.Service
	logger        *slog.Logger
}

// NewToolRegistrar creates a new tool registrar.
func NewToolRegistrar(msgService *messaging.MessagingService, agentService *agents.AgentService) *ToolRegistrar {
	return &ToolRegistrar{
		msgService:   msgService,
		agentService: agentService,
		logger:       slog.Default().With("component", "mcp-tools"),
	}
}

// SetSearchService sets the search service for semantic search support.
func (tr *ToolRegistrar) SetSearchService(svc *search.Service) {
	tr.searchService = svc
}

// RegisterAll registers all tools on the MCP server.
func (tr *ToolRegistrar) RegisterAll(s *server.MCPServer) {
	s.AddTool(tr.sendMessageTool(), tr.handleSendMessage)
	s.AddTool(tr.readInboxTool(), tr.handleReadInbox)
	s.AddTool(tr.claimMessagesTool(), tr.handleClaimMessages)
	s.AddTool(tr.markDoneTool(), tr.handleMarkDone)
	s.AddTool(tr.searchMessagesTool(), tr.handleSearchMessages)
	s.AddTool(tr.registerAgentTool(), tr.handleRegisterAgent)
	s.AddTool(tr.discoverAgentsTool(), tr.handleDiscoverAgents)
	s.AddTool(tr.updateAgentTool(), tr.handleUpdateAgent)
	s.AddTool(tr.deregisterAgentTool(), tr.handleDeregisterAgent)

	tr.logger.Info("all MCP tools registered", "count", 9)
}

// --- Tool Definitions ---

func (tr *ToolRegistrar) sendMessageTool() mcp.Tool {
	return mcp.NewTool("send_message",
		mcp.WithDescription("Send a direct message to another agent or to a channel"),
		mcp.WithString("to", mcp.Description("Name of the recipient agent"), mcp.Required()),
		mcp.WithString("body", mcp.Description("Message body text"), mcp.Required()),
		mcp.WithString("subject", mcp.Description("Conversation subject (optional)")),
		mcp.WithNumber("priority", mcp.Description("Message priority (1-10, default 5)"), mcp.Min(1), mcp.Max(10)),
		mcp.WithString("metadata", mcp.Description("JSON metadata object (optional)")),
		mcp.WithNumber("channel_id", mcp.Description("Channel ID for channel messages (optional)")),
		mcp.WithNumber("reply_to", mcp.Description("ID of the message to reply to (optional, for threading)")),
	)
}

func (tr *ToolRegistrar) readInboxTool() mcp.Tool {
	return mcp.NewTool("read_inbox",
		mcp.WithDescription("Read messages from the authenticated agent's inbox"),
		mcp.WithNumber("limit", mcp.Description("Maximum number of messages to return (default 50)")),
		mcp.WithString("status_filter", mcp.Description("Filter by message status: pending, processing, done, failed")),
		mcp.WithBoolean("include_read", mcp.Description("Include previously read messages (default false)")),
		mcp.WithNumber("min_priority", mcp.Description("Minimum priority filter (1-10)")),
		mcp.WithString("from_agent", mcp.Description("Filter by sender agent name")),
	)
}

func (tr *ToolRegistrar) claimMessagesTool() mcp.Tool {
	return mcp.NewTool("claim_messages",
		mcp.WithDescription("Atomically claim pending messages for processing"),
		mcp.WithNumber("limit", mcp.Description("Maximum number of messages to claim (default 10)")),
	)
}

func (tr *ToolRegistrar) markDoneTool() mcp.Tool {
	return mcp.NewTool("mark_done",
		mcp.WithDescription("Mark a claimed message as done or failed"),
		mcp.WithNumber("message_id", mcp.Description("ID of the message to mark"), mcp.Required()),
		mcp.WithString("status", mcp.Description("New status: 'done' or 'failed' (default 'done')")),
		mcp.WithString("reason", mcp.Description("Failure reason (only for status='failed')")),
	)
}

func (tr *ToolRegistrar) searchMessagesTool() mcp.Tool {
	return mcp.NewTool("search_messages",
		mcp.WithDescription("Search messages using semantic search (if configured) or full-text search. Returns messages ranked by relevance."),
		mcp.WithString("query", mcp.Description("Search query string — supports natural language for semantic search")),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default 10, max 100)")),
		mcp.WithNumber("min_priority", mcp.Description("Minimum priority filter (1-10)")),
		mcp.WithString("from_agent", mcp.Description("Filter by sender agent name")),
		mcp.WithString("status", mcp.Description("Filter by message status")),
		mcp.WithString("search_mode", mcp.Description("Search mode: 'auto' (default), 'semantic', or 'fulltext'")),
		mcp.WithBoolean("semantic", mcp.Description("Force semantic search (shorthand for search_mode='semantic')")),
	)
}

func (tr *ToolRegistrar) registerAgentTool() mcp.Tool {
	return mcp.NewTool("register_agent",
		mcp.WithDescription("Register a new agent and receive an API key"),
		mcp.WithString("name", mcp.Description("Unique agent name"), mcp.Required()),
		mcp.WithString("display_name", mcp.Description("Human-readable display name")),
		mcp.WithString("type", mcp.Description("Agent type: 'ai' or 'human' (default 'ai')")),
		mcp.WithString("capabilities", mcp.Description("JSON capabilities object")),
	)
}

func (tr *ToolRegistrar) discoverAgentsTool() mcp.Tool {
	return mcp.NewTool("discover_agents",
		mcp.WithDescription("Discover agents by capability keywords"),
		mcp.WithString("query", mcp.Description("Capability keyword to search for")),
	)
}

func (tr *ToolRegistrar) updateAgentTool() mcp.Tool {
	return mcp.NewTool("update_agent",
		mcp.WithDescription("Update the authenticated agent's display name or capabilities"),
		mcp.WithString("display_name", mcp.Description("New display name")),
		mcp.WithString("capabilities", mcp.Description("New JSON capabilities object")),
	)
}

func (tr *ToolRegistrar) deregisterAgentTool() mcp.Tool {
	return mcp.NewTool("deregister_agent",
		mcp.WithDescription("Deregister the authenticated agent (soft delete)"),
	)
}

// --- Tool Handlers ---

func (tr *ToolRegistrar) handleSendMessage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	to := req.GetString("to", "")
	body := req.GetString("body", "")
	subject := req.GetString("subject", "")
	priority := req.GetInt("priority", 5)
	metadataStr := req.GetString("metadata", "")

	if to == "" {
		return mcp.NewToolResultError("'to' parameter is required"), nil
	}
	if body == "" {
		return mcp.NewToolResultError("'body' parameter is required"), nil
	}

	var channelID *int64
	if cid := req.GetInt("channel_id", 0); cid > 0 {
		v := int64(cid)
		channelID = &v
	}

	var replyTo *int64
	if rtID := req.GetInt("reply_to", 0); rtID > 0 {
		v := int64(rtID)
		replyTo = &v
	}

	opts := messaging.SendOptions{
		Subject:   subject,
		Priority:  priority,
		Metadata:  metadataStr,
		ChannelID: channelID,
		ReplyTo:   replyTo,
	}

	msg, err := tr.msgService.SendMessage(ctx, agentName, to, body, opts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("send_message failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"message_id":      msg.ID,
		"conversation_id": msg.ConversationID,
		"status":          msg.Status,
	})
}

func (tr *ToolRegistrar) handleReadInbox(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	opts := messaging.ReadOptions{
		Limit:       req.GetInt("limit", 50),
		Status:      req.GetString("status_filter", ""),
		MinPriority: req.GetInt("min_priority", 0),
		FromAgent:   req.GetString("from_agent", ""),
	}

	// Handle include_read boolean
	args := req.GetArguments()
	if v, ok := args["include_read"]; ok {
		if b, ok := v.(bool); ok {
			opts.IncludeRead = b
		}
	}

	messages, err := tr.msgService.ReadInbox(ctx, agentName, opts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read_inbox failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"messages": messages,
		"count":    len(messages),
	})
}

func (tr *ToolRegistrar) handleClaimMessages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	limit := req.GetInt("limit", 10)

	messages, err := tr.msgService.ClaimMessages(ctx, agentName, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("claim_messages failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"messages": messages,
		"count":    len(messages),
	})
}

func (tr *ToolRegistrar) handleMarkDone(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	messageID, err := req.RequireInt("message_id")
	if err != nil {
		return mcp.NewToolResultError("'message_id' parameter is required"), nil
	}

	status := req.GetString("status", "done")
	reason := req.GetString("reason", "")

	switch status {
	case "done":
		if err := tr.msgService.MarkDone(ctx, int64(messageID), agentName); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("mark_done failed: %s", err)), nil
		}
	case "failed":
		if err := tr.msgService.MarkFailed(ctx, int64(messageID), agentName, reason); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("mark_failed failed: %s", err)), nil
		}
	default:
		return mcp.NewToolResultError("status must be 'done' or 'failed'"), nil
	}

	return resultJSON(map[string]any{
		"message_id": messageID,
		"status":     status,
	})
}

func (tr *ToolRegistrar) handleSearchMessages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	query := req.GetString("query", "")

	// If search service is available, use it for unified search
	if tr.searchService != nil {
		searchMode := req.GetString("search_mode", "auto")

		// Handle boolean "semantic" shorthand
		args := req.GetArguments()
		if v, ok := args["semantic"]; ok {
			if b, ok := v.(bool); ok && b {
				searchMode = "semantic"
			}
		}

		opts := search.SearchOptions{
			Query:       query,
			Mode:        searchMode,
			Limit:       req.GetInt("limit", 10),
			FromAgent:   req.GetString("from_agent", ""),
			MinPriority: req.GetInt("min_priority", 0),
		}

		resp, err := tr.searchService.Search(ctx, agentName, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search_messages failed: %s", err)), nil
		}

		// Format results
		resultMsgs := make([]map[string]any, len(resp.Results))
		for i, r := range resp.Results {
			entry := map[string]any{
				"message":    r.Message,
				"match_type": r.MatchType,
			}
			if r.SimilarityScore > 0 {
				entry["similarity_score"] = r.SimilarityScore
			}
			if r.RelevanceScore > 0 {
				entry["relevance_score"] = r.RelevanceScore
			}
			resultMsgs[i] = entry
		}

		result := map[string]any{
			"results":       resultMsgs,
			"count":         resp.TotalResults,
			"search_mode":   resp.SearchMode,
		}
		if resp.Warning != "" {
			result["warning"] = resp.Warning
		}

		return resultJSON(result)
	}

	// Fallback: use messaging service directly (no search service configured)
	msgOpts := messaging.SearchOptions{
		Limit:       req.GetInt("limit", 20),
		MinPriority: req.GetInt("min_priority", 0),
		FromAgent:   req.GetString("from_agent", ""),
		Status:      req.GetString("status", ""),
	}

	messages, err := tr.msgService.SearchMessages(ctx, agentName, query, msgOpts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search_messages failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"messages":     messages,
		"count":        len(messages),
		"search_mode":  "fulltext",
	})
}

func (tr *ToolRegistrar) handleRegisterAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// register_agent does not require authentication
	name := req.GetString("name", "")
	if name == "" {
		return mcp.NewToolResultError("'name' parameter is required"), nil
	}

	displayName := req.GetString("display_name", name)
	agentType := req.GetString("type", "ai")
	capsStr := req.GetString("capabilities", "{}")

	var caps json.RawMessage
	if capsStr != "" {
		if !json.Valid([]byte(capsStr)) {
			return mcp.NewToolResultError("capabilities must be valid JSON"), nil
		}
		caps = json.RawMessage(capsStr)
	}

	// Use owner_id=1 as default (first user). In production, this would come from auth.
	agent, apiKey, err := tr.agentService.Register(ctx, name, displayName, agentType, caps, 1)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("register_agent failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"agent_id":   agent.ID,
		"name":       agent.Name,
		"api_key":    apiKey,
		"created_at": agent.CreatedAt,
	})
}

func (tr *ToolRegistrar) handleDiscoverAgents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	query := req.GetString("query", "")
	_ = agentName // just verifying auth

	agentsList, err := tr.agentService.DiscoverAgents(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("discover_agents failed: %s", err)), nil
	}

	// Strip sensitive fields
	result := make([]map[string]any, len(agentsList))
	for i, a := range agentsList {
		result[i] = map[string]any{
			"name":         a.Name,
			"display_name": a.DisplayName,
			"type":         a.Type,
			"capabilities": a.Capabilities,
			"status":       a.Status,
		}
	}

	return resultJSON(map[string]any{
		"agents": result,
		"count":  len(result),
	})
}

func (tr *ToolRegistrar) handleUpdateAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	displayName := req.GetString("display_name", "")
	capsStr := req.GetString("capabilities", "")

	var caps json.RawMessage
	if capsStr != "" {
		if !json.Valid([]byte(capsStr)) {
			return mcp.NewToolResultError("capabilities must be valid JSON"), nil
		}
		caps = json.RawMessage(capsStr)
	}

	agent, err := tr.agentService.UpdateAgent(ctx, agentName, displayName, caps)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("update_agent failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"name":         agent.Name,
		"display_name": agent.DisplayName,
		"capabilities": agent.Capabilities,
	})
}

func (tr *ToolRegistrar) handleDeregisterAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	// Get the agent to find owner_id
	agent, err := tr.agentService.GetAgent(ctx, agentName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("deregister_agent failed: %s", err)), nil
	}

	if err := tr.agentService.Deregister(ctx, agentName, agent.OwnerID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("deregister_agent failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"name":   agentName,
		"status": "deregistered",
	})
}

// resultJSON marshals data to a JSON text MCP result.
func resultJSON(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal response: %s", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
