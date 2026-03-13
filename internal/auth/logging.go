package auth

import (
	"context"
	"log/slog"
	"net/http"
)

// Auth event type constants.
const (
	EventLoginSuccess     = "login_success"
	EventLoginFailure     = "login_failure"
	EventTokenIssued      = "token_issued"
	EventTokenRefreshed   = "token_refreshed"
	EventTokenRevoked     = "token_revoked"
	EventSessionCreated   = "session_created"
	EventSessionDestroyed = "session_destroyed"
	EventUserCreated      = "user_created"
	EventPasswordChanged  = "password_changed"
)

// AuthEvent represents a structured auth event for logging.
type AuthEvent struct {
	Type     string
	UserID   int64
	Username string
	ClientID string
	RemoteIP string
	Details  map[string]any
}

// LogAuthEvent logs an authentication event with structured fields.
func LogAuthEvent(ctx context.Context, logger *slog.Logger, event AuthEvent) {
	attrs := []any{
		"event", event.Type,
	}

	if event.UserID > 0 {
		attrs = append(attrs, "user_id", event.UserID)
	}
	if event.Username != "" {
		attrs = append(attrs, "username", event.Username)
	}
	if event.ClientID != "" {
		attrs = append(attrs, "client_id", event.ClientID)
	}
	if event.RemoteIP != "" {
		attrs = append(attrs, "remote_ip", event.RemoteIP)
	}
	for k, v := range event.Details {
		attrs = append(attrs, k, v)
	}

	logger.InfoContext(ctx, "auth event", attrs...)
}

// remoteIP extracts the remote IP from an HTTP request.
func remoteIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return forwarded
	}
	return r.RemoteAddr
}
