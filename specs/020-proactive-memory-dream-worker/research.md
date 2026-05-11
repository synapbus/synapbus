# Research — 020-proactive-memory-dream-worker

Resolves all `NEEDS CLARIFICATION` from `plan.md` Technical Context. Each section: **Decision** / **Rationale** / **Alternatives considered**.

## R1 — Dream-worker dispatch path

**Decision**: The `ConsolidatorWorker` invokes a consolidation agent via `harness.Harness.Execute(ExecRequest)`, picking the registered backend (`kubernetes_job` on kubic, `local_subprocess` in dev). The triggering message is `nil` (no inbound DM). A one-time dispatch token is passed via `ExecRequest.Env["SYNAPBUS_DISPATCH_TOKEN"]`, and the consolidation job ID via `ExecRequest.Env["SYNAPBUS_CONSOLIDATION_JOB_ID"]`.

**Rationale**:
- The `harness` package was built specifically as "the single seam between SynapBus's reactor / webhook / MCP entry points and whatever actually runs an agent." Reusing it costs nothing.
- Avoids the user-flagged trap: per `feedback_system_dm_no_trigger.md`, system DMs trigger reactive runs which cascade through the stalemate worker. Going through `Harness.Execute` directly bypasses messaging entirely.
- Tokens via `Env` are already how `harness` propagates run identity (`SYNAPBUS_RUN_ID`); adding two more env vars is idiomatic.

**Alternatives rejected**:
- *System DM into a "dream" channel* — would re-trigger the very pattern we're avoiding.
- *New REST endpoint that the consolidation agent polls* — duplicates what `harness` already does; violates Principle II (MCP-native) by adding a non-MCP agent surface.
- *Cron via OS cron + curl* — leaves the binary; violates Principle I (single binary, self-contained).

## R2 — Owner identification

**Decision**: Owner is `agents.owner_id` (string, non-null per existing schema). Retrieval uses `auth.ContextAgent(ctx).OwnerID` as the scope key.

**Rationale**: `agents.owner_id` is the authoritative scope key per Constitution IV. The auth middleware already populates the agent record into the request context for every MCP and REST call. No new lookup path needed.

**Alternatives rejected**:
- *New `memory_owner` column on messages* — denormalizes data already available via `messages.from_agent → agents.owner_id`. Adds a write-path constraint on every message insert.

## R3 — Soft-delete and supersession storage

**Decision**: Status of a memory (`active` / `soft_deleted` / `superseded`) is derived from rows in `memory_consolidation_jobs.actions` JSON, exposed via a SQL view `memory_status(message_id, status, superseded_by, soft_deleted_at, reason)`. Retrieval joins against this view to filter.

**Rationale**:
- Keeps the hot `messages` table untouched. Migration is additive only.
- The audit log IS the source of truth — every status change is already recorded; deriving the view from it eliminates the dual-write/divergence risk.
- View can be materialized later if join cost becomes measurable.

**Alternatives rejected**:
- *Add `status` column to `messages`* — touches the most-written table on every soft-delete; requires backfill migration; harder to roll back.
- *Separate `memory_status` table updated by triggers* — triggers in SQLite are awkward to test and reason about.

## R4 — Token-budget estimator

**Decision**: Estimate tokens as `(len(s) + 3) / 4`. Enforce budget by greedy fill in descending relevance order; truncate the *last admitted* memory if it overflows.

**Rationale**: For the budget gate (~500 tokens, hard upper bound 1000), char/4 is within ±15% of GPT-4/Claude tokenization for English prose — well inside the margin we'd need to tighten. A real tokenizer would force CGO (tiktoken) or add a meaningful dependency (`pkoukk/tiktoken-go` is pure Go but adds 5MB BPE tables). Not worth it for budget gating.

**Alternatives rejected**:
- `pkoukk/tiktoken-go` — pure Go but bloats the binary and is overkill for a budget gate.
- Exact tokenizer per provider — provider-specific, fragile, and we don't know which provider the consuming agent uses.

## R5 — MCP request-context → caller identification

**Decision**: Existing `auth.ContextAgent(ctx) *agents.Agent` returns the authenticated agent for the current MCP call. The new injection middleware unpacks this once and passes the agent down to `search.BuildContextPacket(ctx, agent, opts)`.

