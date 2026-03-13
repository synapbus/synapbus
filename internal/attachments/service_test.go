package attachments

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db := newTestDB(t)
	dir := t.TempDir()

	cas, err := NewCAS(filepath.Join(dir, "attachments"), slog.Default())
	if err != nil {
		t.Fatalf("NewCAS: %v", err)
	}

	store := NewSQLiteStore(db, slog.Default())
	svc := NewService(store, cas, slog.Default())
	return svc, db
}

func TestService_Upload(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	content := []byte("service upload test content")
	wantHash := sha256Hex(content)

	result, err := svc.Upload(ctx, UploadRequest{
		Content:    bytes.NewReader(content),
		Filename:   "test.txt",
		UploadedBy: "agent-a",
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if result.Hash != wantHash {
		t.Errorf("hash = %s, want %s", result.Hash, wantHash)
	}
	if result.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", result.Size, len(content))
	}
	if result.Filename != "test.txt" {
		t.Errorf("filename = %s, want test.txt", result.Filename)
	}
}

func TestService_Upload_MIMEDetection(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// PNG magic bytes.
	pngContent := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00}

	result, err := svc.Upload(ctx, UploadRequest{
		Content:    bytes.NewReader(pngContent),
		Filename:   "image.png",
		UploadedBy: "agent-a",
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if result.MIMEType != "image/png" {
		t.Errorf("mime_type = %s, want image/png", result.MIMEType)
	}
}

func TestService_Upload_DefaultFilename(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	content := []byte("no filename content here")

	result, err := svc.Upload(ctx, UploadRequest{
		Content:    bytes.NewReader(content),
		UploadedBy: "agent-a",
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if result.Filename == "" {
		t.Error("expected a default filename, got empty")
	}
}

func TestService_Upload_EmptyFile(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.Upload(ctx, UploadRequest{
		Content:    bytes.NewReader([]byte{}),
		Filename:   "empty.txt",
		UploadedBy: "agent-a",
	})
	if err != ErrEmptyFile {
		t.Errorf("expected ErrEmptyFile, got %v", err)
	}
}

func TestService_Upload_SizeLimit(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// Create a reader that yields MaxFileSize+1 bytes.
	oversized := make([]byte, MaxFileSize+1)
	for i := range oversized {
		oversized[i] = 'x'
	}

	_, err := svc.Upload(ctx, UploadRequest{
		Content:    bytes.NewReader(oversized),
		Filename:   "toobig.bin",
		UploadedBy: "agent-a",
	})
	if err != ErrFileTooLarge {
		t.Errorf("expected ErrFileTooLarge, got %v", err)
	}
}

func TestService_Download(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	content := []byte("download test content")

	uploadResult, err := svc.Upload(ctx, UploadRequest{
		Content:    bytes.NewReader(content),
		Filename:   "download.txt",
		UploadedBy: "agent-a",
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	t.Run("successful download", func(t *testing.T) {
		result, err := svc.Download(ctx, uploadResult.Hash)
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		defer result.Content.Close()

		got, err := io.ReadAll(result.Content)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if !bytes.Equal(got, content) {
			t.Error("downloaded content does not match uploaded content")
		}
		if result.Filename != "download.txt" {
			t.Errorf("filename = %s, want download.txt", result.Filename)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.Download(ctx, "deadbeef"+strings.Repeat("0", 56))
		if err != ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestService_Dedup(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	content := []byte("dedup test content")
	wantHash := sha256Hex(content)

	r1, err := svc.Upload(ctx, UploadRequest{
		Content:    bytes.NewReader(content),
		Filename:   "version1.txt",
		UploadedBy: "agent-a",
	})
	if err != nil {
		t.Fatalf("Upload 1: %v", err)
	}

	r2, err := svc.Upload(ctx, UploadRequest{
		Content:    bytes.NewReader(content),
		Filename:   "version2.txt",
		UploadedBy: "agent-b",
	})
	if err != nil {
		t.Fatalf("Upload 2: %v", err)
	}

	if r1.Hash != r2.Hash {
		t.Errorf("hashes differ: %s vs %s", r1.Hash, r2.Hash)
	}
	if r1.Hash != wantHash {
		t.Errorf("hash = %s, want %s", r1.Hash, wantHash)
	}
}

func TestService_GarbageCollect(t *testing.T) {
	svc, db := newTestService(t)
	ctx := context.Background()

	// Upload an orphan (no message_id).
	orphanContent := []byte("orphan gc content")
	_, err := svc.Upload(ctx, UploadRequest{
		Content:    bytes.NewReader(orphanContent),
		Filename:   "orphan.txt",
		UploadedBy: "agent-a",
	})
	if err != nil {
		t.Fatalf("Upload orphan: %v", err)
	}

	// Upload a linked attachment.
	msgID := seedMessage(t, db)
	linkedContent := []byte("linked gc content")
	linkedResult, err := svc.Upload(ctx, UploadRequest{
		Content:    bytes.NewReader(linkedContent),
		Filename:   "linked.txt",
		MessageID:  &msgID,
		UploadedBy: "agent-a",
	})
	if err != nil {
		t.Fatalf("Upload linked: %v", err)
	}

	// Run GC.
	gc, err := svc.GarbageCollect(ctx)
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}

	if gc.FilesRemoved != 1 {
		t.Errorf("files_removed = %d, want 1", gc.FilesRemoved)
	}

	// Verify linked file still downloadable.
	result, err := svc.Download(ctx, linkedResult.Hash)
	if err != nil {
		t.Fatalf("Download linked after GC: %v", err)
	}
	result.Content.Close()
}

func TestService_GarbageCollect_Empty(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	gc, err := svc.GarbageCollect(ctx)
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}
	if gc.FilesRemoved != 0 {
		t.Errorf("files_removed = %d, want 0", gc.FilesRemoved)
	}
	if gc.BytesReclaimed != 0 {
		t.Errorf("bytes_reclaimed = %d, want 0", gc.BytesReclaimed)
	}
}
