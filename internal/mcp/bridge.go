package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/agentquery"
	"github.com/synapbus/synapbus/internal/attachments"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/marketplace"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/reactions"
	"github.com/synapbus/synapbus/internal/search"
	"github.com/synapbus/synapbus/internal/trust"
	"github.com/synapbus/synapbus/internal/wiki"
)

// ServiceBridge implements jsruntime.ToolCaller, mapping action names to
// service method calls. It carries the authenticated agent's identity.
type ServiceBridge struct {
	msgService        *messaging.MessagingService
	agentService      *agents.AgentService
	channelService    *channels.Service
	swarmService      *channels.SwarmService
	attachmentService *attachments.Service
	searchService     *search.Service
	reactionService   *reactions.Service
	trustService      *trust.Service
	wikiService       *wiki.Service
	marketplace       *marketplace.Service
	queryExecutor     *agentquery.Executor
	agentName         string
}

// NewServiceBridge creates a new bridge for the given authenticated agent.
func NewServiceBridge(
	msgService *messaging.MessagingService,
	agentService *agents.AgentService,
	channelService *channels.Service,
	swarmService *channels.SwarmService,
	attachmentService *attachments.Service,
	searchService *search.Service,
	reactionService *reactions.Service,
	trustService *trust.Service,
	wikiService *wiki.Service,
	agentName string,
) *ServiceBridge {
	return &ServiceBridge{
		msgService:        msgService,
		agentService:      agentService,
		channelService:    channelService,
		swarmService:      swarmService,
		attachmentService: attachmentService,
		searchService:     searchService,
		reactionService:   reactionService,
		trustService:      trustService,
		wikiService:       wikiService,
		agentName:         agentName,
	}
}

// Call dispatches an action by name to the appropriate service method.
func (b *ServiceBridge) Call(ctx context.Context, actionName string, args map[string]any) (any, error) {
	switch actionName {
	// --- Messaging ---
	case "read_inbox":
		return b.callReadInbox(ctx, args)
	case "claim_messages":
		return b.callClaimMessages(ctx, args)
	case "mark_done":
		return b.callMarkDone(ctx, args)
	case "search_messages":
		return b.callSearchMessages(ctx, args)
	case "discover_agents":
		return b.callDiscoverAgents(ctx, args)

	// --- Channels ---
	case "create_channel":
		return b.callCreateChannel(ctx, args)
	case "join_channel":
		return b.callJoinChannel(ctx, args)
	case "leave_channel":
		return b.callLeaveChannel(ctx, args)
	case "list_channels":
		return b.callListChannels(ctx, args)
	case "invite_to_channel":
		return b.callInviteToChannel(ctx, args)
	case "kick_from_channel":
		return b.callKickFromChannel(ctx, args)
	case "get_channel_messages":
		return b.callGetChannelMessages(ctx, args)
	case "send_channel_message":
		return b.callSendChannelMessage(ctx, args)
	case "update_channel":
		return b.callUpdateChannel(ctx, args)

	// --- Swarm ---
	case "post_task":
		return b.callPostTask(ctx, args)
	case "bid_task":
		return b.callBidTask(ctx, args)
	case "accept_bid":
		return b.callAcceptBid(ctx, args)
	case "complete_task":
		return b.callCompleteTask(ctx, args)
	case "list_tasks":
		return b.callListTasks(ctx, args)

	// --- Attachments ---
	case "upload_attachment":
		return b.callUploadAttachment(ctx, args)
	case "download_attachment":
		return b.callDownloadAttachment(ctx, args)

	// --- Reactions ---
	case "react":
		return b.callReact(ctx, args)
	case "unreact":
		return b.callUnreact(ctx, args)
	case "get_reactions":
		return b.callGetReactions(ctx, args)
	case "list_by_state":
		return b.callListByState(ctx, args)

	// --- Threads ---
	case "get_replies":
		return b.callGetReplies(ctx, args)

	// --- Trust ---
	case "get_trust":
		return b.callGetTrust(ctx, args)

	// --- SQL Query ---
	case "query":
		return b.callQuery(ctx, args)

	// --- Wiki ---
	case "create_article":
		return b.callCreateArticle(ctx, args)
	case "get_article":
		return b.callGetArticle(ctx, args)
	case "update_article":
		return b.callUpdateArticle(ctx, args)
	case "list_articles":
		return b.callListArticles(ctx, args)
	case "get_backlinks":
		return b.callGetBacklinks(ctx, args)

	// --- DM send (also accessible via bridge for execute tool) ---
	case "send_message":
		return b.callSendMessage(ctx, args)

	// --- Marketplace (spec 016) ---
	case "post_auction":
		return b.callPostAuction(ctx, args)
	case "bid":
		return b.callBid(ctx, args)
	case "award":
		return b.callAward(ctx, args)
	case "mark_task_done":
		return b.callMarkTaskDone(ctx, args)
	case "read_skill_card":
		return b.callReadSkillCard(ctx, args)
	case "query_reputation":
		return b.callQueryReputation(ctx, args)

	default:
		return nil, fmt.Errorf("unknown action: %s", actionName)
	}
}

