# SynapBus Bootstrap Prompt

Paste everything below the line into Claude in the `/Users/user/repos/synapbus` directory.

---

I'm bootstrapping **SynapBus** — an open-source, local-first, MCP-native agent-to-agent messaging service written in Go. Single binary, embedded storage, semantic search, Slack-like Web UI.

The project idea is fully described in `IDEA.md` — read it first.

The repo is at https://github.com/synapbus/synapbus (public, empty).
Domains: synapbus.com + synapbus.dev (to be registered).

## What I need you to do (in order):

### 1. Create CLAUDE.md

Write the project CLAUDE.md with:
- Project overview (from IDEA.md)
- Tech stack: Go 1.23+, modernc.org/sqlite (pure Go), TFMV/hnsw (pure Go vectors), mcp-go (mark3labs), chi router, embedded Svelte SPA, fosite (OAuth 2.1)
- Directory structure (Go standard layout):
  ```
  synapbus/
  ├── cmd/synapbus/         # main.go entry point
  ├── internal/
  │   ├── auth/             # OAuth 2.1, API keys, sessions
  │   ├── messaging/        # core message engine
  │   ├── channels/         # channel management
  │   ├── agents/           # agent registry
  │   ├── search/           # semantic search (embeddings + HNSW)
  │   ├── storage/          # SQLite + migrations
  │   ├── attachments/      # content-addressable FS
  │   ├── mcp/              # MCP server (tools, transport)
  │   ├── api/              # REST API handlers
  │   ├── web/              # embedded Web UI (Svelte SPA)
  │   └── trace/            # agent activity logging
  ├── web/                  # Svelte source (built → internal/web/dist/)
  ├── schema/               # SQLite migrations
  ├── docs/                 # documentation
  ├── .specify/             # speckit specs
  └── Makefile
  ```
- Build commands: `make build`, `make test`, `make dev`, `make web` (build Svelte SPA)
- Run: `./synapbus serve --port 8080 --data ./data`
- Conventions: Go standard, `internal/` for non-public packages, table-driven tests, context propagation, structured logging (slog)
- Environment variables: `SYNAPBUS_PORT`, `SYNAPBUS_DATA_DIR`, `SYNAPBUS_EMBEDDING_PROVIDER` (openai/gemini/ollama), `SYNAPBUS_EMBEDDING_API_KEY`, `SYNAPBUS_OLLAMA_URL`

### 2. Create speckit constitution

Run `/speckit.constitution` and provide these architecture decisions:

**Principles:**
1. **Local-first, single binary** — everything in one Go binary, no external dependencies at runtime
2. **MCP-native** — agents interact exclusively through MCP protocol tools, REST API is internal only
3. **Pure Go, zero CGO** — all dependencies must be pure Go (modernc.org/sqlite, not mattn/go-sqlite3)
4. **Multi-tenant with ownership** — every agent has a human owner; owners control access and see traces
5. **Embedded OAuth 2.1** — authentication server built into SynapBus, not delegated externally
6. **Semantic-ready storage** — SQLite for relational data, HNSW for vector search, both embedded
7. **Swarm intelligence patterns** — first-class support for stigmergy, task auction, agent discovery
8. **Observable by default** — all agent actions traced, searchable, auditable by owners
9. **Progressive complexity** — start with basic messaging, layer on vectors/attachments/swarm later
10. **Web UI as first-class citizen** — embedded Svelte SPA, not an afterthought

**Technology decisions:**
- Go 1.23+ (single binary, cross-compilation)
- modernc.org/sqlite (pure Go SQLite, no CGO)
- TFMV/hnsw (pure Go HNSW vector index)
- mark3labs/mcp-go (MCP server library)
- go-chi/chi (HTTP router)
- ory/fosite (OAuth 2.1 framework)
- Svelte 5 + Tailwind (Web UI, embedded via go:embed)
- slog (structured logging)
- Content-addressable filesystem for attachments (SHA-256)

**Non-goals:**
- No PostgreSQL, Redis, or external DB dependency
- No framework lock-in (LangChain, CrewAI, etc.)
- No A2A protocol support (yet) — MCP only for v1
- No cloud-specific features — local-first always

### 3. Create specs for these features (use /speckit.specify for each):

**Spec 001: Core Messaging**
- Messages: send (DM or channel), read inbox, claim for processing, mark done/failed
- Conversations: threaded, with subjects, auto-created on first message
- Priority levels (1-10), status tracking (pending/processing/done/failed)
- Rich metadata (JSON) on messages for filtering
- Read/unread tracking per agent per conversation
- SQLite storage with migrations
- MCP tools: `send_message`, `read_inbox`, `claim_messages`, `mark_done`, `search_messages` (full-text initially)

**Spec 002: Agent Registry & Auth**
- Agent self-registration via MCP tool `register_agent`
- Each agent has: name (unique), display_name, type (ai/human), capabilities (JSON), owner_id
- API key authentication for agents (generated on registration, returned once)
- Agent CRUD: register, update capabilities, deregister (owner only)
- Owner-scoped access: agents can only see own messages + joined channels
- MCP tools: `register_agent`, `discover_agents`, `update_agent`, `deregister_agent`
- Agent capability cards (JSON schema describing what the agent can do)

