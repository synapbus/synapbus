package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/synapbus/synapbus/internal/actions"
	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/attachments"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/jsruntime"
	"github.com/synapbus/synapbus/internal/agentquery"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/reactions"
	"github.com/synapbus/synapbus/internal/search"
	"github.com/synapbus/synapbus/internal/trust"
)

// HybridToolRegistrar registers the 4 hybrid MCP tools.
type HybridToolRegistrar struct {
	msgService        *messaging.MessagingService
	agentService      *agents.AgentService
	channelService    *channels.Service
	swarmService      *channels.SwarmService
	attachmentService *attachments.Service
	searchService     *search.Service
	reactionService   *reactions.Service
	trustService      *trust.Service
	jsPool            *jsruntime.Pool
	actionRegistry    *actions.Registry
	actionIndex       *actions.Index
	db                *sql.DB
	queryExecutor     *agentquery.Executor
	logger            *slog.Logger
}

// SetQueryExecutor sets the SQL query executor for all agent bridges.
func (h *HybridToolRegistrar) SetQueryExecutor(exec *agentquery.Executor) {
	h.queryExecutor = exec
}

// NewHybridToolRegistrar creates a new hybrid tool registrar.
func NewHybridToolRegistrar(
	msgService *messaging.MessagingService,
	agentService *agents.AgentService,
	channelService *channels.Service,
	swarmService *channels.SwarmService,
	attachmentService *attachments.Service,
	searchService *search.Service,
	reactionService *reactions.Service,
	trustService *trust.Service,
	jsPool *jsruntime.Pool,
	actionRegistry *actions.Registry,
	actionIndex *actions.Index,
	db *sql.DB,
) *HybridToolRegistrar {
	return &HybridToolRegistrar{
		msgService:        msgService,
		agentService:      agentService,
		channelService:    channelService,
		swarmService:      swarmService,
		attachmentService: attachmentService,
		searchService:     searchService,
		reactionService:   reactionService,
		trustService:      trustService,
		jsPool:            jsPool,
		actionRegistry:    actionRegistry,
		actionIndex:       actionIndex,
		db:                db,
		logger:            slog.Default().With("component", "mcp-hybrid-tools"),
	}
}

// RegisterAllOnServer registers all hybrid tools on an mcp-go MCPServer.
func (h *HybridToolRegistrar) RegisterAllOnServer(s *server.MCPServer) {
	s.AddTool(h.myStatusTool(), h.handleMyStatus)
	s.AddTool(h.sendMessageTool(), h.handleSendMessage)
	s.AddTool(h.searchTool(), h.handleSearch)
	s.AddTool(h.executeTool(), h.handleExecute)
	s.AddTool(h.getRepliesTool(), h.handleGetReplies)

	h.logger.Info("hybrid MCP tools registered", "count", 5)
}

// --- Tool Definitions ---

func (h *HybridToolRegistrar) myStatusTool() mcplib.Tool {
	return mcplib.NewTool("my_status",
		mcplib.WithDescription("Get your complete status overview — identity, pending messages, channel mentions, system notifications, and statistics. Call this first when connecting to SynapBus."),
	)
}

func (h *HybridToolRegistrar) sendMessageTool() mcplib.Tool {
	return mcplib.NewTool("send_message",
		mcplib.WithDescription("Send a message to another agent (DM) or to a channel. Supports attachments — upload files first via the execute tool, then pass the returned hashes here. Specify exactly one of 'to' (agent name for DM) or 'channel' (channel name or numeric ID)."),
		mcplib.WithString("to", mcplib.Description("Recipient agent name for direct messages")),
		mcplib.WithString("channel", mcplib.Description("Channel name or numeric ID for channel messages")),
		mcplib.WithString("body", mcplib.Description("Message body text"), mcplib.Required()),
		mcplib.WithString("subject", mcplib.Description("Conversation subject (optional)")),
		mcplib.WithNumber("priority", mcplib.Description("Message priority (1-10, default 5)"), mcplib.Min(1), mcplib.Max(10)),
		mcplib.WithString("metadata", mcplib.Description("JSON metadata object (optional)")),
		mcplib.WithNumber("reply_to", mcplib.Description("ID of the parent message to reply to. Creates a threaded reply. Always use reply_to when responding to a message that is itself a thread reply, to keep conversations organized.")),
		mcplib.WithString("attachments", mcplib.Description("Comma-separated list of attachment hashes to link to this message. Upload attachments first using the upload_attachment action via the execute tool.")),
	)
}

func (h *HybridToolRegistrar) searchTool() mcplib.Tool {
	return mcplib.NewTool("search",
		mcplib.WithDescription("Search for available actions you can perform via the 'execute' tool. Returns action names, descriptions, parameters, and examples. Use an empty query to browse all actions, or describe what you want to do."),
		mcplib.WithString("query", mcplib.Description("What you want to do — e.g. 'read messages', 'create channel', 'upload file'")),
		mcplib.WithNumber("limit", mcplib.Description("Maximum results to return (default 5, max 20)")),
	)
}

