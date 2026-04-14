# MCP Tool Contracts — Dynamic Agent Spawning

All new agent-facing operations are exposed as MCP tools. The shapes below are the wire-level contracts, written as JSON Schema fragments. The matching handlers live in `internal/mcp/tools_*.go`.

Every tool requires Bearer-auth via an agent API key or human OAuth token (existing middleware). Every tool trace-logs via `internal/trace/` on success and failure.

---

## 1. `create_goal`

**Caller**: human owner (via MCP OAuth session) or agent acting on behalf of a human.
**Effect**: Creates a goal row, auto-creates `#goal-<slug>` channel, assigns the pre-built coordinator.

```json
{
  "name": "create_goal",
  "description": "Create a new high-level goal with a backing channel and assign a coordinator.",
  "inputSchema": {
    "type": "object",
    "required": ["title", "description"],
    "properties": {
      "title":                {"type": "string", "minLength": 3,  "maxLength": 200},
      "description":          {"type": "string", "minLength": 10, "maxLength": 16000},
      "budget_dollars_cents": {"type": "integer", "minimum": 0},
      "budget_tokens":        {"type": "integer", "minimum": 0},
      "max_spawn_depth":      {"type": "integer", "minimum": 1, "maximum": 10, "default": 3},
      "coordinator_config":   {"type": "string", "description": "Named coordinator template; defaults to 'default'"}
    }
  },
  "outputSchema": {
    "type": "object",
    "required": ["goal_id", "slug", "channel_id", "coordinator_agent_id"],
    "properties": {
      "goal_id":              {"type": "integer"},
      "slug":                 {"type": "string"},
      "channel_id":           {"type": "integer"},
      "coordinator_agent_id": {"type": "integer"}
    }
  }
}
```

---

## 2. `propose_task_tree`

**Caller**: coordinator agent.
**Effect**: Writes an approval message to `#approvals` carrying the proposed tree. No tasks are materialized until approval.

```json
{
  "name": "propose_task_tree",
  "description": "Propose a decomposition of the current goal into a tree of tasks. Does not create tasks; waits for human approval.",
  "inputSchema": {
    "type": "object",
    "required": ["goal_id", "tree"],
    "properties": {
      "goal_id": {"type": "integer"},
      "reasoning": {"type": "string"},
      "tree": {
        "$ref": "#/definitions/task_node"
      }
    },
    "definitions": {
      "task_node": {
        "type": "object",
        "required": ["title", "description"],
        "properties": {
          "title":               {"type": "string", "minLength": 3, "maxLength": 200},
          "description":         {"type": "string", "minLength": 10},
          "acceptance_criteria": {"type": "string"},
          "billing_code":        {"type": "string"},
          "budget_tokens":       {"type": "integer", "minimum": 0},
          "budget_dollars_cents":{"type": "integer", "minimum": 0},
          "verifier_config":     {"$ref": "#/definitions/verifier_config"},
          "children":            {"type": "array", "items": {"$ref": "#/definitions/task_node"}}
        }
      },
      "verifier_config": {
        "oneOf": [
          {"type": "object", "properties": {"kind": {"const": "auto"}}, "required": ["kind"]},
          {"type": "object", "properties": {"kind": {"const": "peer"}, "agent_id": {"type": "integer"}}, "required": ["kind","agent_id"]},
          {"type": "object", "properties": {"kind": {"const": "command"}, "cmd": {"type": "string"}, "cwd": {"type": "string"}, "timeout_sec": {"type": "integer"}}, "required": ["kind","cmd"]}
        ]
      }
    }
  },
  "outputSchema": {
    "type": "object",
    "required": ["proposal_message_id"],
    "properties": {
      "proposal_message_id": {"type": "integer"},
      "channel_id":          {"type": "integer"}
    }
  }
}
```

On approval (human reacts `approve` on the proposal message), the `tasks` service materializes all nodes atomically in a single transaction, including denormalized ancestry snapshots.

---

## 3. `propose_agent`

