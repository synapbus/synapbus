---
description: "Task list — proactive memory + dream worker"
---

# Tasks: Proactive Memory & Dream Worker

**Input**: Design documents from `/specs/020-proactive-memory-dream-worker/`
**Prerequisites**: plan.md ✓, spec.md ✓, research.md ✓, data-model.md ✓, contracts/ ✓, quickstart.md ✓
**Tests**: Tests are REQUESTED (test-before-impl pairs marked explicitly).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelizable — different files, no in-flight dependencies
- **[Story]**: `[US1]`, `[US2]`, `[US3]` — maps to spec user stories
- Setup / Foundational / Polish tasks carry no `[Story]` label

## Scope for this round

Foundational → US1 (P1 injection) → US2 (P2 core memory) → US3 (P2 dream worker). **US4 (P3 audit UI) is deferred** — captured in plan.md Constitution Check as Phase 2 of this feature.

---

## Phase 1: Setup

Branch and spec artifacts already exist. Only verifying scaffolding.

- [ ] T001 Verify branch `020-proactive-memory-dream-worker` is checked out and all spec artifacts (spec.md, plan.md, research.md, data-model.md, contracts/, quickstart.md) are present at `/Users/user/repos/synapbus/specs/020-proactive-memory-dream-worker/`. No code changes.

---

## Phase 2: Foundational (Blocks all user stories)

**⚠️ CRITICAL**: No `[US*]` task may start until this phase is complete.

- [ ] T002 Write SQL migration in `/Users/user/repos/synapbus/internal/storage/schema/028_memory_consolidation.sql` per data-model.md (6 tables: `memory_core`, `memory_links`, `memory_consolidation_jobs`, `memory_pins`, `memory_dispatch_tokens`, `memory_injections` + `memory_status` view). Idempotent `CREATE TABLE IF NOT EXISTS`, all indexes, the partial-unique index on `memory_consolidation_jobs`, and the CHECK constraints on enums.
- [ ] T003 [P] Migration smoke test in `/Users/user/repos/synapbus/internal/storage/migration_test.go` — verify migration 028 applies cleanly on a fresh in-memory DB and all 6 tables + 1 view are queryable.
- [ ] T004 [P] Add feature-flag env parsing in `/Users/user/repos/synapbus/internal/messaging/options.go` (or appropriate config struct): `SYNAPBUS_INJECTION_ENABLED` (default 0), `SYNAPBUS_INJECTION_BUDGET_TOKENS` (default 500), `SYNAPBUS_INJECTION_MAX_ITEMS` (default 5), `SYNAPBUS_INJECTION_MIN_SCORE` (default 0.25), `SYNAPBUS_CORE_MEMORY_MAX_BYTES` (default 2048), `SYNAPBUS_DREAM_ENABLED` (default 0), `SYNAPBUS_DREAM_INTERVAL` (default 1h), `SYNAPBUS_DREAM_DEEP_CRON` (default `0 3 * * *`), `SYNAPBUS_DREAM_MAX_CONCURRENT` (default 4), `SYNAPBUS_DREAM_WALLCLOCK_BUDGET` (default 10m), `SYNAPBUS_DREAM_WATERMARK` (default 20), `SYNAPBUS_DREAM_AGENT` (default `claude-code`).
- [ ] T005 [P] Owner resolver helper in `/Users/user/repos/synapbus/internal/agents/owner.go`: `OwnerFor(ctx, db, agentName) (string, error)` that looks up `agents.owner_id`. Returns sentinel error if agent not found or unowned.
- [ ] T006 [P] Dispatch-token store in `/Users/user/repos/synapbus/internal/messaging/dispatch_tokens.go`: `Issue(ctx, ownerID, jobID) (token, expiresAt, err)`, `Validate(ctx, token, ownerID, jobID) (ok, err)`, `Revoke(ctx, token)`. Uses `crypto/rand` for 32-byte tokens; 15m TTL.
- [ ] T007 [P] Dispatch-token tests in `/Users/user/repos/synapbus/internal/messaging/dispatch_tokens_test.go`: issue→validate success path; reject expired; reject owner mismatch; reject wrong-job; reject revoked.
- [ ] T008 [P] Memory-channel discovery in `/Users/user/repos/synapbus/internal/messaging/memory_channels.go`: `IsMemoryChannel(ch *Channel) bool` (true when name matches `open-brain` or `reflections-*`, OR `metadata.is_memory == true`); `MemoryChannelIDs(ctx) ([]int64, error)`.

