# Feature Specification: SynapBus Production Readiness & Website Launch

**Feature Branch**: `001-production-readiness`
**Created**: 2026-03-13
**Status**: Draft
**Input**: Transform SynapBus from MVP to production-ready service with DevOps, observability, deployment artifacts, and public website.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Developer Catches Issues Before Push (Priority: P1)

A developer working on SynapBus runs `git commit` and `git push`. Pre-commit hooks automatically run fast checks (formatting, vet, lint). Pre-push hooks run the full test suite and build verification. The developer receives immediate feedback on code quality issues before code reaches the remote repository.

**Why this priority**: Prevents broken code from entering the repository, saving team time and maintaining code quality. Foundation for all other quality processes.

**Independent Test**: Can be tested by making a commit with a lint error and verifying it is rejected, then fixing it and verifying the commit succeeds.

**Acceptance Scenarios**:

1. **Given** a developer stages Go files with lint warnings, **When** they run `git commit`, **Then** the commit is rejected with clear error messages identifying the issues.
2. **Given** a developer has clean code staged, **When** they run `git commit`, **Then** hooks pass and the commit succeeds within 15 seconds.
3. **Given** a developer pushes code that breaks tests, **When** they run `git push`, **Then** the push is rejected with test failure output.
4. **Given** a developer pushes code that passes all tests, **When** they run `git push`, **Then** the push succeeds.

---

### User Story 2 - CI Validates Pull Requests Automatically (Priority: P1)

A contributor opens a pull request on GitHub. The CI pipeline automatically runs linting, tests, and multi-platform build verification. The PR status checks prevent merging broken code. On merge to main, release artifacts are built automatically.

**Why this priority**: Automated quality gates are essential for any production service. Enables safe collaboration.

**Independent Test**: Can be tested by opening a PR with a failing test and verifying the check fails, then fixing and verifying it passes.

**Acceptance Scenarios**:

1. **Given** a PR is opened, **When** CI runs, **Then** it executes lint, test, and build steps and reports status.
2. **Given** CI detects test failures, **When** the PR status is updated, **Then** the merge button is blocked.
3. **Given** a version tag is pushed, **When** the release workflow runs, **Then** binaries for 5 platform/arch combinations are published as GitHub Release assets.
4. **Given** a version tag is pushed, **When** the release workflow runs, **Then** a Docker image is built and pushed to the container registry.

---

### User Story 3 - Operator Monitors SynapBus Health (Priority: P1)

An operator deploys SynapBus and configures monitoring to scrape its metrics endpoint. They can monitor request rates, latencies, message volumes, and agent counts. Liveness and readiness probes work correctly for container orchestration.

**Why this priority**: Observability is non-negotiable for production services. Without it, operators are blind to issues.

**Independent Test**: Can be tested by starting SynapBus, querying the metrics endpoint, and verifying properly formatted output.

**Acceptance Scenarios**:

1. **Given** SynapBus is running with metrics enabled, **When** an operator queries the metrics endpoint, **Then** they receive properly formatted metrics including request counts, latencies, and business metrics.
2. **Given** SynapBus is healthy, **When** a liveness probe hits the health endpoint, **Then** it returns success status.
3. **Given** SynapBus is ready to serve traffic, **When** a readiness probe checks, **Then** it returns success status.
4. **Given** the database is inaccessible, **When** a readiness probe checks, **Then** it returns failure status.

---

### User Story 4 - Operator Deploys SynapBus in Containers (Priority: P2)

An operator deploys SynapBus using containers (standalone or compose) or container orchestration (via configuration templates). Data persists across restarts. Configuration is managed via environment variables or template values.

**Why this priority**: Containerized deployment is the standard for modern services. Templates enable repeatable deployments.

**Independent Test**: Can be tested by building and running the container image, sending a message, restarting, and verifying the message persists.

**Acceptance Scenarios**:

1. **Given** a container build file exists, **When** an operator builds the image, **Then** a minimal image is produced (under 50MB).
2. **Given** a compose file exists, **When** an operator starts the stack, **Then** SynapBus starts and is accessible on the configured port.
3. **Given** a deployment template exists, **When** an operator installs it, **Then** SynapBus deploys with correct service, workload, and persistence configuration.
4. **Given** SynapBus is running with persistent storage, **When** the container restarts, **Then** all data persists.

---

### User Story 5 - Visitor Discovers SynapBus Online (Priority: P2)

A developer or technical decision-maker visits synapbus.dev. They see a visually compelling landing page that explains what SynapBus does, how to install it, and why they should use it. They can browse documentation and read blog posts about agent messaging patterns.

**Why this priority**: A public website is essential for adoption. Developers need documentation and clear value propositions.

**Independent Test**: Can be tested by navigating to the website and verifying all pages load correctly on desktop and mobile.

**Acceptance Scenarios**:

1. **Given** a visitor navigates to synapbus.dev, **When** the page loads, **Then** they see an attractive landing page with clear product description and call-to-action.
2. **Given** a visitor navigates to documentation, **When** the docs page loads, **Then** they see installation instructions, configuration reference, and feature guides.
3. **Given** a visitor navigates to the blog, **When** the blog page loads, **Then** they see at least 2 posts with hero images and diagrams.
4. **Given** a visitor uses a mobile device, **When** they browse the site, **Then** all pages are fully responsive and usable.

---

