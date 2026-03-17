package attachments

import (
	"net/http"
	"path/filepath"
	"strings"
)

// extensionMIMETypes maps file extensions to MIME types for common types
// that net/http.DetectContentType may not identify from magic bytes alone.
var extensionMIMETypes = map[string]string{
	".css":  "text/css",
	".csv":  "text/csv",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".gif":  "image/gif",
	".html": "text/html",
	".jpeg": "image/jpeg",
	".jpg":  "image/jpeg",
	".js":   "application/javascript",
	".json": "application/json",
	".md":   "text/markdown",
	".mp3":  "audio/mpeg",
	".mp4":  "video/mp4",
	".pdf":  "application/pdf",
	".png":  "image/png",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".svg":  "image/svg+xml",
	".tar":  "application/x-tar",
	".txt":  "text/plain",
	".wav":  "audio/wav",
	".webp": "image/webp",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".xml":  "application/xml",
	".yaml": "text/yaml",
	".yml":  "text/yaml",
	".zip":  "application/zip",
}

// imageTypes is the set of MIME types considered "image" for inline preview.
var imageTypes = map[string]bool{
	"image/jpeg":    true,
	"image/png":     true,
	"image/gif":     true,
	"image/webp":    true,
	"image/svg+xml": true,
}

// DetectMIMEType detects the MIME type from file content (magic bytes) with
// fallback to extension-based detection. Returns "application/octet-stream"
// if detection fails entirely.
//
// When magic-byte detection returns a generic type like "text/plain" or
// "application/octet-stream", the extension is checked for a more specific
// type (e.g., .csv -> text/csv, .json -> application/json).
func DetectMIMEType(content []byte, filename string) string {
	var detected string
	if len(content) > 0 {
		detected = http.DetectContentType(content)
	}

	// If detection returned a specific, non-generic type, use it.
	if detected != "" && !isGenericMIME(detected) {
		return detected
	}

	// Extension-based lookup for more specificity.
	if filename != "" {
		ext := strings.ToLower(filepath.Ext(filename))
		if mime, ok := extensionMIMETypes[ext]; ok {
			return mime
		}
	}

	// Return whatever detection found, or fall back to octet-stream.
	if detected != "" {
		return detected
	}
	return "application/octet-stream"
}

// isGenericMIME returns true if the MIME type is a generic catch-all that
// should be overridden by extension-based detection when available.
func isGenericMIME(mimeType string) bool {
	// Normalize: strip parameters like "; charset=utf-8".
	base := mimeType
	if idx := strings.Index(mimeType, ";"); idx >= 0 {
		base = strings.TrimSpace(mimeType[:idx])
	}
	return base == "application/octet-stream" || base == "text/plain"
}

// DefaultFilename returns a default filename based on MIME type when the
// caller provides none.
func DefaultFilename(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/png"):
		return "untitled.png"
	case strings.HasPrefix(mimeType, "image/jpeg"):
		return "untitled.jpg"
	case strings.HasPrefix(mimeType, "image/gif"):
		return "untitled.gif"
	case strings.HasPrefix(mimeType, "image/webp"):
		return "untitled.webp"
	case strings.HasPrefix(mimeType, "image/svg"):
		return "untitled.svg"
	case mimeType == "application/pdf":
		return "untitled.pdf"
	case strings.HasPrefix(mimeType, "text/"):
		return "untitled.txt"
	default:
		return "untitled.bin"
	}
}

// IsImageType returns true if the MIME type is a supported image type for
// inline preview in the Web UI.
func IsImageType(mimeType string) bool {
	return imageTypes[mimeType]
}

// IsAllowedType returns true if the MIME type is allowed for upload.
// Allowed: image/*, application/pdf, text/*.
func IsAllowedType(mimeType string) bool {
	// Normalize: strip parameters like "; charset=utf-8".
	base := mimeType
	if idx := strings.Index(mimeType, ";"); idx >= 0 {
		base = strings.TrimSpace(mimeType[:idx])
	}
	if strings.HasPrefix(base, "image/") {
		return true
	}
	if base == "application/pdf" {
		return true
	}
	if strings.HasPrefix(base, "text/") {
		return true
	}
	// Also allow JSON and XML which may be detected as application/*
	if base == "application/json" || base == "application/xml" {
		return true
	}
	return false
}
