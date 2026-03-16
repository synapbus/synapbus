// Package a2a provides A2A Agent Card discovery for SynapBus.
// The Agent Card is a JSON document that describes the hub and its registered
// agents, following the A2A Agent Card specification.
package a2a

import "encoding/json"

// AgentCard is the A2A Agent Card document returned by the discovery endpoint.
type AgentCard struct {
	Name                string            `json:"name"`
	Description         string            `json:"description"`
	Version             string            `json:"version"`
	SupportedInterfaces []AgentInterface  `json:"supported_interfaces"`
	Capabilities        AgentCapabilities `json:"capabilities"`
	Skills              []AgentSkill      `json:"skills"`
	SecuritySchemes     map[string]any    `json:"security_schemes"`
	DefaultInputModes   []string          `json:"default_input_modes"`
	DefaultOutputModes  []string          `json:"default_output_modes"`
}

// AgentInterface describes a protocol endpoint the hub supports.
type AgentInterface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocol_binding"`
}

// AgentCapabilities declares hub-level capabilities.
type AgentCapabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"push_notifications"`
}

// AgentSkill represents a single agent registered on the hub, mapped as an
// A2A skill.
type AgentSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// AgentInfo is a lightweight struct used to pass agent data from the registry
// into the card generator without leaking internal types.
type AgentInfo struct {
	Name         string
	DisplayName  string
	Type         string
	Capabilities json.RawMessage
}

// GenerateAgentCard builds an AgentCard from the hub configuration and a list
// of registered agents.
func GenerateAgentCard(baseURL string, version string, agents []AgentInfo) *AgentCard {
	skills := make([]AgentSkill, 0, len(agents))
	for _, a := range agents {
		skill := AgentSkill{
			ID:   a.Name,
			Name: a.DisplayName,
		}
		if skill.Name == "" {
			skill.Name = a.Name
		}

		// Parse capabilities JSON for description and tags.
		if len(a.Capabilities) > 0 {
			var caps map[string]interface{}
			if json.Unmarshal(a.Capabilities, &caps) == nil {
				if desc, ok := caps["description"].(string); ok {
					skill.Description = desc
				}
				if role, ok := caps["role"].(string); ok {
					skill.Tags = append(skill.Tags, role)
				}
				if tagsRaw, ok := caps["tags"]; ok {
					switch v := tagsRaw.(type) {
					case []interface{}:
						for _, t := range v {
							if s, ok := t.(string); ok {
								skill.Tags = append(skill.Tags, s)
							}
						}
					}
				}
			}
		}

		// Add agent type as a tag.
		if a.Type != "" {
			skill.Tags = append(skill.Tags, a.Type)
		}

		skills = append(skills, skill)
	}

	return &AgentCard{
		Name:        "SynapBus Hub",
		Description: "MCP-native agent-to-agent messaging hub",
		Version:     version,
		SupportedInterfaces: []AgentInterface{
			{
				URL:             baseURL + "/a2a",
				ProtocolBinding: "JSONRPC",
			},
		},
		Capabilities: AgentCapabilities{
			Streaming:         true,
			PushNotifications: false,
		},
		Skills: skills,
		SecuritySchemes: map[string]any{
			"apiKey": map[string]any{
				"type":   "apiKey",
				"in":     "header",
				"name":   "Authorization",
				"scheme": "Bearer",
			},
			"oauth2": map[string]any{
				"type": "oauth2",
				"flows": map[string]any{
					"authorizationCode": map[string]any{
						"authorizationUrl": baseURL + "/oauth/authorize",
						"tokenUrl":         baseURL + "/oauth/token",
						"scopes": map[string]string{
							"mcp": "MCP protocol access",
						},
					},
				},
			},
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}
}
