# Implementation Plan: SynapBus Production Readiness & Website Launch

**Branch**: `001-production-readiness` | **Date**: 2026-03-13 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/001-production-readiness/spec.md`

## Summary

Transform SynapBus from MVP to production-ready by adding: (1) git pre-commit/pre-push hooks for quality gates, (2) GitHub Actions CI for PR validation and release builds, (3) Prometheus metrics and Kubernetes health endpoints, (4) Dockerfile + docker-compose + Helm chart, (5) public website at synapbus.dev with documentation and blog.

## Technical Context

**Language/Version**: Go 1.23+ (server), SvelteKit 2 / Svelte 5 + Tailwind (website)
**Primary Dependencies**: prometheus/client_golang, chi/v5, cobra, modernc.org/sqlite
**Storage**: SQLite (embedded, pure Go) — unchanged
**Testing**: `go test ./...` (CGO_ENABLED=0), Python E2E tests
**Target Platform**: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
**Project Type**: Multi-deliverable: Go service + deployment artifacts + static website
**Performance Goals**: Health endpoints <10ms, metrics scrape <100ms, website LCP <2s
**Constraints**: Zero CGO, single binary, all deps pure Go
**Scale/Scope**: 5 workstreams, ~25 files to create/modify

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Single Binary | PASS | Prometheus is compiled into binary. Docker/Helm are deployment artifacts. |
| II. MCP-Native | PASS | No changes to MCP interface. |
| III. Pure Go, Zero CGO | PASS | prometheus/client_golang is pure Go. No CGO required. |
| IV. Multi-Tenant | PASS | No changes to tenant model. |
| V. Embedded OAuth | PASS | No changes to auth. |
| VI. Semantic Storage | PASS | No storage changes. |
| VII. Swarm Patterns | PASS | No changes to swarm. |
| VIII. Observable | PASS | Prometheus enhances observability. |
| IX. Progressive | PASS | Metrics are opt-in via --metrics flag. |
| X. Web UI | PASS | Website is separate; embedded UI unchanged. |

All gates pass. No violations.

## Project Structure

### Documentation (this feature)

```text
specs/001-production-readiness/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── spec.md              # Feature specification
└── checklists/
    └── requirements.md  # Validation checklist
```

### Source Code (repository root)

```text
# Workstream 1: DevOps & Hooks
scripts/
├── hooks/
│   ├── pre-commit       # go vet + golangci-lint + fast tests
│   └── pre-push         # full tests + build verify

# Workstream 2: CI/CD
.github/workflows/
├── ci.yml               # PR checks (lint, test, build)
└── release.yml          # Tag-triggered release (binaries + Docker)

# Workstream 3: Observability
internal/
├── metrics/
│   ├── metrics.go       # Prometheus collector registration
│   ├── middleware.go     # HTTP metrics middleware
│   └── metrics_test.go  # Tests
└── health/
    ├── health.go         # /healthz, /readyz handlers
    └── health_test.go    # Tests

# Workstream 4: Deployment
Dockerfile               # Multi-stage build
docker-compose.yml        # Local dev stack
deploy/helm/synapbus/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── pvc.yaml
│   ├── ingress.yaml
│   ├── configmap.yaml
│   └── _helpers.tpl

# Workstream 5: Website (separate repo)
~/repos/synapbus-website/  # SvelteKit on Cloudflare Pages
├── src/routes/
│   ├── +page.svelte       # Landing page
│   ├── docs/              # Documentation
│   ├── blog/              # Blog posts
│   └── install/           # Installation guide
├── static/                # Generated images
└── wrangler.toml          # Cloudflare config
```

**Structure Decision**: Existing Go project structure is preserved. New packages added under `internal/` for metrics and health. Deployment artifacts at repo root. Website in separate repo.

## Implementation Workstreams

### WS-1: Git Hooks (P1, ~30 min)
- Create `scripts/hooks/pre-commit` and `scripts/hooks/pre-push`
- Add `make hooks` target to install them
- Test with intentional lint error

### WS-2: CI/CD Workflows (P1, ~45 min)
- Create `.github/workflows/ci.yml` for PR checks
- Create `.github/workflows/release.yml` for tag releases
- Test by pushing branch

### WS-3: Observability (P1, ~1 hour)
- Add `internal/metrics/` package with Prometheus collectors
- Add `internal/health/` package with /healthz and /readyz
- Wire into main.go router
- Add middleware for HTTP request metrics
- Write unit tests

### WS-4: Deployment Artifacts (P2, ~1 hour)
- Create Dockerfile (multi-stage: builder + scratch)
- Create docker-compose.yml
- Create Helm chart in deploy/helm/synapbus/
- Test Docker build locally

### WS-5: Website (P2, ~2 hours)
- Create synapbus-website repo
- Scaffold SvelteKit + Tailwind project
- Build landing page with AI-generated hero images
- Create documentation pages
- Create 2 blog posts with diagrams
- Deploy to Cloudflare Pages
- Configure DNS for synapbus.dev
