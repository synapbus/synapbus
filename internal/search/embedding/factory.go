package embedding

import "fmt"

// NewProvider creates an EmbeddingProvider based on the provider name.
func NewProvider(provider, apiKey, ollamaURL string) (EmbeddingProvider, error) {
	switch provider {
	case "openai":
		return NewOpenAIProvider(apiKey)
	case "ollama":
		return NewOllamaProvider(ollamaURL)
	case "":
		return nil, fmt.Errorf("no embedding provider specified")
	default:
		return nil, fmt.Errorf("unknown embedding provider: %q (supported: openai, ollama)", provider)
	}
}
