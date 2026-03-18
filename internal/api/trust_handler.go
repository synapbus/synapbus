package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/trust"
)

// TrustHandler handles REST API requests for agent trust scores.
type TrustHandler struct {
	trustService *trust.Service
	logger       *slog.Logger
}

// NewTrustHandler creates a new trust handler.
func NewTrustHandler(trustService *trust.Service) *TrustHandler {
	return &TrustHandler{
		trustService: trustService,
		logger:       slog.Default().With("component", "api.trust"),
	}
}

// GetScores handles GET /api/trust/{name}.
func (h *TrustHandler) GetScores(w http.ResponseWriter, r *http.Request) {
	agentName := chi.URLParam(r, "name")
	if agentName == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_name", "Agent name is required"))
		return
	}

	scores, err := h.trustService.GetScores(r.Context(), agentName)
	if err != nil {
		h.logger.Error("failed to get trust scores", "agent", agentName, "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("internal", "Failed to get trust scores"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scores": scores,
	})
}
