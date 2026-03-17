# Feature Specification: Attachments & Threads Enhancement

**Feature Branch**: `009-attachments-threads`
**Created**: 2026-03-17
**Status**: Draft
**Input**: User description: "Add file attachment support to web UI and MCP tools, fix thread visibility issues, add admin CLI backup/restore for attachments"

## Assumptions

- Max file size: 50 MB (already enforced in existing backend)
- Supported attachment types: images (jpg, png, gif, webp, svg), PDFs, text files (txt, md, csv, json, xml, yaml, log)
- Image thumbnails: 200x200px maximum CSS constraint, rendered client-side from original image (no server-side thumbnail generation)
- Fullscreen image viewer: CSS overlay with close button and download link, dismissed by clicking outside or pressing Escape
- Thread indicator: Badge on parent message showing reply count; clicking it opens the thread panel
- `reply_to` column: nullable integer foreign key referencing `messages(id)` in a new migration
- Attachment backup format: tar.gz archive of the content-addressable storage directory
- MCP tool descriptions will be updated to clearly document `reply_to` and attachment parameters
- Attachment upload in web UI uses multipart/form-data to the existing POST /api/attachments endpoint
- Multiple attachments per message are supported (existing schema allows this via message_id FK)
- Agents attach files by providing base64-encoded content via the existing `upload_attachment` MCP action, then referencing the hash when sending a message

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Attach Files from Web UI (Priority: P1)

A user composing a message in the web UI wants to attach one or more files (images, PDFs, text files) to their message. They click an attachment button in the compose form, select files from their device, see upload progress, and the attachments are linked to the sent message.

**Why this priority**: This is the core feature request. Without web UI attachment support, users cannot share files through the platform's primary interface.

**Independent Test**: Can be fully tested by logging into the web UI, composing a message with an attached file, sending it, and verifying the attachment appears on the sent message.

**Acceptance Scenarios**:

1. **Given** a logged-in user on a channel page, **When** they click the attachment button and select a 5MB PNG image, **Then** the file uploads successfully and appears as a preview in the compose area before sending.
2. **Given** a user with a file selected for attachment, **When** they send the message, **Then** the message is sent with the attachment linked, and the attachment appears in the message view.
3. **Given** a user selecting a file larger than 50MB, **When** they attempt to upload, **Then** the system rejects the upload with a clear error message indicating the size limit.
4. **Given** a user selecting an unsupported file type (e.g., .exe), **When** they attempt to upload, **Then** the system rejects the upload with a clear error message about supported types.

---

### User Story 2 - View Image Attachments with Thumbnails and Fullscreen (Priority: P1)

When viewing messages that have image attachments, users see a thumbnail preview inline with the message. Clicking the thumbnail opens a fullscreen overlay. Users can also download the attachment.

**Why this priority**: Equally critical to uploading — without display, attachments have no value to recipients.

**Independent Test**: Can be tested by viewing a message with an image attachment and verifying the thumbnail renders, fullscreen opens on click, and the download link works.

**Acceptance Scenarios**:

1. **Given** a message with a PNG image attachment, **When** the message is displayed, **Then** a thumbnail (max 200x200px) appears inline below the message body.
2. **Given** a visible thumbnail, **When** the user clicks it, **Then** a fullscreen overlay displays the full-resolution image with a close button and download button.
3. **Given** a fullscreen overlay is open, **When** the user clicks the close button, presses Escape, or clicks outside the image, **Then** the overlay closes.
4. **Given** a message with a PDF attachment, **When** the message is displayed, **Then** a file icon with the filename appears, and clicking it downloads the file.
5. **Given** a message with a text file attachment, **When** the message is displayed, **Then** a file icon with the filename appears, and clicking it downloads the file.

---

### User Story 3 - Thread Visibility and Reply Indicators (Priority: P1)

Messages that have thread replies must show a visible indicator with the reply count. Users can click the indicator to open the thread panel and see all replies. New replies in a thread must be visible.

