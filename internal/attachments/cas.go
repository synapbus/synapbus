package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// CAS implements content-addressable storage on the local filesystem.
// Files are stored at {baseDir}/{hash[0:2]}/{hash[2:4]}/{hash}.
type CAS struct {
	baseDir string
	logger  *slog.Logger
	mu      sync.Mutex // protects concurrent writes of the same hash
}

// NewCAS creates a new content-addressable store rooted at baseDir.
// The directory tree is created automatically if it does not exist.
func NewCAS(baseDir string, logger *slog.Logger) (*CAS, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create CAS base directory: %w", err)
	}
	return &CAS{
		baseDir: baseDir,
		logger:  logger.With("component", "cas"),
	}, nil
}

// shardPath returns the full filesystem path for the given hash.
func (c *CAS) shardPath(hash string) string {
	return filepath.Join(c.baseDir, hash[0:2], hash[2:4], hash)
}

// Write streams content from r, computing its SHA-256 hash while writing to
// a temporary file. On success the temp file is atomically renamed to the
// content-addressed path. If the file already exists (dedup), the temp file
// is removed and the existing hash is returned.
//
// Returns ErrEmptyFile if r yields zero bytes.
func (c *CAS) Write(r io.Reader) (hash string, size int64, err error) {
	// Write to a temp file while computing the hash.
	tmpFile, err := os.CreateTemp(c.baseDir, ".cas-upload-*")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup on any error path.
	defer func() {
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	w := io.MultiWriter(tmpFile, hasher)

	size, err = io.Copy(w, r)
	if err != nil {
		return "", 0, fmt.Errorf("write content: %w", err)
	}

	if size == 0 {
		err = ErrEmptyFile
		return "", 0, err
	}

	if err = tmpFile.Close(); err != nil {
		return "", 0, fmt.Errorf("close temp file: %w", err)
	}

	hash = hex.EncodeToString(hasher.Sum(nil))
	destPath := c.shardPath(hash)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check for dedup: if the file already exists, skip the rename.
	if _, statErr := os.Stat(destPath); statErr == nil {
		os.Remove(tmpPath)
		c.logger.Debug("dedup: file already exists", "hash", hash)
		return hash, size, nil
	}

	// Create shard directories.
	destDir := filepath.Dir(destPath)
	if err = os.MkdirAll(destDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("create shard directory: %w", err)
	}

	// Atomic rename.
	if err = os.Rename(tmpPath, destPath); err != nil {
		return "", 0, fmt.Errorf("rename temp file: %w", err)
	}

	c.logger.Info("file stored", "hash", hash, "size", size)
	return hash, size, nil
}

// Read opens the file identified by hash and returns a ReadCloser.
// Returns ErrNotFound if the file does not exist on disk.
func (c *CAS) Read(hash string) (io.ReadCloser, error) {
	path := c.shardPath(hash)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("open file: %w", err)
	}
	return f, nil
}

// Exists returns true if the file identified by hash exists on disk.
func (c *CAS) Exists(hash string) bool {
	_, err := os.Stat(c.shardPath(hash))
	return err == nil
}

// Delete removes the file identified by hash from disk.
// Returns the size of the deleted file, or 0 if the file did not exist.
func (c *CAS) Delete(hash string) (int64, error) {
	path := c.shardPath(hash)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat file: %w", err)
	}

	size := info.Size()
	if err := os.Remove(path); err != nil {
		return 0, fmt.Errorf("remove file: %w", err)
	}

	c.logger.Info("file deleted", "hash", hash, "size", size)
	return size, nil
}
