# goal-coordinator — universal triage + delegation demo

A 3-agent multi-agent system where a **coordinator** triages arbitrary
goals into one of four outcomes:

| Triage | Action |
|---|---|
| **TRIVIAL** | Coordinator answers directly. No delegation. (`2+2` → `4`) |
| **INFEASIBLE** | Coordinator refuses with a concrete reason. (`transfer $50 from my bank` → `CANNOT: no banking credentials`) |
| **SINGLE-STEP** | Coordinator delegates to `generic-inspector` + `critic-auditor`. |
| **MULTI-STEP** | Coordinator plans multi-phase execution (rare). |

Unlike the [`doc-gardener`](../doc-gardener) example, which hardcodes a
3-task tree for a single domain, this coordinator is **goal-agnostic**:
you DM it any natural-language brief and it decides what to do.

## Architecture

```
algis ──DM──▶ goal-coordinator (Gemini Pro)
                   │
                   ├── reply     → algis                          (TRIVIAL)
                   ├── refuse    → algis (CANNOT: ...)            (INFEASIBLE)
                   └── delegate  → generic-inspector (Gemini Flash)
                                       │
                                       └── artifact → critic-auditor (Gemini Flash)
                                                         │
                                                         ├── FINAL:   → algis
                                                         └── REVISE:  → generic-inspector
```

Key design decisions:

- **Critic is a separate agent.** It has its own `config_hash`,
  independent reputation, and reads only the inspector's output —
  not its reasoning trace. Prevents the critic from rationalizing
  the worker's mistakes.
- **Inspector is one agent, not three.** Scan + verify + report all
  happen in one pass because they share context (the finding list).
  Splitting them forces synchronization for no gain.
- **Coordinator uses a smart model; workers use a fast model.**
  `SYNAPBUS_COORDINATOR_MODEL=gemini-3.1-pro-preview` (default) vs
  `SYNAPBUS_WORKER_MODEL=gemini-2.5-flash` (default). Override either.
- **Harness-agnostic.** Every agent goes through the subprocess
  harness calling `wrapper.sh`. Swap the `gemini` invocation in
  wrapper.sh for `claude`, `codex`, or any other CLI — nothing else
  in SynapBus needs to change.
- **Universal system prompts.** `configs/coordinator.json` contains
  the triage rules; they work for any goal, not just mcpproxy.

## Running

```bash
./start.sh                    # provisions user, agents, harness configs
./run_task.sh "what is 2+2?"  # TRIVIAL path
./run_task.sh "Check what Go version is installed and whether it's >= 1.23"
                              # SINGLE-STEP path (delegates to inspector+critic)
./run_task.sh "Transfer \$50 from my bank account to Bob"
                              # INFEASIBLE path (refusal)
./stop.sh
```

Web UI at http://localhost:18090 (login `algis` / `algis-demo-pw`) —
see each delegation flow in `/runs`, the captured prompts + responses
in run detail, and the goal tree + cost rollup under `/goals`.

## Why this matters

The doc-gardener demo proved spec-018's primitives work. This example
shows what you get when you let an LLM drive them: a coordinator that
**reasons about each goal before delegating**, answers trivial things
directly, refuses infeasible things clearly, and only spawns workers
when real work is needed. The step from doc-gardener (fixed template)
to goal-coordinator (LLM-driven triage) is what makes the system
"agentic" instead of a task-runner.

## Next evolution

The coordinator currently emits a plan JSON which `wrapper.sh` parses
and dispatches via the admin socket. The next step is to give the
Gemini session direct access to the SynapBus MCP tools (`create_goal`,
`propose_task_tree`, `propose_agent`, `claim_task`, `request_resource`,
`list_resources` — all registered at startup; see
`internal/mcp/goals_tools.go`). Then the coordinator calls them
directly in-session, wrapper.sh becomes a 20-line pass-through, and
the whole flow is driven by MCP tool calls end-to-end.
