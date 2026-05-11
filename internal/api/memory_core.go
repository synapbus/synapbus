// REST endpoints for per-(owner, agent) core memory (feature 020 — US2).
// Surfaces the underlying messaging.CoreMemoryStore to the Web UI under
// `/api/owner/{ownerID}/agents/{agentName}/core-memory`.
//
// Auth: every handler enforces that the session-bound owner matches the
// path's `ownerID`. Cross-owner access yields 403 to avoid leaking the
// existence of another owner's resources.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/messaging"
)

// MemoryCoreHandler exposes GET/PUT/DELETE for the `memory_core` table.
type MemoryCoreHandler struct {
	store  *messaging.CoreMemoryStore
	logger *slog.Logger
}

// NewMemoryCoreHandler wires the handler. `store` must be non-nil.
func NewMemoryCoreHandler(store *messaging.CoreMemoryStore) *MemoryCoreHandler {
	return &MemoryCoreHandler{
		store:  store,
		logger: slog.Default().With("component", "api.memory-core"),
	}
}

// authorize resolves the URL `ownerID` and confirms it matches the
// session-bound owner. Returns the resolved owner string (matching the
// memory_core.owner_id TEXT format) plus the agent name on success.
func (h *MemoryCoreHandler) authorize(w http.ResponseWriter, r *http.Request) (ownerStr string, agentName string, ok bool) {
	sessionOwnerID, found := OwnerIDFromContext(r.Context())
	if !found {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return "", "", false
	}

	pathOwner := chi.URLParam(r, "ownerID")
	if pathOwner == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("bad_request", "ownerID is required"))
		return "", "", false
	}
	pathOwnerID, err := strconv.ParseInt(pathOwner, 10, 64)
	if err != nil || pathOwnerID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorBody("bad_request", "ownerID must be a positive integer"))
		return "", "", false
	}
	if pathOwnerID != sessionOwnerID {
		// Use 403, not 404, so the response shape matches other owner-scoped
		// handlers in this package (see agents_handler.GetAgent).
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this owner"))
		return "", "", false
	}

	agentName = chi.URLParam(r, "agentName")
	if agentName == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("bad_request", "agentName is required"))
		return "", "", false
	}

	return strconv.FormatInt(pathOwnerID, 10), agentName, true
}

// Get handles GET /api/owner/{ownerID}/agents/{agentName}/core-memory.
func (h *MemoryCoreHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorBody("unavailable", "core memory store not configured"))
		return
	}
	ownerStr, agentName, ok := h.authorize(w, r)
	if !ok {
		return
	}
	blob, updatedAt, exists, err := h.store.Get(r.Context(), ownerStr, agentName)
	if err != nil {
		h.logger.Error("memory_core get failed", "error", err, "owner", ownerStr, "agent", agentName)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to read core memory"))
		return
	}
	if !exists {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "core memory not set for this agent"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"owner_id":   ownerStr,
		"agent_name": agentName,
		"blob":       blob,
		"updated_at": updatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// Put handles PUT /api/owner/{ownerID}/agents/{agentName}/core-memory.
// Body: {"blob": "..."}. Returns 200 on success, 413 on
// core_memory_too_large, 400 on malformed body.
func (h *MemoryCoreHandler) Put(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorBody("unavailable", "core memory store not configured"))
		return
	}
	ownerStr, agentName, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var body struct {
		Blob      string `json:"blob"`
		UpdatedBy string `json:"updated_by,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("bad_request", "Invalid JSON body"))
		return
	}
	updatedBy := body.UpdatedBy
	if updatedBy == "" {
		updatedBy = "human"
	}
	if err := h.store.Set(r.Context(), ownerStr, agentName, body.Blob, updatedBy); err != nil {
		if errors.Is(err, messaging.ErrCoreMemoryTooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorBody(
				"core_memory_too_large",
				fmt.Sprintf("Blob exceeds %d bytes", h.store.MaxBytes()),
			))
			return
		}
		h.logger.Error("memory_core set failed", "error", err, "owner", ownerStr, "agent", agentName)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to write core memory"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"owner_id":   ownerStr,
		"agent_name": agentName,
		"blob_chars": len(body.Blob),
		"updated_by": updatedBy,
	})
}

// Delete handles DELETE /api/owner/{ownerID}/agents/{agentName}/core-memory.
// Returns 204 when a row existed and was removed, 404 when no row was
// present at the start of the call.
func (h *MemoryCoreHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorBody("unavailable", "core memory store not configured"))
		return
	}
	ownerStr, agentName, ok := h.authorize(w, r)
	if !ok {
		return
	}
	// Check existence so we can return the canonical 204 vs 404. The
	// store's Delete is idempotent — it never errors on missing rows.
	_, _, exists, err := h.store.Get(r.Context(), ownerStr, agentName)
	if err != nil {
		h.logger.Error("memory_core get-before-delete failed", "error", err, "owner", ownerStr, "agent", agentName)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to read core memory"))
		return
	}
	if !exists {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "core memory not set for this agent"))
		return
	}
	if err := h.store.Delete(r.Context(), ownerStr, agentName); err != nil {
		h.logger.Error("memory_core delete failed", "error", err, "owner", ownerStr, "agent", agentName)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to delete core memory"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
