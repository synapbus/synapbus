package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/search"
)

// ToolRegistrar registers all SynapBus MCP tools on the given server.
type ToolRegistrar struct {
	msgService     *messaging.MessagingService
	agentService   *agents.AgentService
	channelService *channels.Service
	searchService  *search.Service
	db             *sql.DB
	logger         *slog.Logger
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

// SetChannelService sets the channel service for my_status support.
func (tr *ToolRegistrar) SetChannelService(svc *channels.Service) {
	tr.channelService = svc
}

// SetDB sets the database handle for direct queries (e.g. owner name lookup).
func (tr *ToolRegistrar) SetDB(db *sql.DB) {
	tr.db = db
}

// RegisterAll registers all tools on the MCP server.
// Note: Agent management tools (register, update, deregister) are NOT exposed via MCP.
// Agents are managed exclusively through the Web UI. MCP is for messaging only.
func (tr *ToolRegistrar) RegisterAll(s *server.MCPServer) {
	s.AddTool(tr.myStatusTool(), tr.handleMyStatus)
	s.AddTool(tr.sendMessageTool(), tr.handleSendMessage)
	s.AddTool(tr.readInboxTool(), tr.handleReadInbox)
	s.AddTool(tr.claimMessagesTool(), tr.handleClaimMessages)
	s.AddTool(tr.markDoneTool(), tr.handleMarkDone)
	s.AddTool(tr.searchMessagesTool(), tr.handleSearchMessages)
	s.AddTool(tr.discoverAgentsTool(), tr.handleDiscoverAgents)

	tr.logger.Info("all MCP tools registered", "count", 7)
}

// --- Tool Definitions ---

func (tr *ToolRegistrar) sendMessageTool() mcp.Tool {
	return mcp.NewTool("send_message",
		mcp.WithDescription("Send a direct message to another agent. Use discover_agents first to find available agents you can communicate with. For channel messages, use send_channel_message instead."),
		mcp.WithString("to", mcp.Description("Name of the recipient agent (required for DMs, omit for channel messages)")),
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
		mcp.WithDescription("Check your message inbox for pending messages. Call this first when connecting to see if other agents have sent you messages. Returns unread/pending direct messages addressed to you."),
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
		mcp.WithDescription("Search for messages across your inbox and channels you are a member of. Supports full-text and semantic search (if configured). Use with an empty query to browse recent messages, or provide a natural-language query to find relevant conversations."),
		mcp.WithString("query", mcp.Description("Search query string — supports natural language for semantic search")),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default 10, max 100)")),
		mcp.WithNumber("min_priority", mcp.Description("Minimum priority filter (1-10)")),
		mcp.WithString("from_agent", mcp.Description("Filter by sender agent name")),
		mcp.WithString("status", mcp.Description("Filter by message status")),
		mcp.WithString("search_mode", mcp.Description("Search mode: 'auto' (default), 'semantic', or 'fulltext'")),
		mcp.WithBoolean("semantic", mcp.Description("Force semantic search (shorthand for search_mode='semantic')")),
	)
}

func (tr *ToolRegistrar) discoverAgentsTool() mcp.Tool {
	return mcp.NewTool("discover_agents",
		mcp.WithDescription("Discover other agents on the bus. Call this to find agents you can communicate with. Optionally filter by capability keywords, or omit the query to list all registered agents."),
		mcp.WithString("query", mcp.Description("Capability keyword to search for")),
	)
}

