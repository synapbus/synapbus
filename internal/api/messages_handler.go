package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/auth"
	"github.com/synapbus/synapbus/internal/messaging"
)

// MessagesHandler handles REST API requests for messages.
type MessagesHandler struct {
	msgService   *messaging.MessagingService
	agentService *agents.AgentService
	broadcaster  *SSEBroadcaster
	logger       *slog.Logger
}

// SetBroadcaster sets the event broadcaster for real-time SSE notifications.
func (h *MessagesHandler) SetBroadcaster(b *SSEBroadcaster) {
	h.broadcaster = b
}

// NewMessagesHandler creates a new messages handler.
func NewMessagesHandler(msgService *messaging.MessagingService, agentService *agents.AgentService) *MessagesHandler {
	return &MessagesHandler{
		msgService:   msgService,
		agentService: agentService,
		logger:       slog.Default().With("component", "api.messages"),
	}
}

// ListMessages handles GET /api/messages.
func (h *MessagesHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusOK, map[string]any{"messages": []*messaging.Message{}, "total": 0})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	status := r.URL.Query().Get("status")
	agentFilter := r.URL.Query().Get("agent")

	var allMessages []*messaging.Message
	for _, agent := range ownedAgents {
		if agentFilter != "" && agent.Name != agentFilter {
			continue
		}
		opts := messaging.ReadOptions{
			Limit:       limit,
			IncludeRead: true,
			Status:      status,
		}
		result, err := h.msgService.ReadInbox(r.Context(), agent.Name, opts)
		if err != nil {
			h.logger.Error("read inbox failed", "agent", agent.Name, "error", err)
			continue
		}
		allMessages = append(allMessages, result.Messages...)
	}

	if allMessages == nil {
		allMessages = []*messaging.Message{}
	}

	sortMessagesByTime(allMessages)
	if len(allMessages) > limit {
		allMessages = allMessages[:limit]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"messages": allMessages,
		"total":    len(allMessages),
	})
}

// GetMessage handles GET /api/messages/{id}.
func (h *MessagesHandler) GetMessage(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_id", "Invalid message ID"))
		return
	}

	msg, err := h.msgService.GetMessageByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Message not found"))
		return
	}

	if !h.isAgentOwnedBy(r, msg.FromAgent, ownerID) && !h.isAgentOwnedBy(r, msg.ToAgent, ownerID) {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this message"))
		return
	}

	writeJSON(w, http.StatusOK, msg)
}

// ListConversations handles GET /api/conversations.
func (h *MessagesHandler) ListConversations(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusOK, map[string]any{"conversations": []any{}})
		return
	}

	type convSummary struct {
		ID          int64  `json:"id"`
		Subject     string `json:"subject"`
		LastMessage string `json:"last_message"`
		LastAgent   string `json:"last_agent"`
		LastTime    string `json:"last_time"`
		MsgCount    int    `json:"message_count"`
	}

	convMap := make(map[int64]*convSummary)
	for _, agent := range ownedAgents {
		opts := messaging.ReadOptions{
			Limit:       100,
			IncludeRead: true,
		}
		result, err := h.msgService.ReadInbox(r.Context(), agent.Name, opts)
		if err != nil {
			continue
		}
		for _, msg := range result.Messages {
			existing, exists := convMap[msg.ConversationID]
			if !exists {
				convMap[msg.ConversationID] = &convSummary{
					ID:          msg.ConversationID,
					LastMessage: truncateStr(msg.Body, 100),
					LastAgent:   msg.FromAgent,
					LastTime:    msg.CreatedAt.Format(time.RFC3339),
					MsgCount:    1,
				}
			} else {
				existing.MsgCount++
				lt, _ := time.Parse(time.RFC3339, existing.LastTime)
				if msg.CreatedAt.After(lt) {
					existing.LastMessage = truncateStr(msg.Body, 100)
					existing.LastAgent = msg.FromAgent
					existing.LastTime = msg.CreatedAt.Format(time.RFC3339)
				}
			}
		}
	}

	conversations := make([]*convSummary, 0, len(convMap))
	for _, c := range convMap {
		conv, _, err := h.msgService.GetConversation(r.Context(), c.ID)
		if err == nil {
			c.Subject = conv.Subject
		}
		conversations = append(conversations, c)
	}

	writeJSON(w, http.StatusOK, map[string]any{"conversations": conversations})
}