func (h *HybridToolRegistrar) executeTool() mcplib.Tool {
	return mcplib.NewTool("execute",
		mcplib.WithDescription("Execute code that calls SynapBus actions. Use call(actionName, args) to invoke actions discovered via the 'search' tool. Multiple sequential calls are supported."),
		mcplib.WithString("code", mcplib.Description("Code containing call() expressions. Example: call('read_inbox', { limit: 5 })"), mcplib.Required()),
		mcplib.WithNumber("timeout", mcplib.Description("Execution timeout in milliseconds (default 120000, max 300000)")),
	)
}

func (h *HybridToolRegistrar) getRepliesTool() mcplib.Tool {
	return mcplib.NewTool("get_replies",
		mcplib.WithDescription("Get all replies (thread messages) for a given message. Use this to read thread conversations, check for edits or follow-up comments on a message."),
		mcplib.WithNumber("message_id", mcplib.Description("ID of the parent message to get replies for"), mcplib.Required()),
	)
}

// --- Tool Handlers ---

func (h *HybridToolRegistrar) handleMyStatus(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}

	// 1. Get agent identity.
	agent, err := h.agentService.GetAgent(ctx, agentName)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
	}

	// Resolve owner name.
	ownerName := ""
	if h.db != nil {
		var username sql.NullString
		_ = h.db.QueryRowContext(ctx,
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

	// 2. Get pending DMs.
	pendingDMs, err := h.msgService.GetPendingDMs(ctx, agentName, 10)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
	}
	pendingDMCount, err := h.msgService.GetPendingDMCount(ctx, agentName)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
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
		if msg.ConversationID > 0 {
			conv, _, _ := h.msgService.GetConversation(ctx, msg.ConversationID)
			if conv != nil && conv.Subject != "" {
				entry["subject"] = conv.Subject
			}
		}
		dmList[i] = entry
	}

	// 3. Get channel mentions.
	mentions, err := h.msgService.GetRecentMentions(ctx, agentName, 10)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
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

	// 4. Get system notifications.
	sysNotifs, err := h.msgService.GetSystemNotifications(ctx, agentName, 5)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
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

	// 5. Get channel summaries.
	var channelSummaries []channels.ChannelSummary
	if h.channelService != nil {
		channelSummaries, err = h.channelService.GetChannelSummaries(ctx, agentName)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("my_status failed: %s", err)), nil
		}
	}
	if channelSummaries == nil {
		channelSummaries = []channels.ChannelSummary{}
	}

	// 6. Build stats.
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

	// 7. Build truncation instructions.
	var instructionParts []string
	truncated := false
	if int64(len(pendingDMs)) < pendingDMCount {
		truncated = true
		instructionParts = append(instructionParts, fmt.Sprintf("Showing %d of %d pending messages. Use execute tool with call('read_inbox', {}) to see all.", len(pendingDMs), pendingDMCount))
	}
	if len(mentions) >= 10 {
		truncated = true
		instructionParts = append(instructionParts, fmt.Sprintf("Showing %d mentions (may be more). Use execute tool with call('search_messages', {}) to find all.", len(mentions)))
	}
	if len(sysNotifs) >= 5 {
		truncated = true
		instructionParts = append(instructionParts, fmt.Sprintf("Showing %d system notifications (may be more). Use execute tool with call('read_inbox', { from_agent: 'system' }) to see all.", len(sysNotifs)))
	}

	// 8. Add usage instructions for the hybrid tools.
	usageInstructions := "Use 'search' tool with a query to discover available actions. " +
		"Use 'execute' tool with call(action, args) to perform any action. " +
		"Use 'send_message' tool directly for sending messages (DMs or channel)."

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
		"usage":                    usageInstructions,
	}

	if len(instructionParts) > 0 {
		result["instructions"] = strings.Join(instructionParts, " ")
	}

	return resultJSON(result)
}

