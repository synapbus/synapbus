package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/synapbus/synapbus/internal/trace"
)

// TracesHandler handles REST API requests for trace data.
type TracesHandler struct {
	store trace.TraceStore
}

// NewTracesHandler creates a new TracesHandler.
func NewTracesHandler(store trace.TraceStore) *TracesHandler {
	return &TracesHandler{store: store}
}

// ListTraces handles GET /api/traces.
func (h *TracesHandler) ListTraces(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	filter := trace.TraceFilter{
		OwnerID:   fmt.Sprintf("%d", ownerID),
		AgentName: r.URL.Query().Get("agent_name"),
		Action:    r.URL.Query().Get("action"),
	}

	if since := r.URL.Query().Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			http.Error(w, `{"error":"invalid 'since' parameter, expected ISO 8601 format"}`, http.StatusBadRequest)
			return
		}
		filter.Since = &t
	}

	if until := r.URL.Query().Get("until"); until != "" {
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			http.Error(w, `{"error":"invalid 'until' parameter, expected ISO 8601 format"}`, http.StatusBadRequest)
			return
		}
		filter.Until = &t
	}

	if page := r.URL.Query().Get("page"); page != "" {
		p, err := strconv.Atoi(page)
		if err == nil {
			filter.Page = p
		}
	}

	if pageSize := r.URL.Query().Get("page_size"); pageSize != "" {
		ps, err := strconv.Atoi(pageSize)
		if err == nil {
			filter.PageSize = ps
		}
	}

	traces, total, err := h.store.Query(r.Context(), filter)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"query failed: %s"}`, err), http.StatusInternalServerError)
		return
	}

	filter.Normalize()
	resp := map[string]any{
		"traces":    traces,
		"total":     total,
		"page":      filter.Page,
		"page_size": filter.PageSize,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// TraceStats handles GET /api/traces/stats.
func (h *TracesHandler) TraceStats(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	counts, err := h.store.CountByAction(r.Context(), fmt.Sprintf("%d", ownerID))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"stats query failed: %s"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"stats": counts,
	})
}
