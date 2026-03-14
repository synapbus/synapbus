package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/webhooks"
)

// WebhooksHandler handles REST API requests for webhooks and deliveries.
type WebhooksHandler struct {
	webhookService *webhooks.WebhookService
	webhookStore   webhooks.WebhookStore
	agentService   *agents.AgentService
	logger         *slog.Logger
}

// NewWebhooksHandler creates a new webhooks handler.
func NewWebhooksHandler(ws *webhooks.WebhookService, store webhooks.WebhookStore, agentService *agents.AgentService) *WebhooksHandler {
	return &WebhooksHandler{
		webhookService: ws,
		webhookStore:   store,
		agentService:   agentService,
		logger:         slog.Default().With("component", "api.webhooks"),
	}
}

// ListWebhooks handles GET /api/webhooks?agent={name}.
func (h *WebhooksHandler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
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

	agentFilter := r.URL.Query().Get("agent")

	var allWebhooks []*webhooks.Webhook
	for _, agent := range ownedAgents {
		if agentFilter != "" && agent.Name != agentFilter {
			continue
		}
		whs, err := h.webhookService.ListWebhooks(r.Context(), agent.Name)
		if err != nil {
			h.logger.Error("list webhooks failed", "agent", agent.Name, "error", err)
			continue
		}
		allWebhooks = append(allWebhooks, whs...)
	}

	if allWebhooks == nil {
		allWebhooks = []*webhooks.Webhook{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"webhooks": allWebhooks,
		"total":    len(allWebhooks),
	})
}

// WebhookDeliveries handles GET /api/webhooks/{id}/deliveries?status={status}&limit={n}.
func (h *WebhooksHandler) WebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	webhookID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_id", "Invalid webhook ID"))
		return
	}

	// Get the webhook to verify ownership
	wh, err := h.webhookStore.GetWebhookByID(r.Context(), webhookID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Webhook not found"))
		return
	}

	if !h.isAgentOwnedBy(r, wh.AgentName, ownerID) {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this webhook"))
		return
	}

	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	deliveries, err := h.webhookStore.GetDeliveriesByAgent(r.Context(), wh.AgentName, status, limit)
	if err != nil {
		h.logger.Error("get deliveries failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to get deliveries"))
		return
	}

	// Filter deliveries to only those belonging to this webhook
	var filtered []*webhooks.WebhookDelivery
	for _, d := range deliveries {
		if d.WebhookID == webhookID {
			filtered = append(filtered, d)
		}
	}
	if filtered == nil {
		filtered = []*webhooks.WebhookDelivery{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deliveries": filtered,
		"total":      len(filtered),
	})
}

// DeadLetters handles GET /api/deliveries/dead-letters?agent={name}&limit={n}.
func (h *WebhooksHandler) DeadLetters(w http.ResponseWriter, r *http.Request) {
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

	agentFilter := r.URL.Query().Get("agent")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	var allDeadLetters []*webhooks.WebhookDelivery
	for _, agent := range ownedAgents {
		if agentFilter != "" && agent.Name != agentFilter {
			continue
		}
		deliveries, err := h.webhookStore.GetDeliveriesByAgent(r.Context(), agent.Name, webhooks.DeliveryStatusDeadLettered, limit)
		if err != nil {
			h.logger.Error("get dead-lettered deliveries failed", "agent", agent.Name, "error", err)
			continue
		}
		allDeadLetters = append(allDeadLetters, deliveries...)
	}

	if allDeadLetters == nil {
		allDeadLetters = []*webhooks.WebhookDelivery{}
	}

	// Trim to limit
	if len(allDeadLetters) > limit {
		allDeadLetters = allDeadLetters[:limit]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"dead_letters": allDeadLetters,
		"total":        len(allDeadLetters),
	})
}

// RetryDelivery handles POST /api/deliveries/{id}/retry.
func (h *WebhooksHandler) RetryDelivery(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	deliveryID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_id", "Invalid delivery ID"))
		return
	}

	delivery, err := h.webhookStore.GetDeliveryByID(r.Context(), deliveryID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Delivery not found"))
		return
	}

	if !h.isAgentOwnedBy(r, delivery.AgentName, ownerID) {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this delivery"))
		return
	}

	if delivery.Status != webhooks.DeliveryStatusDeadLettered {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_status", "Only dead-lettered deliveries can be retried"))
		return
	}

	// Reset delivery to pending for re-processing
	err = h.webhookStore.UpdateDeliveryStatus(r.Context(), deliveryID, webhooks.DeliveryStatusPending, 0, "", nil, nil)
	if err != nil {
		h.logger.Error("retry delivery failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to retry delivery"))
		return
	}

	// Reset attempt counter
	if err := h.webhookStore.UpdateDeliveryAttempts(r.Context(), deliveryID, 0); err != nil {
		h.logger.Error("reset delivery attempts failed", "error", err)
		// Non-fatal: the status was already reset
	}

	writeJSON(w, http.StatusOK, map[string]any{"retried": true})
}

func (h *WebhooksHandler) isAgentOwnedBy(r *http.Request, agentName string, ownerID int64) bool {
	if agentName == "" {
		return false
	}
	agent, err := h.agentService.GetAgent(r.Context(), agentName)
	if err != nil {
		return false
	}
	return agent.OwnerID == ownerID
}