func (tr *ToolRegistrar) myStatusTool() mcp.Tool {
	return mcp.NewTool("my_status",
		mcp.WithDescription("Get your complete status overview — identity, pending messages, channel mentions, system notifications, and statistics. Call this first when connecting to SynapBus."),
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

	result, err := tr.msgService.ReadInbox(ctx, agentName, opts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read_inbox failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"messages": result.Messages,
		"count":    len(result.Messages),
		"total":    result.Total,
		"offset":   result.Offset,
		"limit":    result.Limit,
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

	searchResult, err := tr.msgService.SearchMessages(ctx, agentName, query, msgOpts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search_messages failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"messages":     searchResult.Messages,
		"count":        len(searchResult.Messages),
		"total":        searchResult.Total,
		"offset":       searchResult.Offset,
		"limit":        searchResult.Limit,
		"search_mode":  "fulltext",
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

	// Strip sensitive fields, exclude system agent
	result := make([]map[string]any, 0, len(agentsList))
	for _, a := range agentsList {
		if a.Name == "system" {
			continue
		}
		result = append(result, map[string]any{
			"name":         a.Name,
			"display_name": a.DisplayName,
			"type":         a.Type,
			"capabilities": a.Capabilities,
			"status":       a.Status,
		})
	}

	return resultJSON(map[string]any{
		"agents": result,
		"count":  len(result),
	})
}

func (tr *ToolRegistrar) handleMyStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	// 1. Get agent identity
	agent, err := tr.agentService.GetAgent(ctx, agentName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
	}

	// Resolve owner name from users table
	ownerName := ""
	if tr.db != nil {
		var username sql.NullString
		_ = tr.db.QueryRowContext(ctx,
			`SELECT username FROM users WHERE id = ?`, agent.OwnerID,
		).Scan(&username)
		if username.Valid {
			ownerName = username.String
		}
	}

	agentInfo := map[string]any{
		"name":         agent.Name,
		"display_name": agent.DisplayName,
		"type":         agent.Type,
		"owner":        ownerName,
	}

	// 2. Get pending DMs
	pendingDMs, err := tr.msgService.GetPendingDMs(ctx, agentName, 10)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
	}
	pendingDMCount, err := tr.msgService.GetPendingDMCount(ctx, agentName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
	}

	dmList := make([]map[string]any, len(pendingDMs))
	for i, msg := range pendingDMs {
		body := msg.Body
		if len(body) > 200 {
			body = body[:200] + "..."
		}
		entry := map[string]any{
			"id":         msg.ID,
			"from":       msg.FromAgent,
			"body":       body,
			"priority":   msg.Priority,
			"status":     msg.Status,
			"created_at": msg.CreatedAt,
		}
		// Include subject from conversation if available
		if msg.ConversationID > 0 {
			conv, _, _ := tr.msgService.GetConversation(ctx, msg.ConversationID)
			if conv != nil && conv.Subject != "" {
				entry["subject"] = conv.Subject
			}
		}
		dmList[i] = entry
	}

	// 3. Get channel mentions
	mentions, err := tr.msgService.GetRecentMentions(ctx, agentName, 10)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
	}

	mentionList := make([]map[string]any, len(mentions))
	for i, msg := range mentions {
		body := msg.Body
		if len(body) > 200 {
			body = body[:200] + "..."
		}
		entry := map[string]any{
			"id":         msg.ID,
			"from":       msg.FromAgent,
			"body":       body,
			"created_at": msg.CreatedAt,
		}
		// Try to extract channel name from metadata
		if len(msg.Metadata) > 0 {
			var meta map[string]any
			if json.Unmarshal(msg.Metadata, &meta) == nil {
				if chName, ok := meta["channel_name"].(string); ok {
					entry["channel"] = chName
				}
			}
		}
		mentionList[i] = entry
	}

	// 4. Get system notifications
	sysNotifs, err := tr.msgService.GetSystemNotifications(ctx, agentName, 5)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
	}

	sysNotifList := make([]map[string]any, len(sysNotifs))
	for i, msg := range sysNotifs {
		body := msg.Body
		if len(body) > 200 {
			body = body[:200] + "..."
		}
		sysNotifList[i] = map[string]any{
			"id":         msg.ID,
			"body":       body,
			"created_at": msg.CreatedAt,
		}
	}

	// 5. Get channel summaries
	var channelSummaries []channels.ChannelSummary
	if tr.channelService != nil {
		channelSummaries, err = tr.channelService.GetChannelSummaries(ctx, agentName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
		}
	}
	if channelSummaries == nil {
		channelSummaries = []channels.ChannelSummary{}
	}

	// 6. Build stats
	totalUnreadChannel := 0
	for _, cs := range channelSummaries {
		totalUnreadChannel += cs.UnreadCount
	}

	stats := map[string]any{
		"pending_dms":             pendingDMCount,
		"channels_joined":        len(channelSummaries),
		"unread_channel_messages": totalUnreadChannel,
		"system_notifications":    len(sysNotifs),
	}

	// 7. Build truncation instructions
	var instructionParts []string
	truncated := false
	if int64(len(pendingDMs)) < pendingDMCount {
		truncated = true
		instructionParts = append(instructionParts, fmt.Sprintf("Showing %d of %d pending messages. Use read_inbox to see all.", len(pendingDMs), pendingDMCount))
	}
	if len(mentions) >= 10 {
		truncated = true
		instructionParts = append(instructionParts, fmt.Sprintf("Showing %d mentions (may be more). Use search_messages to find all.", len(mentions)))
	}
	if len(sysNotifs) >= 5 {
		truncated = true
		instructionParts = append(instructionParts, fmt.Sprintf("Showing %d system notifications (may be more). Use read_inbox with from_agent='system' to see all.", len(sysNotifs)))
	}

	result := map[string]any{
		"agent":                    agentInfo,
		"direct_messages":          dmList,
		"direct_messages_total":    pendingDMCount,
		"mentions":                 mentionList,
		"mentions_total":           len(mentions),
		"system_notifications":       sysNotifList,
		"system_notifications_total": len(sysNotifs),
		"channels":                 channelSummaries,
		"stats":                    stats,
		"truncated":                truncated,
	}

	if len(instructionParts) > 0 {
		result["instructions"] = strings.Join(instructionParts, " ")
	}

	return resultJSON(result)
}

// resultJSON marshals data to a JSON text MCP result.
func resultJSON(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal response: %s", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
