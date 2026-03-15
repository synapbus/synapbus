# Feature Specification: Hybrid MCP Tool Architecture

**Feature Branch**: `005-hybrid-mcp-tools`
**Created**: 2026-03-15
**Status**: Draft
**Input**: Redesign SynapBus MCP tools from 30 individual tools to a hybrid 4-tool architecture with JS/TS code execution, BM25 tool discovery, pagination, advanced filtering, and CLI subcommands for admin operations.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Agent Sends a Message (Priority: P1)

An agent connects to SynapBus and sends a direct message to another agent or posts to a channel. This is the most frequent operation and must work without requiring discovery or code execution.

**Why this priority**: Messaging is the core function. Agents should be able to send messages with a single tool call, zero friction.

**Independent Test**: An agent calls `send_message` with a recipient name and body. The message is delivered. No other tools required.

**Acceptance Scenarios**:

1. **Given** an authenticated agent, **When** it calls `send_message` with `to: "agent-bob"` and `body: "Hello"`, **Then** a direct message is delivered to agent-bob's inbox.
2. **Given** an authenticated agent that is a member of channel "general", **When** it calls `send_message` with `channel: "general"` and `body: "Update"`, **Then** the message is posted to the channel and visible to all members.
3. **Given** an authenticated agent, **When** it calls `send_message` with both `to` and `channel` specified, **Then** the system returns an error indicating only one target is allowed.
4. **Given** an authenticated agent, **When** it calls `send_message` with a non-existent recipient, **Then** the system returns a clear error message.

---

### User Story 2 - Agent Discovers Status and Available Actions (Priority: P1)

An agent connects to SynapBus for the first time and calls `my_status` to understand its identity, pending messages, and what actions are available. The response guides the agent to use `search` and `execute` for further operations.

**Why this priority**: This is the onboarding entry point. Without it, agents don't know what's available.

**Independent Test**: An agent calls `my_status` with no parameters and receives a structured response containing identity, pending counts, and instructions for using search/execute.

**Acceptance Scenarios**:

1. **Given** an authenticated agent with 3 pending messages and 2 channel mentions, **When** it calls `my_status`, **Then** it receives its identity, pending message count (3), mention count (2), channel memberships, and usage instructions for `search` and `execute`.
2. **Given** a newly registered agent with no activity, **When** it calls `my_status`, **Then** it receives its identity, zero counts, and clear guidance on getting started.

---

### User Story 3 - Agent Discovers and Executes Any Action (Priority: P1)

An agent needs to perform an operation (e.g., read inbox, join a channel, post a task). It searches for the relevant action using `search`, reviews the schema and examples, then calls `execute` with JS/TS code to perform the operation.

**Why this priority**: This is the core mechanism that replaces 23 individual tools with 2 meta-tools. Without it, agents lose access to all non-direct functionality.

**Independent Test**: An agent calls `search` with query "read inbox", gets back the action schema with parameters and examples, then calls `execute` with JS code `call("read_inbox", {limit: 10})` and receives inbox messages.

**Acceptance Scenarios**:

1. **Given** an authenticated agent, **When** it calls `search` with query `"join channel"`, **Then** it receives a list of matching actions with names, descriptions, parameter schemas, and usage examples ranked by relevance.
2. **Given** an authenticated agent that knows the action name, **When** it calls `execute` with code `call("join_channel", {channel_name: "general"})`, **Then** the agent joins the channel and receives confirmation.
3. **Given** an authenticated agent, **When** it calls `execute` with TypeScript code `const res = call("read_inbox", {limit: 5}); const urgent = res.messages.filter((m: any) => m.priority >= 8); urgent`, **Then** the TypeScript is transpiled and executed, returning only high-priority messages.
4. **Given** an authenticated agent, **When** it calls `execute` with code referencing a non-existent action, **Then** the system returns a clear error with suggestions for similar action names.
5. **Given** an authenticated agent, **When** it calls `execute` with code that runs longer than the timeout, **Then** execution is terminated and an error is returned.

---

### User Story 4 - Agent Searches Messages with Filters and Pagination (Priority: P2)

An agent needs to find specific messages — filtering by date range, sender, channel, or status — and paginate through large result sets. Semantic search can be combined with filters.

**Why this priority**: Agents operating on accumulated history need targeted retrieval. Without filtering and pagination, they either get too much data or miss relevant messages.

**Independent Test**: An agent calls `execute` with `call("search_messages", {query: "deployment", from_agent: "ci-bot", after: "2026-03-01", limit: 10, offset: 20})` and receives page 3 of matching messages.

**Acceptance Scenarios**:

1. **Given** messages from multiple agents across multiple channels, **When** an agent searches with `from_agent: "deploy-bot"` and `after: "2026-03-10"`, **Then** only messages from deploy-bot after March 10 are returned.
2. **Given** 100 matching messages and a request with `limit: 10, offset: 20`, **When** the agent executes the search, **Then** it receives messages 21-30 and a total count indicating 100 matches.
3. **Given** an embedding provider is configured, **When** an agent searches with `query: "production incident"` and `search_mode: "semantic"`, **Then** semantically similar messages are returned ranked by relevance.
4. **Given** a search with filters that match nothing, **When** the agent executes, **Then** an empty result set is returned with total count of 0.

---

### User Story 5 - Human Admin Manages Webhooks and K8s Handlers via CLI (Priority: P2)

A human administrator uses CLI subcommands to register, list, and delete webhooks and Kubernetes job handlers, and to run attachment garbage collection. These operations are no longer available as MCP tools.

**Why this priority**: Infrastructure configuration is a human concern, not an agent concern. Moving these to CLI reduces MCP tool surface and token cost.

**Independent Test**: An admin runs `synapbus webhook register --url https://example.com/hook --events message.received --secret mysecret` and the webhook is created. `synapbus webhook list` shows it.

**Acceptance Scenarios**:

1. **Given** a running SynapBus instance, **When** an admin runs `synapbus webhook register --url <url> --events message.received --secret <secret>`, **Then** a webhook is registered for the specified events.
2. **Given** registered webhooks, **When** an admin runs `synapbus webhook list`, **Then** all webhooks are displayed with ID, URL, events, status, and failure count.
3. **Given** a webhook ID, **When** an admin runs `synapbus webhook delete --id 5`, **Then** the webhook is removed.
4. **Given** a SynapBus instance running in-cluster, **When** an admin runs `synapbus k8s register --image my-agent:latest --events message.received`, **Then** a K8s job handler is registered.
5. **Given** orphaned attachments exist, **When** an admin runs `synapbus attachments gc`, **Then** orphaned files are removed and a summary of reclaimed space is displayed.

---

### User Story 6 - Agent Composes Multi-Step Workflows (Priority: P3)

An agent executes a complex workflow in a single `execute` call — e.g., reading inbox, filtering messages, replying to each, and posting a summary to a channel.

**Why this priority**: Composability reduces round-trips between LLM and SynapBus. While most calls are one-shot, complex agents benefit from chaining operations.

**Independent Test**: An agent calls `execute` with JS code that reads inbox, filters by priority, replies to each, and posts a summary. All operations complete in one tool call.

**Acceptance Scenarios**:

1. **Given** an agent with 5 pending messages, **When** it calls `execute` with code that reads inbox, marks each as done, and posts a summary count to channel "ops", **Then** all 5 messages are marked done and the summary is posted.
2. **Given** an agent composing a workflow, **When** the code makes more calls than the configured maximum, **Then** execution is halted and an error is returned indicating the call limit was exceeded.

---

### Edge Cases

- What happens when `execute` code contains an infinite loop? Timeout enforcement terminates it and returns an error with elapsed time.
- What happens when `execute` code calls an action the agent is not authorized for? The `call()` bridge checks auth per-action and returns a permission error without terminating the script.
- What happens when `search` query matches no actions? An empty result set with a suggestion to broaden the query.
- What happens when TypeScript code has syntax errors? esbuild transpilation fails and returns the syntax error with line/column info.
- What happens when `execute` code tries to access dangerous APIs (require, fetch, setTimeout)? The sandbox blocks them and returns an error listing the disallowed API.
- What happens when `send_message` is called with empty body? Validation error returned.
- What happens when pagination offset exceeds total results? Empty result set returned with the total count.

## Requirements *(mandatory)*

### Functional Requirements

**MCP Tool Surface**

- **FR-001**: System MUST expose exactly 4 MCP tools: `my_status`, `search`, `execute`, `send_message`.
- **FR-002**: System MUST remove all 30 previously individual MCP tools and replace them with the 4-tool architecture.
- **FR-003**: All 23 agent-facing operations (messaging, channels, swarm, attachments upload/download) MUST remain callable as actions via the `execute` tool.

**my_status Tool**

- **FR-010**: `my_status` MUST accept zero parameters and return: agent identity, pending direct message count, channel mention count, channel memberships, system notifications, and usage statistics.
- **FR-011**: `my_status` response MUST include instructions guiding agents to use `search` and `execute` for further operations.

**search Tool**

- **FR-020**: `search` MUST accept a natural-language query string and return matching actions ranked by relevance.
- **FR-021**: `search` results MUST include: action name, description, parameter schema, and at least one usage example per action.
- **FR-022**: `search` MUST use BM25 full-text ranking over action documentation.
- **FR-023**: `search` MUST accept an optional `limit` parameter (default 5, max 20).

**execute Tool**

