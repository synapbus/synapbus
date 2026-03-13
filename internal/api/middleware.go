// Package api provides REST API handlers for the SynapBus Web UI.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type contextKey string

const (
	ownerIDKey   contextKey = "owner_id"
	requestIDKey contextKey = "request_id"
)

// OwnerIDFromContext extracts the owner ID from the context.
func OwnerIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ownerIDKey).(int64)
	return id, ok
}

// ContextWithOwnerID stores the owner ID in the context.
func ContextWithOwnerID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ownerIDKey, id)
}

// RequestIDFromContext extracts the request ID from the context.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// RequestIDMiddleware generates a unique request ID per request and adds it to the context and slog.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := uuid.New().String()
		ctx := context.WithValue(r.Context(), requestIDKey, reqID)
		w.Header().Set("X-Request-ID", reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// LoggingMiddleware logs every HTTP request with structured fields.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(ww, r)

		reqID := RequestIDFromContext(r.Context())
		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", reqID,
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// OwnerAuthMiddleware is a simple middleware that extracts owner_id from an authenticated session.
// In the full system this would validate session tokens. For now it extracts from
// a header or query param for testing purposes. In production, this integrates with
// the auth/session system.
func OwnerAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check X-Owner-ID header (set by session middleware in production)
		ownerIDStr := r.Header.Get("X-Owner-ID")
		if ownerIDStr == "" {
			http.Error(w, `{"error":"unauthorized","message":"Authentication required"}`, http.StatusUnauthorized)
			return
		}

		var ownerID int64
		_, err := fmt.Sscan(ownerIDStr, &ownerID)
		if err != nil || ownerID <= 0 {
			http.Error(w, `{"error":"unauthorized","message":"Invalid owner ID"}`, http.StatusUnauthorized)
			return
		}

		ctx := ContextWithOwnerID(r.Context(), ownerID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