// --- Messaging implementations ---

func (b *ServiceBridge) callSendMessage(ctx context.Context, args map[string]any) (any, error) {
	to := getString(args, "to", "")
	body := getString(args, "body", "")
	if body == "" {
		return nil, fmt.Errorf("'body' parameter is required")
	}

	var channelID *int64
	if cid := getInt(args, "channel_id", 0); cid > 0 {
		v := int64(cid)
		channelID = &v
	}

	var replyTo *int64
	if rtID := getInt(args, "reply_to", 0); rtID > 0 {
		v := int64(rtID)
		replyTo = &v
	}

	var attachmentHashes []string
	if attVal, ok := args["attachments"]; ok {
		switch v := attVal.(type) {
		case string:
			for _, h := range strings.Split(v, ",") {
				h = strings.TrimSpace(h)
				if h != "" {
					attachmentHashes = append(attachmentHashes, h)
				}
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					attachmentHashes = append(attachmentHashes, s)
				}
			}
		case []string:
			attachmentHashes = v
		}
	}

	opts := messaging.SendOptions{
		Subject:     getString(args, "subject", ""),
		Priority:    getInt(args, "priority", 5),
		Metadata:    getString(args, "metadata", ""),
		ChannelID:   channelID,
		ReplyTo:     replyTo,
		Attachments: attachmentHashes,
	}

	msg, err := b.msgService.SendMessage(ctx, b.agentName, to, body, opts)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"message_id":      msg.ID,
		"conversation_id": msg.ConversationID,
		"status":          msg.Status,
	}, nil
}

func (b *ServiceBridge) callReadInbox(ctx context.Context, args map[string]any) (any, error) {
	// read_inbox is a pure peek by default. Callers that want the legacy
	// worker-queue behavior (fetch unread + advance the read pointer) must
	// pass mark_read: true explicitly. See bug 30674.
	opts := messaging.ReadOptions{
		Limit:       getInt(args, "limit", 50),
		Status:      getString(args, "status_filter", ""),
		MinPriority: getInt(args, "min_priority", 0),
		FromAgent:   getString(args, "from_agent", ""),
		IncludeRead: getBool(args, "include_read", false),
		MarkRead:    getBool(args, "mark_read", false),
	}

	page, err := b.msgService.ReadInbox(ctx, b.agentName, opts)
	if err != nil {
		return nil, err
	}

	b.msgService.EnrichMessages(ctx, page.Messages)

	return map[string]any{
		"messages": page.Messages,
		"count":    len(page.Messages),
		"total":    page.Total,
		"offset":   page.Offset,
		"limit":    page.Limit,
	}, nil
}

func (b *ServiceBridge) callClaimMessages(ctx context.Context, args map[string]any) (any, error) {
	limit := getInt(args, "limit", 10)

	messages, err := b.msgService.ClaimMessages(ctx, b.agentName, limit)
	if err != nil {
		return nil, err
	}

	b.msgService.EnrichMessages(ctx, messages)

	return map[string]any{
		"messages": messages,
		"count":    len(messages),
	}, nil
}

func (b *ServiceBridge) callMarkDone(ctx context.Context, args map[string]any) (any, error) {
	messageID := getInt(args, "message_id", 0)
	if messageID == 0 {
		return nil, fmt.Errorf("'message_id' parameter is required")
	}

	status := getString(args, "status", "done")
	reason := getString(args, "reason", "")

	switch status {
	case "done":
		if err := b.msgService.MarkDone(ctx, int64(messageID), b.agentName); err != nil {
			return nil, err
		}
	case "failed":
		if err := b.msgService.MarkFailed(ctx, int64(messageID), b.agentName, reason); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("status must be 'done' or 'failed'")
	}

	return map[string]any{
		"message_id": messageID,
		"status":     status,
	}, nil
}