**Rationale**: Every existing MCP tool handler in `tools_hybrid.go` already calls `auth.ContextAgent(ctx)`. The middleware wraps the handler, so it can intercept the result before serialization without changing handler signatures.

**Alternatives rejected**:
- *Pass agent through every handler* — touches every existing handler; high blast radius for a P1 feature.

## R6 — Memory-channel discovery

**Decision**: A channel is a "memory channel" if either its name matches a hardcoded list (`open-brain`, `reflections-*`) OR its `channels.metadata` JSON includes `{"is_memory": true}`. Owners set the flag via an admin endpoint (Phase 2 UI; CLI/REST in Phase 1).

**Rationale**:
- Keeps the existing #open-brain working with zero migration.
- Per-channel flag is a lightweight JSON update — no schema change.
- Owners can opt new channels into memory without code changes.

**Alternatives rejected**:
- *New `is_memory` boolean column on channels* — requires migration; the existing JSON metadata column already exists for this kind of extension.
- *Make EVERY channel a memory channel* — defeats the owner's ability to keep ephemera (e.g. #bugs-X) out of the memory pool.

## R7 — Dispatch-token lifecycle

**Decision**: Tokens are 32-byte random strings (`crypto/rand`, base64-url encoded). Inserted with `expires_at = now() + 15m` and `used_at = NULL`. The first call to any memory-consolidation tool with a valid unexpired unused token marks it `used_at = now()`. Subsequent calls within the same job session reuse the token (checked by `consolidation_job_id` match). Tokens for a different job, or a soft-expired token, are rejected.

**Rationale**:
- Single-use semantic (in the sense of "bound to one job") prevents stolen-token replay against a fresh job.
- 15m TTL covers wallclock budget (10m) + dispatch slack.
- Random 32 bytes is the standard secret size.

**Alternatives rejected**:
- *JWT* — pulls in a JWT library and signing key management for a 15m intra-process token. Massive overkill.
- *Owner-API-key as token* — would let the agent bypass the audit trail by calling tools as the human. Violates Constitution IV (ownership separation).

## R8 — Concurrent owners

**Decision**: A global `sync.Semaphore`-equivalent (`chan struct{}` of size `SYNAPBUS_DREAM_MAX_CONCURRENT`, default 4) gates how many owners' jobs can run concurrently. Within one owner, only one job per `(owner, job_type)` runs at a time (enforced by a row-level lock in `memory_consolidation_jobs.lease_until`).

**Rationale**: Single-binary, single-instance (Constitution Non-Goals) means we don't need distributed locking. A local semaphore + a leased row is enough.

**Alternatives rejected**:
- *Unlimited concurrency* — could overwhelm the embedding provider's rate limits.
- *Per-owner goroutine pool* — wastes goroutines when most owners are idle.

## R9 — Where injection is wired in

**Decision**: A thin wrapper `mcp.WrapInjection(handler, opts)` is applied at tool-registration time in `internal/mcp/server.go`. The wrapper runs the inner handler, then — if the tool is on the injection-eligible list AND the response is JSON-shaped — appends a `relevant_context` field to the JSON body before returning. Non-JSON results pass through unchanged.

**Rationale**: One wrapper, one point of change, easy to disable globally. Doesn't pollute handler code.

**Alternatives rejected**:
- *Per-handler injection calls* — duplicates the same five lines in six places; easy to forget for new tools.
- *Server-level response interceptor in `chi`* — wrong layer; the MCP framing happens above HTTP.

## R10 — How to verify dream agent works on kubic

**Decision**: Watch `kubectl logs -n synapbus deploy/synapbus -f` for log lines tagged `component=consolidator-worker`. Manually trigger a job via admin CLI: `kubectl exec -n synapbus deploy/synapbus -- /synapbus --socket /data/synapbus.sock memory dream-run --owner algis --job reflection`. The worker logs: dispatched, harness-execute started, agent connected with token, tool calls, completion, audit rows.

**Rationale**: Reuses the existing admin CLI Unix-socket pattern.

**Alternatives rejected**:
- *Wait for cron trigger* — slow feedback loop for verification.

## Open questions deferred to `/speckit.tasks` decomposition

None — all blockers resolved.
