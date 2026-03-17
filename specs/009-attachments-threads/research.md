# Research: Attachments & Threads Enhancement

**Branch**: `009-attachments-threads` | **Date**: 2026-03-17

## Decision 1: Migration Location for reply_to

**Decision**: Migration `007_threads.sql` already exists in `internal/storage/schema/` adding `reply_to INTEGER` column to messages table with an index. No new migration needed for the column itself.

**Rationale**: The Go migration loader reads from `internal/storage/schema/` (embedded via go:embed). The `schema/` directory at repo root is for documentation/reference. The column already exists in the running system.

**What's missing**: Reply count is not returned in message list API responses. Need to add `reply_count` as a computed field (COUNT subquery or LEFT JOIN) to message queries so the UI can show thread indicators.

## Decision 2: Attachment-Message Linking Flow

**Decision**: Use a two-step flow: (1) upload attachment → get hash, (2) send message with `attachments` field containing hash array. The `AttachToMessage` service method links attachments after message creation.

**Rationale**: The existing upload endpoint returns a hash. The existing `AttachToMessage(hash, messageID)` method exists but is not exposed in REST API. Need to:
1. Add `attachments` field to the send message API request
2. After message insert, call `AttachToMessage` for each hash
3. Include attachments in message API responses (query by message_id)

**Alternatives considered**:
- Single multipart upload with message: Too complex for MCP agents, breaks the existing API pattern
- Store attachment hashes in message metadata JSON: Loses relational integrity, harder to query

## Decision 3: Thumbnail Generation

**Decision**: Client-side only. Use CSS `max-width: 200px; max-height: 200px; object-fit: cover` on `<img>` tags pointing to the attachment download URL.

**Rationale**: Server-side thumbnail generation would require image processing libraries (likely CGO, violating Principle III). The original images are served directly; the browser handles resizing. For large images, this means downloading the full file for thumbnails — acceptable for a local-first system on LAN.

**Alternatives considered**:
- Server-side thumbnails with pure Go image library: Adds complexity, new dependency, storage overhead
- Lazy loading with IntersectionObserver: Good optimization to add but not core requirement

## Decision 4: Fullscreen Image Overlay

**Decision**: Simple Svelte component with fixed overlay, `<img>` at natural size (max viewport), close button, download button. Dismiss via Escape, click outside, or close button.

**Rationale**: Minimal implementation that meets requirements. No carousel needed (one image at a time). No zoom/pan needed for v1.

## Decision 5: Admin Backup/Restore

**Decision**: `synapbus attachments backup --output path.tar.gz` and `synapbus attachments restore --input path.tar.gz`. Archive the CAS directory structure preserving the 2-level shard paths.

**Rationale**: The CAS directory is self-contained (`{dataDir}/attachments/`). tar.gz preserves directory structure and is universally supported. Restore skips existing files (same hash = same content = dedup).

**Alternatives considered**:
- Backup via admin socket: Useful for remote backup but adds complexity; CLI direct file access is simpler
- Include SQLite metadata in backup: Not needed — metadata can be reconstructed from CAS files + existing DB

## Decision 6: Thread Visibility in UI

**Decision**: Add reply_count to message list responses. Show a clickable "N replies" badge below messages. Clicking opens ThreadPanel. ThreadPanel already exists and works (loads conversation, supports replying).

**Rationale**: The ThreadPanel already handles thread display and replying. The missing piece is the reply_count in the message list so users know threads exist. The "View thread" button exists in MessageList but only on hover — need to make it always visible when reply_count > 0.

## Decision 7: MCP Tool Descriptions

**Decision**: Update `send_message` tool description to document:
1. `reply_to` parameter: "ID of the parent message to reply to. Creates a threaded reply. Always use this when responding to a message that is part of a thread."
2. `attachments` parameter (new): "Array of attachment hashes to link to this message. Upload attachments first using the upload_attachment action."

**Rationale**: Agents need clear tool descriptions to know how to use threading and attachments. The current reply_to description is minimal.

## Decision 8: File Type Validation

**Decision**: Validate MIME type on upload against an allowlist. Supported: image/*, application/pdf, text/*. Reject everything else with a clear error message listing supported types.

**Rationale**: Security best practice — don't allow arbitrary file uploads. The existing MIME detection in `mime.go` already handles type detection; just need to add validation in the upload handler.
