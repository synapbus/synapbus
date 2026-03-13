package attachments

import (
	"errors"
	"io"
	"time"
)

// MaxFileSize is the maximum allowed upload size (50 MB).
const MaxFileSize = 50 * 1024 * 1024 // 50 MB

// Sentinel errors.
var (
	ErrNotFound      = errors.New("attachment not found")
	ErrFileTooLarge  = errors.New("file exceeds maximum size of 50MB")
	ErrEmptyFile     = errors.New("empty file not allowed")
	ErrFileMissing   = errors.New("attachment file missing from disk")
)

// Attachment represents the metadata for a stored file.
type Attachment struct {
	ID               int64     `json:"id"`
	Hash             string    `json:"hash"`
	OriginalFilename string    `json:"original_filename"`
	Size             int64     `json:"size"`
	MIMEType         string    `json:"mime_type"`
	MessageID        *int64    `json:"message_id,omitempty"`
	UploadedBy       string    `json:"uploaded_by"`
	CreatedAt        time.Time `json:"created_at"`
}

// UploadRequest contains the parameters for uploading an attachment.
type UploadRequest struct {
	Content    io.Reader
	Filename   string
	MIMEType   string
	MessageID  *int64
	UploadedBy string
}

// UploadResult contains the result of a successful upload.
type UploadResult struct {
	Hash     string `json:"hash"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mime_type"`
	Filename string `json:"original_filename"`
}

// DownloadResult contains the result of a successful download.
type DownloadResult struct {
	Content  io.ReadCloser
	Hash     string
	Filename string
	MIMEType string
	Size     int64
}

// GCResult contains the result of a garbage collection run.
type GCResult struct {
	FilesRemoved  int   `json:"files_removed"`
	BytesReclaimed int64 `json:"bytes_reclaimed"`
}