### User Story 6 - Developer Downloads and Installs SynapBus (Priority: P3)

A developer visits the installation page, sees download options for their platform, and can install SynapBus with a single command or download.

**Why this priority**: Easy installation drives adoption. Multiple distribution channels serve different user preferences.

**Independent Test**: Can be tested by following the installation instructions on a clean machine.

**Acceptance Scenarios**:

1. **Given** a developer is on the installation page, **When** they select their platform, **Then** they see a direct download link and command-line install command.
2. **Given** release artifacts exist, **When** a developer downloads one, **Then** it is a self-contained binary that runs without dependencies.

---

### Edge Cases

- What happens when git hooks are installed but lint tools are not available? Hooks should warn and skip lint (not block).
- What happens when the readiness endpoint is called during database migration? Should return failure until migrations complete.
- What happens when metrics scraping is disabled? The metrics endpoint should not be registered on the router.
- What happens when container build runs on ARM64? Multi-architecture build must work for both amd64 and arm64.
- What happens when deployment template is installed without persistence? Should work with ephemeral storage (with a warning in values).

## Requirements *(mandatory)*

### Functional Requirements

**DevOps & Hooks**:
- **FR-001**: Repository MUST include a pre-commit hook script that runs code analysis and fast tests on staged files.
- **FR-002**: Repository MUST include a pre-push hook script that runs the full test suite and verifies the binary builds successfully.
- **FR-003**: Hooks MUST be installable via a single make target and MUST gracefully degrade if optional tools are missing.

**CI/CD**:
- **FR-004**: Repository MUST include an automated workflow for PR checks that runs lint, tests, and build verification.
- **FR-005**: Repository MUST include an automated workflow for releases triggered by version tags that builds binaries for 5 platform/architecture combinations.
- **FR-006**: Release workflow MUST build and push a multi-architecture container image to the project's container registry.
- **FR-007**: PR workflow MUST build the web frontend before the main build to ensure embedded assets are current.

**Observability**:
- **FR-008**: System MUST expose a metrics endpoint in standard monitoring format when metrics are enabled.
- **FR-009**: System MUST expose liveness and readiness health check endpoints that return appropriate status codes.
- **FR-010**: Metrics MUST include: request counts (by method, path, status), request duration distribution, message totals, active agent count, and active connection count.
- **FR-011**: Readiness probe MUST verify database connectivity before reporting ready.

**Deployment**:
- **FR-012**: Repository MUST include a multi-stage container build file that produces a minimal runtime image.
- **FR-013**: Repository MUST include a compose file for local development with volume persistence.
- **FR-014**: Repository MUST include a deployment template with configurable values for image, replicas, resources, persistence, ingress, and environment variables.
- **FR-015**: Deployment template MUST include liveness and readiness probes using the health check endpoints.

**Website**:
- **FR-016**: A separate repository MUST host the synapbus.dev website.
- **FR-017**: Website MUST include: landing page, installation guide, configuration reference, features overview, use cases, and blog.
- **FR-018**: Website MUST be deployed to a CDN with DNS configured for synapbus.dev.
- **FR-019**: Website MUST include at least 2 blog posts with generated hero images and technical diagrams.
- **FR-020**: Website MUST be fully responsive on mobile devices (320px to 1920px+ viewports).

### Key Entities

- **Git Hook**: Script installed in the repository that runs quality checks at commit/push time.
- **CI Workflow**: Automated pipeline definition for build, test, and release processes.
- **Metric**: Named measurement exposed for monitoring systems (counters, gauges, distributions).
- **Health Probe**: Endpoint returning status codes for orchestrator health checking.
- **Container Image**: Packaged application image built from multi-stage build, pushed to registry.
- **Deployment Template**: Configuration template with customizable values for container orchestration.
- **Website Page**: Static page served from CDN edge locations.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Pre-commit hooks complete in under 15 seconds for a typical change.
- **SC-002**: PR validation pipeline completes in under 5 minutes.
- **SC-003**: Release workflow produces verified binaries for all 5 target platforms.
- **SC-004**: Container image size is under 50MB compressed.
- **SC-005**: Health check endpoints respond in under 10ms.
- **SC-006**: Metrics endpoint returns valid, parseable monitoring data.
- **SC-007**: Deployment template deploys successfully on a standard container orchestration cluster.
- **SC-008**: Website pages load in under 2 seconds on a standard connection.
- **SC-009**: Website scores 90+ on mobile performance audits.
- **SC-010**: All blog posts include hero images and at least one technical diagram.

## Assumptions

- Website will be in a separate private repository following established project patterns.
- Container images are pushed to GitHub Container Registry (ghcr.io).
- Deployment templates follow standard container orchestration conventions.
- Pre-commit/pre-push hooks are shell scripts (this is a Go project).
- Metrics use a standard Go metrics library compatible with the project's zero-CGO constraint.
- CI uses GitHub Actions with Go 1.23+ and Node.js 20+ for web builds.
- Image generation API is used for hero graphics on blog posts.
- Website uses dark theme, modern developer-focused aesthetic.
- Blog topics: (1) Agent-to-agent messaging patterns, (2) Building AI swarms with MCP.
- CDN deployment uses the platform's CLI tooling.
- Binary releases use a build matrix approach (not external release tooling).
- The existing `--metrics` flag in the server startup is currently unused and will be wired to the new metrics integration.
