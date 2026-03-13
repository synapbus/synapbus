package search

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/TFMV/hnsw"
)

const hnswIndexFile = "hnsw.idx"

// VectorIndex provides thread-safe approximate nearest neighbor search.
type VectorIndex struct {
	mu     sync.RWMutex
	graph  *hnsw.SavedGraph[int64]
	logger *slog.Logger
}

// SearchResult from the vector index.
type VectorSearchResult struct {
	ID       int64
	Distance float32
}

// NewVectorIndex creates or loads a vector index from dataDir.
func NewVectorIndex(dataDir string) (*VectorIndex, error) {
	path := filepath.Join(dataDir, hnswIndexFile)

	g, err := hnsw.LoadSavedGraph[int64](path)
	if err != nil {
		// If the file is corrupted, start fresh
		slog.Warn("failed to load HNSW index, creating new",
			"path", path,
			"error", err,
		)
		g = &hnsw.SavedGraph[int64]{
			Graph: hnsw.NewGraph[int64](),
			Path:  path,
		}
	}

	// Configure for cosine distance (default in hnsw.NewGraph)
	g.M = 16
	g.EfSearch = 100
	g.Distance = hnsw.CosineDistance

	return &VectorIndex{
		graph:  g,
		logger: slog.Default().With("component", "vector-index"),
	}, nil
}

// NewMemoryVectorIndex creates an in-memory vector index (for testing).
func NewMemoryVectorIndex() *VectorIndex {
	g := hnsw.NewGraph[int64]()
	g.M = 16
	g.EfSearch = 100
	g.Distance = hnsw.CosineDistance

	return &VectorIndex{
		graph: &hnsw.SavedGraph[int64]{
			Graph: g,
			Path:  "",
		},
		logger: slog.Default().With("component", "vector-index"),
	}
}

// AddVector adds a vector to the index.
func (idx *VectorIndex) AddVector(id int64, vector []float32) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	node := hnsw.MakeNode(id, vector)
	if err := idx.graph.Add(node); err != nil {
		return fmt.Errorf("add vector %d: %w", id, err)
	}
	return nil
}

// Search finds the k nearest vectors to the query.
func (idx *VectorIndex) Search(query []float32, k int) ([]VectorSearchResult, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.graph.Len() == 0 {
		return nil, nil
	}

	nodes, err := idx.graph.Search(query, k)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	results := make([]VectorSearchResult, len(nodes))
	for i, n := range nodes {
		// CosineDistance returns 1 - cosine_similarity, so distance is in [0, 2]
		results[i] = VectorSearchResult{
			ID:       n.Key,
			Distance: hnsw.CosineDistance(query, n.Value),
		}
	}
	return results, nil
}

// Delete removes a vector from the index.
func (idx *VectorIndex) Delete(id int64) bool {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.graph.Delete(id)
}

// Save persists the index to disk.
func (idx *VectorIndex) Save() error {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.graph.Path == "" {
		return nil // in-memory index, no save
	}
	return idx.graph.Save()
}

// Len returns the number of vectors in the index.
func (idx *VectorIndex) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.graph.Len()
}

// Rebuild clears the index and re-adds the given vectors.
func (idx *VectorIndex) Rebuild(vectors map[int64][]float32) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Create a fresh graph
	g := hnsw.NewGraph[int64]()
	g.M = 16
	g.EfSearch = 100
	g.Distance = hnsw.CosineDistance

	if len(vectors) > 0 {
		nodes := make([]hnsw.Node[int64], 0, len(vectors))
		for id, vec := range vectors {
			nodes = append(nodes, hnsw.MakeNode(id, vec))
		}
		if err := g.Add(nodes...); err != nil {
			return fmt.Errorf("rebuild: add nodes: %w", err)
		}
	}

	idx.graph.Graph = g
	return nil
}