**Caller**: any existing agent.
**Effect**: Writes an `agent_proposals` row and an approval message in `#approvals`. Server-side pre-checks for delegation-cap and spawn-depth violations.

```json
{
  "name": "propose_agent",
  "description": "Propose spawning a new specialist sub-agent. Subject to delegation-cap rule and spawn-depth limit.",
  "inputSchema": {
    "type": "object",
    "required": ["goal_id","parent_task_id","name","model","system_prompt","tool_scope","autonomy_tier","reason"],
    "properties": {
      "goal_id":        {"type": "integer"},
      "parent_task_id": {"type": "integer"},
      "name":           {"type": "string", "minLength": 3, "maxLength": 64, "pattern": "^[a-z][a-z0-9-]*$"},
      "model":          {"type": "string"},
      "system_prompt":  {"type": "string", "minLength": 20, "maxLength": 16000},
      "tool_scope":     {"type": "array", "items": {"type": "string"}, "minItems": 1},
      "skills":         {"type": "array", "items": {"type": "string"}},
      "mcp_servers":    {"type": "array", "items": {"type": "object",
                              "required": ["name","url","transport"],
                              "properties": {"name": {"type":"string"}, "url": {"type":"string"}, "transport": {"type":"string"}}}},
      "subagents":      {"type": "array", "items": {"type": "string"}},
      "autonomy_tier":  {"enum": ["supervised","assisted","autonomous"]},
      "reason":         {"type": "string", "minLength": 10}
    }
  },
  "outputSchema": {
    "type": "object",
    "required": ["proposal_id","approval_message_id"],
    "properties": {
      "proposal_id":         {"type": "integer"},
      "approval_message_id": {"type": "integer"}
    }
  }
}
```

**Server-side rejections** (returned before writing any rows):

- `ErrSpawnDepthExceeded` — caller's `spawn_depth + 1 > goal.max_spawn_depth`.
- `ErrDelegationCapExceeded` — proposed `tool_scope`, `autonomy_tier`, or budget exceeds caller's own grant.
- `ErrAgentNameTaken` — another active agent with the same name exists under the same owner.

**On approval**, SynapBus:

1. Computes `config_hash` from canonical-JSON of the proposed fields.
2. Creates a new `agents` row with `parent_agent_id`, `spawn_depth`, the computed hash, and a freshly-generated API key.
3. Seeds a `reputation_evidence` row at 70 % of the parent's rolling score in the matching domain.
4. DMs the proposer with the credentials.

---

## 4. `claim_task`

**Caller**: any agent (typically a specialist).
**Effect**: Atomic optimistic-lock claim on a task that is `approved` and unassigned.

```json
{
  "name": "claim_task",
  "description": "Atomically claim an approved task. Returns ErrAlreadyClaimed if already taken.",
  "inputSchema": {
    "type": "object",
    "required": ["task_id"],
    "properties": {
      "task_id": {"type": "integer"}
    }
  },
  "outputSchema": {
    "type": "object",
    "required": ["task_id", "status", "claimed_at"],
    "properties": {
      "task_id":    {"type": "integer"},
      "status":     {"type": "string"},
      "claimed_at": {"type": "string", "format": "date-time"}
    }
  }
}
```

Errors: `ErrAlreadyClaimed`, `ErrTaskNotApproved`, `ErrAgentQuarantined`.

---

## 5. `verify_task`

**Caller**: peer agent whose `verify_task` tool was configured as a task's verifier.
**Effect**: Records a verdict on a task currently in `awaiting_verification` and transitions it to `done` or `failed`.

```json
{
  "name": "verify_task",
  "description": "Record a verification verdict on a task that is awaiting verification.",
  "inputSchema": {
    "type": "object",
    "required": ["task_id", "verdict"],
    "properties": {
      "task_id": {"type": "integer"},
      "verdict": {"enum": ["approve", "reject"]},
      "reason":  {"type": "string"}
    }
  },
  "outputSchema": {
    "type": "object",
    "required": ["task_id", "new_status"],
    "properties": {
      "task_id":     {"type": "integer"},
      "new_status":  {"type": "string"}
    }
  }
}
```

