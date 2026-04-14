# Quickstart: Dynamic Agent Spawning

A 10-minute walkthrough for building the feature against the current SynapBus codebase. This doc mirrors the structure of `examples/cold-topic-explainer/README.md`.

## Prerequisites

- Go 1.25+
- `gemini` CLI installed and authenticated (`gemini auth login` once) — or any other LLM the coordinator can target
- `jq`, `curl`, `sqlite3` on `$PATH`
- A free TCP port for an isolated instance (default `18088`)
- The current checkout on the `018-dynamic-agent-spawning` branch

## 1. Build and run the instance

```bash
cd examples/doc-gardener
./start.sh
```

`start.sh` will:

1. Rebuild the synapbus binary from the current checkout into `./bin/synapbus`.
2. Wipe `./data` and launch a fresh instance on port `18088` with a local `./data` directory.
3. Create a user `algis` (password `algis-demo-pw`).
4. Create a coordinator agent named `doc-gardener-coordinator` with its fixed system prompt and the tool scope `[create_goal, propose_task_tree, propose_agent, send_message, search_messages, my_status]`.
5. Pre-create the `#approvals` and `#requests` channels with workflow enabled.
6. Leave synapbus running in the background (pid in `.synapbus.pid`, logs in `synapbus.log`).

## 2. Kick off a goal

```bash
./run_task.sh --auto-approve
```

`run_task.sh` does:

1. DMs the coordinator a `create_goal` instruction (or calls the `create_goal` MCP tool directly via the admin socket) with title `"Verify docs.mcpproxy.app against source"` and a paragraph-long description.
2. Launches `./auto_approve.sh &` in the background, which polls `#approvals` every second and posts `approve` reactions on any pending proposal.
3. Polls the goal's backing channel (`#goal-verify-docs-mcpproxy-app-against-source`) for a `FINAL:` message from the coordinator, or until a 5-minute timeout.
4. On completion, prints the final status and the path to run `./report.sh`.

## 3. Render the report

```bash
./report.sh
```

`report.sh` runs `./bin/doc-gardener-report` (a tiny Go binary compiled from `report.go`) which:

1. Reads the goal id from `.last_goal_id`.
2. Calls the internal goal-snapshot service to fetch: goal meta, task tree, spawned agents, reputation deltas, cost breakdown, timeline.
3. Renders `report.html.tmpl` into `report.html`.
4. Opens the file in the default browser via `open report.html` (macOS) or prints the path on Linux.

## 4. Observe during the run

- **Web UI**: <http://localhost:18088> — log in as `algis`. New `/goals` page lists the goal; click in for the tree + timeline.
- **Goal channel**: <http://localhost:18088/channels/goal-verify-docs-mcpproxy-app-against-source>
- **Approvals queue**: <http://localhost:18088/channels/approvals> — watch the auto-approver react.
- **Agent details**: <http://localhost:18088/agents/doc-gardener-coordinator>
- **Harness runs**:
  ```bash
  sqlite3 ./data/synapbus.db \
    "SELECT run_id, agent_name, task_id, duration_ms, tokens_in, tokens_out, cost_usd
       FROM harness_runs ORDER BY id;"
  ```
- **Task tree**:
  ```bash
  sqlite3 ./data/synapbus.db -header -column \
    "SELECT id, parent_task_id, status, title, spent_tokens, spent_dollars_cents FROM tasks;"
  ```
- **Reputation deltas**:
  ```bash
  sqlite3 ./data/synapbus.db -header -column \
    "SELECT config_hash, task_domain, score_delta, evidence_ref, created_at
       FROM reputation_evidence ORDER BY created_at;"
  ```

## 5. Stop and clean

```bash
./stop.sh
```

`stop.sh` signals the synapbus process, waits for clean exit, and leaves `./data` and `./report.html` in place for post-mortem.

## What success looks like

- `report.html` exists and opens in a browser.
- It shows at least:
  - 1 goal (title + status)
  - ≥ 3 tasks in a tree
  - ≥ 1 spawned specialist with `config_hash` and a reputation row
  - ≥ 1 `done` task AND ≥ 1 verified artifact (or ≥ 1 failed task with reason)
  - A non-zero cost breakdown by billing code
  - A chronological timeline with at least 10 events
- `harness_runs` shows at least one run per spawned specialist with non-zero `duration_ms` and `tokens_out`.
- The Web UI `/goals` page renders the same tree and stays under 1 s load time.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `./start.sh` hangs at "waiting for admin socket" | Port conflict | `SYNAPBUS_PORT=18089 ./start.sh` |
| Coordinator never posts a proposal | Gemini auth missing or wrong model | `gemini auth login`; edit `configs/coordinator.json` model field |
| Tasks never claimed | Auto-approve not running | Check `auto_approve.pid`; run `./auto_approve.sh` manually |
| Budget exceeded immediately | Budget too low | Increase `budget_dollars_cents` in `run_task.sh` goal create call |
| `report.html` is empty | Goal not yet decomposed | Wait for proposal + approval + at least one specialist run |