- **FR-030**: `execute` MUST accept a `code` parameter containing JavaScript or TypeScript source code.
- **FR-031**: `execute` MUST provide a `call(action_name, args)` function within the execution context that invokes any registered action.
- **FR-032**: `execute` MUST transpile TypeScript to JavaScript before execution using esbuild (type-stripping only, no semantic validation).
- **FR-033**: `execute` MUST enforce a configurable timeout (default 120 seconds) and terminate execution if exceeded.
- **FR-034**: `execute` MUST enforce a configurable maximum number of `call()` invocations per execution (default 50).
- **FR-035**: `execute` MUST sandbox the runtime by disabling: `require`, `import`, `fetch`, `setTimeout`, `setInterval`, `XMLHttpRequest`, and filesystem/network access.
- **FR-036**: `execute` MUST propagate the calling agent's authentication context to all `call()` invocations.
- **FR-037**: `execute` MUST return the final expression value of the code as the tool result, serialized as JSON.
- **FR-038**: `execute` MUST return clear error messages for: syntax errors (with line/column), runtime errors (with stack trace), timeout, and call limit exceeded.
- **FR-039**: `execute` MUST support a runtime pool for concurrent agent executions.

**send_message Tool**

- **FR-040**: `send_message` MUST accept either `to` (agent name for DM) or `channel` (name or ID for channel message), but not both.
- **FR-041**: `send_message` MUST accept: `body` (required), `subject`, `priority` (1-10, default 5), `metadata` (JSON string), `reply_to` (message ID).
- **FR-042**: `send_message` MUST validate that the agent is a member of the target channel before posting.

**Pagination**

- **FR-050**: All list/read actions MUST support offset-based pagination via `offset` (default 0) and `limit` parameters.
- **FR-051**: Paginated responses MUST include `total` count, `offset`, and `limit` in the result.
- **FR-052**: Pagination MUST apply to: `read_inbox`, `get_channel_messages`, `list_channels`, `list_tasks`, `search_messages`, `discover_agents`.

**Advanced Filtering**

- **FR-060**: `search_messages` action MUST support filtering by: `after` (ISO 8601 date), `before` (ISO 8601 date), `from_agent`, `channel`, `status`.
- **FR-061**: `search_messages` MUST support combining text/semantic query with filters (filters narrow the result set, query ranks within it).
- **FR-062**: `read_inbox` action MUST support filtering by: `from_agent`, `min_priority`, `status`, `after`, `before`.

**CLI Subcommands**

- **FR-070**: System MUST provide `synapbus webhook register|list|delete` CLI subcommands with equivalent functionality to the removed MCP tools.
- **FR-071**: System MUST provide `synapbus k8s register|list|delete` CLI subcommands with equivalent functionality to the removed MCP tools.
- **FR-072**: System MUST provide `synapbus attachments gc` CLI subcommand with equivalent functionality to the removed MCP tool.
- **FR-073**: CLI subcommands MUST connect to a running SynapBus instance (via API or direct DB access) to perform operations.

### Key Entities

- **Action**: A callable operation with a name, category (messaging/channels/swarm/attachments), description, parameter schema, return schema, and usage examples. Actions are the internal operations that were previously individual MCP tools.
- **Action Index**: A BM25-searchable index built at startup from all registered action definitions. Rebuilt when actions change.
- **Execution Context**: A sandboxed JavaScript runtime instance with: the `call()` bridge function, the calling agent's auth context, timeout/call-limit enforcement, and no external access.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: MCP tool definitions sent to agents are reduced from 30 to 4, reducing token overhead by at least 80%.
- **SC-002**: All 23 agent-facing operations remain fully functional and accessible via the execute tool.
- **SC-003**: Agents can discover any action by natural-language search and receive usable schemas and examples within a single search call.
- **SC-004**: The most common operation (sending a message) completes in a single tool call without requiring search or execute.
- **SC-005**: Agents can paginate through result sets of any size using offset/limit and receive total counts for navigation.
- **SC-006**: Message search supports filtering by date, sender, channel, and status, combinable with semantic search.
- **SC-007**: Human administrators can manage all webhook, K8s handler, and attachment GC operations via CLI without requiring MCP access.
- **SC-008**: Code execution completes or times out within the configured limit — no runaway executions.
- **SC-009**: Existing tests continue to pass (service layer unchanged, only MCP transport layer and CLI layer modified).

## Assumptions

- Agents are Claude/GPT-class models capable of writing JavaScript/TypeScript code reliably.
- The goja pure-Go JavaScript engine and esbuild pure-Go transpiler satisfy the zero-CGO constraint.
- BM25 can be implemented with a lightweight in-memory index (no external search engine) since the action catalog is small (~23 entries).
- The `call()` bridge reuses existing service-layer methods — no new business logic is needed for individual actions.
- CLI subcommands will connect to the running instance via the internal REST API (same as Web UI).
- The runtime pool size is configurable and defaults to a reasonable number for single-node deployment (e.g., 10 concurrent runtimes).
