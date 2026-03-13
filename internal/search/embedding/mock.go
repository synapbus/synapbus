package embedding

import "context"

// MockProvider implements EmbeddingProvider for testing.
type MockProvider struct {
	dims       int
	name       string
	embedFunc  func(ctx context.Context, text string) ([]float32, error)
	batchFunc  func(ctx context.Context, texts []string) ([][]float32, error)
}

// NewMockProvider creates a mock provider with the given dimensionality.
func NewMockProvider(dims int) *MockProvider {
	return &MockProvider{
		dims: dims,
		name: "mock",
	}
}

// SetEmbedFunc overrides the default embedding behavior.
func (m *MockProvider) SetEmbedFunc(fn func(ctx context.Context, text string) ([]float32, error)) {
	m.embedFunc = fn
}

// SetBatchFunc overrides the default batch embedding behavior.
func (m *MockProvider) SetBatchFunc(fn func(ctx context.Context, texts []string) ([][]float32, error)) {
	m.batchFunc = fn
}

func (m *MockProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedFunc != nil {
		return m.embedFunc(ctx, text)
	}
	// Generate a deterministic vector based on text length
	vec := make([]float32, m.dims)
	for i := range vec {
		vec[i] = float32(len(text)%10+i) / float32(m.dims)
	}
	return vec, nil
}

func (m *MockProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if m.batchFunc != nil {
		return m.batchFunc(ctx, texts)
	}
	results := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = vec
	}
	return results, nil
}

func (m *MockProvider) Dimensions() int { return m.dims }
func (m *MockProvider) Name() string    { return m.name }
