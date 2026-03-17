package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/synapbus/synapbus/internal/push"
)

// PushHandler manages Web Push notification subscription endpoints.
type PushHandler struct {
	pushService *push.Service
	logger      *slog.Logger
}

// NewPushHandler creates a new push notification handler.
func NewPushHandler(pushService *push.Service) *PushHandler {
	return &PushHandler{
		pushService: pushService,
		logger:      slog.Default().With("component", "api.push"),
	}
}

// subscribeRequest is the JSON body for POST /api/push/subscribe.
type subscribeRequest struct {
	Endpoint  string `json:"endpoint"`
	KeyP256dh string `json:"key_p256dh"`
	KeyAuth   string `json:"key_auth"`
}

// unsubscribeRequest is the JSON body for DELETE /api/push/subscribe.
type unsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

// Subscribe handles POST /api/push/subscribe.
// Registers a Web Push subscription for the authenticated user.
func (h *PushHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	var req subscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_request", "Invalid JSON body"))
		return
	}

	if req.Endpoint == "" || req.KeyP256dh == "" || req.KeyAuth == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_error", "endpoint, key_p256dh, and key_auth are required"))
		return
	}

	userAgent := r.Header.Get("User-Agent")

	if err := h.pushService.Subscribe(r.Context(), ownerID, req.Endpoint, req.KeyP256dh, req.KeyAuth, userAgent); err != nil {
		h.logger.Error("push subscribe failed",
			"user_id", ownerID,
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to register subscription"))
		return
	}

	h.logger.Info("push subscription registered",
		"user_id", ownerID,
	)

	writeJSON(w, http.StatusOK, map[string]string{"status": "subscribed"})
}

// Unsubscribe handles DELETE /api/push/subscribe.
// Removes a Web Push subscription for the authenticated user.
func (h *PushHandler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	var req unsubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_request", "Invalid JSON body"))
		return
	}

	if req.Endpoint == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_error", "endpoint is required"))
		return
	}

	if err := h.pushService.Unsubscribe(r.Context(), ownerID, req.Endpoint); err != nil {
		h.logger.Error("push unsubscribe failed",
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to remove subscription"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
}

// VAPIDKey handles GET /api/push/vapid-key.
// Returns the VAPID public key needed by clients to subscribe to push notifications.
func (h *PushHandler) VAPIDKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"vapid_public_key": h.pushService.GetVAPIDPublicKey(),
	})
}
