package attachments

import "testing"

func TestDetectMIMEType(t *testing.T) {
	// PNG magic bytes.
	pngHeader := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	// JPEG magic bytes.
	jpegHeader := []byte{0xff, 0xd8, 0xff, 0xe0}
	// GIF magic bytes.
	gifHeader := []byte("GIF89a")
	// PDF magic bytes.
	pdfHeader := []byte("%PDF-1.4")

	tests := []struct {
		name     string
		content  []byte
		filename string
		want     string
	}{
		{
			name:     "PNG from magic bytes",
			content:  pngHeader,
			filename: "",
			want:     "image/png",
		},
		{
			name:     "JPEG from magic bytes",
			content:  jpegHeader,
			filename: "",
			want:     "image/jpeg",
		},
		{
			name:     "GIF from magic bytes",
			content:  gifHeader,
			filename: "",
			want:     "image/gif",
		},
		{
			name:     "PDF from magic bytes",
			content:  pdfHeader,
			filename: "report.pdf",
			want:     "application/pdf",
		},
		{
			name:     "CSV from extension",
			content:  []byte("a,b,c\n1,2,3"),
			filename: "data.csv",
			want:     "text/csv",
		},
		{
			name:     "JSON from extension",
			content:  []byte(`{"key": "value"}`),
			filename: "config.json",
			want:     "application/json",
		},
		{
			name:     "Markdown from extension",
			content:  []byte("# Hello"),
			filename: "readme.md",
			want:     "text/markdown",
		},
		{
			name:     "ZIP from extension",
			content:  []byte("not real zip"),
			filename: "archive.zip",
			want:     "application/zip",
		},
		{
			name:     "unknown falls back to octet-stream",
			content:  []byte{0x00, 0x01, 0x02},
			filename: "mystery.xyz",
			want:     "application/octet-stream",
		},
		{
			name:     "empty content with extension",
			content:  nil,
			filename: "test.png",
			want:     "image/png",
		},
		{
			name:     "empty everything",
			content:  nil,
			filename: "",
			want:     "application/octet-stream",
		},
		{
			name:     "HTML from magic bytes",
			content:  []byte("<html><body>hello</body></html>"),
			filename: "",
			want:     "text/html; charset=utf-8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectMIMEType(tt.content, tt.filename)
			if got != tt.want {
				t.Errorf("DetectMIMEType(%v, %q) = %q, want %q", tt.content[:min(len(tt.content), 8)], tt.filename, got, tt.want)
			}
		})
	}
}

func TestDefaultFilename(t *testing.T) {
	tests := []struct {
		mimeType string
		want     string
	}{
		{"image/png", "untitled.png"},
		{"image/jpeg", "untitled.jpg"},
		{"image/gif", "untitled.gif"},
		{"image/webp", "untitled.webp"},
		{"image/svg+xml", "untitled.svg"},
		{"application/pdf", "untitled.pdf"},
		{"text/plain", "untitled.txt"},
		{"text/csv", "untitled.txt"},
		{"application/octet-stream", "untitled.bin"},
		{"application/zip", "untitled.bin"},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			got := DefaultFilename(tt.mimeType)
			if got != tt.want {
				t.Errorf("DefaultFilename(%q) = %q, want %q", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestIsImageType(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{"image/jpeg", true},
		{"image/png", true},
		{"image/gif", true},
		{"image/webp", true},
		{"image/svg+xml", true},
		{"application/pdf", false},
		{"text/plain", false},
		{"image/tiff", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			got := IsImageType(tt.mimeType)
			if got != tt.want {
				t.Errorf("IsImageType(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
