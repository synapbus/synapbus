package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/synapbus/internal/apikeys"
)

// APIKeysHandler handles REST API requests for API key management.
type APIKeysHandler struct {
	keyService *apikeys.Service
	logger     *slog.Logger
}

// NewAPIKeysHandler creates a new API keys handler.
func NewAPIKeysHandler(keyService *apikeys.Service) *APIKeysHandler {
	return &APIKeysHandler{
		keyService: keyService,
		logger:     slog.Default().With("component", "api.apikeys"),
	}
}

// ListKeys handles GET /api/keys.
func (h *APIKeysHandler) ListKeys(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	keys, err := h.keyService.ListKeys(r.Context(), ownerID)
	if err != nil {
		h.logger.Error("list API keys failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to list API keys"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

// CreateKey handles POST /api/keys.
func (h *APIKeysHandler) CreateKey(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	var req struct {
		Name            string             `json:"name"`
		AgentID         *int64             `json:"agent_id,omitempty"`
		Permissions     apikeys.Permissions `json:"permissions"`
		AllowedChannels []string           `json:"allowed_channels"`
		ReadOnly        bool               `json:"read_only"`
		ExpiresAt       *string            `json:"expires_at,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_request", "Invalid JSON body"))
		return
	}

	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_error", "Key name is required"))
		return
	}

	createReq := apikeys.CreateKeyRequest{
		UserID:          ownerID,
		AgentID:         req.AgentID,
		Name:            req.Name,
		Permissions:     req.Permissions,
		AllowedChannels: req.AllowedChannels,
		ReadOnly:        req.ReadOnly,
	}

	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody("validation_error", "expires_at must be RFC3339 format"))
			return
		}
		createReq.ExpiresAt = &t
	}

	key, rawKey, err := h.keyService.CreateKey(r.Context(), createReq)
	if err != nil {
		h.logger.Error("create API key failed", "error", err)
		writeJSON(w, http.StatusBadRequest, errorBody("create_failed", err.Error()))
		return
	}

	// Build MCP config example
	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			"synapbus": map[string]any{
				"url": fmt.Sprintf("http://%s/mcp", r.Host),
				"headers": map[string]string{
					"Authorization": "Bearer " + rawKey,
				},
			},
		},
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"key":        key,
		"api_key":    rawKey,
		"mcp_config": mcpConfig,
	})
}

// GetKey handles GET /api/keys/{id}.
func (h *APIKeysHandler) GetKey(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_id", "Invalid key ID"))
		return
	}

	key, err := h.keyService.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "API key not found"))
		return
	}

	if key.UserID != ownerID {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this API key"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"key": key})
}

// RevokeKey handles DELETE /api/keys/{id}.
func (h *APIKeysHandler) RevokeKey(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_id", "Invalid key ID"))
		return
	}

	// Verify ownership
	key, err := h.keyService.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "API key not found"))
		return
	}

	if key.UserID != ownerID {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this API key"))
		return
	}

	if err := h.keyService.RevokeKey(r.Context(), id); err != nil {
		h.logger.Error("revoke API key failed", "error", err)
		writeJSON(w, http.StatusBadRequest, errorBody("revoke_failed", err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}
