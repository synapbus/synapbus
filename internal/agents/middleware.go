package agents

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/synapbus/synapbus/internal/apikeys"
	"github.com/synapbus/synapbus/internal/trace"
)

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
