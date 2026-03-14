# Implementation Plan: Webhooks & Kubernetes Job Runner

**Branch**: `003-webhooks-k8s-runner` | **Date**: 2026-03-14 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/003-webhooks-k8s-runner/spec.md`

## Summary

Add event-driven message delivery to SynapBus via HTTP webhooks and Kubernetes Jobs. When an agent receives a message (DM, channel, or @mention), SynapBus asynchronously delivers the payload to registered webhook URLs (HMAC-signed) or creates K8s Jobs. Includes SSRF prevention, loop detection (depth cap at 5), exponential backoff retry (3 attempts), dead letter queue, per-agent rate limiting (60/min), and auto-disable after 50 consecutive failures. K8s runner is optional, auto-detected via in-cluster config.

## Technical Context

**Language/Version**: Go 1.25+ (from go.mod)
**Primary Dependencies**: mark3labs/mcp-go (MCP tools), go-chi/chi (HTTP), golang.org/x/time/rate (rate limiting), k8s.io/client-go (K8s Jobs — optional)
**Storage**: modernc.org/sqlite (pure Go), migration 009_webhooks.sql
**Testing**: `go test ./...` with CGO_ENABLED=0, table-driven tests, httptest for webhook mocks
**Target Platform**: linux/amd64, darwin/arm64 (cross-compile, zero CGO)
**Project Type**: Single binary web service with embedded storage
**Performance Goals**: Webhook delivery within 5s of message send, 60 deliveries/min/agent rate limit
**Constraints**: Zero CGO, single binary, no external runtime dependencies (K8s client is compile-time only, no-op when not in-cluster)
**Scale/Scope**: Up to 100 concurrent agents, 10,000 delivery records per agent

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Gate | Status |
|-----------|------|--------|
| I. Local-First, Single Binary | No new external runtime dependencies | PASS — webhook engine embedded, K8s runner no-op when not in-cluster |
| II. MCP-Native | All agent operations as MCP tools | PASS — register_webhook, list_webhooks, delete_webhook, register_k8s_handler, list_k8s_handlers, delete_k8s_handler |
| III. Pure Go, Zero CGO | All deps must be pure Go | PASS — net/http, golang.org/x/time, k8s.io/client-go are all pure Go |
| IV. Multi-Tenant with Ownership | Webhooks scoped to agent/owner | PASS — per-agent webhooks, owner-only Web UI access |
| VIII. Observable by Default | All operations traced | PASS — delivery table + trace entries for all tool calls |
| IX. Progressive Complexity | Feature is opt-in | PASS — messaging works without webhooks, K8s disabled when not in-cluster |
| X. Web UI as First-Class Citizen | Management UI included | PASS — webhook management, delivery history, dead letters, K8s job runs in UI |

**Post-design re-check**: All gates still pass. k8s.io/client-go adds ~15MB to binary size but is acceptable as it's pure Go and the runner is a no-op when not in-cluster.

## Project Structure

### Documentation (this feature)

```text
specs/003-webhooks-k8s-runner/
├── plan.md              # This file
├── spec.md              # Feature specification
├── research.md          # Phase 0 research
├── data-model.md        # Data model & migration SQL
├── quickstart.md        # Getting started guide
├── contracts/
│   └── mcp-tools.md     # MCP tool & REST API contracts
└── checklists/
    └── requirements.md  # Spec quality checklist
```

### Source Code (repository root)

```text
internal/
├── webhooks/               # NEW — Webhook engine
│   ├── service.go          # WebhookService: registration CRUD, event matching
│   ├── store.go            # SQLiteWebhookStore: DB operations for webhooks & deliveries
│   ├── delivery.go         # DeliveryEngine: worker pool, dispatch, retry loop
│   ├── security.go         # HMAC signing, SSRF validation, IP blocking
│   ├── ratelimit.go        # Per-agent rate limiter (golang.org/x/time/rate)
│   ├── service_test.go     # Unit tests for WebhookService
│   ├── delivery_test.go    # Unit tests for DeliveryEngine (with httptest)
│   └── security_test.go    # Unit tests for SSRF prevention & signing
├── k8s/                    # NEW — Kubernetes Job Runner
│   ├── runner.go           # JobRunner interface + K8sJobRunner implementation
│   ├── noop.go             # NoopRunner for non-K8s environments
│   ├── store.go            # SQLiteK8sStore: DB operations for handlers & job runs
│   ├── watcher.go          # Job status watcher (K8s informer)
│   ├── runner_test.go      # Unit tests with mock K8s client
│   └── store_test.go       # Unit tests for DB operations
├── dispatcher/             # NEW — Event dispatcher (fan-out to webhooks + K8s)
│   ├── dispatcher.go       # EventDispatcher interface & MultiDispatcher
│   └── dispatcher_test.go  # Unit tests
├── mcp/
│   └── webhook_tools.go    # NEW — MCP tool registrar for webhook & K8s tools
├── api/
│   └── webhook_handler.go  # NEW — REST API handlers for Web UI
└── messaging/
    └── service.go          # MODIFIED — Call dispatcher.Dispatch() after message send

schema/
└── 009_webhooks.sql        # NEW — Migration for webhooks, deliveries, k8s tables

web/src/
├── routes/
│   ├── agents/[name]/webhooks/+page.svelte    # NEW — Webhook management per agent
│   ├── dead-letters/webhooks/+page.svelte     # NEW — Webhook dead letters
│   └── agents/[name]/k8s-handlers/+page.svelte # NEW — K8s handler management
└── lib/api/
    └── client.ts           # MODIFIED — Add webhook & K8s API methods

cmd/synapbus/
└── main.go                 # MODIFIED — Wire WebhookService, K8sRunner, EventDispatcher
```

**Structure Decision**: Follows existing Go internal package layout. New packages `webhooks/`, `k8s/`, and `dispatcher/` are siblings to existing packages like `messaging/`, `channels/`, `agents/`. The dispatcher pattern decouples message sending from delivery mechanisms.

## Complexity Tracking

| Addition | Why Needed | Simpler Alternative Rejected Because |
|----------|------------|-------------------------------------|
| k8s.io/client-go dependency (~15MB) | Native K8s Job support is a key differentiator | HTTP calls to K8s API rejected: reinventing auth, watch, retry logic |
| golang.org/x/time dependency | Token bucket rate limiting | Hand-rolled counter rejected: error-prone, no burst support |
| Event dispatcher abstraction | Decouple message send from delivery | Direct calls in messaging service rejected: tight coupling, hard to test |
