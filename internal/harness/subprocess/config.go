package subprocess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AgentConfig is the typed shape of an agent's harness_config_json
// when that agent targets the subprocess backend. All fields are
// optional — an empty config is valid and simply does nothing.
//
// This config is stored on the agent row (harness_config_json) and
// edited via the admin CLI:
//
//	synapbus harness config get --agent <name>
//	synapbus harness config set --agent <name> --file config.json
//
// At Execute time the harness copies these fields into the per-run
// workdir so Claude Code / Gemini / Codex CLIs can find them via the
// conventions they already use (CLAUDE.md / AGENTS.md / .mcp.json
// in cwd; skills under .claude/skills; subagents under .claude/agents).
type AgentConfig struct {
	// ClaudeMD is the content of CLAUDE.md written into workdir.
	ClaudeMD string `json:"claude_md,omitempty"`

	// AgentsMD is the content of AGENTS.md written into workdir. Used
	// by Codex / Gemini CLIs that follow the AGENTS.md convention.
	AgentsMD string `json:"agents_md,omitempty"`

	// GeminiMD is the content of GEMINI.md written into workdir. The
	// Gemini CLI reads this as the workspace system instructions.
	// When set, MaterialiseAgentConfig ALSO writes a matching
	// workdir/.gemini/settings.json containing the MCP servers below,
	// so `gemini` invoked from the workdir sees both the role prompt
	// and the agent's MCP tool surface in one step.
	GeminiMD string `json:"gemini_md,omitempty"`

	// MCPServers become the `mcpServers` object in workdir/.mcp.json.
	// Claude Code picks this up from cwd automatically; other CLIs
	// can be pointed at it with an explicit flag in local_command.
	MCPServers []MCPServerSpec `json:"mcp_servers,omitempty"`

	// Skills materialise as workdir/.claude/skills/<name>/SKILL.md.
	// Each entry's Content is written verbatim — typically a YAML
	// frontmatter block followed by markdown body, matching the
	// superpowers skill format.
	Skills []SkillSpec `json:"skills,omitempty"`

	// Subagents materialise as workdir/.claude/agents/<name>.md.
	Subagents []SubagentSpec `json:"subagents,omitempty"`

	// Env is an extra env-var map layered on top of the agent's
	// k8s_env_json and the caller-supplied Env. Last write wins, so
	// use this field for agent-specific overrides.
	Env map[string]string `json:"env,omitempty"`
}

// MCPServerSpec is one entry of the .mcp.json `mcpServers` object.
// We intentionally mirror Claude Code's shape so the marshalled file
// is drop-in compatible.
type MCPServerSpec struct {
	// Name is the key in the mcpServers object (e.g. "synapbus").
	Name string `json:"name"`

	// Type is the transport. Claude Code accepts "stdio" (default),
	// "http", and "sse". Empty = stdio.
	Type string `json:"type,omitempty"`

	// Stdio transport fields
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	// HTTP / SSE transport fields
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// Env is passed to the MCP server child (stdio only).
	Env map[string]string `json:"env,omitempty"`
}

// SkillSpec is one entry under .claude/skills.
type SkillSpec struct {
	Name    string `json:"name"`    // directory name — sanitised at write time
	Content string `json:"content"` // SKILL.md body (typically YAML + md)
}

// SubagentSpec is one entry under .claude/agents.
type SubagentSpec struct {
	Name    string `json:"name"`    // file basename — sanitised at write time
	Content string `json:"content"` // markdown body
}

// ParseAgentConfig tolerates empty / nil / invalid JSON. Empty yields
// a zero-value AgentConfig; invalid JSON returns an error so operators
// see the typo at Execute time rather than silently booting a
// misconfigured agent.
func ParseAgentConfig(raw string) (AgentConfig, error) {
	var cfg AgentConfig
	if raw == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, fmt.Errorf("subprocess: parse harness_config_json: %w", err)
	}
	return cfg, nil
}

