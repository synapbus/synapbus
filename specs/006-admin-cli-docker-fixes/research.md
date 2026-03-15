# Research: Admin CLI & Docker Fixes

**Feature**: 006-admin-cli-docker-fixes
**Date**: 2026-03-15

## R1: Alpine vs Scratch Docker Base Image

**Decision**: Use `alpine:3.19` as the runtime base image.

**Rationale**: The `scratch` image has no shell, no `/bin/sh`, no filesystem utilities. This means `kubectl exec` cannot spawn any process other than the entrypoint binary itself. Since admin CLI commands need to connect to the Unix socket created by the running server process, exec'd processes need a working environment. Alpine adds ~7MB but provides `/bin/sh`, basic filesystem operations, and a working process environment.

**Alternatives considered**:
- `distroless/static` (Google): No shell, same problem as scratch.
- `busybox`: Works but no package manager. Alpine is the standard minimal base.
- `debian-slim`: ~80MB, unnecessarily large.

## R2: Admin Socket Protocol for Channel Operations

**Decision**: Add `channels.create` and `channels.join` commands to the existing admin socket dispatch table, following the exact pattern of existing commands (e.g., `agent.create`, `webhook.register`).

**Rationale**: The admin socket already has a well-established request/response pattern: JSON-RPC style `{command, args}` → `{ok, data, error}`. The channel service already exposes `CreateChannel` and `JoinChannel` methods. The admin server already holds a reference to the channel service via `Services.Channels`. No new wiring needed.

**Alternatives considered**:
- HTTP admin API endpoint: Would require API key protection (user explicitly rejected this approach).
- Direct database manipulation via CLI: Bypasses service layer validation, unsafe.

## R3: Default Socket Path

**Decision**: Change default from `./data/synapbus.sock` to `/data/synapbus.sock` (absolute).

**Rationale**: In containers, the working directory is `/` and the data volume is mounted at `/data`. The relative path `./data/synapbus.sock` resolves to `/data/synapbus.sock` from `/`, but this is confusing and fragile. An absolute default matches the Dockerfile's `--data /data` argument and the Helm chart's `volumeMount` at `/data`.

The `SYNAPBUS_SOCKET` environment variable and `--socket` flag still allow overriding for development (e.g., `--socket ./data/synapbus.sock` for local dev).

**Alternatives considered**:
- Keep relative path: Works in containers but confusing for users.
- Use `$SYNAPBUS_DATA_DIR/synapbus.sock` as default: Over-engineered; the socket path flag already exists.

## R4: `channels.create` Admin Handler Design

**Decision**: The `channels.create` handler accepts `{name, description}` args, calls `channelService.CreateChannel` with `created_by: "system"`, and returns the created channel as JSON.

**Rationale**: Admin socket commands are implicitly trusted (localhost-only, process-level access). Using `"system"` as the creator matches the pattern used for the default `#general` channel. The `description` field is optional (defaults to empty string).

## R5: `channels.join` Admin Handler Design

**Decision**: The `channels.join` handler accepts `{channel, agent}` args, looks up the channel by name via `GetChannelByName`, then calls `JoinChannel(channelID, agentName)`. Returns success message.

**Rationale**: The CLI uses channel names (not IDs) because operators work with names. The service's `JoinChannel` already handles idempotency (re-joining is a no-op) and private channel invite checks.