func (b *ServiceBridge) callSearchMessages(ctx context.Context, args map[string]any) (any, error) {
	query := getString(args, "query", "")

	// If search service is available, use it for unified search.
	if b.searchService != nil {
		searchMode := getString(args, "search_mode", "auto")

		opts := search.SearchOptions{
			Query:         query,
			Mode:          searchMode,
			Limit:         getInt(args, "limit", 10),
			FromAgent:     getString(args, "from_agent", ""),
			MinPriority:   getInt(args, "min_priority", 0),
			MinSimilarity: getFloat(args, "min_similarity", 0),
		}

		resp, err := b.searchService.Search(ctx, b.agentName, opts)
		if err != nil {
			return nil, err
		}

		// Enrich messages with attachments
		searchMsgs := make([]*messaging.Message, 0, len(resp.Results))
		for _, r := range resp.Results {
			if r.Message != nil {
				searchMsgs = append(searchMsgs, r.Message)
			}
		}
		b.msgService.EnrichMessages(ctx, searchMsgs)

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
			"results":     resultMsgs,
			"count":       resp.TotalResults,
			"search_mode": resp.SearchMode,
		}
		if resp.Warning != "" {
			result["warning"] = resp.Warning
		}
		return result, nil
	}

	// Fallback: FTS only.
	msgOpts := messaging.SearchOptions{
		Limit:       getInt(args, "limit", 20),
		MinPriority: getInt(args, "min_priority", 0),
		FromAgent:   getString(args, "from_agent", ""),
		Status:      getString(args, "status", ""),
	}

	page, err := b.msgService.SearchMessages(ctx, b.agentName, query, msgOpts)
	if err != nil {
		return nil, err
	}

	b.msgService.EnrichMessages(ctx, page.Messages)

	return map[string]any{
		"messages":    page.Messages,
		"count":       len(page.Messages),
		"total":       page.Total,
		"offset":      page.Offset,
		"limit":       page.Limit,
		"search_mode": "fulltext",
	}, nil
}

func (b *ServiceBridge) callDiscoverAgents(ctx context.Context, args map[string]any) (any, error) {
	query := getString(args, "query", "")

	agentsList, err := b.agentService.DiscoverAgents(ctx, query)
	if err != nil {
		return nil, err
	}

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

	return map[string]any{
		"agents": result,
		"count":  len(result),
	}, nil
}

// --- Channel implementations ---

func (b *ServiceBridge) callCreateChannel(ctx context.Context, args map[string]any) (any, error) {
	name := getString(args, "name", "")
	if name == "" {
		return nil, fmt.Errorf("'name' parameter is required")
	}

	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	createReq := channels.CreateChannelRequest{
		Name:        name,
		Description: getString(args, "description", ""),
		Topic:       getString(args, "topic", ""),
		Type:        getString(args, "type", "standard"),
		IsPrivate:   getBool(args, "is_private", false),
		CreatedBy:   b.agentName,
	}

	ch, err := b.channelService.CreateChannel(ctx, createReq)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"channel_id":  ch.ID,
		"name":        ch.Name,
		"description": ch.Description,
		"topic":       ch.Topic,
		"type":        ch.Type,
		"is_private":  ch.IsPrivate,
		"created_by":  ch.CreatedBy,
	}, nil
}

func (b *ServiceBridge) callJoinChannel(ctx context.Context, args map[string]any) (any, error) {
	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	channelID, err := b.resolveChannelID(ctx, args)
	if err != nil {
		return nil, err
	}

	if err := b.channelService.JoinChannel(ctx, channelID, b.agentName); err != nil {
		return nil, err
	}

	return map[string]any{
		"channel_id": channelID,
		"status":     "joined",
	}, nil
}

func (b *ServiceBridge) callLeaveChannel(ctx context.Context, args map[string]any) (any, error) {
	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	channelID, err := b.resolveChannelID(ctx, args)
	if err != nil {
		return nil, err
	}

	if err := b.channelService.LeaveChannel(ctx, channelID, b.agentName); err != nil {
		return nil, err
	}

	return map[string]any{
		"channel_id": channelID,
		"status":     "left",
	}, nil
}

func (b *ServiceBridge) callListChannels(ctx context.Context, args map[string]any) (any, error) {
	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	chList, err := b.channelService.ListChannels(ctx, b.agentName)
	if err != nil {
		return nil, err
	}

	result := make([]map[string]any, len(chList))
	for i, ch := range chList {
		result[i] = map[string]any{
			"id":           ch.ID,
			"name":         ch.Name,
			"description":  ch.Description,
			"topic":        ch.Topic,
			"type":         ch.Type,
			"is_private":   ch.IsPrivate,
			"created_by":   ch.CreatedBy,
			"member_count": ch.MemberCount,
		}
	}

	return map[string]any{
		"channels": result,
		"count":    len(result),
	}, nil
}

func (b *ServiceBridge) callInviteToChannel(ctx context.Context, args map[string]any) (any, error) {
	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	channelID, err := b.resolveChannelID(ctx, args)
	if err != nil {
		return nil, err
	}

	targetAgent := getString(args, "agent_name", "")
	if targetAgent == "" {
		return nil, fmt.Errorf("'agent_name' parameter is required")
	}

	if err := b.channelService.InviteToChannel(ctx, channelID, targetAgent, b.agentName); err != nil {
		return nil, err
	}

	return map[string]any{
		"channel_id": channelID,
		"agent_name": targetAgent,
		"status":     "invited",
	}, nil
}

