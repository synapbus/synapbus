package embedding

import (
	"context"
	"testing"
)

func TestMockProvider_Embed(t *testing.T) {
	provider := NewMockProvider(128)

	t.Run("returns correct dimensions", func(t *testing.T) {
		vec, err := provider.Embed(context.Background(), "test text")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(vec) != 128 {
			t.Errorf("vector length = %d, want 128", len(vec))
		}
	})

	t.Run("batch embedding", func(t *testing.T) {
		texts := []string{"hello", "world", "test"}
		vecs, err := provider.EmbedBatch(context.Background(), texts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(vecs) != 3 {
			t.Errorf("batch result length = %d, want 3", len(vecs))
		}
		for i, vec := range vecs {
			if len(vec) != 128 {
				t.Errorf("vector %d length = %d, want 128", i, len(vec))
			}
		}
	})

	t.Run("empty batch", func(t *testing.T) {
		vecs, err := provider.EmbedBatch(context.Background(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(vecs) != 0 {
			t.Errorf("empty batch result length = %d, want 0", len(vecs))
		}
	})

	t.Run("custom embed func", func(t *testing.T) {
		p := NewMockProvider(3)
		p.SetEmbedFunc(func(ctx context.Context, text string) ([]float32, error) {
			return []float32{1.0, 2.0, 3.0}, nil
		})

		vec, err := p.Embed(context.Background(), "anything")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if vec[0] != 1.0 || vec[1] != 2.0 || vec[2] != 3.0 {
			t.Errorf("unexpected vector: %v", vec)
		}
	})
}

func TestNewProvider_Factory(t *testing.T) {
	t.Run("unknown provider", func(t *testing.T) {
		_, err := NewProvider("unknown", "", "")
		if err == nil {
			t.Error("expected error for unknown provider")
		}
	})

	t.Run("empty provider", func(t *testing.T) {
		_, err := NewProvider("", "", "")
		if err == nil {
			t.Error("expected error for empty provider")
		}
	})

	t.Run("openai without key", func(t *testing.T) {
		_, err := NewProvider("openai", "", "")
		if err == nil {
			t.Error("expected error for openai without API key")
		}
	})

	t.Run("openai with key", func(t *testing.T) {
		p, err := NewProvider("openai", "test-key", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Name() != "openai" {
			t.Errorf("name = %q, want openai", p.Name())
		}
		if p.Dimensions() != 1536 {
			t.Errorf("dimensions = %d, want 1536", p.Dimensions())
		}
	})

	t.Run("ollama", func(t *testing.T) {
		p, err := NewProvider("ollama", "", "http://localhost:11434")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Name() != "ollama" {
			t.Errorf("name = %q, want ollama", p.Name())
		}
		if p.Dimensions() != 768 {
			t.Errorf("dimensions = %d, want 768", p.Dimensions())
		}
	})
}
