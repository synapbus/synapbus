package onboarding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
)

// GeneratorConfig holds the parameters for generating a CLAUDE.md file.
type GeneratorConfig struct {
	AgentName   string
	Archetype   string
	OwnerName   string
	SynapBusURL string
	APIKey      string
}

// ArchetypeInfo describes an available archetype.
type ArchetypeInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// archetypeDescriptions maps archetype names to human-readable descriptions.
var archetypeDescriptions = map[string]string{
	"researcher": "research and discovery",
	"writer":     "content creation and publishing",
	"commenter":  "community engagement",
	"monitor":    "monitoring and alerting",
	"operator":   "deployment and operations",
	"custom":     "general purpose",
}

// archetypeTemplates maps archetype names to their specific template sections.
var archetypeTemplates = map[string]string{
	"researcher": researcherTemplate,
	"writer":     writerTemplate,
	"commenter":  commenterTemplate,
	"monitor":    monitorTemplate,
	"operator":   operatorTemplate,
	"custom":     customTemplate,
}

// templateData is the data passed to templates during rendering.
type templateData struct {
	AgentName            string
	Archetype            string
	ArchetypeDescription string
	OwnerName            string
	SynapBusURL          string
}

// GenerateCLAUDEMD renders the CLAUDE.md template for the given archetype.
func GenerateCLAUDEMD(config GeneratorConfig) (string, error) {
	archetype := strings.ToLower(config.Archetype)
	if archetype == "" {
		archetype = "custom"
	}

	description, ok := archetypeDescriptions[archetype]
	if !ok {
		return "", fmt.Errorf("unknown archetype: %s", config.Archetype)
	}

	archetypeSection, ok := archetypeTemplates[archetype]
	if !ok {
		return "", fmt.Errorf("no template for archetype: %s", config.Archetype)
	}

	// Combine common + archetype-specific template
	fullTemplate := commonTemplate + archetypeSection

	tmpl, err := template.New("claude-md").Parse(fullTemplate)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	data := templateData{
		AgentName:            config.AgentName,
		Archetype:            archetype,
		ArchetypeDescription: description,
		OwnerName:            config.OwnerName,
		SynapBusURL:          config.SynapBusURL,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

// GenerateMCPConfig returns a JSON snippet for Claude Code MCP settings.
func GenerateMCPConfig(synapbusURL, apiKey string) string {
	config := map[string]any{
		"mcpServers": map[string]any{
			"synapbus": map[string]any{
				"type": "streamable-http",
				"url":  strings.TrimRight(synapbusURL, "/") + "/mcp",
				"headers": map[string]string{
					"Authorization": "Bearer " + apiKey,
				},
			},
		},
	}

	b, _ := json.MarshalIndent(config, "", "  ")
	return string(b)
}

// ListArchetypes returns available archetype examples with descriptions.
// These are starting templates, not rigid categories.
func ListArchetypes() []ArchetypeInfo {
	return []ArchetypeInfo{
		{Name: "custom", Description: "Clean start — core SynapBus protocol only, you define the workflow"},
		{Name: "researcher", Description: "Example: web search, platform discovery, finding deduplication"},
		{Name: "writer", Description: "Example: content creation, blog publishing, draft-review-publish pipeline"},
		{Name: "commenter", Description: "Example: community engagement, comment drafting, approval workflow"},
		{Name: "monitor", Description: "Example: diff checking, change detection, alerts"},
		{Name: "operator", Description: "Example: deployment, incident response, system automation"},
	}
}
