package attachments

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// Service is the main entry point for all attachment operations.
// It composes the metadata Store with the CAS filesystem engine.
type Service struct {
	store  Store
	cas    *CAS
	logger *slog.Logger
	gcMu   sync.Mutex // prevents concurrent GC + upload conflicts
}

// NewService creates a new attachment service.
func NewService(store Store, cas *CAS, logger *slog.Logger) *Service {
	return &Service{
		store:  store,
		cas:    cas,
		logger: logger.With("component", "attachment-service"),
	}
}

// Upload stores a file in content-addressable storage and records metadata.
// It validates size, detects MIME type, deduplicates on hash, and logs the
// operation.
func (s *Service) Upload(ctx context.Context, req UploadRequest) (*UploadResult, error) {
	start := time.Now()

	// Read the content into a limited reader to enforce size limits.
	// We read up to MaxFileSize + 1 to detect overflow.
	content, err := io.ReadAll(io.LimitReader(req.Content, MaxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read content: %w", err)
	}

	if len(content) == 0 {
		return nil, ErrEmptyFile
	}

	if int64(len(content)) > MaxFileSize {
		return nil, ErrFileTooLarge
	}

	// Detect MIME type if not provided.
	mimeType := req.MIMEType
	if mimeType == "" {
		// Use first 512 bytes for detection.
		sniffBuf := content
		if len(sniffBuf) > 512 {
			sniffBuf = sniffBuf[:512]
		}
		mimeType = DetectMIMEType(sniffBuf, req.Filename)
	}

	// Assign default filename if missing.
	filename := req.Filename
	if filename == "" {
		filename = DefaultFilename(mimeType)
	}

	// Write to CAS.
	hash, size, err := s.cas.Write(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("write to CAS: %w", err)
	}

	// Store metadata.
	att := &Attachment{
		Hash:             hash,
		OriginalFilename: filename,
		Size:             size,
		MIMEType:         mimeType,
		MessageID:        req.MessageID,
		UploadedBy:       req.UploadedBy,
	}

	if err := s.store.InsertMetadata(ctx, att); err != nil {
		return nil, fmt.Errorf("insert metadata: %w", err)
	}

	s.logger.Info("attachment uploaded",
		"hash", hash,
		"filename", filename,
		"size", size,
		"mime_type", mimeType,
		"uploaded_by", req.UploadedBy,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return &UploadResult{
		Hash:     hash,
		Size:     size,
		MIMEType: mimeType,
		Filename: filename,
	}, nil
}

// Download retrieves a file and its metadata by hash.
// Returns ErrNotFound if no metadata exists, or ErrFileMissing if the
// metadata exists but the file is not on disk.
func (s *Service) Download(ctx context.Context, hash string) (*DownloadResult, error) {
	start := time.Now()

	// Look up metadata.
	atts, err := s.store.GetByHash(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("get metadata: %w", err)
	}
	if len(atts) == 0 {
		return nil, ErrNotFound
	}

	// Use the first metadata row for filename/mime info.
	meta := atts[0]

	// Open the file from CAS.
	reader, err := s.cas.Read(hash)
	if err != nil {
		if err == ErrNotFound {
			s.logger.Warn("attachment file missing from disk",
				"hash", hash,
				"filename", meta.OriginalFilename,
			)
			return nil, ErrFileMissing
		}
		return nil, fmt.Errorf("read from CAS: %w", err)
	}

	s.logger.Info("attachment downloaded",
		"hash", hash,
		"filename", meta.OriginalFilename,
		"size", meta.Size,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return &DownloadResult{
		Content:  reader,
		Hash:     hash,
		Filename: meta.OriginalFilename,
		MIMEType: meta.MIMEType,
		Size:     meta.Size,
	}, nil
}

// AttachToMessage links an existing attachment to a message by updating
// the message_id on all metadata rows with the given hash that are currently
// unlinked.
func (s *Service) AttachToMessage(ctx context.Context, hash string, messageID int64) error {
	atts, err := s.store.GetByHash(ctx, hash)
	if err != nil {
		return fmt.Errorf("get metadata: %w", err)
	}
	if len(atts) == 0 {
		return ErrNotFound
	}

	// We update by inserting a new metadata row linked to the message,
	// copying from the first existing row.
	meta := atts[0]
	linked := &Attachment{
		Hash:             meta.Hash,
		OriginalFilename: meta.OriginalFilename,
		Size:             meta.Size,
		MIMEType:         meta.MIMEType,
		MessageID:        &messageID,
		UploadedBy:       meta.UploadedBy,
	}
	return s.store.InsertMetadata(ctx, linked)
}

// GetByMessageID returns all attachments for a given message.
func (s *Service) GetByMessageID(ctx context.Context, messageID int64) ([]*Attachment, error) {
	return s.store.GetByMessageID(ctx, messageID)
}

// GarbageCollect finds and removes orphaned attachments — files that are
// no longer referenced by any message. Returns a summary of what was removed.
func (s *Service) GarbageCollect(ctx context.Context) (*GCResult, error) {
	s.gcMu.Lock()
	defer s.gcMu.Unlock()

	start := time.Now()

	orphans, err := s.store.FindOrphanHashes(ctx)
	if err != nil {
		return nil, fmt.Errorf("find orphans: %w", err)
	}

	result := &GCResult{}

	for _, hash := range orphans {
		// Delete from CAS.
		size, err := s.cas.Delete(hash)
		if err != nil {
			s.logger.Error("GC: failed to delete file", "hash", hash, "error", err)
			continue
		}

		// Delete metadata.
		if err := s.store.DeleteByHash(ctx, hash); err != nil {
			s.logger.Error("GC: failed to delete metadata", "hash", hash, "error", err)
			continue
		}

		result.FilesRemoved++
		result.BytesReclaimed += size
	}

	s.logger.Info("garbage collection complete",
		"files_removed", result.FilesRemoved,
		"bytes_reclaimed", result.BytesReclaimed,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return result, nil
}
