# Contract — MCP Tool Response Injection

## Wrapper shape

For every injection-eligible MCP tool, the response JSON body gains an optional `relevant_context` field appended at the top level:

```json
{
  "<existing tool result fields>": "...",
  "relevant_context": {
    "memories": [
      {
        "id": 12345,
        "from_agent": "research-mcpproxy",
        "channel": "open-brain",
        "body": "KuzuDB archived 2025-10-10; not viable for synapbus",
        "created_at": "2026-05-08T14:22:00Z",
        "score": 0.91,
        "match_type": "hybrid",
        "pinned": false,
        "truncated": false
      }
    ],
    "core_memory": "<text>",           // only on session-start tools, may be omitted
    "packet_chars": 412,
    "packet_token_estimate": 103,
    "retrieval_query": "Kuzu graph DB",
    "search_mode": "auto"
  }
}
```

### Field semantics

- `memories` — up to N items (default 5, env `SYNAPBUS_INJECTION_MAX_ITEMS`), ranked by RRF score. Body is verbatim from the message; `truncated=true` only when a single item had to be cut to fit the token budget.
- `core_memory` — Letta-style per-(owner, agent) blob. Present only on session-start-class tools (`my_status` today; extensible). Omitted entirely when no blob is set.
- `packet_chars` — total character count of the assembled packet (memories + core + delimiters).
- `packet_token_estimate` — `packet_chars/4` rounded up.
- `retrieval_query` — the text that drove retrieval (the tool's argument body, or "<recent activity>" fallback). Useful for "why did my agent know this?" debug.
- `search_mode` — `"auto"` / `"semantic"` / `"fulltext"` per `search.Service`.

## Injection-eligible tools

| Tool | Retrieval query source | Include core memory? |
|------|------------------------|----------------------|
| `my_status` | `<recent activity>` | ✅ |
| `claim_messages` | concatenated bodies of claimed messages | ❌ |
| `read_inbox` | concatenated bodies of returned messages | ❌ |
| `send_message` | body of the sent message | ❌ |
| `search` / `search_messages` | the user's query | ❌ |
| `execute` | the request payload (stringified args) | ❌ |
| `read_channel` | channel topic + last N message bodies | ❌ |

Tools NOT injected: `list_resources`, `propose_agent`, `propose_task_tree`, `complete_goal`, `create_goal`, `claim_task`, `get_replies`, `request_resource`, `react` (write-only or metadata).

## Configuration

| Env var | Default | Description |
|---------|---------|-------------|
| `SYNAPBUS_INJECTION_ENABLED` | `0` (off) | Master switch. Off → no `relevant_context` field on any response (FR-012, SC-009). |
| `SYNAPBUS_INJECTION_BUDGET_TOKENS` | `500` | Soft cap. Greedy fill in descending score; truncate last admitted item if needed. |
| `SYNAPBUS_INJECTION_MAX_ITEMS` | `5` | Hard cap on `memories[]` length. |
| `SYNAPBUS_INJECTION_MIN_SCORE` | inherits `search.DefaultMinSimilarity` (0.25) | Floor — pinned memories bypass this. |
| `SYNAPBUS_CORE_MEMORY_MAX_BYTES` | `2048` | Reject `memory_rewrite_core` over this size. |

## Empty / low-signal paths

- If `len(memories) == 0` and no core memory is set → omit the `relevant_context` field entirely. Response shape exactly matches pre-feature.
- If `len(memories) == 0` but core memory IS set → include the field with `memories: []` and `core_memory: "..."`.
- If a tool's response is not JSON-shaped (e.g. binary attachment download), pass through unchanged.

## Cross-owner safety (SC-008)

Retrieval is filtered by `caller.OwnerID` at the SQL level inside `search.Service.Search()`. The injection middleware never inspects or trusts the response body for owner info — it always re-derives owner from `auth.ContextAgent(ctx)`. An adversarial test asserts that an agent owned by H1 making *any* injection-eligible tool call cannot have a memory whose source message was authored by an agent owned by H2 appear in `relevant_context.memories`.

## Audit (FR-025)

Every assembled non-empty packet writes one row to `memory_injections` with `(owner_id, agent_name, tool_name, packet_size_chars, packet_items_count, message_ids[], core_blob_included, created_at)`. Rows older than 24h are purged hourly by the consolidator worker.
