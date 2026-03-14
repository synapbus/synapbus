# Tasks: Webhooks & Kubernetes Job Runner

**Input**: Design documents from `/specs/003-webhooks-k8s-runner/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/mcp-tools.md

**Tests**: Included — user explicitly requested test coverage.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

---

## Phase 1: Setup

**Purpose**: Add dependencies, create migration, create package scaffolding

- [X] T001 Add `golang.org/x/time` dependency via `go get golang.org/x/time`
- [X] T002 Add `k8s.io/client-go` and `k8s.io/api` dependencies via `go get k8s.io/client-go k8s.io/api`
- [X] T003 Create migration file `schema/009_webhooks.sql` with tables: webhooks, webhook_deliveries, k8s_handlers, k8s_job_runs (copy SQL from data-model.md)
- [X] T004 [P] Create package directory `internal/webhooks/` with empty `doc.go`
- [X] T005 [P] Create package directory `internal/k8s/` with empty `doc.go`
- [X] T006 [P] Create package directory `internal/dispatcher/` with empty `doc.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that ALL user stories depend on

**CRITICAL**: No user story work can begin until this phase is complete

- [X] T007 Implement `internal/webhooks/store.go` — SQLiteWebhookStore with methods: InsertWebhook, GetWebhookByID, GetWebhooksByAgent, GetActiveWebhooksByEvent, UpdateWebhookStatus, IncrementConsecutiveFailures, ResetConsecutiveFailures, DeleteWebhook, CountWebhooksByAgent, InsertDelivery, UpdateDeliveryStatus, GetPendingDeliveries, GetRetryableDeliveries, GetDeliveriesByAgent, GetDeadLetteredDeliveries, PurgeOldDeadLetters
- [X] T008 Implement `internal/k8s/store.go` — SQLiteK8sStore with methods: InsertHandler, GetHandlerByID, GetHandlersByAgent, GetActiveHandlersByEvent, UpdateHandlerStatus, DeleteHandler, CountHandlersByAgent, InsertJobRun, UpdateJobRunStatus, GetJobRunsByHandler, GetJobRunsByAgent, GetJobRunByJobName
- [X] T009 Implement `internal/dispatcher/dispatcher.go` — Event type definitions (MessageEvent with event type, message, depth, agent), EventDispatcher interface (Dispatch method), MultiDispatcher that fans out to registered dispatchers
- [X] T010 Integrate migration 009_webhooks.sql into storage layer — ensure `internal/storage/migrations.go` picks up the new migration file via go:embed and applies it on startup

**Checkpoint**: Foundation ready — stores and dispatcher interface exist, migration applied

---

## Phase 3: User Story 1 — Webhook Registration & Delivery (Priority: P1) MVP

**Goal**: Agent registers a webhook, sends a message, webhook is delivered with correct payload and headers

**Independent Test**: Register webhook for agent, send message to that agent, verify HTTP POST arrives with correct JSON payload and X-SynapBus-* headers

### Tests for User Story 1

- [X] T011 [P] [US1] Write test `internal/webhooks/store_test.go` — table-driven tests for InsertWebhook (success, duplicate URL, max 3 limit), GetWebhooksByAgent, DeleteWebhook, InsertDelivery, UpdateDeliveryStatus
- [X] T012 [P] [US1] Write test `internal/webhooks/service_test.go` — table-driven tests for RegisterWebhook (validation, secret generation, max 3 check), ListWebhooks (URL masking), DeleteWebhook (ownership check)
- [X] T013 [P] [US1] Write test `internal/webhooks/delivery_test.go` — test DeliveryEngine using httptest.NewServer: verify POST with correct Content-Type, X-SynapBus-Event, X-SynapBus-Delivery, X-SynapBus-Depth headers; verify payload structure matches contract; verify status transitions pending→delivered on HTTP 200

### Implementation for User Story 1

