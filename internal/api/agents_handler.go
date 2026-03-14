package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/trace"
)

// AgentsHandler handles REST API requests for agents.
type AgentsHandler struct {
	agentService *agents.AgentService
	traceStore   trace.TraceStore
	logger       *slog.Logger
}

// NewAgentsHandler creates a new agents handler.
func NewAgentsHandler(agentService *agents.AgentService, traceStore trace.TraceStore) *AgentsHandler {
	return &AgentsHandler{
		agentService: agentService,
		traceStore:   traceStore,
		logger:       slog.Default().With("component", "api.agents"),
	}
}

// ListAgents handles GET /api/agents.
func (h *AgentsHandler) ListAgents(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	agentList, err := h.agentService.ListAgents(r.Context(), ownerID)
	if err != nil {
		h.logger.Error("list agents failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to list agents"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"agents": agentList})
}

// GetAgent handles GET /api/agents/{name}.
func (h *AgentsHandler) GetAgent(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	name := chi.URLParam(r, "name")
	agent, err := h.agentService.GetAgent(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Agent not found"))
		return
	}

	if agent.OwnerID != ownerID {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this agent"))
		return
	}

	// Get recent traces for this agent
	var traces []trace.Trace
	if h.traceStore != nil {
		filter := trace.TraceFilter{
			AgentName: name,
			PageSize:  20,
			Page:      1,
		}
		traces, _, _ = h.traceStore.Query(r.Context(), filter)
	}
	if traces == nil {
		traces = []trace.Trace{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent":  agent,
		"traces": traces,
	})
}

// RegisterAgent handles POST /api/agents.
func (h *AgentsHandler) RegisterAgent(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	var req struct {
		Name         string          `json:"name"`
		DisplayName  string          `json:"display_name"`
		Type         string          `json:"type"`
		Capabilities json.RawMessage `json:"capabilities,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_request", "Invalid JSON body"))
		return
	}

	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_error", "Agent name is required"))
		return
	}

	agent, apiKey, err := h.agentService.Register(r.Context(), req.Name, req.DisplayName, req.Type, req.Capabilities, ownerID)
	if err != nil {
		h.logger.Error("register agent failed", "error", err)
		writeJSON(w, http.StatusBadRequest, errorBody("register_failed", err.Error()))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"agent":   agent,
		"api_key": apiKey,
	})
}

// UpdateAgent handles PUT /api/agents/{name}.
func (h *AgentsHandler) UpdateAgent(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	name := chi.URLParam(r, "name")

	// Verify ownership
	agent, err := h.agentService.GetAgent(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Agent not found"))
		return
	}
	if agent.OwnerID != ownerID {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this agent"))
		return
	}

	var req struct {
		DisplayName  string          `json:"display_name"`
		Capabilities json.RawMessage `json:"capabilities,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_request", "Invalid JSON body"))
		return
	}

	updated, err := h.agentService.UpdateAgent(r.Context(), name, req.DisplayName, req.Capabilities)
	if err != nil {
		h.logger.Error("update agent failed", "error", err)
		writeJSON(w, http.StatusBadRequest, errorBody("update_failed", err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"agent": updated})
}

// DeleteAgent handles DELETE /api/agents/{name}.
func (h *AgentsHandler) DeleteAgent(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	name := chi.URLParam(r, "name")

	if err := h.agentService.Deregister(r.Context(), name, ownerID); err != nil {
		h.logger.Error("deregister agent failed", "error", err)
		writeJSON(w, http.StatusBadRequest, errorBody("deregister_failed", err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deregistered"})
}

// RevokeKey handles POST /api/agents/{name}/revoke-key.
func (h *AgentsHandler) RevokeKey(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	name := chi.URLParam(r, "name")

	agent, newKey, err := h.agentService.RevokeKey(r.Context(), name, ownerID)
	if err != nil {
		h.logger.Error("revoke key failed", "error", err)
		writeJSON(w, http.StatusBadRequest, errorBody("revoke_failed", err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent":   agent,
		"api_key": newKey,
	})
}
