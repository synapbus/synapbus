package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	openAIEmbeddingURL = "https://api.openai.com/v1/embeddings"
	openAIModel        = "text-embedding-3-small"
	openAIDimensions   = 1536
	openAIMaxTokens    = 8191
)

// OpenAIProvider implements EmbeddingProvider using OpenAI's text-embedding-3-small.
type OpenAIProvider struct {
	apiKey string
	client *http.Client
}

// NewOpenAIProvider creates a new OpenAI embedding provider.
func NewOpenAIProvider(apiKey string) (*OpenAIProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("openai provider requires SYNAPBUS_EMBEDDING_API_KEY to be set")
	}
	return &OpenAIProvider{
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

type openAIRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type openAIResponse struct {
	Data  []openAIEmbedding `json:"data"`
	Error *openAIError      `json:"error,omitempty"`
}

type openAIEmbedding struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

func (p *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	results, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("openai: empty response")
	}
	return results[0], nil
}

func (p *OpenAIProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Truncate texts that are too long (rough character-based limit; 1 token ~ 4 chars)
	truncated := make([]string, len(texts))
	for i, t := range texts {
		maxChars := openAIMaxTokens * 4
		if len(t) > maxChars {
			truncated[i] = t[:maxChars]
		} else {
			truncated[i] = t
		}
	}

	body, err := json.Marshal(openAIRequest{
		Input: truncated,
		Model: openAIModel,
	})
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIEmbeddingURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("openai: invalid API key (401)")
		case http.StatusTooManyRequests:
			return nil, fmt.Errorf("openai: rate limited (429)")
		default:
			return nil, fmt.Errorf("openai: API error %d: %s", resp.StatusCode, string(respBody))
		}
	}

	var result openAIResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("openai: unmarshal response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("openai: %s: %s", result.Error.Type, result.Error.Message)
	}

	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}

	return embeddings, nil
}

func (p *OpenAIProvider) Dimensions() int { return openAIDimensions }
func (p *OpenAIProvider) Name() string    { return "openai" }