- [X] T014 [US1] Implement `internal/webhooks/service.go` — WebhookService struct with fields: store, logger, config (AllowHTTP, AllowPrivateNetworks). Methods: RegisterWebhook (validate URL, check count, generate secret if needed, hash secret, store), ListWebhooks (mask URL paths), DeleteWebhook (ownership check), GetActiveWebhooksForEvent (match event type + agent name)
- [X] T015 [US1] Implement `internal/webhooks/delivery.go` — DeliveryEngine struct with fields: store, httpClient, workerCount, deliveryChan, wg. Methods: Start (spawn worker goroutines), Stop (graceful shutdown), Enqueue (create delivery record, push to channel), deliverOne (build payload, sign, POST, update status). Worker pool pattern: N goroutines reading from buffered channel
- [X] T016 [US1] Implement webhook MCP tools in `internal/mcp/webhook_tools.go` — WebhookToolRegistrar struct, RegisterAll method adding: register_webhook, list_webhooks, delete_webhook. Follow existing registrar pattern (mcp.NewTool with WithDescription, WithString, etc.)
- [X] T017 [US1] Wire WebhookService and DeliveryEngine into `cmd/synapbus/main.go` — create WebhookStore, WebhookService, DeliveryEngine; start/stop engine in server lifecycle; register webhook MCP tools on MCP server
- [X] T018 [US1] Integrate event dispatcher into messaging service — modify `internal/messaging/service.go` SendMessage to call dispatcher.Dispatch() after successful message insert, passing MessageEvent with event type, message data, and depth 0

**Checkpoint**: Core webhook flow works — register, send message, webhook fires with correct payload. Run `make test`.

---

## Phase 4: User Story 2 — Security: HMAC, SSRF, Loop Detection (Priority: P1)

**Goal**: Webhook payloads signed with HMAC-SHA256, private IPs blocked, webhook loops terminate at depth 5

**Independent Test**: Verify signature computation matches expected HMAC; attempt registering private IP URLs (rejected); create chain of webhooks and verify depth cap

### Tests for User Story 2

- [X] T019 [P] [US2] Write test `internal/webhooks/security_test.go` — table-driven tests: ComputeSignature (known input → known HMAC output), ValidateURL (test each blocked IP range: 10.0.0.1, 172.16.0.1, 192.168.1.1, 127.0.0.1, ::1, fe80::1; test allowed public IPs; test HTTP vs HTTPS enforcement), IsPrivateIP (all RFC1918 ranges, loopback, link-local, IPv6 ULA)
- [X] T020 [P] [US2] Write test `internal/webhooks/delivery_test.go` (extend) — test depth tracking: delivery with depth 4 succeeds, delivery with depth 5 is dead-lettered with "loop depth exceeded" reason; test DNS rebinding: mock DNS resolver returning private IP → delivery blocked
- [X] T021 [P] [US2] Write test `internal/webhooks/delivery_test.go` (extend) — test redirect blocking: httptest server returning 301 → delivery marked as failed with "redirects not allowed"

### Implementation for User Story 2

- [X] T022 [US2] Implement `internal/webhooks/security.go` — functions: ComputeHMACSignature(secret, payload []byte) string, ValidateWebhookURL(rawURL string, allowHTTP bool, allowPrivate bool) error, IsPrivateIP(ip net.IP) bool (check all RFC1918, loopback, link-local, IPv6 ULA ranges), NewSSRFSafeTransport(allowPrivate bool) *http.Transport (custom DialContext that resolves DNS and checks IP before connecting)
- [X] T023 [US2] Integrate SSRF-safe HTTP client into DeliveryEngine — replace default http.Client with one using NewSSRFSafeTransport; add CheckRedirect func to block redirects; set timeouts (10s total, 5s TLS handshake, 5s response header)
- [X] T024 [US2] Integrate depth tracking into delivery pipeline — in deliverOne: check depth >= 5 → dead-letter with reason; add X-SynapBus-Depth header to outgoing request; when message arrives via webhook-triggered action, extract depth from context and pass to dispatcher
- [X] T025 [US2] Integrate URL validation into RegisterWebhook — call ValidateWebhookURL before storing; pass AllowHTTP and AllowPrivateNetworks config flags

**Checkpoint**: Security hardened — HMAC signatures verified, SSRF blocked, loops capped. Run `make test`.

---

## Phase 5: User Story 3 — Retry, Rate Limiting, Dead Letters (Priority: P2)

**Goal**: Failed deliveries retry with exponential backoff, rate-limited per agent, dead letters queryable

**Independent Test**: Point webhook at mock server returning 500, verify 3 retries at correct intervals, delivery lands in dead letter queue. Test rate limiter: burst 61 deliveries, verify 61st is queued not dropped.

### Tests for User Story 3

