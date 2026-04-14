package trust

import (
	"testing"
)

func baseConfig() AgentConfig {
	return AgentConfig{
		Model:        "claude-opus-4",
		SystemPrompt: "You are a careful research assistant.",
		ToolScope:    []string{"search", "fetch", "summarize"},
		Skills:       []string{"writing", "research"},
		Subagents:    []string{"reviewer", "critic"},
		MCPServers: []MCPServerRef{
			{Name: "synapbus", URL: "http://localhost:8080/mcp", Transport: "http"},
			{Name: "fs", URL: "stdio://fs", Transport: "stdio"},
		},
	}
}

func TestConfigHash_Deterministic(t *testing.T) {
	canonical := baseConfig()
	want := ConfigHash(canonical)

	tests := []struct {
		name  string
		mutate func(*AgentConfig)
	}{
		{
			name: "shuffled tool_scope",
			mutate: func(c *AgentConfig) {
				c.ToolScope = []string{"summarize", "fetch", "search"}
			},
		},
		{
			name: "shuffled skills",
			mutate: func(c *AgentConfig) {
				c.Skills = []string{"research", "writing"}
			},
		},
		{
			name: "shuffled subagents",
			mutate: func(c *AgentConfig) {
				c.Subagents = []string{"critic", "reviewer"}
			},
		},
		{
			name: "shuffled mcp_servers",
			mutate: func(c *AgentConfig) {
				c.MCPServers = []MCPServerRef{
					{Name: "fs", URL: "stdio://fs", Transport: "stdio"},
					{Name: "synapbus", URL: "http://localhost:8080/mcp", Transport: "http"},
				}
			},
		},
		{
			name: "shuffled all collections",
			mutate: func(c *AgentConfig) {
				c.ToolScope = []string{"fetch", "summarize", "search"}
				c.Skills = []string{"research", "writing"}
				c.Subagents = []string{"critic", "reviewer"}
				c.MCPServers = []MCPServerRef{
					{Name: "fs", URL: "stdio://fs", Transport: "stdio"},
					{Name: "synapbus", URL: "http://localhost:8080/mcp", Transport: "http"},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig()
			tt.mutate(&cfg)
			got := ConfigHash(cfg)
			if got != want {
				t.Errorf("ConfigHash mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func TestConfigHash_SensitiveToChanges(t *testing.T) {
	base := baseConfig()
	baseHash := ConfigHash(base)

	tests := []struct {
		name   string
		mutate func(*AgentConfig)
	}{
		{
			name:   "model changed",
			mutate: func(c *AgentConfig) { c.Model = "claude-sonnet-4" },
		},
		{
			name:   "system prompt changed",
			mutate: func(c *AgentConfig) { c.SystemPrompt = "You are a sloppy assistant." },
		},
		{
			name:   "tool added",
			mutate: func(c *AgentConfig) { c.ToolScope = append(c.ToolScope, "execute") },
		},
		{
			name:   "tool removed",
			mutate: func(c *AgentConfig) { c.ToolScope = []string{"search", "fetch"} },
		},
		{
			name:   "tool renamed",
			mutate: func(c *AgentConfig) { c.ToolScope = []string{"search", "fetch", "summarise"} },
		},
		{
			name: "mcp server url changed",
			mutate: func(c *AgentConfig) {
				c.MCPServers[0].URL = "http://kubic.home.arpa:30088/mcp"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig()
			tt.mutate(&cfg)
			got := ConfigHash(cfg)
			if got == baseHash {
				t.Errorf("expected different hash, both = %s", got)
			}
		})
	}
}

func TestConfigHash_EmptyFieldsStable(t *testing.T) {
	empty := AgentConfig{}
	h1 := ConfigHash(empty)
	h2 := ConfigHash(AgentConfig{
		ToolScope:  []string{},
		Skills:     []string{},
		Subagents:  []string{},
		MCPServers: []MCPServerRef{},
	})
	if h1 != h2 {
		t.Errorf("nil and empty slice configs should hash equal\n h1: %s\n h2: %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex sha256, got len=%d", len(h1))
	}
}
