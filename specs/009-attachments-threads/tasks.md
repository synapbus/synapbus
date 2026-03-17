# Tasks: Attachments & Threads Enhancement

**Input**: Design documents from `/specs/009-attachments-threads/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Included (explicitly requested in feature description)

**Organization**: Tasks grouped by user story for independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Phase 1: Setup

**Purpose**: No new project setup needed — extending existing codebase. Verify current state.

- [x] T001 Verify migration 007_threads.sql exists and is applied in internal/storage/schema/007_threads.sql
- [x] T002 Run `make test` to confirm existing tests pass before changes

**Checkpoint**: Baseline verified — proceed with foundational changes

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Backend data model changes that ALL user stories depend on

**CRITICAL**: No user story work can begin until this phase is complete

- [x] T003 Add ReplyCount and Attachments fields to Message struct in internal/messaging/types.go
- [x] T004 Add GetReplyCounts batch query method to store in internal/messaging/store.go
- [x] T005 Add AttachmentLinker interface and EnrichMessages method to wire attachment store into message service in internal/messaging/service.go
- [x] T006 Add AttachToMessage call in SendMessage flow — accept attachment hashes, link after message insert in internal/messaging/service.go
- [x] T007 Add file type validation (allowlist: image/*, application/pdf, text/*) to upload in internal/attachments/service.go
- [x] T008 Write tests for reply_count queries in internal/messaging/store_test.go
- [x] T009 Write tests for attachment linking in SendMessage in internal/messaging/service_test.go
- [x] T010 Write tests for file type validation in internal/attachments/service_test.go

**Checkpoint**: Foundation ready — message API returns reply_count and attachments, file type validation enforced

---

## Phase 3: User Story 1 — Attach Files from Web UI (Priority: P1)

**Goal**: Users can attach files in the compose form and send messages with attachments

**Independent Test**: Log in to web UI, compose message with attached file, send, verify attachment appears

### Implementation for User Story 1

- [x] T011 [US1] Accept `attachments` (array of hashes) in send message API request and link them after message creation in internal/api/messages_handler.go
- [x] T012 [US1] Enrich message API responses with attachments array (query by message_id) in internal/api/messages_handler.go
- [x] T013 [US1] Add `uploadAttachment` method to API client (multipart form upload) in web/src/lib/api/client.ts
- [x] T014 [US1] Add attachment upload button, file picker, upload progress, and preview chips to ComposeForm in web/src/lib/components/ComposeForm.svelte
- [x] T015 [US1] Send attachment hashes with message when submitting compose form in web/src/lib/components/ComposeForm.svelte

**Checkpoint**: Users can attach and send files from web UI

---

## Phase 4: User Story 2 — View Image Attachments with Thumbnails and Fullscreen (Priority: P1)

**Goal**: Messages display image thumbnails inline; click opens fullscreen; non-image attachments show file icon with download

**Independent Test**: View a message with image attachment, verify thumbnail renders, click for fullscreen, download works

### Implementation for User Story 2

- [x] T016 [US2] Create AttachmentPreview.svelte component with image thumbnail (200x200 CSS), fullscreen overlay, close/download buttons in web/src/lib/components/AttachmentPreview.svelte
- [x] T017 [US2] Integrate attachment display into MessageList.svelte — render AttachmentPreview for images, file icons for others in web/src/lib/components/MessageList.svelte
- [x] T018 [US2] Add keyboard handler for Escape key to close fullscreen overlay in web/src/lib/components/AttachmentPreview.svelte
- [x] T019 [US2] Add attachment display to ThreadPanel.svelte for thread messages in web/src/lib/components/ThreadPanel.svelte

**Checkpoint**: Attachment display fully functional in web UI

---

## Phase 5: User Story 3 — Thread Visibility and Reply Indicators (Priority: P1)

**Goal**: Messages with replies show reply count badge; clicking opens thread panel; thread replies visible

**Independent Test**: Send reply to a message, verify parent shows "N replies" badge, click to open thread panel

### Implementation for User Story 3

- [x] T020 [US3] Ensure all message handlers return reply_count in response JSON via EnrichMessages in internal/api/messages_handler.go
- [x] T021 [US3] Add always-visible thread indicator badge ("N replies") to MessageList when reply_count > 0 in web/src/lib/components/MessageList.svelte
- [x] T022 [US3] Wire thread indicator click to openThread() to open ThreadPanel in web/src/lib/components/MessageList.svelte

**Checkpoint**: Thread visibility fixed — users can see and navigate thread replies

---

## Phase 6: User Story 4 — Agents Attach Files via MCP Tools (Priority: P2)

**Goal**: Agents upload files and link them to messages via MCP tools

**Independent Test**: Use MCP execute tool to upload_attachment, then send_message with attachments, verify linked

### Implementation for User Story 4

- [x] T023 [US4] Add `attachments` parameter (comma-separated hashes) to send_message MCP tool schema in internal/mcp/tools_hybrid.go
- [x] T024 [US4] Handle attachments parameter in handleSendMessage — parse hashes, pass to service in internal/mcp/tools_hybrid.go
- [x] T025 [US4] Handle attachments in bridge.go callSendMessage and callSendChannelMessage in internal/mcp/bridge.go
- [x] T026 [US4] Include attachments in MCP message responses via EnrichMessages in internal/mcp/tools_hybrid.go
- [x] T027 [US4] Update BroadcastMessage signature to accept attachments in internal/channels/service.go

**Checkpoint**: Agents can attach files to messages via MCP

---

## Phase 7: User Story 5 — Agents Reply in Threads via MCP (Priority: P2)

**Goal**: MCP tool descriptions clearly document threading; agents can reliably use reply_to

**Independent Test**: Send threaded message via MCP, verify reply appears in thread, tool descriptions are clear

### Implementation for User Story 5

- [x] T028 [US5] Update send_message tool description to clearly document reply_to usage and threading behavior in internal/mcp/tools_hybrid.go
- [x] T029 [US5] Update upload_attachment action description to document workflow in internal/actions/registry.go
- [x] T030 [US5] Include reply_to field in MCP message responses so agents see thread context in internal/mcp/tools_hybrid.go

**Checkpoint**: MCP tools are self-documenting for threading and attachments

---

## Phase 8: User Story 6 — Admin Backup and Restore Attachments (Priority: P3)

**Goal**: Admin CLI can backup attachments to tar.gz and restore from archive

**Independent Test**: Run backup, verify archive, restore to empty dir, verify files recovered

### Implementation for User Story 6

- [x] T031 [US6] Add `synapbus attachments backup --output <path>` subcommand using archive/tar + compress/gzip in cmd/synapbus/admin.go
- [x] T032 [US6] Add `synapbus attachments restore --input <path>` subcommand with dedup-safe extraction in cmd/synapbus/admin.go

**Checkpoint**: Admin can backup and restore attachment files

---

## Phase 9: Polish & Cross-Cutting Concerns

**Purpose**: Final integration, edge cases, build verification

- [x] T033 Run `go test ./...` to verify all Go tests pass (25 packages, 0 failures)
- [x] T034 Run `make web` to build Svelte SPA and verify no build errors
- [x] T035 Run `make build` to compile final binary (90MB arm64)
- [x] T036 Run integration tests to verify no regressions (9 E2E tests pass)

---

## Completion Summary

All 36 tasks completed. All tests pass. Build successful.