**Spec 003: Human Auth (OAuth 2.1)**
- OAuth 2.1 authorization server embedded in SynapBus (using fosite)
- Local accounts: username + password (bcrypt hashed)
- Token endpoints: /oauth/authorize, /oauth/token, /oauth/introspect
- Grant types: authorization_code (Web UI), client_credentials (programmatic)
- Session management for Web UI (httponly cookies)
- User CRUD: create account, change password, list owned agents
- PKCE required for all authorization code flows
- Refresh token rotation

**Spec 004: Channels**
- Public channels: any registered agent can join
- Private channels: invite-only, managed by creator
- Channel metadata: name, description, topic, created_by
- Membership management: join, leave, invite, kick (owner only)
- Channel message broadcast: message sent to channel delivered to all members
- MCP tools: `create_channel`, `join_channel`, `leave_channel`, `list_channels`, `invite_to_channel`

**Spec 005: Web UI**
- Svelte 5 + Tailwind CSS embedded SPA
- Pages: Login, Dashboard (recent messages), Conversations (thread view), Channels, Agents, Settings
- Real-time updates via SSE
- Compose: send DM or channel message, select recipient from dropdown
- Search: full-text search across messages
- Agent management: view owned agents, their traces, revoke API keys
- Responsive, dark mode support
- Built with `make web`, embedded in Go binary via `go:embed`

**Spec 006: MCP Server**
- MCP server using mark3labs/mcp-go
- SSE transport (primary) + Streamable HTTP transport
- All messaging operations exposed as MCP tools
- Tool authentication: API key in MCP request headers
- Tool listing with JSON schema descriptions
- Health check endpoint
- Connection management: track connected agents

**Spec 007: Trace Logging & Observability**
- All agent actions logged: tool calls, messages sent/received, channel joins, errors
- Traces stored in SQLite with agent_name, action, details, timestamp
- Owner can view traces for their agents via Web UI
- Filterable by agent, action type, time range
- Exportable as JSON/CSV
- Optional Prometheus metrics endpoint (/metrics)
- Structured logging (slog) to stdout

**Spec 008: Semantic Search**
- Message embedding on ingest (async, configurable provider)
- Providers: OpenAI text-embedding-3-small, Gemini embedding, Ollama (local)
- HNSW vector index (TFMV/hnsw) for ANN search
- Combined search: vector similarity + metadata filters + full-text
- MCP tool: `search_messages` with query, filters, limit
- Incremental indexing: new messages embedded in background
- Fallback: full-text search if no embedding provider configured

**Spec 009: Attachments**
- Upload files up to 50MB per message
- Content-addressable storage: SHA-256 hash as filename, dedup
- Store in `{data_dir}/attachments/{hash[0:2]}/{hash[2:4]}/{hash}`
- Metadata in SQLite: hash, original_filename, size, mime_type, message_id
- MCP tools: `upload_attachment` (returns hash), `download_attachment` (by hash)
- Web UI: inline preview for images, download link for others
- Garbage collection: remove orphaned attachments

**Spec 010: Swarm Patterns**
- **Stigmergy (Blackboard)**: tagged messages on a shared channel that agents read and react to. Tags: `#finding`, `#task`, `#decision`, `#trace`
- **Task Auction**: `post_task` with requirements + deadline → agents `bid_task` with capabilities + time estimate → poster selects winner → task assigned
- **Agent Discovery**: `discover_agents` searches capability cards by keyword/semantic match
- Channel types: `standard` (chat), `blackboard` (stigmergy), `auction` (tasks)
- MCP tools: `post_task`, `bid_task`, `accept_bid`, `complete_task`

### 4. Generate tasks from specs

After creating all specs, run `/speckit.tasks` for each spec to generate implementation tasks.

### 5. Create initial project files

- `go.mod` with module `github.com/synapbus/synapbus`
- `Makefile` with targets: build, test, dev, web, clean, lint
- `cmd/synapbus/main.go` — cobra CLI with `serve` command (placeholder)
- `schema/001_initial.sql` — SQLite migration for agents, messages, conversations, channels, channel_members, inbox_state, traces, attachments
- `README.md` — project overview, installation, quick start
- `.gitignore` — Go + Node + data directory
- `LICENSE` — Apache 2.0

### 6. Push initial commit

Stage all files, commit with message "feat: initial SynapBus project scaffolding", push to `main` on origin.

---

**Key design constraints to keep in mind:**
- ZERO CGO — the binary must cross-compile cleanly for linux/amd64, darwin/arm64
- All storage in a single `--data` directory (SQLite DB file + attachments dir + vector index)
- MCP is THE interface for agents — REST API is for internal Web UI use only
- Every agent action must be traceable by the human owner
- OAuth 2.1 is built INTO the binary, not a separate service
- Web UI is built from Svelte source, embedded at compile time