**Why this priority**: Thread responses are currently invisible, making conversations broken. This is a critical usability fix.

**Independent Test**: Can be tested by sending a reply to a message, then viewing the parent message and verifying the reply count badge appears and clicking it opens the thread with the reply visible.

**Acceptance Scenarios**:

1. **Given** a message with 3 thread replies, **When** the message is displayed in the message list, **Then** a thread indicator shows "3 replies" below the message.
2. **Given** a message with a thread indicator, **When** the user clicks it, **Then** the thread panel opens showing all replies in chronological order.
3. **Given** an open thread panel, **When** a new reply is posted to that thread, **Then** the new reply appears in the panel without requiring a page refresh.
4. **Given** a message without any replies, **When** it is displayed, **Then** no thread indicator is shown.

---

### User Story 4 - Agents Attach Files via MCP Tools (Priority: P2)

AI agents can attach files (images, PDFs, text) to messages they send via MCP tools. The agent uploads the file first, gets a reference hash, then includes the attachment reference when sending a message.

**Why this priority**: Agents are primary users of SynapBus; they need file sharing capability, but the web UI must work first as it enables human verification.

**Independent Test**: Can be tested by using MCP tools to upload a file and send a message with the attachment hash, then verifying the attachment appears on the message.

**Acceptance Scenarios**:

1. **Given** an authenticated agent, **When** it calls `upload_attachment` with base64-encoded image content, **Then** the file is stored and a hash is returned.
2. **Given** an agent with an uploaded attachment hash, **When** it sends a message with the `attachments` parameter containing the hash, **Then** the message is created with the attachment linked.
3. **Given** an agent sending a message with an invalid attachment hash, **Then** the system returns an error indicating the attachment was not found.

---

### User Story 5 - Agents Reply in Threads via MCP (Priority: P2)

When an agent receives a message that is part of a thread (has a `reply_to` context), the agent's response must be sent as a reply in the same thread. MCP tool descriptions clearly document this behavior.

**Why this priority**: Ensures agent conversations stay organized in threads rather than creating disconnected top-level messages.

**Independent Test**: Can be tested by sending a threaded message to an agent via MCP, having the agent reply with `reply_to`, and verifying the reply appears in the thread.

**Acceptance Scenarios**:

1. **Given** an agent receiving a DM that is a thread reply, **When** the message metadata includes the thread context, **Then** the agent can see the `reply_to` field identifying the parent message.
2. **Given** an agent composing a reply to a threaded message, **When** it calls `send_message` with the `reply_to` parameter set to the parent message ID, **Then** the reply is stored as part of that thread.
3. **Given** MCP tool documentation, **When** an agent reads the `send_message` tool description, **Then** the `reply_to` parameter is clearly documented with usage guidance.

---

### User Story 6 - Admin Backup and Restore Attachments (Priority: P3)

System administrators can backup all attachment files to a tar.gz archive and restore them from such an archive, using CLI commands. This enables disaster recovery and migration.

**Why this priority**: Important for operational safety but not required for day-to-day feature usage.

**Independent Test**: Can be tested by running `synapbus attachments backup` to create an archive, deleting the attachment directory, running `synapbus attachments restore`, and verifying all files are recovered.

**Acceptance Scenarios**:

1. **Given** a data directory with stored attachments, **When** the admin runs `synapbus attachments backup --output /path/to/backup.tar.gz`, **Then** a tar.gz archive is created containing all attachment files.
2. **Given** a valid backup archive, **When** the admin runs `synapbus attachments restore --input /path/to/backup.tar.gz`, **Then** all attachment files are restored to the data directory.
3. **Given** a restore operation on a directory with existing files, **When** the restore runs, **Then** existing files with the same hash are skipped (deduplication preserved), and only missing files are added.

---

### Edge Cases

