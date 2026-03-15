# Tasks: Admin CLI & Docker Fixes

**Feature**: 006-admin-cli-docker-fixes
**Created**: 2026-03-15
**Plan**: [plan.md](plan.md)

## Phase 1: Setup

- [x] **T01**: Change default socket path from `./data/synapbus.sock` to `/data/synapbus.sock` in `cmd/synapbus/admin.go` (line 922) and update the `SYNAPBUS_SOCKET` env var check comparison string.
  - Files: `cmd/synapbus/admin.go`

## Phase 2: Core — Admin Socket Handlers

- [x] **T02**: Add `channels.create` handler to `internal/admin/socket.go` dispatch table and implement `handleChannelsCreate` method.
  - Files: `internal/admin/socket.go`
  - Depends on: T01

- [x] **T03**: Add `channels.join` handler to `internal/admin/socket.go` dispatch table and implement `handleChannelsJoin` method.
  - Files: `internal/admin/socket.go`
  - Depends on: T01

## Phase 3: Core — CLI Commands

- [x] **T04**: Add `synapbus channels create` cobra command with `--name` (required) and `--description` (optional) flags in `cmd/synapbus/admin.go`.
  - Files: `cmd/synapbus/admin.go`
  - Depends on: T02

- [x] **T05**: Add `synapbus channels join` cobra command with `--channel` (required) and `--agent` (required) flags in `cmd/synapbus/admin.go`.
  - Files: `cmd/synapbus/admin.go`
  - Depends on: T03

## Phase 4: Docker

- [x] **T06**: Change Dockerfile runtime stage from `FROM scratch` to `FROM alpine:3.19` and add `RUN apk add --no-cache ca-certificates tzdata` (removing COPY of certs/tzdata from builder).
  - Files: `Dockerfile`

## Phase 5: Tests & Validation

- [x] **T07**: Add unit tests for `channels.create` and `channels.join` admin socket handlers.
  - Files: `cmd/synapbus/admin_test.go`
  - Depends on: T04, T05

- [x] **T08**: Run `make build` and `make test` to verify all changes compile and pass.
  - Depends on: T01-T07
