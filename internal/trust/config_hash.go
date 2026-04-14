package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// AgentConfig captures the immutable inputs that define an agent's identity
// for the dynamic-spawning trust model. Two agents with the same canonical
// AgentConfig share a config_hash and therefore share reputation.
type AgentConfig struct {
	Model        string         `json:"model"`
	SystemPrompt string         `json:"system_prompt"`
	ToolScope    []string       `json:"tool_scope"`
	Skills       []string       `json:"skills"`
	Subagents    []string       `json:"subagents"`
	MCPServers   []MCPServerRef `json:"mcp_servers"`
}

// MCPServerRef describes a single MCP server attached to an agent.
type MCPServerRef struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Transport string `json:"transport"`
}

// ConfigHash returns the hex-encoded SHA-256 of the canonical-JSON
// representation of cfg.
//
// The function is deterministic: the input slices may be in any order,
// the result depends only on the multiset of values. Object keys are sorted
// (encoding/json already does this) and slice contents are sorted alphabetically
// before hashing so that two equivalent configs always produce the same hash.
func ConfigHash(cfg AgentConfig) string {
	tools := append([]string(nil), cfg.ToolScope...)
	sort.Strings(tools)
	skills := append([]string(nil), cfg.Skills...)
	sort.Strings(skills)
	subagents := append([]string(nil), cfg.Subagents...)
	sort.Strings(subagents)

	servers := make([]map[string]string, 0, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		servers = append(servers, map[string]string{
			"name":      s.Name,
			"url":       s.URL,
			"transport": s.Transport,
		})
	}
	sort.Slice(servers, func(i, j int) bool {
		return servers[i]["name"] < servers[j]["name"]
	})

	canonical := map[string]any{
		"model":         cfg.Model,
		"system_prompt": cfg.SystemPrompt,
		"tool_scope":    tools,
		"skills":        skills,
		"subagents":     subagents,
		"mcp_servers":   servers,
	}

	// encoding/json sorts map keys alphabetically, giving canonical output.
	data, err := json.Marshal(canonical)
	if err != nil {
		// Marshalling a map of strings/slices cannot fail in practice;
		// fall back to a stable sentinel hash so callers never see a panic.
		sum := sha256.Sum256([]byte("synapbus:trust:config_hash:marshal_error"))
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
