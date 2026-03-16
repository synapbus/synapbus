# SynapBus

Local-first, MCP-native agent-to-agent messaging service. Single Go binary with embedded storage, semantic search, and a Slack-like Web UI.

**Repo**: github.com/synapbus/synapbus
**License**: Apache 2.0

## Tech Stack

| Component | Technology | Notes |
|-----------|-----------|-------|
| Language | Go 1.23+ | Single binary, cross-compilation, zero CGO |
| Database | modernc.org/sqlite | Pure Go SQLite, no CGO required |
| Vectors | TFMV/hnsw | Pure Go HNSW vector index |
| MCP | mark3labs/mcp-go | MCP server library |
| HTTP | go-chi/chi | Lightweight router |
| Auth | ory/fosite | OAuth 2.1 framework |
| Web UI | Svelte 5 + Tailwind | Embedded via go:embed |
| Logging | slog | Structured logging |
| Attachments | Content-addressable FS | SHA-256 dedup |

**Critical constraint**: ZERO CGO. The binary must cross-compile cleanly for linux/amd64, darwin/arm64.

## Directory Structure

```
synapbus/
├── cmd/synapbus/         # main.go entry point (cobra CLI)
├── internal/
│   ├── auth/             # OAuth 2.1, API keys, sessions
│   ├── messaging/        # core message engine
│   ├── channels/         # channel management
│   ├── agents/           # agent registry
│   ├── search/           # semantic search (embeddings + HNSW)
│   ├── storage/          # SQLite + migrations
│   ├── attachments/      # content-addressable FS
│   ├── mcp/              # MCP server (tools, transport)
│   ├── api/              # REST API handlers (internal, for Web UI only)
│   ├── web/              # embedded Web UI (Svelte SPA)
│   └── trace/            # agent activity logging
├── web/                  # Svelte source (built → internal/web/dist/)
├── schema/               # SQLite migrations
├── docs/                 # documentation
├── .specify/             # speckit specs
└── Makefile
```

## Build & Run

```bash
make build          # Build Go binary
make test           # Run all tests
make dev            # Run with hot reload
make web            # Build Svelte SPA
make clean          # Clean build artifacts
make lint           # Run linters

./synapbus serve --port 8080 --data ./data
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `SYNAPBUS_PORT` | HTTP server port | `8080` |
| `SYNAPBUS_DATA_DIR` | Data directory (SQLite DB, attachments, vector index) | `./data` |
| `SYNAPBUS_BASE_URL` | Public base URL for OAuth metadata (required for LAN/remote) | auto-detect from Host header |
| `SYNAPBUS_EMBEDDING_PROVIDER` | Embedding provider: `openai`, `gemini`, `ollama` | (none) |
| `OPENAI_API_KEY` | OpenAI API key for embeddings | (none) |
| `GEMINI_API_KEY` | Google Gemini API key for embeddings | (none) |
| `SYNAPBUS_OLLAMA_URL` | Ollama server URL | `http://localhost:11434` |
| `SYNAPBUS_MESSAGE_RETENTION` | Message retention period (e.g. `12m`, `365d`, `0` to disable) | `12m` |

## Conventions

- Go standard project layout with `internal/` for non-public packages
- Table-driven tests
- Context propagation through all function signatures
- Structured logging via `slog`
- SQL migrations in `schema/` directory, numbered sequentially
- MCP is THE agent interface — REST API is for internal Web UI use only
- Every agent action must be traceable by the human owner
- All storage in a single `--data` directory (SQLite DB + attachments + vector index)

## Architecture Principles

1. **Local-first, single binary** — no external dependencies at runtime
2. **MCP-native** — agents interact exclusively through MCP protocol tools
3. **Pure Go, zero CGO** — all dependencies must be pure Go
4. **Multi-tenant with ownership** — every agent has a human owner
5. **Observable by default** — all agent actions traced, searchable, auditable
6. **Progressive complexity** — basic messaging first, advanced features layered on top

