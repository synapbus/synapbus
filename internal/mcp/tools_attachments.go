package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/synapbus/synapbus/internal/attachments"
)

// AttachmentToolRegistrar registers attachment MCP tools on the server.
type AttachmentToolRegistrar struct {
	attachmentService *attachments.Service
}

// NewAttachmentToolRegistrar creates a new attachment tool registrar.
func NewAttachmentToolRegistrar(attachmentService *attachments.Service) *AttachmentToolRegistrar {
	return &AttachmentToolRegistrar{
		attachmentService: attachmentService,
	}
}

// RegisterAll registers all attachment tools on the MCP server.
func (atr *AttachmentToolRegistrar) RegisterAll(s *server.MCPServer) {
	s.AddTool(atr.uploadAttachmentTool(), atr.handleUploadAttachment)
	s.AddTool(atr.downloadAttachmentTool(), atr.handleDownloadAttachment)
	s.AddTool(atr.gcAttachmentsTool(), atr.handleGCAttachments)
}

// --- Tool Definitions ---

func (atr *AttachmentToolRegistrar) uploadAttachmentTool() mcp.Tool {
	return mcp.NewTool("upload_attachment",
		mcp.WithDescription("Upload a file attachment. Content must be base64-encoded. Returns the SHA-256 hash for later retrieval. Max file size: 50MB."),
		mcp.WithString("content", mcp.Description("Base64-encoded file content"), mcp.Required()),
		mcp.WithString("filename", mcp.Description("Original filename (optional, used for MIME detection and display)")),
		mcp.WithString("mime_type", mcp.Description("MIME type override (optional, auto-detected from content if not provided)")),
		mcp.WithNumber("message_id", mcp.Description("Message ID to attach the file to (optional, can be linked later)")),
	)
}

func (atr *AttachmentToolRegistrar) downloadAttachmentTool() mcp.Tool {
	return mcp.NewTool("download_attachment",
		mcp.WithDescription("Download an attachment by its SHA-256 hash. Returns base64-encoded content along with filename and MIME type metadata."),
		mcp.WithString("hash", mcp.Description("SHA-256 hash of the attachment"), mcp.Required()),
	)
}

func (atr *AttachmentToolRegistrar) gcAttachmentsTool() mcp.Tool {
	return mcp.NewTool("gc_attachments",
		mcp.WithDescription("Run garbage collection to remove orphaned attachments not referenced by any message. Returns a summary of files removed and bytes reclaimed."),
	)
}

// --- Tool Handlers ---

func (atr *AttachmentToolRegistrar) handleUploadAttachment(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	contentB64 := req.GetString("content", "")
	if contentB64 == "" {
		return mcp.NewToolResultError("'content' parameter is required"), nil
	}

	// Check base64 size before decoding to avoid buffering oversized content.
	// Base64 expands data by ~4/3, so decoded size is roughly 3/4 of encoded.
	if int64(len(contentB64))*3/4 > attachments.MaxFileSize {
		return mcp.NewToolResultError("file exceeds maximum size of 50MB"), nil
	}

	decoded, err := base64.StdEncoding.DecodeString(contentB64)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid base64 content: %s", err)), nil
	}

	if int64(len(decoded)) > attachments.MaxFileSize {
		return mcp.NewToolResultError("file exceeds maximum size of 50MB"), nil
	}

	uploadReq := attachments.UploadRequest{
		Content:    bytes.NewReader(decoded),
		Filename:   req.GetString("filename", ""),
		MIMEType:   req.GetString("mime_type", ""),
		UploadedBy: agentName,
	}

	// Optional message_id.
	if mid := req.GetInt("message_id", 0); mid > 0 {
		v := int64(mid)
		uploadReq.MessageID = &v
	}

	result, err := atr.attachmentService.Upload(ctx, uploadReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("upload_attachment failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"hash":              result.Hash,
		"size":              result.Size,
		"mime_type":         result.MIMEType,
		"original_filename": result.Filename,
	})
}

func (atr *AttachmentToolRegistrar) handleDownloadAttachment(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	hash := req.GetString("hash", "")
	if hash == "" {
		return mcp.NewToolResultError("'hash' parameter is required"), nil
	}

	result, err := atr.attachmentService.Download(ctx, hash)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("download_attachment failed: %s", err)), nil
	}
	defer result.Content.Close()

	// Read content and base64-encode it.
	content, err := io.ReadAll(result.Content)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read attachment content failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"hash":              result.Hash,
		"content":           base64.StdEncoding.EncodeToString(content),
		"original_filename": result.Filename,
		"mime_type":         result.MIMEType,
		"size":              result.Size,
	})
}

func (atr *AttachmentToolRegistrar) handleGCAttachments(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	result, err := atr.attachmentService.GarbageCollect(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("gc_attachments failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"files_removed":   result.FilesRemoved,
		"bytes_reclaimed": result.BytesReclaimed,
	})
}
