# Research: Webhooks & Kubernetes Job Runner

**Feature**: 003-webhooks-k8s-runner
**Date**: 2026-03-14

## 1. Webhook Security Best Practices

### Decision: HMAC-SHA256 Payload Signing
- **Approach**: Sign raw JSON body with HMAC-SHA256 using per-webhook shared secret.
- **Rationale**: Industry standard used by Stripe (`Stripe-Signature`), GitHub (`X-Hub-Signature-256`), Slack (`X-Slack-Signature`). HMAC-SHA256 provides both authentication and integrity verification.
- **Implementation**:
  - Header: `X-SynapBus-Signature: sha256=<hex digest>`
  - Include `X-SynapBus-Timestamp` to prevent replay attacks (reject payloads older than 5 minutes)
  - Secret stored as SHA-256 hash in DB; original shown once at registration
  - Secret rotation: agent deletes old webhook and creates new one (simple, avoids complexity of dual-secret windows)
- **Alternatives considered**: JWT-signed payloads (rejected: heavier, requires key management), API key in header (rejected: no integrity check)

### Decision: SSRF Prevention via Custom DialContext
- **Approach**: Custom `net.Dialer.Control` function that validates resolved IPs before connecting.
- **Rationale**: DNS resolution happens at connection time; validating the URL string alone is insufficient (DNS rebinding). Custom DialContext intercepts after DNS resolution but before TCP connect.
- **Implementation**:
  - Block RFC1918 (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
  - Block loopback (127.0.0.0/8, ::1)
  - Block link-local (169.254.0.0/16, fe80::/10)
  - Block ULA IPv6 (fc00::/7)
  - Configurable override: `SYNAPBUS_ALLOW_PRIVATE_NETWORKS=true`
  - URL scheme validation at registration time (HTTPS required, HTTP only with flag)
- **Alternatives considered**: URL allowlisting (rejected: too restrictive for general use), DNS pre-resolution + IP pinning (rejected: complex, doesn't prevent rebinding between resolution and connect)

### Decision: Loop Prevention via Depth Header
- **Approach**: `X-SynapBus-Depth` header incremented on each webhook-triggered message. Dead-letter at depth 5.
- **Rationale**: Simple, stateless approach. Each hop is independently verifiable. No need for distributed tracing infrastructure.
- **Implementation**:
  - When a message arrives via a webhook-triggered action, incoming depth is extracted from context
  - Each outgoing webhook delivery increments depth by 1
  - At depth >= 5, delivery is dead-lettered with reason "loop depth exceeded"
  - Depth is stored in the delivery record for debugging
- **Alternatives considered**: Circuit breakers per agent pair (rejected: stateful, complex), global rate limiting only (rejected: doesn't catch fast loops within rate window)

### Decision: Token Bucket Rate Limiting with golang.org/x/time/rate
- **Approach**: Per-agent token bucket rate limiter, 60 tokens/minute (1/second sustained).
- **Rationale**: `golang.org/x/time/rate` is pure Go, stdlib-adjacent, well-tested. Token bucket allows bursts while capping sustained rate.
- **Implementation**:
  - `rate.NewLimiter(rate.Every(time.Second), 60)` per agent
  - Excess deliveries are queued (Wait), not dropped
  - Limiter instances stored in sync.Map keyed by agent name
  - Limiter garbage-collected when agent has no active webhooks
- **Alternatives considered**: Leaky bucket (rejected: no burst tolerance), fixed window counter (rejected: thundering herd at window boundaries)

### Decision: Exponential Backoff with 3 Retries
- **Approach**: 3 attempts at 1s, 5s, 30s intervals. Dead-letter after exhaustion.
- **Rationale**: Matches common industry practice (GitHub: 10s, 60s, 360s; Stripe: exponential up to 3 days but we're simpler). Short total window (~36s) prevents stale delivery.
- **Implementation**:
  - Retry intervals: [1s, 5s, 30s]
  - Respect `Retry-After` header for 429 responses
  - HTTP timeout per attempt: 10 seconds
  - No follow redirects (prevent open redirect attacks)
  - Auto-disable webhook after 50 consecutive failures across all deliveries
- **Alternatives considered**: Longer retry window with jitter (rejected: messages become stale), infinite retry (rejected: dead letters need visibility)

## 2. Go Libraries & Patterns

### Decision: Standard Library net/http for Delivery (No External Webhook Library)
- **Approach**: Use `net/http.Client` with custom `Transport` for SSRF protection. No external webhook library.
- **Rationale**: SynapBus needs are specific (SSRF prevention, depth tracking, custom signing). External libraries like svix-go are SDKs for their SaaS, not embeddable engines. Gitea and Mattermost both use custom implementations.
- **Implementation**:
  - Custom `http.Transport` with `DialContext` that validates IPs
  - `CheckRedirect: func(...) error { return http.ErrUseLastResponse }` to block redirects
  - Timeouts: `Timeout: 10s`, `TLSHandshakeTimeout: 5s`, `ResponseHeaderTimeout: 5s`
  - Connection pooling via default Transport pool settings
- **Alternatives considered**: svix-webhooks Go SDK (rejected: SaaS-oriented, not embeddable), go-resty (rejected: unnecessary abstraction)

### Decision: Goroutine Worker Pool with Buffered Channel (No External Pool Library)
- **Approach**: Fixed-size goroutine pool reading from buffered channel. Pool size configurable via `SYNAPBUS_WEBHOOK_WORKERS` (default 10).
- **Rationale**: Simple, well-understood pattern. No external dependency needed. Matches SynapBus's approach of minimal dependencies.
- **Implementation**:
  - `deliveryChan chan *DeliveryJob` (buffered, size = workers * 10)
  - N worker goroutines consuming from channel
  - Graceful shutdown: close channel, wait for in-flight deliveries
  - On startup: re-queue pending/retrying deliveries from DB
- **Alternatives considered**: gammazero/workerpool (rejected: unnecessary dependency for simple pattern), pond (rejected: same reason)

### Decision: golang.org/x/time/rate for Rate Limiting
- **Approach**: Use `golang.org/x/time/rate` from the Go extended stdlib.
- **Rationale**: Pure Go, well-maintained, already in Go module ecosystem. Token bucket is the right algorithm for bursty webhook delivery.
- **Alternatives considered**: uber-go/ratelimit (rejected: leaky bucket, no burst), hand-rolled sliding window (rejected: error-prone)

## 3. Kubernetes Job Runner

### Decision: k8s.io/client-go with In-Cluster Config Auto-Detection
- **Approach**: Use `client-go` with `rest.InClusterConfig()`. If it fails, K8s features are disabled (no-op).
- **Rationale**: `client-go` is the official Go client, pure Go, widely used. In-cluster config uses the ServiceAccount token mounted by K8s automatically.
- **Implementation**:
  - `internal/k8s/runner.go` with interface `JobRunner`
  - `NewJobRunner()` attempts `rest.InClusterConfig()`. If error, returns `NoopRunner`
  - `JobRunner.CreateJob(ctx, handler, message)` creates K8s Job
  - `JobRunner.WatchJobs(ctx)` watches for Job completions/failures
  - `NoopRunner` satisfies interface but returns "not available" errors
- **Alternatives considered**: Direct REST API calls (rejected: reinventing client-go), CRDs (rejected: over-engineering), Helm templates (rejected: not programmatic)

### Decision: Environment Variables for Message Data
- **Approach**: Inject message data as env vars (`SYNAPBUS_MESSAGE_ID`, `SYNAPBUS_MESSAGE_BODY`, etc.)
- **Rationale**: Simplest approach. Works for messages up to ~32KB (env var limit varies by OS). For larger messages, body could reference a message ID for the Job to fetch via MCP.
- **Implementation**:
  - `SYNAPBUS_MESSAGE_ID` - Message ID (integer as string)
  - `SYNAPBUS_MESSAGE_BODY` - Message body (truncated to 32KB)
  - `SYNAPBUS_FROM_AGENT` - Sender agent name
  - `SYNAPBUS_EVENT` - Event type
  - `SYNAPBUS_CHANNEL` - Channel name (if applicable)
  - `SYNAPBUS_TIMESTAMP` - Event timestamp (ISO 8601)
  - Plus user-defined env vars from handler registration
- **Alternatives considered**: ConfigMap (rejected: requires cleanup, race conditions), command args (rejected: limited size, visible in `ps`), volume mount (rejected: complex, requires PV)

### Decision: Job Watcher Goroutine with Informer
- **Approach**: Use a K8s informer to watch Job status changes in registered namespaces.
- **Rationale**: Informers are the standard K8s watch pattern with caching, reconnection, and backoff built in.
- **Implementation**:
  - Single informer watching Jobs with label `app.kubernetes.io/managed-by=synapbus`
  - On Job completion/failure: update `k8s_job_runs` table
  - On startup: reconcile existing Jobs with DB records
- **Alternatives considered**: Polling (rejected: wasteful, slow), CRD controller (rejected: over-engineering)

### Decision: RBAC with Minimal Permissions
- **Approach**: SynapBus ServiceAccount needs: create/get/list/watch/delete Jobs in agent namespaces.
- **Required RBAC**:
  - `apiGroups: ["batch"]`, `resources: ["jobs"]`, `verbs: ["create", "get", "list", "watch", "delete"]`
  - `apiGroups: [""]`, `resources: ["pods/log"]`, `verbs: ["get"]` (for log viewing)
  - RoleBinding per namespace (not ClusterRoleBinding)
- **Alternatives considered**: ClusterRole (rejected: violates least privilege), per-agent ServiceAccount (rejected: complexity without proportional security benefit for v1)

## 4. Architecture Decisions

### Decision: Separate internal/webhooks/ and internal/k8s/ Packages
- **Approach**: Two new packages:
  - `internal/webhooks/` - Webhook registration, delivery engine, SSRF protection, rate limiting
  - `internal/k8s/` - K8s handler registration, job runner, job watcher
- **Rationale**: Separation of concerns. K8s package can be no-op when not in cluster. Webhook package has no K8s dependency.
- **Shared**: Both use a common `EventDispatcher` interface called by the messaging service when events occur.

### Decision: Event Dispatcher Pattern for Message-to-Delivery
- **Approach**: `internal/webhooks/dispatcher.go` with interface:
  ```
  type EventDispatcher interface {
      Dispatch(ctx context.Context, event Event) error
  }
  ```
  The messaging service calls `dispatcher.Dispatch()` after sending a message. The dispatcher fans out to webhook and K8s deliveries.
- **Rationale**: Decouples message sending from delivery mechanisms. Easy to add new delivery types later.

### Decision: Migration Number 009
- **Approach**: Use `009_webhooks.sql` for all new tables (webhooks, webhook_deliveries, k8s_handlers, k8s_job_runs).
- **Rationale**: Next available migration number after existing 008_dead_letters.sql.

### Decision: New Dependencies
- `golang.org/x/time` - Rate limiting (pure Go, stdlib-adjacent)
- `k8s.io/client-go` - K8s API client (pure Go, optional at runtime)
- `k8s.io/api` - K8s API types (transitive from client-go)
- No new CGO dependencies.