func (b *ServiceBridge) callKickFromChannel(ctx context.Context, args map[string]any) (any, error) {
	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	channelID, err := b.resolveChannelID(ctx, args)
	if err != nil {
		return nil, err
	}

	targetAgent := getString(args, "agent_name", "")
	if targetAgent == "" {
		return nil, fmt.Errorf("'agent_name' parameter is required")
	}

	if err := b.channelService.KickFromChannel(ctx, channelID, targetAgent, b.agentName); err != nil {
		return nil, err
	}

	return map[string]any{
		"channel_id": channelID,
		"agent_name": targetAgent,
		"status":     "kicked",
	}, nil
}

func (b *ServiceBridge) callGetChannelMessages(ctx context.Context, args map[string]any) (any, error) {
	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	channelID, err := b.resolveChannelID(ctx, args)
	if err != nil {
		return nil, err
	}

	// Verify membership.
	isMember, err := b.channelService.IsMember(ctx, channelID, b.agentName)
	if err != nil {
		return nil, err
	}
	if !isMember {
		return nil, fmt.Errorf("you are not a member of this channel")
	}

	limit := getInt(args, "limit", 50)
	if limit > 200 {
		limit = 200
	}

	offset := getInt(args, "offset", 0)
	page, err := b.msgService.GetChannelMessages(ctx, channelID, limit, offset)
	if err != nil {
		return nil, err
	}

	b.msgService.EnrichMessages(ctx, page.Messages)

	result := make([]map[string]any, len(page.Messages))
	for i, msg := range page.Messages {
		result[i] = map[string]any{
			"id":          msg.ID,
			"from":        msg.FromAgent,
			"body":        msg.Body,
			"priority":    msg.Priority,
			"status":      msg.Status,
			"created_at":  msg.CreatedAt,
			"attachments": msg.Attachments,
		}
		if len(msg.Metadata) > 0 {
			result[i]["metadata"] = msg.Metadata
		}
	}

	return map[string]any{
		"channel_id": channelID,
		"messages":   result,
		"count":      len(result),
		"total":      page.Total,
		"offset":     page.Offset,
		"limit":      page.Limit,
	}, nil
}

func (b *ServiceBridge) callSendChannelMessage(ctx context.Context, args map[string]any) (any, error) {
	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	channelID, err := b.resolveChannelID(ctx, args)
	if err != nil {
		return nil, err
	}

	body := getString(args, "body", "")
	if body == "" {
		return nil, fmt.Errorf("'body' parameter is required")
	}

	priority := getInt(args, "priority", 5)
	metadata := getString(args, "metadata", "")

	var replyTo *int64
	if v, ok := args["reply_to"]; ok {
		if f, ok := v.(float64); ok {
			r := int64(f)
			replyTo = &r
		}
	}

	var attachmentHashes []string
	if attVal, ok := args["attachments"]; ok {
		switch v := attVal.(type) {
		case string:
			for _, h := range strings.Split(v, ",") {
				h = strings.TrimSpace(h)
				if h != "" {
					attachmentHashes = append(attachmentHashes, h)
				}
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					attachmentHashes = append(attachmentHashes, s)
				}
			}
		case []string:
			attachmentHashes = v
		}
	}

	messages, err := b.channelService.BroadcastMessage(ctx, channelID, b.agentName, body, priority, metadata, replyTo, attachmentHashes)
	if err != nil {
		return nil, err
	}

	var messageID int64
	if len(messages) > 0 {
		messageID = messages[0].ID
	}

	return map[string]any{
		"channel_id": channelID,
		"message_id": messageID,
		"status":     "sent",
	}, nil
}

func (b *ServiceBridge) callUpdateChannel(ctx context.Context, args map[string]any) (any, error) {
	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	channelID, err := b.resolveChannelID(ctx, args)
	if err != nil {
		return nil, err
	}

	updateReq := channels.UpdateChannelRequest{}
	if v, ok := args["topic"]; ok {
		if s, ok := v.(string); ok {
			updateReq.Topic = &s
		}
	}
	if v, ok := args["description"]; ok {
		if s, ok := v.(string); ok {
			updateReq.Description = &s
		}
	}

	ch, err := b.channelService.UpdateChannel(ctx, channelID, updateReq, b.agentName)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"channel_id":  ch.ID,
		"name":        ch.Name,
		"description": ch.Description,
		"topic":       ch.Topic,
	}, nil
}

// --- Swarm implementations ---