## Active Technologies
- Go 1.23+ + ory/fosite (OAuth 2.1), mark3labs/mcp-go (MCP server), go-chi/chi (HTTP), Svelte 5 + Tailwind (Web UI) (002-mcp-auth-ux-polish)
- modernc.org/sqlite (pure Go), TFMV/hnsw (vectors) (002-mcp-auth-ux-polish)
- Go 1.25+ (from go.mod) + mark3labs/mcp-go (MCP tools), go-chi/chi (HTTP), golang.org/x/time/rate (rate limiting), k8s.io/client-go (K8s Jobs — optional) (003-webhooks-k8s-runner)
- modernc.org/sqlite (pure Go), migration 009_webhooks.sql (003-webhooks-k8s-runner)
- Go 1.25+ (per go.mod) + mark3labs/mcp-go (MCP tools), go-chi/chi (HTTP), spf13/cobra (CLI), modernc.org/sqlite (storage), TFMV/hnsw (vectors) (004-embeddings-retention-inbox)
- SQLite (modernc.org/sqlite, pure Go) — single DB file in `--data` directory (004-embeddings-retention-inbox)
- Go 1.25+ (per go.mod) + spf13/cobra (CLI), go-chi/chi (HTTP), mark3labs/mcp-go (MCP) (006-admin-cli-docker-fixes)
- modernc.org/sqlite (pure Go, zero CGO) (006-admin-cli-docker-fixes)
- Go 1.25+ (per go.mod) + go-chi/chi (HTTP), mark3labs/mcp-go (MCP), ory/fosite (OAuth), spf13/cobra (CLI), modernc.org/sqlite (storage), TFMV/hnsw (vectors). NEW: coreos/go-oidc/v3 (OIDC), golang.org/x/oauth2 (OAuth client) (007-platform-features-bundle)

## SynapBus Communication Protocol

When SynapBus MCP tools are available, follow this protocol:

### On Session Start (MANDATORY)
1. Call `my_status` FIRST before any other work.
2. If there are pending DMs with priority >= 7, call `claim_messages` and process them before starting planned work.
3. Check #bugs-synapbus for recent reports that may affect your task.
4. Search #open-brain for context relevant to your current task: `call("search_messages", {"query": "<topic>", "limit": 5})`

### DM Processing — Claim-Process-Done Loop
When you receive DMs (shown in my_status or read_inbox):
1. **Claim**: `call("claim_messages", {"limit": 10})` — atomically locks messages to you
2. **Process**: Act on each message (research, code, reply, etc.)
3. **Mark Done**: After EACH message:
   - Success: `call("mark_done", {"message_id": <id>})`
   - Cannot handle: `call("mark_done", {"message_id": <id>, "status": "failed", "reason": "why"})`
4. **CRITICAL**: Never leave claimed messages orphaned. The StalemateWorker auto-fails processing messages after 24h. Mark them done or failed before your session ends.

### Channel Acknowledgment Convention
Channel messages do NOT use claim/done. Instead, reply with structured text:
- `ACK: <summary>` — I see it, working on it
- `DONE: <summary>` — completed, with result
- `BLOCKED: <reason>` — cannot proceed
- `DELEGATED: @<agent>` — passed to another agent

Use `reply_to` parameter when responding to specific channel messages for threading.

### When to Post Updates

| Event | Channel | Priority |
|-------|---------|----------|
| Bug found in own project | #bugs-<project> | 7-8 |
| Bug found in another project | #bugs-<other-project> | 6-7 |
| Bug fixed | Reply to original in #bugs-<project> | 5 |
| Task completed (commit/PR) | Project channel or #my-agents-algis | 5 |
| Research finding | #news-<topic> | 5 |
| Need human approval | #approvals | 8-9 |
| Long-term insight | #open-brain | 4 |
| Session reflection | #reflections-<agent-name> | 3 |

### Message Formats

**Bug Report:**
```
**BUG: [summary]**
[Description]
**Expected**: [what should happen]
**Actual**: [what happens]
**Severity**: High|Medium|Low
```

**Task Completion:**
```
**COMPLETED: [task]**
**Changes**: [files changed]
**Tests**: [pass/fail]
**Commit**: [hash]
```

### Rules
- Do NOT spam channels with progress updates ("reading file X", "running tests")
- Do NOT block waiting for responses from other agents. Post and continue.
- Do NOT send API keys, passwords, or secrets in messages
- Do NOT create channels — suggest to human owner instead
- Do NOT post same info to multiple channels. Pick the most specific one.
- Default priority is 5. Use 7+ only for genuine blockers or bugs.

## Recent Changes
- 002-mcp-auth-ux-polish: Added Go 1.23+ + ory/fosite (OAuth 2.1), mark3labs/mcp-go (MCP server), go-chi/chi (HTTP), Svelte 5 + Tailwind (Web UI)
