package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/synapbus/internal/attachments"
)

// AttachmentsHandler provides REST API endpoints for attachment operations.
// These endpoints are intended for the Web UI, not for agent-to-agent use
// (agents use MCP tools instead).
type AttachmentsHandler struct {
	service *attachments.Service
	logger  *slog.Logger
}

// NewAttachmentsHandler creates a new attachments REST handler.
func NewAttachmentsHandler(service *attachments.Service) *AttachmentsHandler {
	return &AttachmentsHandler{
		service: service,
		logger:  slog.Default().With("component", "api-attachments"),
	}
}

// Download streams an attachment file to the client.
// GET /api/attachments/{hash}
func (h *AttachmentsHandler) Download(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if hash == "" {
		http.Error(w, `{"error":"hash parameter required"}`, http.StatusBadRequest)
		return
	}

	result, err := h.service.Download(r.Context(), hash)
	if err != nil {
		switch err {
		case attachments.ErrNotFound, attachments.ErrFileMissing:
			http.Error(w, `{"error":"attachment not found"}`, http.StatusNotFound)
		default:
			h.logger.Error("download attachment failed", "hash", hash, "error", err)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		}
		return
	}
	defer result.Content.Close()

	w.Header().Set("Content-Type", result.MIMEType)
	if result.Size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", result.Size))
	}

	// Images are displayed inline; everything else triggers a download.
	if attachments.IsImageType(result.MIMEType) {
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", result.Filename))
	} else {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", result.Filename))
	}

	if _, err := streamContent(w, result.Content); err != nil {
		h.logger.Error("stream attachment failed", "hash", hash, "error", err)
	}
}

// Metadata returns attachment metadata as JSON.
// GET /api/attachments/{hash}/meta
func (h *AttachmentsHandler) Metadata(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if hash == "" {
		http.Error(w, `{"error":"hash parameter required"}`, http.StatusBadRequest)
		return
	}

	result, err := h.service.Download(r.Context(), hash)
	if err != nil {
		switch err {
		case attachments.ErrNotFound, attachments.ErrFileMissing:
			http.Error(w, `{"error":"attachment not found"}`, http.StatusNotFound)
		default:
			h.logger.Error("get attachment metadata failed", "hash", hash, "error", err)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		}
		return
	}
	// Close the content reader immediately since we only need metadata.
	result.Content.Close()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"hash":              result.Hash,
		"original_filename": result.Filename,
		"mime_type":         result.MIMEType,
		"size":              result.Size,
		"is_image":          attachments.IsImageType(result.MIMEType),
	})
}

// Upload handles multipart file uploads from the Web UI.
// POST /api/attachments
func (h *AttachmentsHandler) Upload(w http.ResponseWriter, r *http.Request) {
	// Limit request body to MaxFileSize + overhead for multipart headers.
	r.Body = http.MaxBytesReader(w, r.Body, attachments.MaxFileSize+1024*1024)

	if err := r.ParseMultipartForm(attachments.MaxFileSize); err != nil {
		http.Error(w, `{"error":"file too large or invalid multipart form"}`, http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"file field required"}`, http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Extract uploader identity from context (set by auth middleware).
	uploadedBy := "web-ui"
	if ownerID, ok := OwnerIDFromContext(r.Context()); ok {
		uploadedBy = fmt.Sprintf("owner-%d", ownerID)
	}

	req := attachments.UploadRequest{
		Content:    file,
		Filename:   header.Filename,
		UploadedBy: uploadedBy,
	}

	result, err := h.service.Upload(r.Context(), req)
	if err != nil {
		switch err {
		case attachments.ErrEmptyFile:
			http.Error(w, `{"error":"empty file not allowed"}`, http.StatusBadRequest)
		case attachments.ErrFileTooLarge:
			http.Error(w, `{"error":"file exceeds maximum size of 50MB"}`, http.StatusRequestEntityTooLarge)
		default:
			h.logger.Error("upload attachment failed", "error", err)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"hash":              result.Hash,
		"size":              result.Size,
		"mime_type":         result.MIMEType,
		"original_filename": result.Filename,
	})
}

// streamContent copies the reader to the response writer.
func streamContent(dst http.ResponseWriter, src io.Reader) (int64, error) {
	return io.Copy(dst, src)
}
