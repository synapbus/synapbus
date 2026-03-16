package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateAgentCard_WithAgents(t *testing.T) {
	agents := []AgentInfo{
		{Name: "research-bot", DisplayName: "Research Bot", Type: "ai", Capabilities: json.RawMessage(`{"role":"researcher","description":"Searches the web"}`)},
		{Name: "social-commenter", DisplayName: "Social Commenter", Type: "ai", Capabilities: json.RawMessage(`{"tags":["social","marketing"]}`)},
		{Name: "data-analyst", DisplayName: "", Type: "ai", Capabilities: json.RawMessage(`{}`)},
	}

	card := GenerateAgentCard("http://localhost:8080", "1.0.0", agents)

	if card.Name != "SynapBus Hub" {
		t.Errorf("name = %q, want %q", card.Name, "SynapBus Hub")
	}
	if card.Version != "1.0.0" {
		t.Errorf("version = %q, want %q", card.Version, "1.0.0")
	}
	if len(card.Skills) != 3 {
		t.Fatalf("skills count = %d, want 3", len(card.Skills))
	}

	// Verify first skill has description and tags from capabilities
	s0 := card.Skills[0]
	if s0.ID != "research-bot" {
		t.Errorf("skill[0].id = %q, want %q", s0.ID, "research-bot")
	}
	if s0.Name != "Research Bot" {
		t.Errorf("skill[0].name = %q, want %q", s0.Name, "Research Bot")
	}
	if s0.Description != "Searches the web" {
		t.Errorf("skill[0].description = %q, want %q", s0.Description, "Searches the web")
	}
	// Should have "researcher" from role + "ai" from type
	if len(s0.Tags) < 2 {
		t.Errorf("skill[0].tags = %v, expected at least 2 tags", s0.Tags)
	}

	// Second skill should have tags from capabilities "tags" field
	s1 := card.Skills[1]
	if s1.ID != "social-commenter" {
		t.Errorf("skill[1].id = %q, want %q", s1.ID, "social-commenter")
	}
	foundSocial := false
	for _, tag := range s1.Tags {
		if tag == "social" {
			foundSocial = true
		}
	}
	if !foundSocial {
		t.Errorf("skill[1].tags = %v, expected 'social' tag", s1.Tags)
	}

	// Third skill should use name as display name (since DisplayName is empty)
	s2 := card.Skills[2]
	if s2.Name != "data-analyst" {
		t.Errorf("skill[2].name = %q, want %q (fallback to Name)", s2.Name, "data-analyst")
	}

	// Verify JSON serialization round-trips cleanly
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	var decoded AgentCard
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	if decoded.Name != card.Name {
		t.Errorf("round-trip name mismatch")
	}
	if len(decoded.Skills) != 3 {
		t.Errorf("round-trip skills count = %d, want 3", len(decoded.Skills))
	}
}

func TestGenerateAgentCard_WithCapabilities_TagsPopulated(t *testing.T) {
	agents := []AgentInfo{
		{
			Name:         "smart-agent",
			DisplayName:  "Smart Agent",
			Type:         "ai",
			Capabilities: json.RawMessage(`{"role":"analyst","description":"Analyzes data","tags":["ml","data"]}`),
		},
	}

	card := GenerateAgentCard("http://example.com", "2.0.0", agents)

	if len(card.Skills) != 1 {
		t.Fatalf("skills count = %d, want 1", len(card.Skills))
	}

	skill := card.Skills[0]
	if skill.Description != "Analyzes data" {
		t.Errorf("description = %q, want %q", skill.Description, "Analyzes data")
	}

	// Expect tags: "analyst" (from role), "ml", "data" (from tags), "ai" (from type)
	expectedTags := map[string]bool{"analyst": false, "ml": false, "data": false, "ai": false}
	for _, tag := range skill.Tags {
		if _, ok := expectedTags[tag]; ok {
			expectedTags[tag] = true
		}
	}
	for tag, found := range expectedTags {
		if !found {
			t.Errorf("missing expected tag %q in %v", tag, skill.Tags)
		}
	}
}

func TestGenerateAgentCard_NoAgents(t *testing.T) {
	card := GenerateAgentCard("http://localhost:8080", "0.1.0", nil)

	if card.Name != "SynapBus Hub" {
		t.Errorf("name = %q, want %q", card.Name, "SynapBus Hub")
	}
	if len(card.Skills) != 0 {
		t.Errorf("skills count = %d, want 0", len(card.Skills))
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("interfaces count = %d, want 1", len(card.SupportedInterfaces))
	}
	if card.SupportedInterfaces[0].URL != "http://localhost:8080/a2a" {
		t.Errorf("interface url = %q, want %q", card.SupportedInterfaces[0].URL, "http://localhost:8080/a2a")
	}
	if card.SecuritySchemes == nil {
		t.Error("security_schemes should not be nil")
	}
}

// mockAgentLister implements AgentLister for handler tests.
type mockAgentLister struct {
	agents []AgentInfo
	err    error
}

func (m *mockAgentLister) ListAllActiveAgents(_ context.Context) ([]AgentInfo, error) {
	return m.agents, m.err
}

func TestHandler_Returns200WithCorrectContentType(t *testing.T) {
	lister := &mockAgentLister{
		agents: []AgentInfo{
			{Name: "bot-1", DisplayName: "Bot One", Type: "ai"},
			{Name: "bot-2", DisplayName: "Bot Two", Type: "ai"},
		},
	}

	handler := NewAgentCardHandler(lister, "http://localhost:8080", "1.0.0")
	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var card AgentCard
	if err := json.NewDecoder(rr.Body).Decode(&card); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if card.Name != "SynapBus Hub" {
		t.Errorf("card.name = %q, want %q", card.Name, "SynapBus Hub")
	}
	if len(card.Skills) != 2 {
		t.Errorf("card.skills count = %d, want 2", len(card.Skills))
	}
}

func TestHandler_DerivesBaseURLFromRequest(t *testing.T) {
	lister := &mockAgentLister{agents: nil}

	// Empty configuredBaseURL — should derive from request
	handler := NewAgentCardHandler(lister, "", "1.0.0")
	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	req.Host = "myhost:9090"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var card AgentCard
	if err := json.NewDecoder(rr.Body).Decode(&card); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if card.SupportedInterfaces[0].URL != "http://myhost:9090/a2a" {
		t.Errorf("interface url = %q, want %q", card.SupportedInterfaces[0].URL, "http://myhost:9090/a2a")
	}
}

func TestHandler_RejectsNonGET(t *testing.T) {
	lister := &mockAgentLister{agents: nil}
	handler := NewAgentCardHandler(lister, "http://localhost:8080", "1.0.0")
	req := httptest.NewRequest(http.MethodPost, "/.well-known/agent-card.json", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}