func (b *ServiceBridge) callPostTask(ctx context.Context, args map[string]any) (any, error) {
	if b.swarmService == nil {
		return nil, fmt.Errorf("swarm service not available")
	}

	channelName := getString(args, "channel_name", "")
	if channelName == "" {
		return nil, fmt.Errorf("'channel_name' parameter is required")
	}

	title := getString(args, "title", "")
	if title == "" {
		return nil, fmt.Errorf("'title' parameter is required")
	}

	description := getString(args, "description", "")
	requirementsStr := getString(args, "requirements", "{}")
	deadlineStr := getString(args, "deadline", "")

	var requirements json.RawMessage
	if requirementsStr != "" {
		if !json.Valid([]byte(requirementsStr)) {
			return nil, fmt.Errorf("requirements must be valid JSON")
		}
		requirements = json.RawMessage(requirementsStr)
	}

	var deadline *time.Time
	if deadlineStr != "" {
		t, err := time.Parse(time.RFC3339, deadlineStr)
		if err != nil {
			return nil, fmt.Errorf("deadline must be ISO 8601 format: %s", err)
		}
		deadline = &t
	}

	ch, err := b.channelService.GetChannelByName(ctx, channelName)
	if err != nil {
		return nil, err
	}

	task, err := b.swarmService.PostTask(ctx, ch.ID, b.agentName, title, description, requirements, deadline)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"task_id":    task.ID,
		"channel_id": task.ChannelID,
		"title":      task.Title,
		"status":     task.Status,
		"posted_by":  task.PostedBy,
		"deadline":   task.Deadline,
		"created_at": task.CreatedAt,
	}, nil
}

func (b *ServiceBridge) callBidTask(ctx context.Context, args map[string]any) (any, error) {
	if b.swarmService == nil {
		return nil, fmt.Errorf("swarm service not available")
	}

	taskID := getInt(args, "task_id", 0)
	if taskID == 0 {
		return nil, fmt.Errorf("'task_id' parameter is required")
	}

	capabilitiesStr := getString(args, "capabilities", "{}")
	timeEstimate := getString(args, "time_estimate", "")
	message := getString(args, "message", "")

	var capabilities json.RawMessage
	if capabilitiesStr != "" {
		if !json.Valid([]byte(capabilitiesStr)) {
			return nil, fmt.Errorf("capabilities must be valid JSON")
		}
		capabilities = json.RawMessage(capabilitiesStr)
	}

	bid, err := b.swarmService.BidOnTask(ctx, int64(taskID), b.agentName, capabilities, timeEstimate, message)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"bid_id":        bid.ID,
		"task_id":       bid.TaskID,
		"agent_name":    bid.AgentName,
		"time_estimate": bid.TimeEstimate,
		"status":        bid.Status,
	}, nil
}

func (b *ServiceBridge) callAcceptBid(ctx context.Context, args map[string]any) (any, error) {
	if b.swarmService == nil {
		return nil, fmt.Errorf("swarm service not available")
	}

	taskID := getInt(args, "task_id", 0)
	if taskID == 0 {
		return nil, fmt.Errorf("'task_id' parameter is required")
	}

	bidID := getInt(args, "bid_id", 0)
	if bidID == 0 {
		return nil, fmt.Errorf("'bid_id' parameter is required")
	}

	if err := b.swarmService.AcceptBid(ctx, int64(taskID), int64(bidID), b.agentName); err != nil {
		return nil, err
	}

	return map[string]any{
		"task_id": taskID,
		"bid_id":  bidID,
		"status":  "accepted",
	}, nil
}

func (b *ServiceBridge) callCompleteTask(ctx context.Context, args map[string]any) (any, error) {
	if b.swarmService == nil {
		return nil, fmt.Errorf("swarm service not available")
	}

	taskID := getInt(args, "task_id", 0)
	if taskID == 0 {
		return nil, fmt.Errorf("'task_id' parameter is required")
	}

	if err := b.swarmService.CompleteTask(ctx, int64(taskID), b.agentName); err != nil {
		return nil, err
	}

	return map[string]any{
		"task_id": taskID,
		"status":  "completed",
	}, nil
}

