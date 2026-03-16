package agents

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ory/fosite"

	"github.com/synapbus/synapbus/internal/apikeys"
	"github.com/synapbus/synapbus/internal/trace"
)

// OAuthAgentResolver resolves an agent name from an OAuth bearer token.
// It returns the agent name stored in the token session, or empty string if none.
type OAuthAgentResolver interface {
	// ResolveAgentFromToken introspects a bearer token and returns the agent name
	// and owner ID if the token contains agent identity information.
	ResolveAgentFromToken(ctx context.Context, token string) (agentName string, ownerID string, ok bool)
}

type contextKey string

const (
	agentContextKey  contextKey = "agent"
	apiKeyContextKey contextKey = "api_key"
)

// AgentFromContext extracts the authenticated agent from the context.
func AgentFromContext(ctx context.Context) (*Agent, bool) {
	agent, ok := ctx.Value(agentContextKey).(*Agent)
	return agent, ok
}

// ContextWithAgent returns a new context with the agent set.
func ContextWithAgent(ctx context.Context, agent *Agent) context.Context {
	return context.WithValue(ctx, agentContextKey, agent)
}

// APIKeyFromContext extracts the API key metadata from the context.
func APIKeyFromContext(ctx context.Context) (*apikeys.APIKey, bool) {
	key, ok := ctx.Value(apiKeyContextKey).(*apikeys.APIKey)
	return key, ok
}

// ContextWithAPIKey returns a new context with the API key set.
func ContextWithAPIKey(ctx context.Context, key *apikeys.APIKey) context.Context {
	return context.WithValue(ctx, apiKeyContextKey, key)
}

// OptionalAuthMiddleware creates HTTP middleware that authenticates requests
// via API key if an Authorization header is present, but allows
// unauthenticated requests to pass through.
func OptionalAuthMiddleware(service *AgentService) func(http.Handler) http.Handler {
	return OptionalAuthMiddlewareWithAPIKeys(service, nil)
}

// OptionalAuthMiddlewareWithAPIKeys creates HTTP middleware that authenticates
// via agent API keys or the new managed API keys. Unauthenticated requests
// pass through for endpoints like MCP where some tools work without auth.
func OptionalAuthMiddlewareWithAPIKeys(service *AgentService, keyService *apikeys.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				next.ServeHTTP(w, r)
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				next.ServeHTTP(w, r)
				return
			}

			bearerToken := parts[1]

			// 1. Try existing agent API key auth
			agent, err := service.Authenticate(r.Context(), bearerToken)
			if err == nil {
				slog.Debug("agent authenticated (MCP)",
					"agent", agent.Name,
					"remote_addr", r.RemoteAddr,
				)
				ctx := ContextWithAgent(r.Context(), agent)
				ctx = trace.ContextWithOwnerID(ctx, fmt.Sprintf("%d", agent.OwnerID))
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 2. Try new managed API key auth (sb_ prefixed keys)
			if keyService != nil && strings.HasPrefix(bearerToken, "sb_") {
				apiKey, keyErr := keyService.Authenticate(r.Context(), bearerToken)
				if keyErr == nil {
					ctx := r.Context()
					ctx = ContextWithAPIKey(ctx, apiKey)
					ctx = trace.ContextWithOwnerID(ctx, fmt.Sprintf("%d", apiKey.UserID))

					// If the key has an agent_id, load and set the agent context
					if apiKey.AgentID != nil {
						agentByID, agentErr := service.GetAgentByID(r.Context(), *apiKey.AgentID)
						if agentErr == nil {
							ctx = ContextWithAgent(ctx, agentByID)
						}
					}

					slog.Debug("API key authenticated (MCP)",
						"key_id", apiKey.ID,
						"key_name", apiKey.Name,
						"agent_id", apiKey.AgentID,
						"remote_addr", r.RemoteAddr,
					)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			slog.Warn("MCP authentication failed",
				"remote_addr", r.RemoteAddr,
				"error", err,
			)
			http.Error(w, `{"error":"unauthorized","message":"Invalid API key"}`, http.StatusUnauthorized)
		})
	}
}

