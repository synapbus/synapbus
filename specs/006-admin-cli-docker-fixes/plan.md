# Implementation Plan: Admin CLI & Docker Fixes

**Branch**: `006-admin-cli-docker-fixes` | **Date**: 2026-03-15 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/006-admin-cli-docker-fixes/spec.md`

## Summary

Fix four operational issues blocking reliable admin CLI usage in containerized SynapBus: switch Docker base image from `scratch` to `alpine:3.19` so admin socket CLI works via `kubectl exec`, add `synapbus channels create` and `synapbus channels join` CLI commands backed by new admin socket handlers, and change the default socket path from relative `./data/synapbus.sock` to absolute `/data/synapbus.sock`.

## Technical Context

**Language/Version**: Go 1.25+ (per go.mod)
**Primary Dependencies**: spf13/cobra (CLI), go-chi/chi (HTTP), mark3labs/mcp-go (MCP)
**Storage**: modernc.org/sqlite (pure Go, zero CGO)
**Testing**: `go test ./...` (table-driven tests)
**Target Platform**: linux/amd64, darwin/arm64 (Docker + local dev)
**Project Type**: CLI / web-service (single binary)
**Performance Goals**: Admin socket commands complete in < 1 second
**Constraints**: Zero CGO, single binary, pure Go
**Scale/Scope**: Single instance, admin-only operations

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Local-First, Single Binary | PASS | No new external dependencies. Alpine base image only adds shell availability. |
| II. MCP-Native | PASS | Changes are admin CLI only, no MCP interface changes. |
| III. Pure Go, Zero CGO | PASS | No new Go dependencies. Dockerfile still builds with `CGO_ENABLED=0`. |
| IV. Multi-Tenant with Ownership | PASS | Admin socket is localhost-only, trusted operator context. |
| V. Embedded OAuth 2.1 | N/A | No auth changes. |
| VI. Semantic-Ready Storage | N/A | No storage schema changes. |
| VII. Swarm Intelligence | N/A | No swarm pattern changes. |
| VIII. Observable by Default | PASS | Channel create/join are traced via existing channel service. |
| IX. Progressive Complexity | PASS | New CLI commands add no complexity for basic usage. |
| X. Web UI | N/A | No UI changes. |

**Gate Result**: PASS — no violations.

## Project Structure

### Documentation (this feature)

```text
specs/006-admin-cli-docker-fixes/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (admin socket protocol)
└── tasks.md             # Phase 2 output (via /speckit.tasks)
```

### Source Code (repository root)

```text
# Files modified:
Dockerfile                          # scratch → alpine:3.19
cmd/synapbus/admin.go               # Add channels create/join commands, fix default socket path
internal/admin/socket.go            # Add channels.create and channels.join handlers
cmd/synapbus/admin_test.go          # Tests for new CLI commands

# Files unchanged but referenced:
internal/channels/service.go        # CreateChannel, JoinChannel (already exist)
internal/channels/store.go          # GetChannelByName (already exists)
internal/admin/server.go            # Services struct (already has Channels field)
```

**Structure Decision**: This feature modifies 3 existing files and adds no new files. All changes fit within the existing project structure.

## Complexity Tracking

No constitution violations to justify.