// GetConversation handles GET /api/conversations/{id}.
func (h *MessagesHandler) GetConversation(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_id", "Invalid conversation ID"))
		return
	}

	conv, messages, err := h.msgService.GetConversation(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Conversation not found"))
		return
	}

	hasAccess := false
	for _, msg := range messages {
		if h.isAgentOwnedBy(r, msg.FromAgent, ownerID) || h.isAgentOwnedBy(r, msg.ToAgent, ownerID) {
			hasAccess = true
			break
		}
	}
	if !hasAccess && len(messages) > 0 {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this conversation"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"conversation": conv,
		"messages":     messages,
	})
}

// SendMessage handles POST /api/messages.
func (h *MessagesHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	var req struct {
		From           string `json:"from"`
		To             string `json:"to"`
		Body           string `json:"body"`
		Priority       int    `json:"priority"`
		ChannelID      *int64 `json:"channel_id,omitempty"`
		ConversationID *int64 `json:"conversation_id,omitempty"`
		Subject        string `json:"subject,omitempty"`
		ReplyTo        *int64 `json:"reply_to,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_request", "Invalid JSON body"))
		return
	}

	if req.Body == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_error", "Message body is required"))
		return
	}

	// For session-authenticated users (Web UI), always send as the human agent
	// regardless of what `from` was provided in the request.
	if _, isSession := auth.SessionIDFromContext(r.Context()); isSession {
		humanAgent, err := h.agentService.GetHumanAgentForUser(r.Context(), ownerID)
		if err != nil {
			h.logger.Error("get human agent failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to determine sender"))
			return
		}
		if humanAgent == nil {
			writeJSON(w, http.StatusBadRequest, errorBody("no_human_agent", "No human agent found. Please log in again to auto-create one."))
			return
		}
		req.From = humanAgent.Name
	} else if req.From == "" {
		// Non-session auth (API key, bearer token): fall back to finding an agent
		ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
		if err != nil || len(ownedAgents) == 0 {
			writeJSON(w, http.StatusBadRequest, errorBody("no_agents", "No agents registered. Register an agent first."))
			return
		}
		// Prefer human-type agent
		req.From = ownedAgents[0].Name
		for _, a := range ownedAgents {
			if a.Type == "human" {
				req.From = a.Name
				break
			}
		}
	}

	if !h.isAgentOwnedBy(r, req.From, ownerID) {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not own agent: "+req.From))
		return
	}

	opts := messaging.SendOptions{
		Priority:       req.Priority,
		ChannelID:      req.ChannelID,
		ConversationID: req.ConversationID,
		Subject:        req.Subject,
		ReplyTo:        req.ReplyTo,
	}

	msg, err := h.msgService.SendMessage(r.Context(), req.From, req.To, req.Body, opts)
	if err != nil {
		h.logger.Error("send message failed", "error", err)
		writeJSON(w, http.StatusBadRequest, errorBody("send_failed", err.Error()))
		return
	}

	// SSE broadcast is handled by the MessageListener on the messaging
	// service, so it fires for both REST and MCP message paths.

	writeJSON(w, http.StatusCreated, msg)
}

// MarkDone handles POST /api/messages/{id}/done.
func (h *MessagesHandler) MarkDone(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_id", "Invalid message ID"))
		return
	}

	msg, err := h.msgService.GetMessageByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Message not found"))
		return
	}

	agentToCheck := msg.ToAgent
	if msg.ClaimedBy != "" {
		agentToCheck = msg.ClaimedBy
	}
	if !h.isAgentOwnedBy(r, agentToCheck, ownerID) {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this message"))
		return
	}

	if msg.Status == messaging.StatusPending {
		_, _ = h.msgService.ClaimMessages(r.Context(), msg.ToAgent, 1)
		msg, err = h.msgService.GetMessageByID(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, errorBody("not_found", "Message not found"))
			return
		}
	}

	if msg.Status != messaging.StatusProcessing {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_status", "Message is not in processing status"))
		return
	}

	if err := h.msgService.MarkDone(r.Context(), id, msg.ClaimedBy); err != nil {
		h.logger.Error("mark done failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "done"})
}

// SearchMessages handles GET /api/messages/search.
func (h *MessagesHandler) SearchMessages(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("missing_query", "Search query 'q' is required"))
		return
	}

	ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
	if err != nil {
		h.logger.Error("list agents failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to list agents"))
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}

	var allMessages []*messaging.Message
	for _, agent := range ownedAgents {
		opts := messaging.SearchOptions{Limit: limit}
		result, err := h.msgService.SearchMessages(r.Context(), agent.Name, query, opts)
		if err != nil {
			continue
		}
		allMessages = append(allMessages, result.Messages...)
	}

	if allMessages == nil {
		allMessages = []*messaging.Message{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"messages": allMessages,
		"query":    query,
		"total":    len(allMessages),
	})
}

// GetReplies handles GET /api/messages/{id}/replies.
func (h *MessagesHandler) GetReplies(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_id", "Invalid message ID"))
		return
	}

	// Verify the parent message exists and user has access
	msg, err := h.msgService.GetMessageByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Message not found"))
		return
	}

	if !h.isAgentOwnedBy(r, msg.FromAgent, ownerID) && !h.isAgentOwnedBy(r, msg.ToAgent, ownerID) {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this message"))
		return
	}

	replies, err := h.msgService.GetReplies(r.Context(), id)
	if err != nil {
		h.logger.Error("get replies failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to get replies"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"replies": replies,
		"total":   len(replies),
	})
}

// DMMessages handles GET /api/agents/{name}/messages — returns DM messages with a specific agent.
func (h *MessagesHandler) DMMessages(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	peerAgent := chi.URLParam(r, "name")

	ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
	if err != nil {
		h.logger.Error("list agents failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to list agents"))
		return
	}

	if len(ownedAgents) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"messages": []*messaging.Message{}, "total": 0})
		return
	}

	agentNames := make([]string, len(ownedAgents))
	for i, a := range ownedAgents {
		agentNames[i] = a.Name
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}

	msgs, err := h.msgService.GetDMMessages(r.Context(), agentNames, peerAgent, limit)
	if err != nil {
		h.logger.Error("get dm messages failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to get messages"))
		return
	}

	// Include last_read_message_id for the human agent's DM with the peer
	lastRead, _ := h.msgService.GetLastReadForDM(r.Context(), agentNames, peerAgent)

	writeJSON(w, http.StatusOK, map[string]any{
		"messages":             msgs,
		"total":                len(msgs),
		"last_read_message_id": lastRead,
	})
}

func (h *MessagesHandler) isAgentOwnedBy(r *http.Request, agentName string, ownerID int64) bool {
	if agentName == "" {
		return false
	}
	agent, err := h.agentService.GetAgent(r.Context(), agentName)
	if err != nil {
		return false
	}
	return agent.OwnerID == ownerID
}

func sortMessagesByTime(msgs []*messaging.Message) {
	for i := 1; i < len(msgs); i++ {
		for j := i; j > 0 && msgs[j].CreatedAt.After(msgs[j-1].CreatedAt); j-- {
			msgs[j], msgs[j-1] = msgs[j-1], msgs[j]
		}
	}
}
