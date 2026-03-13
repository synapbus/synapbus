// Package embedding provides embedding provider implementations for semantic search.
package embedding

import "context"

// EmbeddingProvider generates vector embeddings from text.
type EmbeddingProvider interface {
	// Embed generates an embedding vector for a single text.
	Embed(ctx context.Context, text string) ([]float32, error)
	// EmbedBatch generates embedding vectors for multiple texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	// Dimensions returns the embedding dimensionality.
	Dimensions() int
	// Name returns the provider name (e.g. "openai", "ollama").
	Name() string
}
