# Feature Specification: Admin CLI & Docker Fixes

**Feature Branch**: `006-admin-cli-docker-fixes`
**Created**: 2026-03-15
**Status**: Draft
**Input**: User description: "Fix admin socket accessibility in Docker, add channels create/join CLI commands, fix default socket path"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Admin CLI Works in Kubernetes Pods (Priority: P1)

An operator needs to run admin CLI commands inside a Kubernetes pod (e.g., `kubectl exec -n synapbus deploy/synapbus -- /synapbus channels list`). With the current `scratch` base image, there is no shell and the admin socket is unreachable via exec'd processes. Switching to `alpine` allows `kubectl exec` with a shell and gives the admin CLI a working environment.

**Why this priority**: This is a blocker — without this fix, all admin CLI operations fail after pod restart in production.

**Independent Test**: Build Docker image with alpine base, deploy to a test pod, run `kubectl exec ... -- /synapbus channels list` and verify it returns results.

**Acceptance Scenarios**:

1. **Given** a SynapBus pod running with the alpine-based image, **When** an operator runs `kubectl exec deploy/synapbus -- /synapbus channels list`, **Then** the command executes and returns channel data over the admin socket.
2. **Given** a SynapBus pod running with the alpine-based image, **When** an operator runs `kubectl exec deploy/synapbus -- sh`, **Then** they get an interactive shell.

---

### User Story 2 - Create Channels via CLI (Priority: P1)

An operator needs to create channels without using the Web UI or REST API with session cookies. The `synapbus channels create` command should create a channel via the admin socket.

**Why this priority**: Required for automated provisioning scripts and headless setups.

**Independent Test**: Start SynapBus server, run `synapbus channels create --name test-channel --description "A test channel"`, then verify with `synapbus channels list`.

**Acceptance Scenarios**:

1. **Given** a running SynapBus server, **When** an operator runs `synapbus channels create --name news-feed --description "News feed channel"`, **Then** the channel is created and a success response with channel details is printed.
2. **Given** a running SynapBus server, **When** an operator runs `synapbus channels create --name news-feed` without `--description`, **Then** the channel is created with an empty description.
3. **Given** a channel named "news-feed" already exists, **When** an operator runs `synapbus channels create --name news-feed`, **Then** an appropriate error message is displayed.

---

### User Story 3 - Join Agents to Channels via CLI (Priority: P1)

An operator needs to add agents to channels via the admin CLI so agents can post messages. The `synapbus channels join` command should add an agent to a channel's membership.

**Why this priority**: Agents cannot post to channels they haven't joined; this is required for initial agent setup and automation.

**Independent Test**: Create a channel and an agent, run `synapbus channels join --channel test-channel --agent my-agent`, then verify with `synapbus channels show --name test-channel`.

**Acceptance Scenarios**:

1. **Given** a channel "test-channel" and agent "my-agent" exist, **When** an operator runs `synapbus channels join --channel test-channel --agent my-agent`, **Then** the agent is added as a member and a success response is printed.
2. **Given** an agent is already a member of "test-channel", **When** an operator runs `synapbus channels join --channel test-channel --agent my-agent`, **Then** the operation succeeds idempotently (no error).
3. **Given** channel "nonexistent" does not exist, **When** an operator runs `synapbus channels join --channel nonexistent --agent my-agent`, **Then** an error message indicates the channel was not found.

---

### User Story 4 - Absolute Default Socket Path (Priority: P2)

The default socket path for admin CLI commands is currently `./data/synapbus.sock` (relative). In containers where CWD varies, this is confusing. The default should be `/data/synapbus.sock` (absolute) to match the container layout.

**Why this priority**: Quality-of-life improvement; the current relative path works but is confusing.

**Independent Test**: Run `synapbus --help` and verify the default socket path shows `/data/synapbus.sock`.

**Acceptance Scenarios**:

1. **Given** the `--socket` flag is not provided, **When** the CLI resolves the admin socket path, **Then** it defaults to `/data/synapbus.sock`.
2. **Given** the `SYNAPBUS_SOCKET` environment variable is set, **When** the CLI resolves the admin socket path, **Then** it uses the environment variable value.
3. **Given** the `--socket` flag is provided with a custom path, **When** the CLI resolves the admin socket path, **Then** it uses the custom path.

---

### Edge Cases

- What happens when channel name contains invalid characters? The existing `ValidateChannelName` rules apply, and the CLI reports the validation error.
- What happens when the admin socket is not reachable? The CLI prints a connection error with "is synapbus serve running?" hint.
- What happens when an agent name doesn't exist during channel join? The operation fails with a clear error message from the channel service.
- What happens when the `--name` flag is missing on `channels create`? Cobra enforces the required flag and prints usage.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The Docker image MUST use `alpine:3.19` as the runtime base image instead of `scratch`.
- **FR-002**: The system MUST provide a `synapbus channels create` CLI command with `--name` (required) and `--description` (optional) flags.
- **FR-003**: The `channels create` command MUST send a `channels.create` request over the admin socket and display the result.
- **FR-004**: The admin socket server MUST handle `channels.create` commands by creating a channel via the channel service.
- **FR-005**: The system MUST provide a `synapbus channels join` CLI command with `--channel` (required) and `--agent` (required) flags.
- **FR-006**: The `channels join` command MUST send a `channels.join` request over the admin socket and display the result.
- **FR-007**: The admin socket server MUST handle `channels.join` commands by looking up the channel by name and adding the agent as a member.
- **FR-008**: The default value of the `--socket` persistent flag MUST be `/data/synapbus.sock` (absolute path).
- **FR-009**: The `SYNAPBUS_SOCKET` environment variable MUST override the default socket path when the flag is not explicitly set.
- **FR-010**: The Docker image MUST remain minimal — only the binary, TLS certs, and timezone data should be included from the build stage.

### Key Entities

- **Channel**: Named communication space with type, description, privacy flag, and member list.
- **Agent**: Named entity (AI or human) that can be a member of channels.
- **Admin Socket**: Unix domain socket at a known path, used by CLI commands to communicate with the running server.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Operators can execute all admin CLI commands inside a Kubernetes pod via `kubectl exec` without errors.
- **SC-002**: `synapbus channels create --name <name>` successfully creates a channel and returns channel details within 1 second.
- **SC-003**: `synapbus channels join --channel <name> --agent <name>` successfully adds an agent to a channel within 1 second.
- **SC-004**: The default socket path displayed in help text is `/data/synapbus.sock`.
- **SC-005**: The Docker image size remains under 50MB (alpine adds minimal overhead vs scratch).

## Assumptions

- Alpine 3.19 is acceptable as the runtime base image (adds ~7MB over scratch).
- The `channels.create` admin command uses `"system"` as the `created_by` field since admin socket operations are implicitly trusted.
- The `channels.join` admin command adds the agent with the `"member"` role (not owner).
- Channel type defaults to `"standard"` if not specified.
- No `--private` or `--type` flags are needed for the initial `channels create` command — they can be added later.
- The Helm chart deployment.yaml does not need changes since it already passes `--data /data`.
