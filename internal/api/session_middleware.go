package api

import (
	"net/http"

	"github.com/synapbus/synapbus/internal/auth"
)

// SessionToOwnerMiddleware wraps RequireSession and extracts the owner ID
// into the API context so that API handlers can use OwnerIDFromContext.
func SessionToOwnerMiddleware(userStore auth.UserStore, sessionStore auth.SessionStore) func(http.Handler) http.Handler {
	sessionMW := auth.RequireSession(userStore, sessionStore)

	return func(next http.Handler) http.Handler {
		return sessionMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok {
				http.Error(w, `{"error":"unauthorized","message":"Authentication required"}`, http.StatusUnauthorized)
				return
			}
			ctx := ContextWithOwnerID(r.Context(), user.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		}))
	}
}
