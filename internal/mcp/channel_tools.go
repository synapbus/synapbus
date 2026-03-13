package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/smart-mcp-proxy/synapbus/internal/channels"
)

// ChannelToolRegistrar registers channel MCP tools on the server.
type ChannelToolRegistrar struct {
	channelService *channels.Service
}

// NewChannelToolRegistrar creates a new channel tool registrar.
func NewChannelToolRegistrar(channelService *channels.Service) *ChannelToolRegistrar {
	return &ChannelToolRegistrar{
		channelService: channelService,
	}
}

// RegisterAll registers all channel tools on the MCP server.
func (ctr *ChannelToolRegistrar) RegisterAll(s *server.MCPServer) {
	s.AddTool(ctr.createChannelTool(), ctr.handleCreateChannel)
	s.AddTool(ctr.joinChannelTool(), ctr.handleJoinChannel)
	s.AddTool(ctr.leaveChannelTool(), ctr.handleLeaveChannel)
	s.AddTool(ctr.listChannelsTool(), ctr.handleListChannels)
	s.AddTool(ctr.inviteToChannelTool(), ctr.handleInviteToChannel)
	s.AddTool(ctr.kickFromChannelTool(), ctr.handleKickFromChannel)
	s.AddTool(ctr.sendChannelMessageTool(), ctr.handleSendChannelMessage)
	s.AddTool(ctr.updateChannelTool(), ctr.handleUpdateChannel)
}

// --- Tool Definitions ---

func (ctr *ChannelToolRegistrar) createChannelTool() mcp.Tool {
	return mcp.NewTool("create_channel",
		mcp.WithDescription("Create a new channel for group communication"),
		mcp.WithString("name", mcp.Description("Unique channel name (alphanumeric, hyphens, underscores, max 64 chars)"), mcp.Required()),
		mcp.WithString("description", mcp.Description("Channel description")),
		mcp.WithString("topic", mcp.Description("Current channel topic")),
		mcp.WithString("type", mcp.Description("Channel type: 'standard', 'blackboard', or 'auction' (default 'standard')")),
		mcp.WithBoolean("is_private", mcp.Description("Whether the channel is private (invite-only). Default false")),
	)
}

func (ctr *ChannelToolRegistrar) joinChannelTool() mcp.Tool {
	return mcp.NewTool("join_channel",
		mcp.WithDescription("Join an existing channel"),
		mcp.WithNumber("channel_id", mcp.Description("ID of the channel to join")),
		mcp.WithString("channel_name", mcp.Description("Name of the channel to join (alternative to channel_id)")),
	)
}

func (ctr *ChannelToolRegistrar) leaveChannelTool() mcp.Tool {
	return mcp.NewTool("leave_channel",
		mcp.WithDescription("Leave a channel you are a member of"),
		mcp.WithNumber("channel_id", mcp.Description("ID of the channel to leave")),
		mcp.WithString("channel_name", mcp.Description("Name of the channel to leave (alternative to channel_id)")),
	)
}

func (ctr *ChannelToolRegistrar) listChannelsTool() mcp.Tool {
	return mcp.NewTool("list_channels",
		mcp.WithDescription("List all channels visible to the authenticated agent (all public channels plus private channels you are a member of or have been invited to)"),
	)
}

func (ctr *ChannelToolRegistrar) inviteToChannelTool() mcp.Tool {
	return mcp.NewTool("invite_to_channel",
		mcp.WithDescription("Invite an agent to a channel (only the channel owner can invite to private channels)"),
		mcp.WithNumber("channel_id", mcp.Description("ID of the channel")),
		mcp.WithString("channel_name", mcp.Description("Name of the channel (alternative to channel_id)")),
		mcp.WithString("agent_name", mcp.Description("Name of the agent to invite"), mcp.Required()),
	)
}

func (ctr *ChannelToolRegistrar) kickFromChannelTool() mcp.Tool {
	return mcp.NewTool("kick_from_channel",
		mcp.WithDescription("Remove an agent from a channel (only the channel owner can kick)"),
		mcp.WithNumber("channel_id", mcp.Description("ID of the channel")),
		mcp.WithString("channel_name", mcp.Description("Name of the channel (alternative to channel_id)")),
		mcp.WithString("agent_name", mcp.Description("Name of the agent to kick"), mcp.Required()),
	)
}

func (ctr *ChannelToolRegistrar) sendChannelMessageTool() mcp.Tool {
	return mcp.NewTool("send_channel_message",
		mcp.WithDescription("Send a message to all members of a channel"),
		mcp.WithNumber("channel_id", mcp.Description("ID of the channel")),
		mcp.WithString("channel_name", mcp.Description("Name of the channel (alternative to channel_id)")),
		mcp.WithString("body", mcp.Description("Message body text"), mcp.Required()),
		mcp.WithNumber("priority", mcp.Description("Message priority (1-10, default 5)"), mcp.Min(1), mcp.Max(10)),
		mcp.WithString("metadata", mcp.Description("JSON metadata object (optional)")),
	)
}

func (ctr *ChannelToolRegistrar) updateChannelTool() mcp.Tool {
	return mcp.NewTool("update_channel",
		mcp.WithDescription("Update channel topic or description (only the channel owner can update)"),
		mcp.WithNumber("channel_id", mcp.Description("ID of the channel")),
		mcp.WithString("channel_name", mcp.Description("Name of the channel (alternative to channel_id)")),
		mcp.WithString("topic", mcp.Description("New channel topic")),
		mcp.WithString("description", mcp.Description("New channel description")),
	)
}

