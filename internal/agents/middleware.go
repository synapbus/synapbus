package agents

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

type contextKey string

const agentContextKey contextKey = "agent"

// AgentFromContext extracts the authenticated agent from the context.
func AgentFromContext(ctx context.Context) (*Agent, bool) {
	agent, ok := ctx.Value(agentContextKey).(*Agent)
	return agent, ok
}

// ContextWithAgent returns a new context with the agent set.
func ContextWithAgent(ctx context.Context, agent *Agent) context.Context {
	return context.WithValue(ctx, agentContextKey, agent)
}

// AuthMiddleware creates HTTP middleware that authenticates requests via API key.
func AuthMiddleware(service *AgentService) func(http.Handler) http.Handler {
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

			apiKey := parts[1]
			agent, err := service.Authenticate(r.Context(), apiKey)
			if err != nil {
				slog.Warn("authentication failed",
					"remote_addr", r.RemoteAddr,
					"error", err,
				)
				http.Error(w, `{"error":"unauthorized","message":"Invalid API key"}`, http.StatusUnauthorized)
				return
			}

			slog.Debug("agent authenticated",
				"agent", agent.Name,
				"remote_addr", r.RemoteAddr,
			)

			ctx := ContextWithAgent(r.Context(), agent)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
