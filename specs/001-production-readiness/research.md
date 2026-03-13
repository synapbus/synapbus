# Research: SynapBus Production Readiness

## R1: Prometheus Client for Zero-CGO Go

**Decision**: Use `github.com/prometheus/client_golang` v1.20+
**Rationale**: Pure Go library, no CGO required. Industry standard for Go services. Provides promhttp handler, collectors, and middleware patterns.
**Alternatives considered**: VictoriaMetrics client (lighter but less ecosystem support), OpenTelemetry (heavier, would add complexity)

## R2: Health Check Patterns

**Decision**: Implement `/healthz` (liveness) and `/readyz` (readiness) following Kubernetes probe conventions.
**Rationale**: Standard K8s pattern. Liveness = "is the process alive" (always 200 unless deadlocked). Readiness = "can it serve traffic" (checks DB connectivity).
**Alternatives considered**: Single `/health` endpoint (already exists but doesn't distinguish liveness from readiness)

## R3: Multi-Stage Docker Build

**Decision**: Two-stage build: `golang:1.23-alpine` builder → `scratch` runtime with ca-certificates and tzdata.
**Rationale**: Scratch produces smallest possible image. CGO_ENABLED=0 ensures static binary. Alpine builder provides build tools without bloating runtime.
**Alternatives considered**: Alpine runtime (adds ~5MB but has shell for debugging), distroless (Google's option, slightly larger)

## R4: Helm Chart Structure

**Decision**: Standard Helm 3 chart with Deployment, Service, PVC, optional Ingress. Single values.yaml.
**Rationale**: Follows Helm best practices. PVC for data persistence. Ingress optional for cloud deployments.
**Alternatives considered**: Kustomize (less user-friendly), raw K8s manifests (no templating)

## R5: GitHub Actions CI Strategy

**Decision**: Two workflows: `ci.yml` (on PR) and `release.yml` (on tag push v*). Use `actions/setup-go@v5` and `actions/setup-node@v4`.
**Rationale**: Separating PR checks from releases keeps CI fast. Tag-based releases are idiomatic for Go projects.
**Alternatives considered**: GoReleaser (adds dependency), single workflow with conditionals (harder to maintain)

## R6: Website Technology

**Decision**: SvelteKit 2 + Svelte 5 + Tailwind in separate private repo, deployed to Cloudflare Pages.
**Rationale**: Matches user's tech preferences (see CLAUDE.md). Cloudflare Pages provides free hosting with edge CDN. Separate repo avoids bloating the Go project.
**Alternatives considered**: Astro (used for mcpproxy.app), Hugo (Go-native but less flexible for interactive pages)

## R7: Pre-commit/Pre-push Hooks

**Decision**: Shell scripts in `scripts/hooks/`, installed via `make hooks` symlink.
**Rationale**: Go projects don't use npm (no husky). Shell scripts are universal, zero dependencies. Graceful degradation if golangci-lint not installed.
**Alternatives considered**: lefthook (Go-based, good but adds dependency), pre-commit framework (Python dependency)

## R8: Docker Image Registry

**Decision**: ghcr.io/smart-mcp-proxy/synapbus
**Rationale**: GitHub Container Registry is free for public repos, integrates with GitHub Actions natively (GITHUB_TOKEN auth).
**Alternatives considered**: Docker Hub (rate limits), ECR (AWS-specific)
