package subprocess_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapbus/synapbus/internal/harness/subprocess"
)

func TestParseAgentConfig_Empty(t *testing.T) {
	cfg, err := subprocess.ParseAgentConfig("")
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if cfg.ClaudeMD != "" || len(cfg.MCPServers) != 0 {
		t.Errorf("empty config not zero: %+v", cfg)
	}
}

func TestParseAgentConfig_InvalidJSON(t *testing.T) {
	_, err := subprocess.ParseAgentConfig("not-json")
	if err == nil {
		t.Fatal("expected parse error for invalid JSON")
	}
}

func TestParseAgentConfig_AllFields(t *testing.T) {
	raw := `{
		"claude_md": "You are X",
		"agents_md": "You are X (codex)",
		"mcp_servers": [
			{"name":"synapbus","type":"http","url":"http://kubic.home.arpa:30088/mcp","headers":{"Authorization":"Bearer abc"}},
			{"name":"local","command":"npx","args":["-y","@foo/mcp"],"env":{"KEY":"val"}}
		],
		"skills": [
			{"name":"brainstorming","content":"---\nname: brainstorming\n---\nbody"}
		],
		"subagents": [
			{"name":"researcher","content":"# researcher"}
		],
		"env": {"EXTRA":"yes"}
	}`
	cfg, err := subprocess.ParseAgentConfig(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.ClaudeMD != "You are X" {
		t.Errorf("claude_md = %q", cfg.ClaudeMD)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("mcp_servers len = %d", len(cfg.MCPServers))
	}
	if cfg.MCPServers[0].Type != "http" || cfg.MCPServers[0].URL == "" {
		t.Errorf("http mcp server: %+v", cfg.MCPServers[0])
	}
	if cfg.MCPServers[1].Command != "npx" || len(cfg.MCPServers[1].Args) != 2 {
		t.Errorf("stdio mcp server: %+v", cfg.MCPServers[1])
	}
	if len(cfg.Skills) != 1 || cfg.Skills[0].Name != "brainstorming" {
		t.Errorf("skills: %+v", cfg.Skills)
	}
	if len(cfg.Subagents) != 1 || cfg.Subagents[0].Name != "researcher" {
		t.Errorf("subagents: %+v", cfg.Subagents)
	}
	if cfg.Env["EXTRA"] != "yes" {
		t.Errorf("env: %+v", cfg.Env)
	}
}

func TestMaterialise_WritesAllArtifacts(t *testing.T) {
	workdir := t.TempDir()
	cfg := subprocess.AgentConfig{
		ClaudeMD: "# Agent\nYou are helpful.",
		AgentsMD: "# Agent (codex form)",
		MCPServers: []subprocess.MCPServerSpec{
			{Name: "synapbus", Type: "http", URL: "http://kubic:30088/mcp", Headers: map[string]string{"Authorization": "Bearer xxx"}},
			{Name: "filesystem", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}},
		},
		Skills: []subprocess.SkillSpec{
			{Name: "brainstorming", Content: "---\nname: brainstorming\n---\n\nBody"},
			{Name: "debugging", Content: "---\nname: debugging\n---\n\nBody2"},
		},
		Subagents: []subprocess.SubagentSpec{
			{Name: "researcher", Content: "# researcher agent"},
		},
	}
	if err := subprocess.MaterialiseAgentConfig(workdir, cfg); err != nil {
		t.Fatalf("materialise: %v", err)
	}

	// CLAUDE.md
	got, err := os.ReadFile(filepath.Join(workdir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(got), "You are helpful.") {
		t.Errorf("CLAUDE.md content = %q", got)
	}

	// AGENTS.md
	got, err = os.ReadFile(filepath.Join(workdir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md: %v", err)
	}
	if !strings.Contains(string(got), "codex form") {
		t.Errorf("AGENTS.md content = %q", got)
	}

	// .mcp.json — verify structure matches Claude Code expected shape
	raw, err := os.ReadFile(filepath.Join(workdir, ".mcp.json"))
	if err != nil {
		t.Fatalf(".mcp.json: %v", err)
	}
	var parsed struct {
		MCPServers map[string]struct {
			Type    string            `json:"type,omitempty"`
			Command string            `json:"command,omitempty"`
			Args    []string          `json:"args,omitempty"`
			URL     string            `json:"url,omitempty"`
			Headers map[string]string `json:"headers,omitempty"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	syn, ok := parsed.MCPServers["synapbus"]
	if !ok {
		t.Fatal(".mcp.json missing synapbus entry")
	}
	if syn.Type != "http" || syn.URL != "http://kubic:30088/mcp" {
		t.Errorf("synapbus entry = %+v", syn)
	}
	if syn.Headers["Authorization"] != "Bearer xxx" {
		t.Errorf("synapbus headers = %+v", syn.Headers)
	}
	fs, ok := parsed.MCPServers["filesystem"]
	if !ok {
		t.Fatal(".mcp.json missing filesystem entry")
	}
	if fs.Command != "npx" || len(fs.Args) != 3 {
		t.Errorf("filesystem entry = %+v", fs)
	}

	// .claude/skills/<name>/SKILL.md for each skill
	for _, name := range []string{"brainstorming", "debugging"} {
		p := filepath.Join(workdir, ".claude", "skills", name, "SKILL.md")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("skill %q not materialised: %v", name, err)
		}
	}

	// .claude/agents/<name>.md
	if _, err := os.Stat(filepath.Join(workdir, ".claude", "agents", "researcher.md")); err != nil {
		t.Errorf("subagent not materialised: %v", err)
	}
}

func TestMaterialise_EmptyConfigIsNoOp(t *testing.T) {
	workdir := t.TempDir()
	if err := subprocess.MaterialiseAgentConfig(workdir, subprocess.AgentConfig{}); err != nil {
		t.Fatalf("empty: %v", err)
	}
	entries, _ := os.ReadDir(workdir)
	if len(entries) != 0 {
		t.Errorf("empty config produced %d entries: %v", len(entries), entries)
	}
}

func TestMaterialise_SanitizesSkillNames(t *testing.T) {
	workdir := t.TempDir()
	cfg := subprocess.AgentConfig{
		Skills: []subprocess.SkillSpec{
			{Name: "../escape", Content: "x"},
			{Name: "/etc/passwd", Content: "x"},
			{Name: "ok-skill_1.0", Content: "x"},
		},
	}
	if err := subprocess.MaterialiseAgentConfig(workdir, cfg); err != nil {
		t.Fatalf("materialise: %v", err)
	}

	// The "../escape" should become "..escape" after sanitisation — no path escape.
	// Path traversal via cfg must NOT land outside workdir.
	parent := filepath.Dir(workdir)
	escaped, _ := os.ReadDir(parent)
	for _, e := range escaped {
		if e.Name() == "escape" || e.Name() == "etc" {
			t.Errorf("sanitisation failed: found %q outside workdir", e.Name())
		}
	}

	// Valid skill is present.
	if _, err := os.Stat(filepath.Join(workdir, ".claude", "skills", "ok-skill_1.0", "SKILL.md")); err != nil {
		t.Errorf("valid skill missing: %v", err)
	}
}

func TestMaterialise_GeminiMD_WritesSettings(t *testing.T) {
	workdir := t.TempDir()
	cfg := subprocess.AgentConfig{
		GeminiMD: "You are Gemini agent X.\nRespond concisely.",
		MCPServers: []subprocess.MCPServerSpec{
			{Name: "synapbus", URL: "http://localhost:18088/mcp"},
		},
	}
	if err := subprocess.MaterialiseAgentConfig(workdir, cfg); err != nil {
		t.Fatalf("materialise: %v", err)
	}

	// GEMINI.md
	got, err := os.ReadFile(filepath.Join(workdir, "GEMINI.md"))
	if err != nil {
		t.Fatalf("GEMINI.md: %v", err)
	}
	if !strings.Contains(string(got), "Respond concisely.") {
		t.Errorf("GEMINI.md content = %q", got)
	}

	// .gemini/settings.json
	raw, err := os.ReadFile(filepath.Join(workdir, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	var parsed struct {
		MCPServers map[string]struct {
			URL string `json:"url,omitempty"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	syn, ok := parsed.MCPServers["synapbus"]
	if !ok {
		t.Fatalf("settings.json missing synapbus entry: %s", raw)
	}
	if syn.URL != "http://localhost:18088/mcp" {
		t.Errorf("synapbus url = %q", syn.URL)
	}
}

func TestMaterialise_GeminiMD_WithoutMCP_WritesEmptyServers(t *testing.T) {
	workdir := t.TempDir()
	cfg := subprocess.AgentConfig{GeminiMD: "You are Gemini."}
	if err := subprocess.MaterialiseAgentConfig(workdir, cfg); err != nil {
		t.Fatalf("materialise: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(workdir, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	if !strings.Contains(string(raw), `"mcpServers"`) {
		t.Errorf("settings.json missing mcpServers key: %s", raw)
	}
}

func TestMaterialise_MCPServerWithoutName_Skipped(t *testing.T) {
	workdir := t.TempDir()
	cfg := subprocess.AgentConfig{
		MCPServers: []subprocess.MCPServerSpec{
			{Name: "", URL: "http://ignored"},
			{Name: "good", URL: "http://good"},
		},
	}
	if err := subprocess.MaterialiseAgentConfig(workdir, cfg); err != nil {
		t.Fatalf("materialise: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(workdir, ".mcp.json"))
	if !strings.Contains(string(raw), `"good"`) {
		t.Errorf("good entry missing: %s", raw)
	}
	if strings.Contains(string(raw), "ignored") {
		t.Errorf("empty-name entry should be dropped: %s", raw)
	}
}
