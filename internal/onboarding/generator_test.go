package onboarding

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateCLAUDEMD_Researcher(t *testing.T) {
	config := GeneratorConfig{
		AgentName:   "test-bot",
		Archetype:   "researcher",
		OwnerName:   "alice",
		SynapBusURL: "http://localhost:8080",
	}

	md, err := GenerateCLAUDEMD(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check common sections
	checks := []string{
		"# test-bot",
		"Startup Loop",
		"Reactions",
		"Trust",
		"Research & Discovery",
	}
	for _, check := range checks {
		if !strings.Contains(md, check) {
			t.Errorf("expected CLAUDE.md to contain %q", check)
		}
	}

	// Check researcher-specific sections
	researcherChecks := []string{
		"Research & Discovery",
		"Web Search",
		"Finding Deduplication",
	}
	for _, check := range researcherChecks {
		if !strings.Contains(md, check) {
			t.Errorf("expected CLAUDE.md to contain researcher section %q", check)
		}
	}
}

func TestGenerateCLAUDEMD_AllArchetypes(t *testing.T) {
	archetypes := ListArchetypes()
	for _, archetype := range archetypes {
		t.Run(archetype.Name, func(t *testing.T) {
			config := GeneratorConfig{
				AgentName:   "test-agent",
				Archetype:   archetype.Name,
				OwnerName:   "owner",
				SynapBusURL: "http://localhost:8080",
			}

			md, err := GenerateCLAUDEMD(config)
			if err != nil {
				t.Fatalf("unexpected error for archetype %s: %v", archetype.Name, err)
			}

			if !strings.Contains(md, "# test-agent") {
				t.Error("expected agent name in output")
			}
			if !strings.Contains(md, "Startup Loop") {
				t.Error("expected common sections in output")
			}
		})
	}
}

func TestGenerateCLAUDEMD_UnknownArchetype(t *testing.T) {
	config := GeneratorConfig{
		AgentName: "test-agent",
		Archetype: "nonexistent",
	}

	_, err := GenerateCLAUDEMD(config)
	if err == nil {
		t.Fatal("expected error for unknown archetype")
	}
	if !strings.Contains(err.Error(), "unknown archetype") {
		t.Errorf("expected 'unknown archetype' error, got: %v", err)
	}
}

func TestGenerateCLAUDEMD_EmptyArchetypeDefaultsToCustom(t *testing.T) {
	config := GeneratorConfig{
		AgentName:   "test-agent",
		Archetype:   "",
		OwnerName:   "owner",
		SynapBusURL: "http://localhost:8080",
	}

	md, err := GenerateCLAUDEMD(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Custom template has only common sections — no "Example Workflow" section
	if !strings.Contains(md, "Startup Loop") {
		t.Error("expected common protocol sections for empty archetype")
	}
}

func TestGenerateMCPConfig(t *testing.T) {
	result := GenerateMCPConfig("http://localhost:8080", "sk-test-key-123")

	// Should be valid JSON
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !strings.Contains(result, "/mcp") {
		t.Error("expected MCP endpoint URL")
	}
	if !strings.Contains(result, "sk-test-key-123") {
		t.Error("expected API key in config")
	}
	if !strings.Contains(result, "streamable-http") {
		t.Error("expected streamable-http type")
	}
}

func TestListArchetypes(t *testing.T) {
	archetypes := ListArchetypes()
	if len(archetypes) != 6 {
		t.Errorf("expected 6 archetypes, got %d", len(archetypes))
	}

	names := make(map[string]bool)
	for _, a := range archetypes {
		names[a.Name] = true
		if a.Description == "" {
			t.Errorf("archetype %s has empty description", a.Name)
		}
	}

	expected := []string{"researcher", "writer", "commenter", "monitor", "operator", "custom"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected archetype %s in list", name)
		}
	}
}

func TestListSkills(t *testing.T) {
	skills, err := ListSkills()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(skills) < 2 {
		t.Errorf("expected at least 2 skills, got %d", len(skills))
	}

	names := make(map[string]bool)
	for _, s := range skills {
		names[s.Name] = true
	}

	if !names["stigmergy-workflow"] {
		t.Error("expected stigmergy-workflow skill")
	}
	if !names["task-auction"] {
		t.Error("expected task-auction skill")
	}
}

func TestGetSkill(t *testing.T) {
	content, err := GetSkill("stigmergy-workflow")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(content, "Stigmergy Workflow") {
		t.Error("expected skill content to contain title")
	}
}

func TestGetSkill_NotFound(t *testing.T) {
	_, err := GetSkill("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
}