Errors: `ErrTaskNotAwaitingVerification`, `ErrVerifierNotConfigured`, `ErrNotPermittedVerifier`.

---

## 6. `request_resource`

**Caller**: any agent running inside a task.
**Effect**: Writes a `resource_requests` row and posts a structured message to `#requests`.

```json
{
  "name": "request_resource",
  "description": "Ask the human owner for a missing credential or resource. The value is never returned via MCP.",
  "inputSchema": {
    "type": "object",
    "required": ["name","resource_type","reason","task_id"],
    "properties": {
      "name":          {"type": "string", "pattern": "^[A-Z][A-Z0-9_]*$"},
      "resource_type": {"enum": ["api_key","credential","url","file","other"]},
      "reason":        {"type": "string", "minLength": 5},
      "task_id":       {"type": "integer"}
    }
  },
  "outputSchema": {
    "type": "object",
    "required": ["request_id"],
    "properties": {
      "request_id": {"type": "integer"},
      "request_message_id": {"type": "integer"}
    }
  }
}
```

---

## 7. `list_resources`

**Caller**: any agent.
**Effect**: Returns the names and availability of secrets in scope for the caller. **Values are never returned.**

```json
{
  "name": "list_resources",
  "description": "List names of secrets available to the caller's scope. Values are never returned.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "task_id": {"type": "integer", "description": "Optional: if provided, task-scoped secrets are included"}
    }
  },
  "outputSchema": {
    "type": "object",
    "required": ["resources"],
    "properties": {
      "resources": {
        "type": "array",
        "items": {
          "type": "object",
          "required": ["name", "scope", "available"],
          "properties": {
            "name":      {"type": "string"},
            "scope":     {"enum": ["user","agent","task"]},
            "available": {"type": "boolean"}
          }
        }
      }
    }
  }
}
```

---

## 8. `resume_goal`

**Caller**: human owner.
**Effect**: Transitions a paused goal back to `active`.

```json
{
  "name": "resume_goal",
  "description": "Resume a goal previously paused by budget or manual action.",
  "inputSchema": {
    "type": "object",
    "required": ["goal_id"],
    "properties": {
      "goal_id": {"type": "integer"},
      "new_budget_dollars_cents": {"type": "integer", "minimum": 0},
      "new_budget_tokens":        {"type": "integer", "minimum": 0}
    }
  },
  "outputSchema": {
    "type": "object",
    "required": ["goal_id","status"],
    "properties": {
      "goal_id": {"type": "integer"},
      "status":  {"type": "string"}
    }
  }
}
```

---

## 9. `unquarantine_agent`

**Caller**: human owner.
**Effect**: Lifts quarantine on an agent, clears `quarantined_at`, posts a trace row.

```json
{
  "name": "unquarantine_agent",
  "description": "Lift quarantine on an agent. Reputation is unchanged.",
  "inputSchema": {
    "type": "object",
    "required": ["agent_id"],
    "properties": {
      "agent_id": {"type": "integer"}
    }
  },
  "outputSchema": {
    "type": "object",
    "required": ["agent_id","status"],
    "properties": {
      "agent_id": {"type": "integer"},
      "status":   {"type": "string"}
    }
  }
}
```

---

## REST endpoints (internal, Web UI only)

| Method | Path | Response |
|---|---|---|
| `GET`  | `/api/goals` | List of `{id, slug, title, status, owner_user_id, channel_id, cost_totals, task_count, last_activity_at}` |
| `GET`  | `/api/goals/:id` | Full snapshot: `{goal, tasks_tree, spawned_agents, cost_breakdown, recent_events}` |
| `POST` | `/api/goals/:id/resume` | Wraps `resume_goal` MCP tool |
| `GET`  | `/api/agents/:id/reputation` | `{config_hash, rolling_score, evidence_count, last_evidence_at}` |
| `GET`  | `/api/secrets/:scope_type/:scope_id` | Array of `{name, available, last_used_at}` (no values) |

All are session-authenticated via the existing cookie middleware. No external exposure.