func (b *ServiceBridge) callListTasks(ctx context.Context, args map[string]any) (any, error) {
	if b.swarmService == nil {
		return nil, fmt.Errorf("swarm service not available")
	}
	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	channelName := getString(args, "channel_name", "")
	if channelName == "" {
		return nil, fmt.Errorf("'channel_name' parameter is required")
	}

	statusFilter := getString(args, "status", "")

	ch, err := b.channelService.GetChannelByName(ctx, channelName)
	if err != nil {
		return nil, err
	}

	if ch.Type != channels.TypeAuction {
		return nil, fmt.Errorf("list_tasks requires a channel of type 'auction', got '%s'", ch.Type)
	}

	tasks, err := b.swarmService.ListTasks(ctx, ch.ID, statusFilter)
	if err != nil {
		return nil, err
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

	return map[string]any{
		"tasks": result,
		"count": len(result),
	}, nil
}

// --- Attachment implementations ---

func (b *ServiceBridge) callUploadAttachment(ctx context.Context, args map[string]any) (any, error) {
	if b.attachmentService == nil {
		return nil, fmt.Errorf("attachment service not available")
	}

	contentB64 := getString(args, "content", "")
	if contentB64 == "" {
		return nil, fmt.Errorf("'content' parameter is required")
	}

	if int64(len(contentB64))*3/4 > attachments.MaxFileSize {
		return nil, fmt.Errorf("file exceeds maximum size of 50MB")
	}

	decoded, err := base64.StdEncoding.DecodeString(contentB64)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 content: %s", err)
	}

	if int64(len(decoded)) > attachments.MaxFileSize {
		return nil, fmt.Errorf("file exceeds maximum size of 50MB")
	}

	uploadReq := attachments.UploadRequest{
		Content:    bytes.NewReader(decoded),
		Filename:   getString(args, "filename", ""),
		MIMEType:   getString(args, "mime_type", ""),
		UploadedBy: b.agentName,
	}

	if mid := getInt(args, "message_id", 0); mid > 0 {
		v := int64(mid)
		uploadReq.MessageID = &v
	}

	uploadResult, err := b.attachmentService.Upload(ctx, uploadReq)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"hash":              uploadResult.Hash,
		"size":              uploadResult.Size,
		"mime_type":         uploadResult.MIMEType,
		"original_filename": uploadResult.Filename,
	}, nil
}

func (b *ServiceBridge) callDownloadAttachment(ctx context.Context, args map[string]any) (any, error) {
	if b.attachmentService == nil {
		return nil, fmt.Errorf("attachment service not available")
	}

	hash := getString(args, "hash", "")
	if hash == "" {
		return nil, fmt.Errorf("'hash' parameter is required")
	}

	dlResult, err := b.attachmentService.Download(ctx, hash)
	if err != nil {
		return nil, err
	}
	defer dlResult.Content.Close()

	content, err := io.ReadAll(dlResult.Content)
	if err != nil {
		return nil, fmt.Errorf("read attachment content: %s", err)
	}

	return map[string]any{
		"hash":              dlResult.Hash,
		"content":           base64.StdEncoding.EncodeToString(content),
		"original_filename": dlResult.Filename,
		"mime_type":         dlResult.MIMEType,
		"size":              dlResult.Size,
	}, nil
}

// --- Reaction implementations ---

func (b *ServiceBridge) callReact(ctx context.Context, args map[string]any) (any, error) {
	if b.reactionService == nil {
		return nil, fmt.Errorf("reaction service not available")
	}

	messageID := getInt(args, "message_id", 0)
	if messageID == 0 {
		return nil, fmt.Errorf("'message_id' parameter is required")
	}

	reaction := getString(args, "reaction", "")
	if reaction == "" {
		return nil, fmt.Errorf("'reaction' parameter is required")
	}

	var metadata json.RawMessage
	if metaStr := getString(args, "metadata", ""); metaStr != "" {
		if !json.Valid([]byte(metaStr)) {
			return nil, fmt.Errorf("metadata must be valid JSON")
		}
		metadata = json.RawMessage(metaStr)
	}

	result, err := b.reactionService.Toggle(ctx, int64(messageID), b.agentName, reaction, metadata)
	if err != nil {
		return nil, err
	}

	resp := map[string]any{
		"action":     result.Action,
		"message_id": messageID,
		"reaction":   reaction,
	}
	if result.Reaction != nil {
		resp["id"] = result.Reaction.ID
		resp["created_at"] = result.Reaction.CreatedAt
	}

	// After the toggle, get current reactions and workflow state
	rxns, state, err := b.reactionService.GetReactions(ctx, int64(messageID))
	if err != nil {
		// Non-fatal: still return the toggle result
		slog.Warn("failed to get reactions after toggle", "error", err)
	} else {
		resp["workflow_state"] = state
		resp["reactions"] = rxns
	}

	return resp, nil
}

func (b *ServiceBridge) callUnreact(ctx context.Context, args map[string]any) (any, error) {
	if b.reactionService == nil {
		return nil, fmt.Errorf("reaction service not available")
	}

	messageID := getInt(args, "message_id", 0)
	if messageID == 0 {
		return nil, fmt.Errorf("'message_id' parameter is required")
	}

	reaction := getString(args, "reaction", "")
	if reaction == "" {
		return nil, fmt.Errorf("'reaction' parameter is required")
	}

	if err := b.reactionService.Remove(ctx, int64(messageID), b.agentName, reaction); err != nil {
		return nil, err
	}

	return map[string]any{
		"message_id": messageID,
		"reaction":   reaction,
		"status":     "removed",
	}, nil
}

func (b *ServiceBridge) callGetReactions(ctx context.Context, args map[string]any) (any, error) {
	if b.reactionService == nil {
		return nil, fmt.Errorf("reaction service not available")
	}

	messageID := getInt(args, "message_id", 0)
	if messageID == 0 {
		return nil, fmt.Errorf("'message_id' parameter is required")
	}

	rxns, state, err := b.reactionService.GetReactions(ctx, int64(messageID))
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"reactions":      rxns,
		"workflow_state": state,
		"count":          len(rxns),
	}, nil
}