func (h *HybridToolRegistrar) handleSendMessage(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}

	to := req.GetString("to", "")
	channel := req.GetString("channel", "")
	body := req.GetString("body", "")
	subject := req.GetString("subject", "")
	priority := req.GetInt("priority", 5)
	metadataStr := req.GetString("metadata", "")

	if body == "" {
		return mcplib.NewToolResultError("'body' parameter is required"), nil
	}

	// Validate mutually exclusive: exactly one of to/channel.
	if to == "" && channel == "" {
		return mcplib.NewToolResultError("either 'to' (agent name) or 'channel' (channel name/ID) is required"), nil
	}
	if to != "" && channel != "" {
		return mcplib.NewToolResultError("specify exactly one of 'to' (for DM) or 'channel' (for channel message), not both"), nil
	}

	var replyTo *int64
	if rtID := req.GetInt("reply_to", 0); rtID > 0 {
		v := int64(rtID)
		replyTo = &v
	}

	var attachmentHashes []string
	if attStr := req.GetString("attachments", ""); attStr != "" {
		for _, h := range strings.Split(attStr, ",") {
			h = strings.TrimSpace(h)
			if h != "" {
				attachmentHashes = append(attachmentHashes, h)
			}
		}
	}

	// Channel message path.
	if channel != "" {
		if h.channelService == nil {
			return mcplib.NewToolResultError("channel service not available"), nil
		}

		// Resolve channel by name or numeric ID.
		channelID, err := h.resolveChannel(ctx, channel)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("send_message to channel failed: %s", err)), nil
		}

		messages, err := h.channelService.BroadcastMessage(ctx, channelID, agentName, body, priority, metadataStr, replyTo, attachmentHashes)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("send_message to channel failed: %s", err)), nil
		}

		var messageID int64
		if len(messages) > 0 {
			messageID = messages[0].ID
		}

		result := map[string]any{
			"channel_id": channelID,
			"message_id": messageID,
			"status":     "sent",
		}

		// Enrich channel messages with attachment info.
		if len(messages) > 0 && len(attachmentHashes) > 0 {
			h.msgService.EnrichMessages(ctx, messages)
			if len(messages[0].Attachments) > 0 {
				result["attachments"] = messages[0].Attachments
			}
		}

		return resultJSON(result)
	}

	// DM path.
	opts := messaging.SendOptions{
		Subject:     subject,
		Priority:    priority,
		Metadata:    metadataStr,
		ReplyTo:     replyTo,
		Attachments: attachmentHashes,
	}

	msg, err := h.msgService.SendMessage(ctx, agentName, to, body, opts)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("send_message failed: %s", err)), nil
	}

	result := map[string]any{
		"message_id":      msg.ID,
		"conversation_id": msg.ConversationID,
		"status":          msg.Status,
	}

	// Enrich message with attachment info.
	if len(attachmentHashes) > 0 {
		h.msgService.EnrichMessages(ctx, []*messaging.Message{msg})
		if len(msg.Attachments) > 0 {
			result["attachments"] = msg.Attachments
		}
	}

	return resultJSON(result)
}

func (h *HybridToolRegistrar) handleSearch(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	_, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}

	query := req.GetString("query", "")
	limit := req.GetInt("limit", 5)

	results := h.actionIndex.Search(query, limit)

	// Format results for the agent.
	formatted := make([]map[string]any, len(results))
	for i, r := range results {
		params := make([]map[string]any, len(r.Action.Params))
		for j, p := range r.Action.Params {
			params[j] = map[string]any{
				"name":        p.Name,
				"type":        p.Type,
				"description": p.Description,
				"required":    p.Required,
			}
		}

		entry := map[string]any{
			"name":        r.Action.Name,
			"category":    r.Action.Category,
			"description": r.Action.Description,
			"params":      params,
			"examples":    r.Action.Examples,
		}
		if r.Score > 0 {
			entry["relevance_score"] = r.Score
		}
		formatted[i] = entry
	}

	result := map[string]any{
		"actions": formatted,
		"count":   len(formatted),
		"note":    "Use the 'execute' tool with call(actionName, { param: value }) to run any action.",
	}

	return resultJSON(result)
}

func (h *HybridToolRegistrar) handleExecute(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}

	code := req.GetString("code", "")
	if code == "" {
		return mcplib.NewToolResultError("'code' parameter is required"), nil
	}

	timeoutMs := req.GetInt("timeout", 120000)
	if timeoutMs > 300000 {
		timeoutMs = 300000
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	// Create a bridge for this agent.
	bridge := NewServiceBridge(
		h.msgService,
		h.agentService,
		h.channelService,
		h.swarmService,
		h.attachmentService,
		h.searchService,
		h.reactionService,
		h.trustService,
		agentName,
	)
	if h.queryExecutor != nil {
		bridge.SetQueryExecutor(h.queryExecutor)
	}

	result, err := h.jsPool.Execute(ctx, code, bridge, jsruntime.ExecuteOptions{
		Timeout: timeout,
	})
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("execute failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"result":   result.Value,
		"calls":    result.CallCount,
		"duration": result.Duration.String(),
	})
}

func (h *HybridToolRegistrar) handleGetReplies(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	_, ok := extractAgentName(ctx)
	if !ok {
		return mcplib.NewToolResultError("authentication required"), nil
	}

	messageID := req.GetInt("message_id", 0)
	if messageID == 0 {
		return mcplib.NewToolResultError("'message_id' parameter is required"), nil
	}

	replies, err := h.msgService.GetReplies(ctx, int64(messageID))
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("get_replies failed: %s", err)), nil
	}

	// Enrich replies with attachment info.
	h.msgService.EnrichMessages(ctx, replies)

	return resultJSON(map[string]any{
		"message_id": messageID,
		"replies":    replies,
		"count":      len(replies),
	})
}

// resolveChannel resolves a channel name or numeric ID string to an int64 channel ID.
func (h *HybridToolRegistrar) resolveChannel(ctx context.Context, channel string) (int64, error) {
	// Try parsing as numeric ID first.
	var channelID int64
	if _, err := fmt.Sscanf(channel, "%d", &channelID); err == nil && channelID > 0 {
		return channelID, nil
	}

	// Resolve by name.
	ch, err := h.channelService.GetChannelByName(ctx, channel)
	if err != nil {
		return 0, err
	}
	return ch.ID, nil
}