**Checkpoint**: Foundation ready — US1/US2/US3 may proceed in parallel.

---

## Phase 3: User Story 1 — Proactive Injection (Priority: P1) 🎯 MVP

**Goal**: Every injection-eligible MCP tool response carries a `relevant_context` packet (owner-scoped, hybrid-ranked, token-budgeted).

**Independent Test**: With `SYNAPBUS_INJECTION_ENABLED=1` and seeded memories, calling `my_status` returns `relevant_context.memories` populated. Toggling `SYNAPBUS_INJECTION_ENABLED=0` removes the field entirely from the response.

### Tests for User Story 1 (write first, ensure they FAIL)

- [ ] T009 [P] [US1] Contract test for relevant_context shape in `/Users/user/repos/synapbus/internal/mcp/injection_wrap_test.go`: build a fake handler that returns canned JSON; assert wrapper appends `relevant_context` with the expected fields per `contracts/mcp-injection.md`; assert wrapper omits the field when memories slice is empty AND no core_memory set.
- [ ] T010 [P] [US1] Retrieval test in `/Users/user/repos/synapbus/internal/search/injection_test.go`: assert `BuildContextPacket` enforces owner scoping (memory from another owner is excluded), respects the score floor (drops items below 0.25), respects the token budget (truncates last item that overflows), and marks `truncated=true` on the truncated item.
- [ ] T011 [P] [US1] Cross-owner adversarial test in `/Users/user/repos/synapbus/internal/mcp/injection_e2e_test.go`: seed memories for owners H1 and H2; call any wrapped tool as an H1 agent and an H2 agent; assert their `relevant_context.memories` are disjoint (SC-008).
- [ ] T012 [P] [US1] Audit-ring test in `/Users/user/repos/synapbus/internal/messaging/memory_injections_test.go`: write 50 injection rows; assert cleanup deletes those older than 24h and keeps younger ones; assert ring is owner-scoped on read.

### Implementation for User Story 1

- [ ] T013 [P] [US1] Audit ring writer in `/Users/user/repos/synapbus/internal/messaging/memory_injections.go`: `Record(ctx, row)` writes one row; `Cleanup(ctx, olderThan time.Duration)` deletes old rows; `ListRecent(ctx, ownerID, limit)` reads back for debug.
- [ ] T014 [P] [US1] Build-context-packet implementation in `/Users/user/repos/synapbus/internal/search/injection.go`: `BuildContextPacket(ctx, agent *agents.Agent, query string, opts InjectionOpts) (*ContextPacket, error)`. Calls existing `search.Service.Search()` with `owner_id` filter, applies pin overlay (always include pinned), greedy-fills under token budget (char/4), returns `ContextPacket{Memories []MemoryItem, CoreMemory string, PacketChars int, RetrievalQuery string, SearchMode string}`. Accepts a `CoreMemoryProvider` interface so US2 can plug in without modifying this file.
- [ ] T015 [P] [US1] MCP injection middleware in `/Users/user/repos/synapbus/internal/mcp/injection_wrap.go`: `WrapInjection(handler ToolHandler, cfg WrapConfig) ToolHandler`. Runs inner handler, on JSON-shaped success result calls `BuildContextPacket`, marshals merged JSON, calls `memory_injections.Record`. Skip on non-JSON result.
- [ ] T016 [US1] Register middleware against eligible tools in `/Users/user/repos/synapbus/internal/mcp/tools_hybrid.go`. Wrap `my_status`, `claim_messages`, `read_inbox`, `send_message`, `search` / `search_messages`, `execute`, `read_channel`. For each, supply the right `query_source` (recent activity / claimed bodies / message body / query string / args / channel topic). Depends on T014, T015.
- [ ] T017 [US1] Wire injection cleanup into stalemate worker tick (or new cleanup task in `consolidator.go` if it lands first) — call `memory_injections.Cleanup(ctx, 24*time.Hour)` once per hour. File: `/Users/user/repos/synapbus/internal/messaging/stalemate.go` (extend existing tick).

