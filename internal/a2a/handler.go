package a2a

import (
	"context"
	"encoding/json"
	"net/http"
)

// AgentLister abstracts the operation of listing active non-human agents.
type AgentLister interface {
	ListAllActiveAgents(ctx context.Context) ([]AgentInfo, error)
}

// NewAgentCardHandler returns an http.HandlerFunc that serves the A2A Agent
// Card JSON document at /.well-known/agent-card.json.
//
// The handler is public (no auth required) because Agent Cards are meant for
// discovery. If configuredBaseURL is empty the base URL is derived from the
// incoming request (respecting X-Forwarded-* headers).
func NewAgentCardHandler(agentLister AgentLister, configuredBaseURL string, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Derive base URL from request if not configured.
		baseURL := configuredBaseURL
		if baseURL == "" {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
				scheme = proto
			}
			host := r.Host
			if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
				host = fwdHost
			}
			baseURL = scheme + "://" + host
		}

		agents, err := agentLister.ListAllActiveAgents(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		card := GenerateAgentCard(baseURL, version, agents)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=60")
		json.NewEncoder(w).Encode(card)
	}
}
