package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
)

// ChannelsHandler handles REST API requests for channels.
type ChannelsHandler struct {
	channelService  *channels.Service
	agentService    *agents.AgentService
	msgService      *messaging.MessagingService
	reactionService ChannelReactionService
	logger          *slog.Logger
}

// ChannelReactionService is the subset of reactions.Service needed by ChannelsHandler.
type ChannelReactionService interface {
	ListByState(ctx context.Context, channelID int64, state string) ([]int64, error)
}

// NewChannelsHandler creates a new channels handler.
func NewChannelsHandler(channelService *channels.Service, agentService *agents.AgentService, msgService *messaging.MessagingService) *ChannelsHandler {
	return &ChannelsHandler{
		channelService: channelService,
		agentService:   agentService,
		msgService:     msgService,
		logger:         slog.Default().With("component", "api.channels"),
	}
}

// SetReactionService sets the reaction service for workflow state queries.
func (h *ChannelsHandler) SetReactionService(svc ChannelReactionService) {
	h.reactionService = svc
}

// ListChannels handles GET /api/channels.
func (h *ChannelsHandler) ListChannels(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	// Get the first owned agent to list channels visible to it
	ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
	if err != nil {
		h.logger.Error("list agents failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to list agents"))
		return
	}

	var allChannels []*channels.ChannelWithCount
	seen := make(map[int64]bool)

	for _, agent := range ownedAgents {
		chs, err := h.channelService.ListChannels(r.Context(), agent.Name)
		if err != nil {
			continue
		}
		for _, ch := range chs {
			if !seen[ch.ID] {
				seen[ch.ID] = true
				allChannels = append(allChannels, ch)
			}
		}
	}

	if allChannels == nil {
		allChannels = []*channels.ChannelWithCount{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"channels": allChannels})
}

// GetChannel handles GET /api/channels/{name}.
func (h *ChannelsHandler) GetChannel(w http.ResponseWriter, r *http.Request) {
	_, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	name := chi.URLParam(r, "name")
	ch, err := h.channelService.GetChannelByName(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Channel not found"))
		return
	}

	members, err := h.channelService.GetMembers(r.Context(), ch.ID)
	if err != nil {
		members = []*channels.Membership{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"channel": ch,
		"members": members,
	})
}

// CreateChannel handles POST /api/channels.
func (h *ChannelsHandler) CreateChannel(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Topic       string `json:"topic"`
		Type        string `json:"type"`
		IsPrivate   bool   `json:"is_private"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_request", "Invalid JSON body"))
		return
	}

	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_error", "Channel name is required"))
		return
	}

	// Use the first owned agent as the creator
	ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
	if err != nil || len(ownedAgents) == 0 {
		writeJSON(w, http.StatusBadRequest, errorBody("no_agents", "No agents registered. Register an agent first."))
		return
	}

	createReq := channels.CreateChannelRequest{
		Name:        req.Name,
		Description: req.Description,
		Topic:       req.Topic,
		Type:        req.Type,
		IsPrivate:   req.IsPrivate,
		CreatedBy:   ownedAgents[0].Name,
	}

	ch, err := h.channelService.CreateChannel(r.Context(), createReq)
	if err != nil {
		h.logger.Error("create channel failed", "error", err)
		writeJSON(w, http.StatusBadRequest, errorBody("create_failed", err.Error()))
		return
	}

	writeJSON(w, http.StatusCreated, ch)
}

// JoinChannel handles POST /api/channels/{name}/join.
func (h *ChannelsHandler) JoinChannel(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	name := chi.URLParam(r, "name")
	ch, err := h.channelService.GetChannelByName(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Channel not found"))
		return
	}

	var req struct {
		Agent string `json:"agent"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	// If no agent specified, use the first owned agent
	agentName := req.Agent
	if agentName == "" {
		ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
		if err != nil || len(ownedAgents) == 0 {
			writeJSON(w, http.StatusBadRequest, errorBody("no_agents", "No agents registered"))
			return
		}
		agentName = ownedAgents[0].Name
	}

	// Verify the agent belongs to this user
	agent, err := h.agentService.GetAgent(r.Context(), agentName)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_agent", "Agent not found"))
		return
	}
	if agent.OwnerID != ownerID {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not own this agent"))
		return
	}

	if err := h.channelService.JoinChannel(r.Context(), ch.ID, agentName); err != nil {
		h.logger.Error("join channel failed", "error", err)
		writeJSON(w, http.StatusBadRequest, errorBody("join_failed", err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "joined"})
}

