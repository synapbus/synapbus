# Implementation Plan: Proactive Memory & Dream Worker

**Branch**: `020-proactive-memory-dream-worker` | **Date**: 2026-05-11 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/020-proactive-memory-dream-worker/spec.md`

## Summary

Make SynapBus push minimal owner-scoped memory into every agent task automatically (Story 1, P1), give each agent a small editable identity blob always-included in session-start (Story 2, P2), and run a background "dream" worker that periodically dispatches consolidation work to a Claude-Code agent through the existing `harness.Harness` seam (Story 3, P2). An owner-facing audit/override UI lands later (Story 4, P3). All work reuses existing primitives: `search.Service` for retrieval, `harness.Harness` for dispatch, `messages` table for the memory pool, the `StalemateWorker` ticker pattern for the new `ConsolidatorWorker`. One new SQLite migration adds six tables. Zero CGO, zero new external dependencies.

## Technical Context

**Language/Version**: Go 1.25+ (per `go.mod`)
**Primary Dependencies**: `mark3labs/mcp-go` (MCP tools), `go-chi/chi` (HTTP), `modernc.org/sqlite` (storage), `TFMV/hnsw` (vectors via existing `search.Service`), existing `internal/harness` package (dispatch seam). **No new external dependencies.**
**Storage**: SQLite via `modernc.org/sqlite` — one new migration `028_memory_consolidation.sql`. Memory pool reuses the existing `messages` table on memory-flagged channels.
**Testing**: `go test ./...` table-driven tests, mocked `harness.Harness` for dream-worker tests, in-memory SQLite for storage tests
**Target Platform**: `linux/amd64` (kubic deployment), `darwin/arm64` (dev). Pure-Go cross-compilation required.
**Project Type**: Single-binary Go service with embedded Svelte SPA (existing layout).
**Performance Goals**: Median injection overhead < 50ms per tool call (SC-002). Full owner-level consolidation pass ≤ 10 min for ≤ 5,000 memories (SC-005).
**Constraints**: Zero CGO (Constitution III). Single binary (Constitution I). Injection MUST be disable-able with zero payload-shape change when disabled (FR-012, SC-009).
**Scale/Scope**: One owner with ~500 memories today; design for 5k/owner, 10 owners on the reference deployment.

## Constitution Check

*Gate: must pass before Phase 0 research. Re-evaluated after Phase 1 design.*

| Principle | Check | Status |
|-----------|-------|--------|
| I. Local-first, single binary | New worker ships in the same binary; no external service introduced. | ✅ |
| II. MCP-native | New memory-consolidation tools registered via `mark3labs/mcp-go`. REST endpoints are added only for the Web UI audit tab (Phase 2). | ✅ |
| III. Pure Go, zero CGO | No new dependencies; reuses `modernc.org/sqlite` + `TFMV/hnsw`. | ✅ |
| IV. Multi-tenant w/ ownership | Owner scoping is the *core* of the design. Retrieval filters by `owner_id`; dispatch tokens are owner-bound. | ✅ |
| V. Embedded OAuth 2.1 | Unchanged; consolidation agents authenticate via the existing API-key path. | ✅ |
| VI. Semantic-ready storage | Reuses `search.Service.Search()`; gracefully degrades to FTS when no embedding provider configured. | ✅ |
| VII. Swarm intelligence | `#open-brain` is a stigmergic blackboard already. Reflection writes annotated memories back to the blackboard. | ✅ |
| VIII. Observable by default | Every consolidation mutation hits an immutable `memory_consolidation_jobs` audit row + a `memory_audit_log` row keyed by dispatch token. Every injection hits `memory_injections` (24h ring). | ✅ |
| IX. Progressive complexity | Feature is gated by `SYNAPBUS_INJECTION_ENABLED` and `SYNAPBUS_DREAM_ENABLED`. Default off until owner audit UI lands. | ✅ |
| X. Web UI first-class | Memory tab is in scope but deferred to Phase 2 of this feature (post-kubic-deploy). Audit data is captured from day one so the UI can render history retroactively. | ⚠ deferred — tracked, not skipped |

**Gate result**: PASS. No violations require Complexity Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/020-proactive-memory-dream-worker/
├── plan.md              # This file
├── spec.md              # Feature spec
├── research.md          # Phase 0 — technical-unknowns resolution
├── data-model.md        # Phase 1 — entities and tables
├── contracts/
│   ├── mcp-injection.md           # Shape of relevant_context block on tool responses
│   └── mcp-memory-tools.md        # 6 new dream-agent tools (input schemas, behavior, errors)
├── quickstart.md        # Phase 1 — how to verify locally and on kubic
├── tasks.md             # (Phase 2 — produced by /speckit.tasks)
└── checklists/
    └── requirements.md  # Spec quality checklist
```

### Source Code (repository root, existing layout extended)

```text
cmd/synapbus/main.go               # MODIFIED: wire up ConsolidatorWorker, gate new MCP tools

