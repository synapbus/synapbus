package mcp

import (
	"context"

	"github.com/synapbus/synapbus/internal/agents"
)

type mcpContextKey string

const agentNameKey mcpContextKey = "mcp_agent_name"

// AgentNameFromContext extracts the agent name from the MCP context.
func AgentNameFromContext(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(agentNameKey).(string)
	return name, ok
}

// ContextWithAgentName returns a new context with the agent name set.
func ContextWithAgentName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, agentNameKey, name)
}

// extractAgentName gets the agent name from the request context.
// It first checks for the MCP-level agent name, then falls back to the
// agents package context (set by HTTP auth middleware).
func extractAgentName(ctx context.Context) (string, bool) {
	if name, ok := AgentNameFromContext(ctx); ok {
		return name, true
	}
	if agent, ok := agents.AgentFromContext(ctx); ok {
		return agent.Name, true
	}
	return "", false
}