// --- Tool Handlers ---

func (ctr *ChannelToolRegistrar) handleCreateChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	name := req.GetString("name", "")
	if name == "" {
		return mcp.NewToolResultError("'name' parameter is required"), nil
	}

	isPrivate := false
	args := req.GetArguments()
	if v, ok := args["is_private"]; ok {
		if b, ok := v.(bool); ok {
			isPrivate = b
		}
	}

	createReq := channels.CreateChannelRequest{
		Name:        name,
		Description: req.GetString("description", ""),
		Topic:       req.GetString("topic", ""),
		Type:        req.GetString("type", "standard"),
		IsPrivate:   isPrivate,
		CreatedBy:   agentName,
	}

	ch, err := ctr.channelService.CreateChannel(ctx, createReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create_channel failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"channel_id":  ch.ID,
		"name":        ch.Name,
		"description": ch.Description,
		"topic":       ch.Topic,
		"type":        ch.Type,
		"is_private":  ch.IsPrivate,
		"created_by":  ch.CreatedBy,
	})
}

func (ctr *ChannelToolRegistrar) handleJoinChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	channelID, err := ctr.resolveChannelID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("join_channel failed: %s", err)), nil
	}

	if err := ctr.channelService.JoinChannel(ctx, channelID, agentName); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("join_channel failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"channel_id": channelID,
		"status":     "joined",
	})
}

func (ctr *ChannelToolRegistrar) handleLeaveChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	channelID, err := ctr.resolveChannelID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("leave_channel failed: %s", err)), nil
	}

	if err := ctr.channelService.LeaveChannel(ctx, channelID, agentName); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("leave_channel failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"channel_id": channelID,
		"status":     "left",
	})
}

func (ctr *ChannelToolRegistrar) handleListChannels(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	chList, err := ctr.channelService.ListChannels(ctx, agentName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list_channels failed: %s", err)), nil
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

	return resultJSON(map[string]any{
		"channels": result,
		"count":    len(result),
	})
}

func (ctr *ChannelToolRegistrar) handleInviteToChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	channelID, err := ctr.resolveChannelID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invite_to_channel failed: %s", err)), nil
	}

	targetAgent := req.GetString("agent_name", "")
	if targetAgent == "" {
		return mcp.NewToolResultError("'agent_name' parameter is required"), nil
	}

	if err := ctr.channelService.InviteToChannel(ctx, channelID, targetAgent, agentName); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invite_to_channel failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"channel_id": channelID,
		"agent_name": targetAgent,
		"status":     "invited",
	})
}

func (ctr *ChannelToolRegistrar) handleKickFromChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	channelID, err := ctr.resolveChannelID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("kick_from_channel failed: %s", err)), nil
	}

	targetAgent := req.GetString("agent_name", "")
	if targetAgent == "" {
		return mcp.NewToolResultError("'agent_name' parameter is required"), nil
	}

	if err := ctr.channelService.KickFromChannel(ctx, channelID, targetAgent, agentName); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("kick_from_channel failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"channel_id": channelID,
		"agent_name": targetAgent,
		"status":     "kicked",
	})
}

func (ctr *ChannelToolRegistrar) handleSendChannelMessage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	channelID, err := ctr.resolveChannelID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("send_channel_message failed: %s", err)), nil
	}

	body := req.GetString("body", "")
	if body == "" {
		return mcp.NewToolResultError("'body' parameter is required"), nil
	}

	priority := req.GetInt("priority", 5)
	metadata := req.GetString("metadata", "")

	messages, err := ctr.channelService.BroadcastMessage(ctx, channelID, agentName, body, priority, metadata)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("send_channel_message failed: %s", err)), nil
	}

	msgIDs := make([]int64, len(messages))
	for i, m := range messages {
		msgIDs[i] = m.ID
	}

	return resultJSON(map[string]any{
		"channel_id":      channelID,
		"recipients":      len(messages),
		"message_ids":     msgIDs,
	})
}

func (ctr *ChannelToolRegistrar) handleUpdateChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	channelID, err := ctr.resolveChannelID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("update_channel failed: %s", err)), nil
	}

	updateReq := channels.UpdateChannelRequest{}
	args := req.GetArguments()
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

	ch, err := ctr.channelService.UpdateChannel(ctx, channelID, updateReq, agentName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("update_channel failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"channel_id":  ch.ID,
		"name":        ch.Name,
		"description": ch.Description,
		"topic":       ch.Topic,
	})
}

// resolveChannelID resolves a channel ID from either channel_id or channel_name parameter.
func (ctr *ChannelToolRegistrar) resolveChannelID(ctx context.Context, req mcp.CallToolRequest) (int64, error) {
	if cid := req.GetInt("channel_id", 0); cid > 0 {
		return int64(cid), nil
	}

	name := req.GetString("channel_name", "")
	if name != "" {
		ch, err := ctr.channelService.GetChannelByName(ctx, name)
		if err != nil {
			return 0, err
		}
		return ch.ID, nil
	}

	return 0, fmt.Errorf("either 'channel_id' or 'channel_name' is required")
}