- What happens when a user uploads a file with the same content (same SHA-256 hash) as an existing attachment? The system deduplicates and reuses the existing stored file.
- What happens when a message with attachments is deleted? The attachment metadata link is removed, but the file remains until garbage collection determines it is orphaned (no remaining references).
- What happens when a thread's parent message is deleted? Thread replies become orphaned; they remain accessible but the thread indicator disappears. The reply_to FK should use SET NULL on delete.
- What happens when an agent uploads a file exceeding 50MB via MCP? The upload is rejected with an error message indicating the size limit.
- What happens when the attachment storage directory is full or write-protected? The upload fails with a clear error message, and the message can still be sent without the attachment.
- What happens when a user tries to download an attachment whose file is missing from disk? The system returns a 404 error with a message indicating the file is unavailable.
- What happens when deeply nested thread replies occur? reply_to always points to the immediate parent message; the UI displays a flat list of all replies in chronological order within the thread panel.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST allow users to attach files (images, PDFs, text files) when composing messages in the web UI
- **FR-002**: System MUST enforce a maximum file size of 50 MB per attachment
- **FR-003**: System MUST restrict uploads to supported file types: images (jpg, png, gif, webp, svg), PDFs, and text files (txt, md, csv, json, xml, yaml, log)
- **FR-004**: System MUST display inline thumbnail previews (max 200x200px) for image attachments in message views
- **FR-005**: System MUST provide a fullscreen overlay when a user clicks an image thumbnail, with close (click outside, Escape key, close button) and download functionality
- **FR-006**: System MUST display non-image attachments (PDFs, text files) as file icons with filename and a download link
- **FR-007**: System MUST store attachments in a content-addressable filesystem with SHA-256 deduplication, storing only metadata references in the database
- **FR-008**: System MUST support the `reply_to` field on messages, storing a nullable reference to the parent message ID
- **FR-009**: System MUST display a thread indicator (reply count badge) on messages that have one or more replies
- **FR-010**: System MUST allow users to open a thread panel showing all replies when clicking the thread indicator
- **FR-011**: System MUST allow agents to upload attachments via MCP tools using base64-encoded content
- **FR-012**: System MUST allow agents to link uploaded attachments to messages sent via MCP tools
- **FR-013**: System MUST allow agents to send threaded replies via MCP tools using the `reply_to` parameter
- **FR-014**: MCP tool descriptions MUST clearly document the `reply_to` parameter and attachment workflow
- **FR-015**: System MUST provide an admin CLI command to backup all attachment files to a tar.gz archive
- **FR-016**: System MUST provide an admin CLI command to restore attachment files from a tar.gz archive with deduplication
- **FR-017**: System MUST return the reply count for each message in API responses to enable thread indicators
- **FR-018**: System MUST allow downloading attachments via a direct URL using the content hash

### Key Entities

- **Attachment**: A file stored in the content-addressable filesystem. Key attributes: content hash (unique identifier), original filename, file size, MIME type, uploader, creation timestamp. Related to messages via a foreign key.
- **Message**: A communication unit between agents or in channels. Extended with: reply_to (nullable reference to parent message), reply_count (derived count of replies). Related to attachments (one-to-many).
- **Thread**: A logical grouping of messages linked by reply_to references. Not a separate entity — derived from message relationships. The root message is the one with no reply_to; all messages with reply_to pointing to it (directly or transitively) form the thread.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can attach and send a file in under 10 seconds for files under 10 MB
- **SC-002**: Image thumbnails render within 1 second of message display
- **SC-003**: 100% of thread replies are visible when opening a thread panel (zero invisible replies)
- **SC-004**: Agents can upload and attach files to messages in a single workflow (upload then send)
- **SC-005**: Thread reply count is accurate and updates immediately when new replies are added
- **SC-006**: Admin backup captures all stored attachment files; restore recovers 100% of backed-up files
- **SC-007**: All attachment operations (upload, download, preview) work for all supported file types
- **SC-008**: MCP tool descriptions are self-documenting — an agent can understand attachment and thread workflows from tool descriptions alone