// RequiredAuthMiddlewareWithOAuth creates HTTP middleware that requires authentication
// via agent API keys, managed API keys, or OAuth bearer tokens.
// If no auth is provided, returns 401 with WWW-Authenticate header pointing to OAuth metadata.
// This is the required middleware for the /mcp route.
func RequiredAuthMiddlewareWithOAuth(service *AgentService, keyService *apikeys.Service, oauthProvider fosite.OAuth2Provider) func(http.Handler) http.Handler {
	// If no OAuth provider, fall back to required API-key-only middleware
	if oauthProvider == nil {
		return AuthMiddlewareWithAPIKeys(service, keyService)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="/.well-known/oauth-authorization-server"`)
				http.Error(w, `{"error":"unauthorized","message":"Authentication required. Use API key or OAuth 2.1 flow."}`, http.StatusUnauthorized)
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="/.well-known/oauth-authorization-server"`)
				http.Error(w, `{"error":"unauthorized","message":"Invalid Authorization header. Use Bearer token."}`, http.StatusUnauthorized)
				return
			}

			bearerToken := parts[1]

			// 1. Try existing agent API key auth
			agent, err := service.Authenticate(r.Context(), bearerToken)
			if err == nil {
				slog.Debug("agent authenticated (MCP)",
					"agent", agent.Name,
					"remote_addr", r.RemoteAddr,
				)
				ctx := ContextWithAgent(r.Context(), agent)
				ctx = trace.ContextWithOwnerID(ctx, fmt.Sprintf("%d", agent.OwnerID))
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 2. Try managed API key auth (sb_ prefixed keys)
			if keyService != nil && strings.HasPrefix(bearerToken, "sb_") {
				apiKey, keyErr := keyService.Authenticate(r.Context(), bearerToken)
				if keyErr == nil {
					ctx := r.Context()
					ctx = ContextWithAPIKey(ctx, apiKey)
					ctx = trace.ContextWithOwnerID(ctx, fmt.Sprintf("%d", apiKey.UserID))

					if apiKey.AgentID != nil {
						agentByID, agentErr := service.GetAgentByID(r.Context(), *apiKey.AgentID)
						if agentErr == nil {
							ctx = ContextWithAgent(ctx, agentByID)
						}
					}

					slog.Debug("API key authenticated (MCP)",
						"key_id", apiKey.ID,
						"key_name", apiKey.Name,
						"agent_id", apiKey.AgentID,
						"remote_addr", r.RemoteAddr,
					)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			// 3. Try OAuth bearer token (fallback for MCP OAuth 2.1 flow)
			if oauthProvider != nil {
				agentName, ownerID, ok := resolveOAuthToken(r.Context(), oauthProvider, bearerToken, service)
				if ok {
					slog.Debug("OAuth bearer token authenticated (MCP)",
						"agent", agentName,
						"remote_addr", r.RemoteAddr,
					)
					ctx := r.Context()
					if ownerID != "" {
						ctx = trace.ContextWithOwnerID(ctx, ownerID)
					}
					// Look up the agent by name and inject into context
					if agentName != "" {
						agentObj, agentErr := service.GetAgent(ctx, agentName)
						if agentErr == nil {
							ctx = ContextWithAgent(ctx, agentObj)
						}
					}
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			slog.Warn("MCP authentication failed",
				"remote_addr", r.RemoteAddr,
				"error", err,
			)
			http.Error(w, `{"error":"unauthorized","message":"Invalid API key or token"}`, http.StatusUnauthorized)
		})
	}
}

// resolveOAuthToken introspects an OAuth bearer token and extracts agent identity.
func resolveOAuthToken(ctx context.Context, provider fosite.OAuth2Provider, token string, service *AgentService) (agentName string, ownerID string, ok bool) {
	// Decouple from the HTTP request context so token introspection completes
	// even if the client disconnects (fixes "context canceled" errors during
	// concurrent MCP connections from claude.ai).
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	_, ar, err := provider.IntrospectToken(dbCtx, token, fosite.AccessToken, &oauthIntrospectSession{})
	if err != nil {
		return "", "", false
	}

	// Extract agent_name from session data (JSON)
	sess := ar.GetSession()
	if sess == nil {
		return "", "", false
	}

	// The session is stored as JSON in the database. We need to extract the agent_name.
	// Since we can't cast to fositeSession (it's in the auth package), we use the
	// Subject field which contains the username, and try to get agent_name via
	// the session's extra data.
	subject := sess.GetSubject()
	username := sess.GetUsername()
	_ = username

	// Try type assertion for our session type
	type agentNameGetter interface {
		GetAgentName() string
		GetUserID() int64
	}
	if ang, ok := sess.(agentNameGetter); ok {
		name := ang.GetAgentName()
		uid := ang.GetUserID()
		oid := ""
		if uid > 0 {
			oid = fmt.Sprintf("%d", uid)
		}
		return name, oid, name != ""
	}

	// Fallback: use subject as agent name if set
	if subject != "" {
		return subject, "", true
	}

	return "", "", false
}

// oauthIntrospectSession is a minimal fosite.Session for introspection.
// It mirrors the fositeSession in the auth package to allow JSON deserialization.
type oauthIntrospectSession struct {
	UserID       int64                          `json:"user_id"`
	Username     string                         `json:"username"`
	Subject      string                         `json:"subject"`
	AgentName    string                         `json:"agent_name,omitempty"`
	ExpiresAtMap map[fosite.TokenType]time.Time `json:"expires_at_map"`
}

func (s *oauthIntrospectSession) SetExpiresAt(key fosite.TokenType, exp time.Time) {
	if s.ExpiresAtMap == nil {
		s.ExpiresAtMap = make(map[fosite.TokenType]time.Time)
	}
	s.ExpiresAtMap[key] = exp
}

func (s *oauthIntrospectSession) GetExpiresAt(key fosite.TokenType) time.Time {
	if s.ExpiresAtMap == nil {
		return time.Time{}
	}
	return s.ExpiresAtMap[key]
}

func (s *oauthIntrospectSession) GetUsername() string { return s.Username }
func (s *oauthIntrospectSession) GetSubject() string  { return s.Subject }
func (s *oauthIntrospectSession) GetAgentName() string { return s.AgentName }
func (s *oauthIntrospectSession) GetUserID() int64     { return s.UserID }

func (s *oauthIntrospectSession) Clone() fosite.Session {
	expiresAtMap := make(map[fosite.TokenType]time.Time)
	for k, v := range s.ExpiresAtMap {
		expiresAtMap[k] = v
	}
	return &oauthIntrospectSession{
		UserID:       s.UserID,
		Username:     s.Username,
		Subject:      s.Subject,
		AgentName:    s.AgentName,
		ExpiresAtMap: expiresAtMap,
	}
}

// AuthMiddleware creates HTTP middleware that authenticates requests via API key.
func AuthMiddleware(service *AgentService) func(http.Handler) http.Handler {
	return AuthMiddlewareWithAPIKeys(service, nil)
}

// AuthMiddlewareWithAPIKeys creates HTTP middleware that authenticates requests
// via agent API keys or the new managed API keys. Authentication is required.
func AuthMiddlewareWithAPIKeys(service *AgentService, keyService *apikeys.Service) func(http.Handler) http.Handler {
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

			bearerToken := parts[1]

			// 1. Try existing agent API key auth
			agent, err := service.Authenticate(r.Context(), bearerToken)
			if err == nil {
				slog.Debug("agent authenticated",
					"agent", agent.Name,
					"remote_addr", r.RemoteAddr,
				)
				ctx := ContextWithAgent(r.Context(), agent)
				ctx = trace.ContextWithOwnerID(ctx, fmt.Sprintf("%d", agent.OwnerID))
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 2. Try new managed API key auth (sb_ prefixed keys)
			if keyService != nil && strings.HasPrefix(bearerToken, "sb_") {
				apiKey, keyErr := keyService.Authenticate(r.Context(), bearerToken)
				if keyErr == nil {
					ctx := r.Context()
					ctx = ContextWithAPIKey(ctx, apiKey)
					ctx = trace.ContextWithOwnerID(ctx, fmt.Sprintf("%d", apiKey.UserID))

					if apiKey.AgentID != nil {
						agentByID, agentErr := service.GetAgentByID(r.Context(), *apiKey.AgentID)
						if agentErr == nil {
							ctx = ContextWithAgent(ctx, agentByID)
						}
					}

					slog.Debug("API key authenticated",
						"key_id", apiKey.ID,
						"key_name", apiKey.Name,
						"agent_id", apiKey.AgentID,
						"remote_addr", r.RemoteAddr,
					)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			slog.Warn("authentication failed",
				"remote_addr", r.RemoteAddr,
				"error", err,
			)
			http.Error(w, `{"error":"unauthorized","message":"Invalid API key"}`, http.StatusUnauthorized)
		})
	}
}