- [X] T026 [P] [US3] Write test `internal/webhooks/delivery_test.go` (extend) — test retry logic: mock server returns 503 → verify 3 attempts with backoff timing (use time mocking or short intervals for test); after 3 failures → status is dead_lettered; mock server returns 429 with Retry-After header → verify retry waits
- [X] T027 [P] [US3] Write test `internal/webhooks/ratelimit_test.go` — test per-agent rate limiter: 60 deliveries allowed in rapid succession; 61st blocks until window; different agents have independent limits
- [X] T028 [P] [US3] Write test `internal/webhooks/service_test.go` (extend) — test auto-disable: simulate 50 consecutive failures on a webhook → verify webhook status changes to disabled; verify no further deliveries attempted; test re-enable resets counter

### Implementation for User Story 3

- [X] T029 [US3] Implement `internal/webhooks/ratelimit.go` — AgentRateLimiter struct with sync.Map of per-agent *rate.Limiter. Methods: Wait(ctx, agentName) error (get-or-create limiter, call limiter.Wait), Remove(agentName). Limiter config: rate.Every(time.Second), burst 60
- [X] T030 [US3] Integrate retry logic into DeliveryEngine — in deliverOne: on HTTP 4xx/5xx, if attempts < maxAttempts, set status=retrying and next_retry_at; add retryLoop goroutine that polls GetRetryableDeliveries every second and re-enqueues due deliveries; handle 429 with Retry-After header
- [X] T031 [US3] Integrate rate limiter into DeliveryEngine — before each delivery attempt, call rateLimiter.Wait(ctx, agentName); if context cancelled (shutdown), skip delivery
- [X] T032 [US3] Implement auto-disable logic — after each failed delivery: call store.IncrementConsecutiveFailures; if consecutive_failures >= 50, call store.UpdateWebhookStatus(disabled); on successful delivery, call store.ResetConsecutiveFailures
- [X] T033 [US3] Implement dead letter purge — background goroutine in DeliveryEngine that runs daily, calls store.PurgeOldDeadLetters(30 days)
- [X] T034 [US3] Add REST API endpoints for dead letter management in `internal/api/webhook_handler.go` — GET /api/webhook-deliveries (list with filters), POST /api/webhook-deliveries/{id}/retry (reset delivery to pending), POST /api/webhooks/{id}/enable (re-enable + reset failures), POST /api/webhooks/{id}/disable
- [X] T035 [US3] Wire webhook REST API routes into `internal/api/router.go` — add route group under auth middleware

**Checkpoint**: Retry/dead letter flow complete. Run `make test`.

---

## Phase 6: User Story 4 — Kubernetes Job Runner (Priority: P2)

**Goal**: Register K8s handler, message triggers K8s Job creation, job status tracked

**Independent Test**: With mock K8s client, register handler, send message, verify Job spec matches contract, status updates on completion

### Tests for User Story 4

- [X] T036 [P] [US4] Write test `internal/k8s/store_test.go` — table-driven tests for InsertHandler (success, max 3 limit), GetHandlersByAgent, DeleteHandler, InsertJobRun, UpdateJobRunStatus, GetJobRunsByAgent
- [X] T037 [P] [US4] Write test `internal/k8s/runner_test.go` — test with fake K8s clientset (k8s.io/client-go/kubernetes/fake): CreateJob (verify job name, namespace, env vars, resource limits, activeDeadlineSeconds, ttlSecondsAfterFinished), test NoopRunner returns error
- [ ] T038 [P] [US4] Write test `internal/k8s/watcher_test.go` — test job status updates: simulate Job completion event → verify store updated to succeeded; simulate Job failure event → verify store updated to failed with reason

### Implementation for User Story 4