func (b *ServiceBridge) callListByState(ctx context.Context, args map[string]any) (any, error) {
	if b.reactionService == nil {
		return nil, fmt.Errorf("reaction service not available")
	}
	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	channelName := getString(args, "channel", "")
	if channelName == "" {
		return nil, fmt.Errorf("'channel' parameter is required")
	}

	state := getString(args, "state", "")
	if state == "" {
		return nil, fmt.Errorf("'state' parameter is required")
	}

	ch, err := b.channelService.GetChannelByName(ctx, channelName)
	if err != nil {
		return nil, err
	}

	messageIDs, err := b.reactionService.ListByState(ctx, ch.ID, state)
	if err != nil {
		return nil, err
	}

	if messageIDs == nil {
		messageIDs = []int64{}
	}

	totalCount := len(messageIDs)

	// Apply limit and offset for pagination
	limit := getInt(args, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := getInt(args, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	if offset > len(messageIDs) {
		offset = len(messageIDs)
	}
	end := offset + limit
	if end > len(messageIDs) {
		end = len(messageIDs)
	}
	pageIDs := messageIDs[offset:end]

	resp := map[string]any{
		"message_ids": pageIDs,
		"count":       len(pageIDs),
		"total":       totalCount,
		"channel":     channelName,
		"state":       state,
		"limit":       limit,
		"offset":      offset,
	}

	includeMessages := getBool(args, "include_messages", false)
	if includeMessages && len(pageIDs) > 0 && b.msgService != nil {
		maxBodyLen := getInt(args, "max_body_length", 500)
		if maxBodyLen <= 0 {
			maxBodyLen = 500
		}
		var msgSlice []*messaging.Message
		for _, id := range pageIDs {
			msg, err := b.msgService.GetMessageByID(ctx, id)
			if err != nil {
				continue
			}
			msgSlice = append(msgSlice, msg)
		}
		b.msgService.EnrichMessages(ctx, msgSlice)

		var messages []map[string]any
		for _, msg := range msgSlice {
			body := msg.Body
			if len(body) > maxBodyLen {
				body = body[:maxBodyLen] + "..."
			}
			messages = append(messages, map[string]any{
				"id":          msg.ID,
				"from_agent":  msg.FromAgent,
				"body":        body,
				"priority":    msg.Priority,
				"created_at":  msg.CreatedAt,
				"reply_to":    msg.ReplyTo,
				"attachments": msg.Attachments,
			})
		}
		resp["messages"] = messages
	}

	return resp, nil
}

// --- Threads ---

func (b *ServiceBridge) callGetReplies(ctx context.Context, args map[string]any) (any, error) {
	messageID := getInt(args, "message_id", 0)
	if messageID == 0 {
		return nil, fmt.Errorf("'message_id' parameter is required")
	}

	replies, err := b.msgService.GetReplies(ctx, int64(messageID))
	if err != nil {
		return nil, err
	}

	// Enrich with attachments
	b.msgService.EnrichMessages(ctx, replies)

	return map[string]any{
		"message_id": messageID,
		"replies":    replies,
		"count":      len(replies),
	}, nil
}

// --- Trust implementations ---

func (b *ServiceBridge) callGetTrust(ctx context.Context, args map[string]any) (any, error) {
	if b.trustService == nil {
		return nil, fmt.Errorf("trust service not available")
	}

	agentName := getString(args, "agent_name", "")
	if agentName == "" {
		agentName = b.agentName
	}

	scores, err := b.trustService.GetScores(ctx, agentName)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"agent_name": agentName,
		"scores":     scores,
	}, nil
}

// SetQueryExecutor sets the SQL query executor for the bridge.
func (b *ServiceBridge) SetQueryExecutor(exec *agentquery.Executor) {
	b.queryExecutor = exec
}

func (b *ServiceBridge) callQuery(ctx context.Context, args map[string]any) (any, error) {
	if b.queryExecutor == nil {
		return nil, fmt.Errorf("SQL query not available")
	}

	sqlStr := getString(args, "sql", "")
	if sqlStr == "" {
		return nil, fmt.Errorf("sql parameter is required")
	}

	result, err := b.queryExecutor.Execute(ctx, b.agentName, sqlStr)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// --- Wiki implementations ---

func (b *ServiceBridge) callCreateArticle(ctx context.Context, args map[string]any) (any, error) {
	if b.wikiService == nil {
		return nil, fmt.Errorf("wiki not available")
	}

	slug := getString(args, "slug", "")
	if slug == "" {
		return nil, fmt.Errorf("'slug' parameter is required")
	}
	title := getString(args, "title", "")
	if title == "" {
		return nil, fmt.Errorf("'title' parameter is required")
	}
	body := getString(args, "body", "")
	if body == "" {
		return nil, fmt.Errorf("'body' parameter is required")
	}

	article, err := b.wikiService.CreateArticle(ctx, slug, title, body, b.agentName)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"id":        article.ID,
		"slug":      article.Slug,
		"title":     article.Title,
		"revision":  article.Revision,
		"word_count": article.WordCount,
		"created_at": article.CreatedAt,
		"outgoing_links": article.OutgoingLinks,
	}, nil
}

func (b *ServiceBridge) callGetArticle(ctx context.Context, args map[string]any) (any, error) {
	if b.wikiService == nil {
		return nil, fmt.Errorf("wiki not available")
	}

	slug := getString(args, "slug", "")
	if slug == "" {
		return nil, fmt.Errorf("'slug' parameter is required")
	}

	article, err := b.wikiService.GetArticle(ctx, slug)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"id":             article.ID,
		"slug":           article.Slug,
		"title":          article.Title,
		"body":           article.Body,
		"created_by":     article.CreatedBy,
		"updated_by":     article.UpdatedBy,
		"revision":       article.Revision,
		"word_count":     article.WordCount,
		"created_at":     article.CreatedAt,
		"updated_at":     article.UpdatedAt,
		"outgoing_links": article.OutgoingLinks,
		"backlinks":      article.Backlinks,
	}

	if getBool(args, "include_history", false) {
		revisions, err := b.wikiService.GetRevisions(ctx, slug)
		if err != nil {
			return nil, err
		}
		result["revisions"] = revisions
	}

	return result, nil
}

