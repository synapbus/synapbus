// Package search provides semantic and full-text search for SynapBus messages.
package search

import (
	"os"
	"strconv"
	"time"
)

// Config holds configuration for the search subsystem.
type Config struct {
	// Provider specifies the embedding provider: "openai", "ollama", or empty for none.
	Provider string
	// APIKey is the API key for the embedding provider (required for openai).
	APIKey string
	// OllamaURL is the Ollama server URL (default http://localhost:11434).
	OllamaURL string
	// BatchSize is the number of messages to embed in a single batch (default 10).
	BatchSize int
	// WorkerCount is the number of embedding worker goroutines (default 1).
	WorkerCount int
	// PollInterval is how often to poll for unembedded messages (default 2s).
	PollInterval time.Duration
	// RetryMaxAttempts is the max retry count for failed embeddings (default 3).
	RetryMaxAttempts int
	// RetryBaseDelay is the base delay for exponential backoff (default 1s).
	RetryBaseDelay time.Duration
}

// LoadConfigFromEnv creates a Config from environment variables.
func LoadConfigFromEnv() Config {
	cfg := Config{
		Provider:         os.Getenv("SYNAPBUS_EMBEDDING_PROVIDER"),
		APIKey:           os.Getenv("SYNAPBUS_EMBEDDING_API_KEY"),
		OllamaURL:        os.Getenv("SYNAPBUS_OLLAMA_URL"),
		BatchSize:        10,
		WorkerCount:      1,
		PollInterval:     2 * time.Second,
		RetryMaxAttempts: 3,
		RetryBaseDelay:   1 * time.Second,
	}

	if cfg.OllamaURL == "" {
		cfg.OllamaURL = "http://localhost:11434"
	}

	if v := os.Getenv("SYNAPBUS_EMBEDDING_BATCH_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BatchSize = n
		}
	}

	if v := os.Getenv("SYNAPBUS_EMBEDDING_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.WorkerCount = n
		}
	}

	return cfg
}

// IsEnabled returns true if an embedding provider is configured.
func (c Config) IsEnabled() bool {
	return c.Provider != ""
}
