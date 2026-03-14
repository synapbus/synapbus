package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
)

// ChannelsHandler handles REST API requests for channels.
type ChannelsHandler struct {
	channelService *channels.Service
	agentService   *agents.AgentService
	msgService     *messaging.MessagingService
	logger         *slog.Logger
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
