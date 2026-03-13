package attachments

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestCAS(t *testing.T) *CAS {
	t.Helper()
	dir := t.TempDir()
	cas, err := NewCAS(filepath.Join(dir, "attachments"), slog.Default())
	if err != nil {
		t.Fatalf("NewCAS: %v", err)
	}
	return cas
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestCAS_Write(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		wantErr error
	}{
		{
			name:    "normal file",
			content: []byte("hello world"),
		},
		{
			name:    "binary content",
			content: []byte{0x00, 0x01, 0xff, 0xfe, 0x89, 0x50, 0x4e, 0x47},
		},
		{
			name:    "zero bytes",
			content: []byte{},
			wantErr: ErrEmptyFile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cas := newTestCAS(t)
			hash, size, err := cas.Write(bytes.NewReader(tt.content))

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Write: %v", err)
			}

			wantHash := sha256Hex(tt.content)
			if hash != wantHash {
				t.Errorf("hash = %s, want %s", hash, wantHash)
			}
			if size != int64(len(tt.content)) {
				t.Errorf("size = %d, want %d", size, len(tt.content))
			}

			// Verify sharded path exists.
			path := filepath.Join(cas.baseDir, hash[0:2], hash[2:4], hash)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("file not found at sharded path: %v", err)
			}
		})
	}
}

func TestCAS_Read(t *testing.T) {
	cas := newTestCAS(t)
	content := []byte("test content for reading")

	hash, _, err := cas.Write(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	t.Run("existing file", func(t *testing.T) {
		rc, err := cas.Read(hash)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		defer rc.Close()

		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if !bytes.Equal(got, content) {
			t.Errorf("content mismatch: got %q, want %q", got, content)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := cas.Read("deadbeef" + strings.Repeat("0", 56))
		if err != ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestCAS_Exists(t *testing.T) {
	cas := newTestCAS(t)
	content := []byte("existence check")

	hash, _, err := cas.Write(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if !cas.Exists(hash) {
		t.Error("Exists returned false for stored file")
	}
	if cas.Exists("deadbeef" + strings.Repeat("0", 56)) {
		t.Error("Exists returned true for non-existent file")
	}
}

func TestCAS_Delete(t *testing.T) {
	cas := newTestCAS(t)
	content := []byte("deletable content")

	hash, _, err := cas.Write(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	t.Run("delete existing", func(t *testing.T) {
		size, err := cas.Delete(hash)
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if size != int64(len(content)) {
			t.Errorf("deleted size = %d, want %d", size, len(content))
		}
		if cas.Exists(hash) {
			t.Error("file still exists after delete")
		}
	})

	t.Run("delete non-existent", func(t *testing.T) {
		size, err := cas.Delete("deadbeef" + strings.Repeat("0", 56))
		if err != nil {
			t.Fatalf("Delete non-existent: %v", err)
		}
		if size != 0 {
			t.Errorf("deleted size = %d, want 0", size)
		}
	})
}

func TestCAS_Dedup(t *testing.T) {
	cas := newTestCAS(t)
	content := []byte("duplicate content")

	hash1, _, err := cas.Write(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("first Write: %v", err)
	}

	hash2, _, err := cas.Write(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("hashes differ: %s vs %s", hash1, hash2)
	}

	// Verify only one file on disk.
	path := filepath.Join(cas.baseDir, hash1[0:2], hash1[2:4], hash1)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if info.Size() != int64(len(content)) {
		t.Errorf("file size = %d, want %d", info.Size(), len(content))
	}
}

func TestCAS_ConcurrentWrite(t *testing.T) {
	cas := newTestCAS(t)
	content := []byte("concurrent content")
	expected := sha256Hex(content)

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	hashes := make([]string, n)

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			h, _, err := cas.Write(bytes.NewReader(content))
			hashes[idx] = h
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Errorf("goroutine %d: %v", i, errs[i])
		}
		if hashes[i] != expected {
			t.Errorf("goroutine %d: hash = %s, want %s", i, hashes[i], expected)
		}
	}

	// Verify single file on disk.
	if !cas.Exists(expected) {
		t.Error("file does not exist after concurrent writes")
	}
}

func TestCAS_DirectoryStructure(t *testing.T) {
	cas := newTestCAS(t)
	content := []byte("structure check")

	hash, _, err := cas.Write(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Check two-level sharding.
	level1 := filepath.Join(cas.baseDir, hash[0:2])
	level2 := filepath.Join(level1, hash[2:4])
	file := filepath.Join(level2, hash)

	for _, p := range []string{level1, level2} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("directory %s not found: %v", p, err)
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", p)
		}
	}

	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if info.IsDir() {
		t.Error("file is a directory")
	}
}