internal/
├── messaging/
│   ├── consolidator.go            # NEW: ConsolidatorWorker (ticker + dispatch via harness)
│   ├── consolidator_test.go       # NEW
│   ├── memory.go                  # NEW: core-memory CRUD, link CRUD, supersession, pin/protected
│   ├── memory_test.go             # NEW
│   ├── memory_channels.go         # NEW: discover memory-flagged channels per owner
│   └── stalemate.go               # UNCHANGED — pattern template
├── search/
│   └── injection.go               # NEW: BuildContextPacket(ctx, agent, opts) → ContextPacket
├── mcp/
│   ├── injection_wrap.go          # NEW: middleware appending relevant_context to tool results
│   ├── memory_tools.go            # NEW: 6 dream-agent tools (memory_list_unprocessed,
│   │                              #      memory_write_reflection, memory_rewrite_core,
│   │                              #      memory_mark_duplicate, memory_supersede, memory_add_link)
│   ├── memory_tools_test.go       # NEW
│   ├── tools_hybrid.go            # MODIFIED: wrap eligible tool handlers via injection_wrap
│   └── server.go                  # MODIFIED: register memory tools when dream worker enabled
├── storage/schema/
│   └── 028_memory_consolidation.sql   # NEW: 6 tables (see data-model.md)
└── harness/                         # UNCHANGED — used via existing Execute() API

docs/superpowers/specs/2026-05-11-internal-only-disable-approvals-design.md  # pre-existing, untouched
```

**Structure Decision**: Single-project Go layout (matches existing `cmd/synapbus` + `internal/*` structure). Memory pool reuses `messages`; six new tables in one new migration are the only schema addition. No new top-level package — new files split between `internal/messaging` (storage-adjacent), `internal/search` (retrieval-adjacent), and `internal/mcp` (tool-surface-adjacent).

## Phase 0 — Research

See [research.md](./research.md). Resolved unknowns:

1. **How does the dream worker invoke a Claude Code agent without using a system DM?** → through `harness.Harness.Execute(ExecRequest)` with the existing `kubernetes_job` or `local_subprocess` backend. The agent's MCP session carries a one-time `dispatch_token` injected via `ExecRequest.Env` that authorizes the consolidation tools.
2. **Where do owners live in the data model today?** → `agents.owner_id` (text) is the canonical scope. Every retrieval JOINs `agents` to resolve the caller's owner.
3. **Soft-delete on messages — new column or metadata flag?** → metadata flag on `memory_consolidation_jobs.actions` + a derived view. Avoids touching the hot `messages` table. Soft-delete is enforced at retrieval time by joining against `memory_status` view.
4. **Token-budget estimator** → `len(s)/4` heuristic. Faster than tokenizing, accurate enough for budget enforcement.
5. **MCP transport — how do we identify the calling agent's API key/owner?** → existing `auth.ContextAgent(ctx)` returns `*agents.Agent` from the request context; `agent.OwnerID` is the scope key.

## Phase 1 — Design & Contracts

### Data model

See [data-model.md](./data-model.md). Migration `028_memory_consolidation.sql` adds six tables:

- `memory_core` — per-(owner, agent_name) editable text blob, size-capped at `SYNAPBUS_CORE_MEMORY_MAX_BYTES` (default 2048).
- `memory_links` — directed typed edges between two message IDs.
- `memory_consolidation_jobs` — audit log of every dispatched dream job + the actions it took.
- `memory_pins` — owner-pinned message IDs, always-included in injection.
- `memory_dispatch_tokens` — one-time, owner-bound, single-job-bound tokens.
- `memory_injections` — 24h rolling ring of (tool_name, agent_id, packet_summary) for debug-ability.

Plus one view `memory_status` (active / soft-deleted / superseded — derived from `memory_consolidation_jobs.actions`).

### Contracts

- [contracts/mcp-injection.md](./contracts/mcp-injection.md) — shape of `relevant_context` block appended to tool responses.
- [contracts/mcp-memory-tools.md](./contracts/mcp-memory-tools.md) — input/output schemas for the six new memory-consolidation tools.

### Quickstart

See [quickstart.md](./quickstart.md) — covers local dev (build → migrate → seed → run with `SYNAPBUS_INJECTION_ENABLED=1 SYNAPBUS_DREAM_ENABLED=1`) and kubic deployment (build image, push, kustomize apply, tail dream-worker logs, dispatch a manual reflection job, verify audit log).

### Agent context update

Triggered by the workflow: `update-agent-context.sh claude` adds the new packages and migration to `CLAUDE.md` "Active Technologies" block.

## Phase 2 — Tasks

(Produced by `/speckit.tasks`. Not created by this command.)

## Complexity Tracking

No constitution violations. Memory-tab Web UI is deferred (tracked as Phase 2 of this feature) but is not a constitution violation since audit data is captured from day one and surfaces in CLI/REST until the SPA tab lands.
