package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
)

// NotificationsHandler handles REST API requests for notification badges.
type NotificationsHandler struct {
	msgService     *messaging.MessagingService
	agentService   *agents.AgentService
	channelService *channels.Service
	logger         *slog.Logger
}

// NewNotificationsHandler creates a new notifications handler.
func NewNotificationsHandler(msgService *messaging.MessagingService, agentService *agents.AgentService, channelService *channels.Service) *NotificationsHandler {
	return &NotificationsHandler{
		msgService:     msgService,
		agentService:   agentService,
		channelService: channelService,
		logger:         slog.Default().With("component", "api.notifications"),
	}
}

// channelUnread is the JSON shape for a channel's unread info.
type channelUnread struct {
	Name          string `json:"name"`
	UnreadCount   int    `json:"unread_count"`
	LastMessageID int64  `json:"last_message_id"`
}

// dmUnread is the JSON shape for a DM peer's unread info.
type dmUnread struct {
	Agent         string `json:"agent"`
	UnreadCount   int    `json:"unread_count"`
	LastMessageID int64  `json:"last_message_id"`
}

// UnreadCounts handles GET /api/notifications/unread.
func (h *NotificationsHandler) UnreadCounts(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
	if err != nil {
		h.logger.Error("list agents failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to list agents"))
		return
	}

	if len(ownedAgents) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"channels":     []channelUnread{},
			"dms":          []dmUnread{},
			"total_unread": 0,
		})
		return
	}

	// Aggregate channel summaries across all owned agents
	channelMap := make(map[string]*channelUnread)
	for _, agent := range ownedAgents {
		if h.channelService == nil {
			break
		}
		summaries, err := h.channelService.GetChannelSummaries(r.Context(), agent.Name)
		if err != nil {
			h.logger.Error("get channel summaries failed", "agent", agent.Name, "error", err)
			continue
		}
		for _, cs := range summaries {
			existing, ok := channelMap[cs.Name]
			if !ok {
				channelMap[cs.Name] = &channelUnread{
					Name:          cs.Name,
					UnreadCount:   cs.UnreadCount,
					LastMessageID: cs.LastMessageID,
				}
			} else {
				// Take the max unread count (different agents may see different counts)
				if cs.UnreadCount > existing.UnreadCount {
					existing.UnreadCount = cs.UnreadCount
				}
				if cs.LastMessageID > existing.LastMessageID {
					existing.LastMessageID = cs.LastMessageID
				}
			}
		}
	}

	channelsList := make([]channelUnread, 0, len(channelMap))
	for _, cu := range channelMap {
		channelsList = append(channelsList, *cu)
	}

	// Aggregate DM unread counts across all owned agents
	dmMap := make(map[string]*dmUnread)
	for _, agent := range ownedAgents {
		counts, err := h.msgService.GetDMUnreadCounts(r.Context(), agent.Name)
		if err != nil {
			h.logger.Error("get dm unread counts failed", "agent", agent.Name, "error", err)
			continue
		}
		for _, dc := range counts {
			existing, ok := dmMap[dc.Agent]
			if !ok {
				dmMap[dc.Agent] = &dmUnread{
					Agent:         dc.Agent,
					UnreadCount:   dc.UnreadCount,
					LastMessageID: dc.LastMessageID,
				}
			} else {
				existing.UnreadCount += dc.UnreadCount
				if dc.LastMessageID > existing.LastMessageID {
					existing.LastMessageID = dc.LastMessageID
				}
			}
		}
	}

	dmsList := make([]dmUnread, 0, len(dmMap))
	for _, du := range dmMap {
		dmsList = append(dmsList, *du)
	}

	totalUnread := 0
	for _, cu := range channelsList {
		totalUnread += cu.UnreadCount
	}
	for _, du := range dmsList {
		totalUnread += du.UnreadCount
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"channels":     channelsList,
		"dms":          dmsList,
		"total_unread": totalUnread,
	})
}

// MarkRead handles POST /api/notifications/mark-read.
func (h *NotificationsHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	var req struct {
		Type          string `json:"type"`
		Target        string `json:"target"`
		LastMessageID int64  `json:"last_message_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_request", "Invalid JSON body"))
		return
	}

	if req.Type == "" || req.Target == "" || req.LastMessageID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_error", "type, target, and last_message_id are required"))
		return
	}

	if req.Type != "channel" && req.Type != "dm" {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_error", "type must be 'channel' or 'dm'"))
		return
	}

	ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
	if err != nil {
		h.logger.Error("list agents failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to list agents"))
		return
	}

	if len(ownedAgents) == 0 {
		writeJSON(w, http.StatusBadRequest, errorBody("no_agents", "No agents registered"))
		return
	}

	agentNames := make([]string, len(ownedAgents))
	for i, a := range ownedAgents {
		agentNames[i] = a.Name
	}

	if req.Type == "channel" {
		if h.channelService == nil {
			writeJSON(w, http.StatusBadRequest, errorBody("not_available", "Channel service not available"))
			return
		}

		ch, err := h.channelService.GetChannelByName(r.Context(), req.Target)
		if err != nil {
			writeJSON(w, http.StatusNotFound, errorBody("not_found", "Channel not found"))
			return
		}

		// Get conversation IDs for messages in this channel up to the given message ID
		convIDs, err := h.msgService.GetConversationIDsForChannel(r.Context(), ch.ID, req.LastMessageID)
		if err != nil {
			h.logger.Error("get conversation ids failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to get conversations"))
			return
		}

		// Update inbox state for all owned agents on all relevant conversations
		for _, agentName := range agentNames {
			for _, convID := range convIDs {
				if err := h.msgService.UpdateInboxState(r.Context(), agentName, convID, req.LastMessageID); err != nil {
					h.logger.Error("update inbox state failed",
						"agent", agentName,
						"conversation_id", convID,
						"error", err,
					)
				}
			}
		}
	} else {
		// DM mark-read
		convIDs, err := h.msgService.GetConversationIDsForDM(r.Context(), agentNames, req.Target, req.LastMessageID)
		if err != nil {
			h.logger.Error("get dm conversation ids failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to get conversations"))
			return
		}

		for _, agentName := range agentNames {
			for _, convID := range convIDs {
				if err := h.msgService.UpdateInboxState(r.Context(), agentName, convID, req.LastMessageID); err != nil {
					h.logger.Error("update inbox state failed",
						"agent", agentName,
						"conversation_id", convID,
						"error", err,
					)
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
