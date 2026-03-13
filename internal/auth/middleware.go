package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ory/fosite"
)

type contextKey string

const (
	userContextKey   contextKey = "auth_user"
	clientContextKey contextKey = "auth_client"
	sessionContextKey contextKey = "auth_session_id"
)

// UserFromContext extracts the authenticated user from the context.
func UserFromContext(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(userContextKey).(*User)
	return user, ok
}

// ContextWithUser returns a new context with the user set.
func ContextWithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// SessionIDFromContext extracts the session ID from the context.
func SessionIDFromContext(ctx context.Context) (string, bool) {
	sid, ok := ctx.Value(sessionContextKey).(string)
	return sid, ok
}

// ContextWithSessionID returns a new context with the session ID set.
func ContextWithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionContextKey, sessionID)
}

// ClientFromContext extracts the authenticated client identity from the context.
func ClientFromContext(ctx context.Context) (string, bool) {
	cid, ok := ctx.Value(clientContextKey).(string)
	return cid, ok
}

// ContextWithClient returns a new context with the client ID set.
func ContextWithClient(ctx context.Context, clientID string) context.Context {
	return context.WithValue(ctx, clientContextKey, clientID)
}

// SessionCookieName is the name of the session cookie.
const SessionCookieName = "synapbus_session"

// RequireSession creates middleware that checks for a valid session cookie.
// If valid, it injects the user into the context. If not, returns 401.
func RequireSession(userStore UserStore, sessionStore SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				http.Error(w, `{"error":"unauthorized","message":"No session cookie"}`, http.StatusUnauthorized)
				return
			}

			session, err := sessionStore.GetSession(r.Context(), cookie.Value)
			if err != nil {
				slog.Debug("session lookup failed", "error", err)
				http.Error(w, `{"error":"unauthorized","message":"Invalid or expired session"}`, http.StatusUnauthorized)
				return
			}

			user, err := userStore.GetUserByID(r.Context(), session.UserID)
			if err != nil {
				slog.Error("user lookup failed for session", "user_id", session.UserID, "error", err)
				http.Error(w, `{"error":"unauthorized","message":"User not found"}`, http.StatusUnauthorized)
				return
			}

			ctx := ContextWithUser(r.Context(), user)
			ctx = ContextWithSessionID(ctx, session.SessionID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireBearer creates middleware that validates an OAuth access token.
// If valid, it injects the user/client identity into the context.
func RequireBearer(provider fosite.OAuth2Provider, userStore UserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error":"unauthorized","message":"Missing Authorization header"}`, http.StatusUnauthorized)
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				http.Error(w, `{"error":"unauthorized","message":"Invalid Authorization header format"}`, http.StatusUnauthorized)
				return
			}

			token := parts[1]
			_ = token

			// Use fosite introspection
			_, ar, err := provider.IntrospectToken(r.Context(), parts[1], fosite.AccessToken, new(fositeSession))
			if err != nil {
				slog.Debug("bearer token validation failed", "error", err)
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				http.Error(w, `{"error":"unauthorized","message":"Invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			ctx := r.Context()
			ctx = ContextWithClient(ctx, ar.GetClient().GetID())

			// If the token has a user session, load the user
			if sess, ok := ar.GetSession().(*fositeSession); ok && sess.UserID > 0 {
				user, err := userStore.GetUserByID(ctx, sess.UserID)
				if err == nil {
					ctx = ContextWithUser(ctx, user)
				}
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth creates middleware that accepts either a session cookie or bearer token.
func RequireAuth(userStore UserStore, sessionStore SessionStore, provider fosite.OAuth2Provider) func(http.Handler) http.Handler {
	sessionMW := RequireSession(userStore, sessionStore)
	bearerMW := RequireBearer(provider, userStore)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check for bearer token first
			if r.Header.Get("Authorization") != "" {
				bearerMW(next).ServeHTTP(w, r)
				return
			}
			// Fall back to session cookie
			sessionMW(next).ServeHTTP(w, r)
		})
	}
}

// RequireAdmin creates middleware that requires the user to have admin role.
// Must be used after RequireSession or RequireAuth.
func RequireAdmin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok {
				http.Error(w, `{"error":"unauthorized","message":"Authentication required"}`, http.StatusUnauthorized)
				return
			}
			if user.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden","message":"Admin access required"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