- [X] T039 [US4] Implement `internal/k8s/runner.go` — JobRunner interface (CreateJob, IsAvailable, GetJobLogs). K8sJobRunner struct with clientset. NewJobRunner() tries rest.InClusterConfig(), returns K8sJobRunner on success. CreateJob builds batch/v1 Job spec with: name synapbus-{agent}-{msgID}, container with image/env/resources, restartPolicy=Never, activeDeadlineSeconds, ttlSecondsAfterFinished=3600, backoffLimit=0, label app.kubernetes.io/managed-by=synapbus. GetJobLogs proxies pod logs via clientset.
- [X] T040 [US4] Implement `internal/k8s/noop.go` — NoopRunner that satisfies JobRunner interface, returns "not available" errors for all methods, IsAvailable() returns false
- [ ] T041 [US4] Implement `internal/k8s/watcher.go` — JobWatcher struct using informers.NewSharedInformerFactory. Watches Jobs with label managed-by=synapbus. On Add/Update: extract job name → look up K8sJobRun → update status based on Job conditions (Active→running, Succeeded→succeeded, Failed→failed with reason from conditions)
- [X] T042 [US4] Implement K8s handler MCP tools in `internal/mcp/webhook_tools.go` (extend) — add register_k8s_handler, list_k8s_handlers, delete_k8s_handler tools to WebhookToolRegistrar. register_k8s_handler checks runner.IsAvailable() first
- [X] T043 [US4] Implement K8s dispatcher in `internal/k8s/dispatcher.go` (extend) — K8sDispatcher struct wrapping JobRunner + K8sStore. On Dispatch: find active handlers matching event, for each create K8sJobRun record then call runner.CreateJob
- [X] T044 [US4] Wire K8s runner into `cmd/synapbus/main.go` — create K8sStore, JobRunner (try in-cluster, fallback to Noop), JobWatcher; register K8sDispatcher with MultiDispatcher; start/stop watcher in lifecycle
- [X] T045 [US4] Add K8s REST API endpoints in `internal/api/webhook_handler.go` (extend) — GET /api/k8s-handlers, GET /api/k8s-job-runs, GET /api/k8s-job-runs/{id}/logs; wire routes

**Checkpoint**: K8s runner functional (with mock client in tests, real client in-cluster). Run `make test`.

---

## Phase 7: User Story 5 — @Mention Webhook Triggers (Priority: P3)

**Goal**: @mention in channel message triggers `message.mentioned` event for mentioned agents

**Independent Test**: Register webhook for message.mentioned, post channel message with @agentname, verify webhook fires with mention context

### Tests for User Story 5

- [X] T046 [P] [US5] Write test `internal/dispatcher/dispatcher_test.go` (extend) — test mention event: message body contains "@agent-a", dispatcher extracts mentioned agents and fires message.mentioned event to agent-a's webhooks; verify channel.message fires to all channel members with webhooks for that event; verify no double-delivery if agent has both message.mentioned and channel.message

### Implementation for User Story 5

- [X] T047 [US5] Implement mention extraction in `internal/dispatcher/dispatcher.go` — ExtractMentions(body string) []string function that finds @agent-name patterns. On channel message dispatch: identify mentioned agents, fire message.mentioned event to each mentioned agent's webhooks/handlers (in addition to channel.message to all members)
- [X] T048 [US5] Integrate mention dispatch into messaging service — in SendMessage for channel messages: pass list of mentioned agents to event dispatcher alongside channel members

**Checkpoint**: @mention triggers work. Run `make test`.

---

## Phase 8: User Story 6 — Web UI Management (Priority: P3)

**Goal**: Web UI pages for webhook management, delivery history, dead letters, K8s handler management

**Independent Test**: Navigate to agent's webhook page, see registered webhooks, delivery history, dead letters with retry button

### Implementation for User Story 6

- [X] T049 [P] [US6] Add webhook and K8s API methods to `web/src/lib/api/client.ts` — webhooks.list(agent), webhooks.enable(id), webhooks.disable(id), webhookDeliveries.list(agent, status, limit), webhookDeliveries.retry(id), k8sHandlers.list(agent), k8sJobRuns.list(agent, status, limit), k8sJobRuns.logs(id)
- [X] T050 [P] [US6] Create `web/src/routes/agents/[name]/webhooks/+page.svelte` — list agent's webhooks (URL, events, status badge, failure count), enable/disable toggle, delete button; delivery history table (recent 50, status badge, timestamp); filter by status
- [X] T051 [P] [US6] Create `web/src/routes/dead-letters/webhooks/+page.svelte` — list dead-lettered deliveries across all agents (for owner), show URL, event, error, attempts, timestamps; "Retry" button per delivery; pagination
- [X] T052 [P] [US6] Create `web/src/routes/agents/[name]/k8s-handlers/+page.svelte` — list K8s handlers (image, events, namespace, resources, status); job run history table (job name, status badge, duration, timestamp); link to logs (fetched via API)
- [X] T053 [US6] Add navigation links to webhook/K8s pages — update `web/src/lib/Sidebar.svelte` or agent detail page to include links to webhooks and K8s handlers pages
- [X] T054 [US6] Build web assets with `make web` and verify embedded assets compile

**Checkpoint**: Web UI complete. Run `make web && make build`.

---

## Phase 9: Polish & Cross-Cutting Concerns

**Purpose**: Integration testing, documentation, cleanup

