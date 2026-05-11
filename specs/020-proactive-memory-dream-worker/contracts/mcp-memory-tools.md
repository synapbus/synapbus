# Contract — MCP Memory-Consolidation Tools

These six tools are registered only when `SYNAPBUS_DREAM_ENABLED=1`. They reject any caller that does not present a valid `SYNAPBUS_DISPATCH_TOKEN` (header `X-Synapbus-Dispatch-Token` or env propagated by the harness). Token validation is described in `research.md` R7.

All six tools accept an implicit `dispatch_token` from the request context and explicit `owner_id` (asserted to match the token's owner; otherwise error `dispatch_token_owner_mismatch`).

---

## 1. `memory_list_unprocessed`

List recent memory-eligible messages the owner's pool has not yet consolidated.

**Input**:
```json
{
  "owner_id": "algis",
  "since_message_id": 0,    // optional — exclusive lower bound
  "limit": 50               // optional — default 50, max 200
}
```

**Output**:
```json
{
  "memories": [
    {
      "id": 12345,
      "from_agent": "research-mcpproxy",
      "channel": "open-brain",
      "body": "...",
      "created_at": "2026-05-08T14:22:00Z",
      "links": [{"to": 12000, "type": "mention"}]
    }
  ],
  "max_id_returned": 12567   // pass as since_message_id on next call
}
```

**Behavior**: Returns active memories from memory channels for the owner, ordered by id ascending, excluding any already linked via `refines` / `duplicate_of` / `superseded_by` to a more recent memory.

---

## 2. `memory_write_reflection`

Write a higher-level abstraction back to the memory pool as a new message in `#open-brain` (or a `#reflections-<owner>` channel if one exists), tagged.

**Input**:
```json
{
  "owner_id": "algis",
  "body": "Across recent discussions, the team has committed to ...",
  "source_message_ids": [12001, 12030, 12089],
  "tags": ["reflection", "weekly"]
}
```

**Output**:
```json
{
  "memory_id": 12601,
  "channel": "open-brain",
  "links_created": 3   // one 'refines' link per source
}
```

**Behavior**:
1. Inserts a message authored by a synthetic `dream:<owner>` agent into the memory channel (preferring `#reflections-<owner>` if it exists, else `#open-brain`).
2. Inserts a `refines` link from the new memory to each source.
3. Embeds the new message via the existing async pipeline.
4. Records `{tool, args, target_message_id: new_id}` in the parent job's `actions` JSON.

**Errors**: `source_not_found` if any source ID is missing or belongs to a different owner.

---

## 3. `memory_rewrite_core`

Replace the per-(owner, agent) core memory blob wholesale.

**Input**:
```json
{
  "owner_id": "algis",
  "agent_name": "research-mcpproxy",
  "blob": "You are research-mcpproxy. Currently focused on benchmarking ..."
}
```

**Output**:
```json
{
  "owner_id": "algis",
  "agent_name": "research-mcpproxy",
  "previous_blob": "...",
  "new_blob_chars": 412,
  "updated_at": "2026-05-11T03:00:14Z"
}
```

**Errors**:
- `core_memory_too_large` if `len(blob) > SYNAPBUS_CORE_MEMORY_MAX_BYTES`.
- `agent_not_owned` if the target agent's `owner_id != caller.owner_id`.
- `agent_protected` if the agent has `metadata.protected_core=true`.

---

## 4. `memory_mark_duplicate`

Mark two memories as duplicates; one is kept canonical, the other is soft-deleted.

**Input**:
```json
{
  "owner_id": "algis",
  "a_id": 12001,
  "b_id": 12089,
  "keep_id": 12001,
  "reason": "Same fact about KuzuDB archival, b is shorter paraphrase"
}
```

**Output**:
```json
{
  "keep_id": 12001,
  "soft_deleted_id": 12089,
  "link_created_id": 4501
}
```

**Behavior**:
1. Inserts a `duplicate_of` link from `loser_id → keep_id`.
2. Appends `{tool: "memory_mark_duplicate", target_message_id: loser_id, args: {keep_id, ...}}` to the job's `actions` JSON.
3. The `memory_status` view then derives `loser_id` as `soft_deleted`.

**Errors**: `not_same_owner`, `keep_id_not_in_pair`, `already_duplicate`.

---

## 5. `memory_supersede`

Mark memory A as obsoleted by memory B (temporal validity).

**Input**:
```json
{
  "owner_id": "algis",
  "a_id": 12001,
  "b_id": 12500,
  "reason": "Fact updated: as of 2026-04, kubernetes deploy moved off helm chart"
}
```

**Output**:
```json
{
  "superseded_id": 12001,
  "by_id": 12500,
  "link_created_id": 4502
}
```

**Behavior**: Inserts `superseded_by` link from `a → b`. View derives `a.status = 'superseded'`, `a.superseded_by = b`. Reason is preserved in the action JSON.

**Errors**: `not_same_owner`, `cycle_detected` (b transitively superseded by a).

---

## 6. `memory_add_link`

Add a typed link between two memories (A-MEM Zettelkasten style).

**Input**:
```json
{
  "owner_id": "algis",
  "src_id": 12030,
  "dst_id": 12085,
  "relation_type": "refines",
  "metadata": {"confidence": 0.84}
}
```

**Output**:
```json
{
  "link_id": 4510
}
```

**Constraints**:
- `relation_type` ∈ {`refines`, `contradicts`, `examples`, `related`}. The reserved auto-types (`mention`, `reply_to`, `channel_cooccurrence`) and consolidation-types (`duplicate_of`, `superseded_by`) are written by other tools / jobs and rejected here.

**Errors**: `relation_type_reserved`, `not_same_owner`, `link_already_exists`.

---

## Common error codes

| Code | Meaning |
|------|---------|
| `dispatch_token_missing` | No token in request context |
| `dispatch_token_expired` | Token past `expires_at` |
| `dispatch_token_revoked` | Token explicitly revoked |
| `dispatch_token_owner_mismatch` | Request's `owner_id` differs from token's |
| `dispatch_token_wrong_job` | Token bound to a different `consolidation_job_id` than active call sequence |
| `not_same_owner` | A referenced message belongs to a different owner |
| `source_not_found` | Referenced message id does not exist |
| `core_memory_too_large` | Blob exceeds size cap |

All errors follow MCP's standard `{"error": {"code": "...", "message": "..."}}` shape.

## Audit interaction

Every successful invocation of any of the six tools appends an entry to the parent `memory_consolidation_jobs.actions` JSON array:

```json
{
  "tool": "memory_mark_duplicate",
  "args": {"a_id": 12001, "b_id": 12089, "keep_id": 12001, "reason": "..."},
  "target_message_id": 12089,
  "at": "2026-05-11T03:00:14Z"
}
```

On job completion (success / partial / failed), the worker updates `status`, `summary`, `finished_at`. The token's `used_at` is set on first call; the token remains usable for the rest of the same job.