**Checkpoint**: US1 functional — seed → call `my_status` → see `relevant_context`. MVP ready.

---

## Phase 4: User Story 2 — Per-Agent Core Memory (Priority: P2)

**Goal**: Each `(owner, agent)` has a small editable blob always included in session-start responses.

**Independent Test**: Set a core memory via admin CLI; call `my_status` as that agent; assert `relevant_context.core_memory` matches the set blob. Set a blob over the size cap; assert rejection with `core_memory_too_large`.

### Tests for User Story 2 (write first)

- [ ] T018 [P] [US2] Core-memory store tests in `/Users/user/repos/synapbus/internal/messaging/memory_core_test.go`: Get/Set/Delete round-trip; reject over-size on Set; replace-wholesale semantic (no merge); owner scoping (cannot read another owner's blob).

### Implementation for User Story 2

- [ ] T019 [P] [US2] Core-memory store in `/Users/user/repos/synapbus/internal/messaging/memory_core.go`: `GetCore(ctx, ownerID, agentName) (string, error)`, `SetCore(ctx, ownerID, agentName, blob, updatedBy) error` (enforces `SYNAPBUS_CORE_MEMORY_MAX_BYTES`), `DeleteCore(ctx, ownerID, agentName)`.
- [ ] T020 [US2] Implement `CoreMemoryProvider` adapter that `BuildContextPacket` calls — a one-liner wrapper around `messaging.GetCore` registered in `cmd/synapbus/main.go` wiring. Update injection wrapper config so session-start tools (i.e. `my_status`) get a provider; other tools get nil.
- [ ] T021 [P] [US2] Admin CLI command in `/Users/user/repos/synapbus/cmd/synapbus/main.go` (add to existing cobra tree under `memory` subcommand): `synapbus memory core get --owner --agent`, `set --owner --agent --blob-file`, `delete --owner --agent`. Uses the existing socket-RPC pattern.
- [ ] T022 [P] [US2] REST endpoint for the Web UI placeholder in `/Users/user/repos/synapbus/internal/api/memory_core.go`: `GET/PUT/DELETE /api/owner/{ownerID}/agents/{agentName}/core-memory` guarded by session auth (owner only).

**Checkpoint**: US2 functional — admin sets a core, agent receives it on session start.

---

## Phase 5: User Story 3 — Dream Worker (Priority: P2)

**Goal**: A background worker dispatches consolidation jobs to a Claude Code agent through `harness.Harness.Execute`. The agent uses 6 new MCP tools, gated by dispatch token, fully audited.

**Independent Test**: Seed pool with deliberate duplicates + contradictions, force a manual dream-run via admin CLI, observe `memory_consolidation_jobs.actions` populated and the `memory_status` view reflecting the changes. Confirm logs include `component=consolidator-worker` lines through the full dispatch → succeeded lifecycle.

### Tests for User Story 3 (write first)

- [ ] T023 [P] [US3] Memory-link store tests in `/Users/user/repos/synapbus/internal/messaging/memory_links_test.go`: insert each relation type; reject reserved types from `memory_add_link` caller path; assert owner-scoped queries.
- [ ] T024 [P] [US3] Memory-pin store tests in `/Users/user/repos/synapbus/internal/messaging/memory_pins_test.go`: pin/unpin; pinned memories surface in `BuildContextPacket` even below the score floor.
- [ ] T025 [P] [US3] Consolidator-worker tests in `/Users/user/repos/synapbus/internal/messaging/consolidator_test.go` using a mocked `harness.Harness`: assert watermark trigger fires only when N≥threshold; assert cron-deep-pass fires at the configured wallclock; assert at-most-one-in-flight per (owner, job_type) via the partial-unique-index path; assert wallclock budget terminates a runaway job with `partial` status; assert no system-DM is ever sent.
- [ ] T026 [P] [US3] MCP-memory-tools tests in `/Users/user/repos/synapbus/internal/mcp/memory_tools_test.go`: each of the 6 tools — happy path + every documented error code (`dispatch_token_missing`, `dispatch_token_expired`, `dispatch_token_owner_mismatch`, `not_same_owner`, `core_memory_too_large`, `relation_type_reserved`, `source_not_found`).
- [ ] T027 [P] [US3] memory_status view test in `/Users/user/repos/synapbus/internal/storage/memory_status_view_test.go`: insert `memory_consolidation_jobs.actions` JSON for dedup + supersede; query the view; assert correct derivations of `status`, `superseded_by`, `soft_deleted_at`.

### Implementation for User Story 3

- [ ] T028 [P] [US3] Link store in `/Users/user/repos/synapbus/internal/messaging/memory_links.go`: `AddLink(ctx, src, dst, relType, ownerID, createdBy, metadata)`, `ListLinks(ctx, msgID)`, `LinksForOwner(ctx, ownerID, types[], limit)`. Reject reserved types when `createdBy` starts with `agent:` (auto-types still allowed when `createdBy` starts with `auto:`).
- [ ] T029 [P] [US3] Pin store in `/Users/user/repos/synapbus/internal/messaging/memory_pins.go`: `Pin(ctx, ownerID, msgID, pinnedBy, note)`, `Unpin(ctx, ownerID, msgID)`, `ListPins(ctx, ownerID)`. Update `BuildContextPacket` overlay path to include pins.
- [ ] T030 [P] [US3] memory_status query helpers in `/Users/user/repos/synapbus/internal/messaging/memory_status.go`: `StatusFor(ctx, msgIDs []int64) (map[int64]MemoryStatus, error)`. Used by retrieval (joins to filter out non-active).
- [ ] T031 [US3] Plumb status filter into `BuildContextPacket` (modifies `/Users/user/repos/synapbus/internal/search/injection.go`). Depends on T014 and T030.
- [ ] T032 [P] [US3] Six MCP memory tools in `/Users/user/repos/synapbus/internal/mcp/memory_tools.go`: `memory_list_unprocessed`, `memory_write_reflection`, `memory_rewrite_core`, `memory_mark_duplicate`, `memory_supersede`, `memory_add_link`. Each: validates dispatch token via `messaging.Validate`, calls the underlying store, appends to `memory_consolidation_jobs.actions` JSON via `Jobs.AppendAction(ctx, jobID, action)`.
- [ ] T033 [US3] Tool registration gate in `/Users/user/repos/synapbus/internal/mcp/server.go`: register memory tools only when `SYNAPBUS_DREAM_ENABLED=1`. Depends on T032.
- [ ] T034 [P] [US3] Consolidation-jobs store in `/Users/user/repos/synapbus/internal/messaging/consolidation_jobs.go`: `Create(ctx, ownerID, jobType, trigger) (jobID, error)`, `Dispatch(ctx, jobID, harnessRunID, token)`, `Lease(ctx, jobID, until)`, `AppendAction(ctx, jobID, action)`, `Complete(ctx, jobID, status, summary, error)`, `ListRecent(ctx, ownerID, limit)`.
- [ ] T035 [P] [US3] Auto-link emitter in `/Users/user/repos/synapbus/internal/messaging/auto_links.go`: on `OnMessageCreated` (callable from existing `MessagingService`), insert `mention`, `reply_to`, `channel_cooccurrence` links automatically. Configurable, no LLM. Wire from `service.go` post-insert hook (one-line addition).
- [ ] T036 [P] [US3] ConsolidatorWorker in `/Users/user/repos/synapbus/internal/messaging/consolidator.go`: ticker pattern modeled on `StalemateWorker`. Each tick: per owner, evaluate watermark for reflection / link-gen / dedup; cron-evaluate sleep-time-rewrite. When trigger fires, `Create` job → `Issue` token → `Execute` via `harness.Harness` with `Env={SYNAPBUS_DISPATCH_TOKEN, SYNAPBUS_CONSOLIDATION_JOB_ID, SYNAPBUS_JOB_TYPE, SYNAPBUS_OWNER_ID}` → on `ExecResult`, call `Complete`. Honors `SYNAPBUS_DREAM_MAX_CONCURRENT` global semaphore. Hourly call to `memory_injections.Cleanup(ctx, 24h)`. Wallclock-budget on `Execute` via `Budget.MaxWallClock`.
- [ ] T037 [P] [US3] Dream-agent prompts in `/Users/user/repos/synapbus/internal/messaging/consolidator_prompts.go`: one short prompt string per job type (`reflection`, `core_rewrite`, `dedup_contradiction`, `link_gen`) — passed to `harness.Execute` as the task body. Each prompt: explains the job, instructs to use only `memory_*` tools, gives the dispatch token reference.
- [ ] T038 [US3] Wire `ConsolidatorWorker` lifecycle into `/Users/user/repos/synapbus/cmd/synapbus/main.go`: start when `SYNAPBUS_DREAM_ENABLED=1` after DB + messaging service ready; stop on shutdown. Depends on T036.
- [ ] T039 [P] [US3] Admin CLI `synapbus memory dream-run --owner --job` in `/Users/user/repos/synapbus/cmd/synapbus/main.go` (extend the `memory` subtree from T021): forces a single job dispatch bypassing the trigger check. Useful for kubic verification.

**Checkpoint**: All three user stories functional. Memory pool is owner-scoped, injection works, dream worker dispatches and audits consolidation jobs end-to-end.

---

## Phase 6: Polish & Cross-Cutting

- [ ] T040 [P] Run `go vet ./...` and `gofmt -l .` — fix any lint findings.
- [ ] T041 Run `make test` — full suite must pass; fix any regressions.
- [ ] T042 Cross-compile linux/amd64 binary: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ./synapbus-linux-amd64 ./cmd/synapbus`. Confirms zero-CGO constitution constraint.
- [ ] T043 Build kubic container image: `docker build --platform=linux/amd64 -t kubic.home.arpa:32000/synapbus:020-proactive-memory .` then push.
- [ ] T044 Deploy to kubic: `kubectl -n synapbus set image deploy/synapbus synapbus=kubic.home.arpa:32000/synapbus:020-proactive-memory` then `kubectl set env deploy/synapbus SYNAPBUS_INJECTION_ENABLED=1 SYNAPBUS_DREAM_ENABLED=1`.
- [ ] T045 Run quickstart.md "kubic: verify dream agent" section: tail logs, force a manual reflection job, verify `memory_consolidation_jobs` row reaches `status=succeeded` or `partial`. Capture the log excerpt as evidence.
- [ ] T046 Update `/Users/user/repos/synapbus/CLAUDE.md` "Active Technologies" with migration 028 entry (already partially done by speckit script).
- [ ] T047 Post completion summary to SynapBus `#my-agents-algis` channel (once auth re-established) — what shipped + the kubic log evidence.

---

## Dependencies & Execution Order

### Phase order

1. Setup (T001) — single check, ~no work.
2. Foundational (T002–T008) — blocks everything.
3. Stories run in priority order but US1/US2/US3 can proceed in parallel after Foundational completes:
   - US1 (T009–T017) — MVP. Ship-able alone with `SYNAPBUS_INJECTION_ENABLED=1`.
   - US2 (T018–T022) — depends only on `T014` (BuildContextPacket exists) from US1 for the integration point; the rest is parallel.
   - US3 (T023–T039) — depends on Foundational. Independent of US1/US2 except `T031` (status filter into injection) which extends `T014`.
4. Polish (T040–T047).

### Within each story

- Tests first; fail; then impl.
- Stores before MCP tool wiring before main-wiring.

### File-level conflicts (do not parallel)

| Tasks | Shared file | Order |
|-------|-------------|-------|
| T014 ↔ T031 | `internal/search/injection.go` | T014 first |
| T015 ↔ T016 | `internal/mcp/injection_wrap.go` + `tools_hybrid.go` | T015 first |
| T032 ↔ T033 | `internal/mcp/memory_tools.go` + `server.go` | T032 first |
| T036 ↔ T038 | `cmd/synapbus/main.go` | T036 first (creates worker), T038 wires it |
| T021 ↔ T039 | `cmd/synapbus/main.go` (memory CLI subtree) | T021 first (creates subtree), T039 adds command |
| T017 ↔ T036 | `internal/messaging/stalemate.go` OR consolidator | one of them owns the hourly cleanup |

### Parallel opportunities

After T002 lands, T003/T004/T005/T006/T008 run in parallel. After Foundational completes, all `[P]` tasks within a story run in parallel — for US1 that's T009/T010/T011/T012/T013/T014/T015 simultaneously; for US3 that's T023/T024/T025/T026/T027 (tests) then T028/T029/T030/T032/T034/T035/T036/T037 (impl) in parallel.

---

## Parallel-Subagent Cluster Plan

The next step is to dispatch subagents in parallel. Suggested clusters (each cluster = one subagent):

- **Cluster F (Foundational)**: T002, T003, T004, T005, T006, T007, T008.
- **Cluster I1 (US1 retrieval + middleware)**: T009, T010, T013, T014, T015, T016 — owns `injection.go`, `injection_wrap.go`, `tools_hybrid.go`, `memory_injections.go`.
- **Cluster I2 (US1 cross-owner safety + cleanup)**: T011, T012, T017.
- **Cluster C (US2 core memory)**: T018, T019, T020, T021, T022.
- **Cluster D1 (US3 stores + view)**: T023, T024, T027, T028, T029, T030.
- **Cluster D2 (US3 MCP tools + jobs)**: T026, T032, T033, T034, T037.
- **Cluster D3 (US3 worker + wiring + CLI)**: T025, T035, T036, T038, T039.

Clusters that share files are serialized; clusters in different file sets are parallel.

---

## Implementation Strategy

### MVP increments

1. Foundational → US1 → STOP. Ship `SYNAPBUS_INJECTION_ENABLED=1` to dev. This alone delivers the headline value ("agents get context without asking").
2. Add US2. Per-agent core memory active; session-start tools include the blob.
3. Add US3. Dream worker dispatches consolidation jobs; verify on kubic.

### Stop conditions before kubic deploy

- All `go test ./...` green.
- `go vet ./...` clean.
- Linux/amd64 binary cross-compiles with `CGO_ENABLED=0`.
- A local smoke test of US1 → US2 → US3 quickstart steps passes.

### Kubic deploy verification

- Logs show `component=consolidator-worker` startup line.
- Manual `dream-run` produces a `memory_consolidation_jobs` row with `status` ∈ {`succeeded`, `partial`}.
- An adversarial cross-owner injection test on the deployed instance returns no leakage.

---

## Notes

- `[P]` = different files AND no in-flight dependency. Conservative parallel marks where doubt.
- Auto-types `mention` / `reply_to` / `channel_cooccurrence` are created automatically by T035, NOT through `memory_add_link` (which rejects them — see contracts/mcp-memory-tools.md). The dream-agent only writes the four semantic types.
- The harness/dispatch path is the contractual non-system-DM route. Any deviation (e.g. tempted to "just send a DM to dream-agent") violates `feedback_system_dm_no_trigger.md` and recreates the cascading-stalemate bug.
- US4 (audit UI) is deferred. Until it ships, owners audit via `sqlite3` or the future REST endpoint from T022.
