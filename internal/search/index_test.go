package search

import (
	"os"
	"testing"
)

func TestVectorIndex_AddAndSearch(t *testing.T) {
	idx := NewMemoryVectorIndex()

	// Add some vectors
	vectors := map[int64][]float32{
		1: {1.0, 0.0, 0.0},
		2: {0.0, 1.0, 0.0},
		3: {0.0, 0.0, 1.0},
		4: {0.9, 0.1, 0.0}, // close to vector 1
	}

	for id, vec := range vectors {
		if err := idx.AddVector(id, vec); err != nil {
			t.Fatalf("AddVector(%d): %v", id, err)
		}
	}

	if idx.Len() != 4 {
		t.Errorf("Len() = %d, want 4", idx.Len())
	}

	// Search for vectors near [1.0, 0.0, 0.0]
	results, err := idx.Search([]float32{1.0, 0.0, 0.0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("search results = %d, want 2", len(results))
	}

	// First result should be vector 1 (exact match) or 4 (close match)
	found1 := false
	found4 := false
	for _, r := range results {
		if r.ID == 1 {
			found1 = true
		}
		if r.ID == 4 {
			found4 = true
		}
	}
	if !found1 {
		t.Error("expected to find vector 1 in top-2 results")
	}
	if !found4 {
		t.Error("expected to find vector 4 in top-2 results")
	}
}

func TestVectorIndex_Delete(t *testing.T) {
	idx := NewMemoryVectorIndex()

	if err := idx.AddVector(1, []float32{1.0, 0.0, 0.0}); err != nil {
		t.Fatalf("AddVector: %v", err)
	}
	if err := idx.AddVector(2, []float32{0.0, 1.0, 0.0}); err != nil {
		t.Fatalf("AddVector: %v", err)
	}

	if idx.Len() != 2 {
		t.Errorf("Len() = %d, want 2", idx.Len())
	}

	deleted := idx.Delete(1)
	if !deleted {
		t.Error("Delete(1) returned false, want true")
	}

	if idx.Len() != 1 {
		t.Errorf("Len() after delete = %d, want 1", idx.Len())
	}
}

func TestVectorIndex_EmptySearch(t *testing.T) {
	idx := NewMemoryVectorIndex()

	results, err := idx.Search([]float32{1.0, 0.0, 0.0}, 5)
	if err != nil {
		t.Fatalf("Search on empty index: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on empty index, got %d", len(results))
	}
}

func TestVectorIndex_Rebuild(t *testing.T) {
	idx := NewMemoryVectorIndex()

	// Add initial vectors
	if err := idx.AddVector(1, []float32{1.0, 0.0, 0.0}); err != nil {
		t.Fatalf("AddVector: %v", err)
	}
	if err := idx.AddVector(2, []float32{0.0, 1.0, 0.0}); err != nil {
		t.Fatalf("AddVector: %v", err)
	}

	if idx.Len() != 2 {
		t.Errorf("Len() = %d, want 2", idx.Len())
	}

	// Rebuild with new vectors
	newVectors := map[int64][]float32{
		10: {1.0, 0.0, 0.0},
		20: {0.0, 1.0, 0.0},
		30: {0.0, 0.0, 1.0},
	}
	if err := idx.Rebuild(newVectors); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if idx.Len() != 3 {
		t.Errorf("Len() after rebuild = %d, want 3", idx.Len())
	}

	// Rebuild with empty clears index
	if err := idx.Rebuild(nil); err != nil {
		t.Fatalf("Rebuild(nil): %v", err)
	}

	if idx.Len() != 0 {
		t.Errorf("Len() after empty rebuild = %d, want 0", idx.Len())
	}
}

func TestVectorIndex_Persistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "synapbus-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create and populate an index
	idx, err := NewVectorIndex(tmpDir)
	if err != nil {
		t.Fatalf("NewVectorIndex: %v", err)
	}

	if err := idx.AddVector(1, []float32{1.0, 0.0, 0.0}); err != nil {
		t.Fatalf("AddVector: %v", err)
	}
	if err := idx.AddVector(2, []float32{0.0, 1.0, 0.0}); err != nil {
		t.Fatalf("AddVector: %v", err)
	}

	// Save
	if err := idx.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load into new index
	idx2, err := NewVectorIndex(tmpDir)
	if err != nil {
		t.Fatalf("NewVectorIndex (reload): %v", err)
	}

	if idx2.Len() != 2 {
		t.Errorf("reloaded Len() = %d, want 2", idx2.Len())
	}

	// Search should still work
	results, err := idx2.Search([]float32{1.0, 0.0, 0.0}, 1)
	if err != nil {
		t.Fatalf("Search after reload: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("search results after reload = %d, want 1", len(results))
	}
	if results[0].ID != 1 {
		t.Errorf("closest result ID = %d, want 1", results[0].ID)
	}
}
