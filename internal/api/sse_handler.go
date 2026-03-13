package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// SSEEvent represents a server-sent event.
type SSEEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// SSEHub manages SSE client connections and event broadcasting.
type SSEHub struct {
	mu      sync.RWMutex
	clients map[int64]map[chan SSEEvent]struct{} // ownerID -> set of channels
	nextID  int64
	logger  *slog.Logger
}

// NewSSEHub creates a new SSE hub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[int64]map[chan SSEEvent]struct{}),
		logger:  slog.Default().With("component", "api.sse"),
	}
}

// Broadcast sends an event to all clients for the given owner.
func (h *SSEHub) Broadcast(ownerID int64, event SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	clientSet, ok := h.clients[ownerID]
	if !ok {
		return
	}

	for ch := range clientSet {
		select {
		case ch <- event:
		default:
			// Client channel full, skip
		}
	}
}

// BroadcastAll sends an event to all connected clients.
func (h *SSEHub) BroadcastAll(event SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, clientSet := range h.clients {
		for ch := range clientSet {
			select {
			case ch <- event:
			default:
			}
		}
	}
}

func (h *SSEHub) addClient(ownerID int64) chan SSEEvent {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan SSEEvent, 32)
	if _, ok := h.clients[ownerID]; !ok {
		h.clients[ownerID] = make(map[chan SSEEvent]struct{})
	}
	h.clients[ownerID][ch] = struct{}{}

	h.logger.Info("SSE client connected", "owner_id", ownerID)
	return ch
}

func (h *SSEHub) removeClient(ownerID int64, ch chan SSEEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if clientSet, ok := h.clients[ownerID]; ok {
		delete(clientSet, ch)
		if len(clientSet) == 0 {
			delete(h.clients, ownerID)
		}
	}
	close(ch)

	h.logger.Info("SSE client disconnected", "owner_id", ownerID)
}

// HandleEvents handles GET /api/events — the SSE endpoint.
func (h *SSEHub) HandleEvents(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := h.addClient(ownerID)
	defer h.removeClient(ownerID, ch)

	// Send initial connected event
	writeSSE(w, flusher, "connected", map[string]any{
		"owner_id":  ownerID,
		"timestamp": time.Now().Format(time.RFC3339),
	})

	// Heartbeat ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, flusher, event.Type, event.Data)
		case <-ticker.C:
			writeSSE(w, flusher, "heartbeat", map[string]any{
				"timestamp": time.Now().Format(time.RFC3339),
			})
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, eventType string, data any) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(jsonData))
	flusher.Flush()
}