// ChannelMessages handles GET /api/channels/{name}/messages.
func (h *ChannelsHandler) ChannelMessages(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	name := chi.URLParam(r, "name")
	ch, err := h.channelService.GetChannelByName(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Channel not found"))
		return
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	paginated, err := h.msgService.GetChannelMessages(r.Context(), ch.ID, limit, 0)
	if err != nil {
		h.logger.Error("get channel messages failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to get messages"))
		return
	}

	// Enrich messages with reply counts and attachments.
	h.msgService.EnrichMessages(r.Context(), paginated.Messages)

	// Compute last_read_message_id across owned agents
	var lastReadMessageID int64
	ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
	if err == nil {
		for _, agent := range ownedAgents {
			lr, err := h.msgService.GetLastReadForChannel(r.Context(), agent.Name, ch.ID)
			if err == nil && lr > lastReadMessageID {
				lastReadMessageID = lr
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"messages":             paginated.Messages,
		"total":                paginated.Total,
		"last_read_message_id": lastReadMessageID,
	})
}

// LeaveChannel handles POST /api/channels/{name}/leave.
func (h *ChannelsHandler) LeaveChannel(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	name := chi.URLParam(r, "name")
	ch, err := h.channelService.GetChannelByName(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Channel not found"))
		return
	}

	var req struct {
		Agent string `json:"agent"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	agentName := req.Agent
	if agentName == "" {
		ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
		if err != nil || len(ownedAgents) == 0 {
			writeJSON(w, http.StatusBadRequest, errorBody("no_agents", "No agents registered"))
			return
		}
		agentName = ownedAgents[0].Name
	}

	agent, err := h.agentService.GetAgent(r.Context(), agentName)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_agent", "Agent not found"))
		return
	}
	if agent.OwnerID != ownerID {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not own this agent"))
		return
	}

	if err := h.channelService.LeaveChannel(r.Context(), ch.ID, agentName); err != nil {
		h.logger.Error("leave channel failed", "error", err)
		writeJSON(w, http.StatusBadRequest, errorBody("leave_failed", err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "left"})
}

// UpdateSettings handles PUT /api/channels/{name}/settings.
func (h *ChannelsHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	_, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	name := chi.URLParam(r, "name")
	ch, err := h.channelService.GetChannelByName(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Channel not found"))
		return
	}

	var req struct {
		WorkflowEnabled        *bool    `json:"workflow_enabled"`
		AutoApprove            *bool    `json:"auto_approve"`
		StalemateRemindAfter   *string  `json:"stalemate_remind_after"`
		StalemateEscalateAfter *string  `json:"stalemate_escalate_after"`
		PublishThreshold       *float64 `json:"publish_threshold"`
		ApproveThreshold       *float64 `json:"approve_threshold"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_request", "Invalid JSON body"))
		return
	}

	settings := channels.ChannelSettings{
		WorkflowEnabled:        ch.WorkflowEnabled,
		AutoApprove:            ch.AutoApprove,
		StalemateRemindAfter:   ch.StalemateRemindAfter,
		StalemateEscalateAfter: ch.StalemateEscalateAfter,
		PublishThreshold:       ch.PublishThreshold,
		ApproveThreshold:       ch.ApproveThreshold,
	}

	if req.WorkflowEnabled != nil {
		settings.WorkflowEnabled = *req.WorkflowEnabled
	}
	if req.AutoApprove != nil {
		settings.AutoApprove = *req.AutoApprove
	}
	if req.StalemateRemindAfter != nil {
		settings.StalemateRemindAfter = *req.StalemateRemindAfter
	}
	if req.StalemateEscalateAfter != nil {
		settings.StalemateEscalateAfter = *req.StalemateEscalateAfter
	}
	if req.PublishThreshold != nil {
		settings.PublishThreshold = *req.PublishThreshold
	}
	if req.ApproveThreshold != nil {
		settings.ApproveThreshold = *req.ApproveThreshold
	}

	updated, err := h.channelService.UpdateChannelSettings(r.Context(), ch.ID, settings)
	if err != nil {
		h.logger.Error("update channel settings failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to update channel settings"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"channel": updated})
}

// ListByState handles GET /api/channels/{name}/messages/by-state?state=X.
func (h *ChannelsHandler) ListByState(w http.ResponseWriter, r *http.Request) {
	_, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	if h.reactionService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorBody("unavailable", "Reactions service not configured"))
		return
	}

	name := chi.URLParam(r, "name")
	ch, err := h.channelService.GetChannelByName(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Channel not found"))
		return
	}

	state := r.URL.Query().Get("state")
	if state == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("missing_state", "Query parameter 'state' is required"))
		return
	}

	ids, err := h.reactionService.ListByState(r.Context(), ch.ID, state)
	if err != nil {
		h.logger.Error("list by state failed", "error", err)
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_state", err.Error()))
		return
	}

	// Load messages by IDs
	var messages []*messaging.Message
	for _, id := range ids {
		msg, err := h.msgService.GetMessageByID(r.Context(), id)
		if err != nil {
			continue
		}
		messages = append(messages, msg)
	}

	if messages == nil {
		messages = []*messaging.Message{}
	}

	// Enrich messages with reactions, reply counts, attachments
	h.msgService.EnrichMessages(r.Context(), messages)

	writeJSON(w, http.StatusOK, map[string]any{
		"messages": messages,
		"state":    state,
		"total":    len(messages),
	})
}