- [ ] T055 [P] Write integration test `internal/webhooks/integration_test.go` — end-to-end: register agent, register webhook (httptest server), send message, verify delivery arrives with correct signature, verify delivery record in DB
- [ ] T056 [P] Write integration test `internal/k8s/integration_test.go` — end-to-end with fake clientset: register agent, register K8s handler, send message, verify Job created with correct spec
- [X] T057 Add trace recording to all webhook/K8s MCP tool handlers in `internal/mcp/webhook_tools.go` — call tracer.Record() for register_webhook, delete_webhook, register_k8s_handler, delete_k8s_handler
- [X] T058 Add startup re-queue logic to DeliveryEngine — on Start(), query DB for deliveries with status pending or retrying, re-enqueue them
- [X] T059 Update MCP tool descriptions in `internal/mcp/webhook_tools.go` — ensure descriptions guide agent behavior per Constitution Principle II
- [X] T060 Add new environment variables to `cmd/synapbus/main.go` — SYNAPBUS_WEBHOOK_WORKERS, SYNAPBUS_ALLOW_HTTP_WEBHOOKS, SYNAPBUS_ALLOW_PRIVATE_NETWORKS; bind to cobra flags
- [ ] T061 Run quickstart.md validation — verify all steps in quickstart.md work with running server
- [X] T062 Run full test suite `make test` and fix any failures
- [X] T063 Run `make build` for cross-compilation check (CGO_ENABLED=0)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — can start immediately
- **Foundational (Phase 2)**: Depends on Phase 1 (dependencies and migration)
- **US1 (Phase 3)**: Depends on Phase 2 (stores and dispatcher)
- **US2 (Phase 4)**: Depends on Phase 3 (needs delivery engine to add security to)
- **US3 (Phase 5)**: Depends on Phase 3 (needs delivery engine for retry/rate limit)
- **US4 (Phase 6)**: Depends on Phase 2 (stores) — can run in parallel with US2/US3
- **US5 (Phase 7)**: Depends on Phase 3 (needs dispatcher)
- **US6 (Phase 8)**: Depends on Phases 5+6 (needs REST API endpoints from US3/US4)
- **Polish (Phase 9)**: Depends on all user stories complete

### User Story Dependencies

- **US1 (P1)**: Foundation only — MVP
- **US2 (P1)**: Depends on US1 (adds security to delivery engine)
- **US3 (P2)**: Depends on US1 (adds retry/rate limiting to delivery engine)
- **US4 (P2)**: Foundation only — independent of webhooks (parallel-capable with US2/US3)
- **US5 (P3)**: Depends on US1 (extends dispatcher with mention events)
- **US6 (P3)**: Depends on US3 + US4 (needs REST endpoints from both)

### Parallel Opportunities

- **Phase 1**: T004, T005, T006 can all run in parallel (different directories)
- **Phase 3**: T011, T012, T013 can all run in parallel (different test files)
- **Phase 4**: T019, T020, T021 can all run in parallel (different test aspects)
- **Phase 5**: T026, T027, T028 can all run in parallel (different test files)
- **Phase 6**: T036, T037, T038 can all run in parallel (different test files)
- **US4 can run in parallel with US2+US3** (independent packages)
- **Phase 8**: T049, T050, T051, T052 can all run in parallel (different files)

---

## Implementation Strategy

### MVP First (User Story 1 + 2 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational
3. Complete Phase 3: US1 — Webhook registration + delivery
4. Complete Phase 4: US2 — Security hardening
5. **STOP and VALIDATE**: Test webhook flow end-to-end with HMAC verification
6. This delivers: event-driven webhooks with security. Usable in production.

### Incremental Delivery

1. Setup + Foundational → Foundation ready
2. US1 + US2 → Secure webhook delivery (MVP!)
3. US3 → Retry + dead letters → Production-ready reliability
4. US4 → K8s Jobs → Cloud-native support
5. US5 → @Mention triggers → Multi-agent collaboration
6. US6 → Web UI → Human oversight dashboard
7. Polish → Integration tests, docs, cleanup

### Parallel Team Strategy

With multiple agents:
- Agent A: US1 → US2 → US3 (webhook pipeline)
- Agent B: US4 (K8s runner, independent after foundation)
- Agent C: US5 + US6 (after US1 complete)

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story is independently completable and testable
- Verify tests fail before implementing (TDD where applicable)
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Total: 63 tasks across 9 phases