func (b *ServiceBridge) callUpdateArticle(ctx context.Context, args map[string]any) (any, error) {
	if b.wikiService == nil {
		return nil, fmt.Errorf("wiki not available")
	}

	slug := getString(args, "slug", "")
	if slug == "" {
		return nil, fmt.Errorf("'slug' parameter is required")
	}
	body := getString(args, "body", "")
	if body == "" {
		return nil, fmt.Errorf("'body' parameter is required")
	}
	title := getString(args, "title", "")

	article, err := b.wikiService.UpdateArticle(ctx, slug, title, body, b.agentName)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"id":             article.ID,
		"slug":           article.Slug,
		"title":          article.Title,
		"revision":       article.Revision,
		"word_count":     article.WordCount,
		"updated_at":     article.UpdatedAt,
		"outgoing_links": article.OutgoingLinks,
	}, nil
}

func (b *ServiceBridge) callListArticles(ctx context.Context, args map[string]any) (any, error) {
	if b.wikiService == nil {
		return nil, fmt.Errorf("wiki not available")
	}

	query := getString(args, "query", "")
	limit := getInt(args, "limit", 50)

	articles, err := b.wikiService.ListArticles(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"articles": articles,
		"count":    len(articles),
	}, nil
}

func (b *ServiceBridge) callGetBacklinks(ctx context.Context, args map[string]any) (any, error) {
	if b.wikiService == nil {
		return nil, fmt.Errorf("wiki not available")
	}

	slug := getString(args, "slug", "")
	if slug == "" {
		return nil, fmt.Errorf("'slug' parameter is required")
	}

	backlinks, err := b.wikiService.GetBacklinks(ctx, slug)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"slug":      slug,
		"backlinks": backlinks,
		"count":     len(backlinks),
	}, nil
}

// --- Helpers ---

// resolveChannelID resolves a channel ID from either channel_id or channel_name in args.
func (b *ServiceBridge) resolveChannelID(ctx context.Context, args map[string]any) (int64, error) {
	if cid := getInt(args, "channel_id", 0); cid > 0 {
		return int64(cid), nil
	}

	name := getString(args, "channel_name", "")
	if name != "" {
		ch, err := b.channelService.GetChannelByName(ctx, name)
		if err != nil {
			return 0, err
		}
		return ch.ID, nil
	}

	return 0, fmt.Errorf("either 'channel_id' or 'channel_name' is required")
}

// getString extracts a string value from args with a default.
func getString(args map[string]any, key, defaultVal string) string {
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	s, ok := v.(string)
	if !ok {
		return defaultVal
	}
	return s
}

// getInt extracts an int value from args with a default.
// Handles both int and float64 (JSON numbers decode as float64).
func getInt(args map[string]any, key string, defaultVal int) int {
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return defaultVal
		}
		return int(i)
	}
	return defaultVal
}

// getFloat extracts a float64 value from args with a default.
func getFloat(args map[string]any, key string, defaultVal float64) float64 {
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return defaultVal
		}
		return f
	}
	return defaultVal
}

// getBool extracts a bool value from args with a default.
func getBool(args map[string]any, key string, defaultVal bool) bool {
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	b, ok := v.(bool)
	if !ok {
		return defaultVal
	}
	return b
}