// MaterialiseAgentConfig writes the config into workdir so the child
// process can find it. Existing files are overwritten; missing parent
// dirs are created. Returns the first write error encountered.
func MaterialiseAgentConfig(workdir string, cfg AgentConfig) error {
	if cfg.ClaudeMD != "" {
		if err := writeFile(filepath.Join(workdir, "CLAUDE.md"), cfg.ClaudeMD); err != nil {
			return err
		}
	}
	if cfg.AgentsMD != "" {
		if err := writeFile(filepath.Join(workdir, "AGENTS.md"), cfg.AgentsMD); err != nil {
			return err
		}
	}
	if cfg.GeminiMD != "" {
		if err := writeFile(filepath.Join(workdir, "GEMINI.md"), cfg.GeminiMD); err != nil {
			return err
		}
		// A workspace-level .gemini/settings.json with mcpServers
		// overrides the user's ~/.gemini/settings.json for MCP
		// discovery, so the agent sees exactly the servers the
		// operator configured here. We write an empty mcpServers
		// block even when MCPServers is empty — this explicitly
		// clears any inherited home-level servers.
		if err := writeGeminiSettings(workdir, cfg.MCPServers); err != nil {
			return err
		}
	}
	if len(cfg.MCPServers) > 0 {
		if err := writeMCPConfig(workdir, cfg.MCPServers); err != nil {
			return err
		}
	}
	for _, sk := range cfg.Skills {
		if err := writeSkill(workdir, sk); err != nil {
			return err
		}
	}
	for _, sa := range cfg.Subagents {
		if err := writeSubagent(workdir, sa); err != nil {
			return err
		}
	}
	return nil
}

// mcpConfigFile is the wire format of .mcp.json. We keep the outer
// struct small so marshalling produces exactly the shape Claude Code
// expects.
type mcpConfigFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// geminiSettingsFile is the shape Gemini CLI expects at
// .gemini/settings.json. We keep it minimal — only mcpServers — so we
// don't clobber unrelated settings the user might merge in later.
type geminiSettingsFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

func writeGeminiSettings(workdir string, servers []MCPServerSpec) error {
	out := geminiSettingsFile{MCPServers: map[string]mcpServerEntry{}}
	for _, s := range servers {
		if s.Name == "" {
			continue
		}
		out.MCPServers[s.Name] = mcpServerEntry{
			Type:    s.Type,
			Command: s.Command,
			Args:    s.Args,
			URL:     s.URL,
			Headers: s.Headers,
			Env:     s.Env,
		}
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("subprocess: marshal gemini settings: %w", err)
	}
	dir := filepath.Join(workdir, ".gemini")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("subprocess: mkdir .gemini: %w", err)
	}
	return writeFile(filepath.Join(dir, "settings.json"), string(raw))
}

func writeMCPConfig(workdir string, servers []MCPServerSpec) error {
	out := mcpConfigFile{MCPServers: map[string]mcpServerEntry{}}
	for _, s := range servers {
		if s.Name == "" {
			continue
		}
		out.MCPServers[s.Name] = mcpServerEntry{
			Type:    s.Type,
			Command: s.Command,
			Args:    s.Args,
			URL:     s.URL,
			Headers: s.Headers,
			Env:     s.Env,
		}
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("subprocess: marshal mcp config: %w", err)
	}
	return writeFile(filepath.Join(workdir, ".mcp.json"), string(raw))
}

func writeSkill(workdir string, sk SkillSpec) error {
	name := sanitizeChildName(sk.Name)
	if name == "" {
		return fmt.Errorf("subprocess: skill has empty name")
	}
	dir := filepath.Join(workdir, ".claude", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("subprocess: mkdir skill %q: %w", name, err)
	}
	return writeFile(filepath.Join(dir, "SKILL.md"), sk.Content)
}

func writeSubagent(workdir string, sa SubagentSpec) error {
	name := sanitizeChildName(sa.Name)
	if name == "" {
		return fmt.Errorf("subprocess: subagent has empty name")
	}
	dir := filepath.Join(workdir, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("subprocess: mkdir agents: %w", err)
	}
	return writeFile(filepath.Join(dir, name+".md"), sa.Content)
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("subprocess: mkdir %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("subprocess: write %q: %w", path, err)
	}
	return nil
}

// sanitizeChildName strips path separators and other characters that
// would escape the workdir. Keeps [A-Za-z0-9._-] only.
func sanitizeChildName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '.' || c == '_' || c == '-':
			out = append(out, c)
		}
	}
	return string(out)
}
