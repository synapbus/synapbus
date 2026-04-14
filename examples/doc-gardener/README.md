# doc-gardener

End-to-end demo of the **dynamic agent spawning** feature (spec `018-dynamic-agent-spawning`).

A human owner defines a high-level goal ("verify docs.mcpproxy.app against the mcpproxy source code"). A pre-built **coordinator** meta-agent decomposes the goal into a task tree, proposes spawning **specialist sub-agents** with capped autonomy, the specialists claim tasks and produce artifacts, and a rich HTML report is generated from the run.

This example exercises the feature's **data primitives** end-to-end: goal creation with a backing channel, task-tree materialization with denormalized ancestry, `config_hash`-rooted trust, delegation-cap enforcement, atomic task claim, append-only reputation ledger, cost rollup, HTML rendering from DB state.

## Status of the MVP demo

| Piece | Status |
|---|---|
| Goal creation + backing channel | ✅ real |
| Task tree materialization (ancestry snapshots) | ✅ real |
| Atomic optimistic-lock task claim | ✅ real (covered by 50-goroutine race test in `internal/goaltasks/`) |
| Config-hash computation (deterministic, sensitive to capability changes) | ✅ real (tested in `internal/trust/`) |
| Delegation cap enforcement (child ≤ parent) | ✅ real (tested in `internal/trust/`) |
| Append-only reputation ledger with 70 %-of-parent seed and exponential decay | ✅ real (tested in `internal/trust/`) |
| Cost rollup via recursive CTE | ✅ real (tested in `internal/goaltasks/`) |
| Rich HTML report (goal / tree / agents / costs / timeline) | ✅ real |
| Secret encryption + scoped env injection | ✅ real (`internal/secrets/`, tested) |
| Coordinator driven by a real LLM | ❌ deferred — the demo's coordinator logic lives in Go (`cmd/docgardener/flow.go`); the LLM-in-the-loop path needs MCP tool wiring + reactor integration |
| Specialist subprocess runs via the harness | ❌ deferred — the demo produces synthetic artifacts |
| Full MCP tool surface (`create_goal`, `propose_task_tree`, `propose_agent`, `claim_task`, `verify_task`, `request_resource`, `list_resources`) | ❌ contracts live in `specs/018-dynamic-agent-spawning/contracts/mcp-tools.md`; wiring is deferred |
| Svelte `/goals` UI | ❌ deferred |

See `specs/018-dynamic-agent-spawning/tasks.md` for the full phase breakdown and what remains.

## Prereqs

- Go 1.25+
- `sqlite3`, `curl` on `$PATH`
- A free TCP port (default `18089`)

## Run it

```bash
./start.sh            # build + launch synapbus on port 18089
./run_task.sh         # execute the demo flow
./report.sh           # render report.html
./stop.sh             # shut down synapbus
```

`run_task.sh` can be re-run any number of times against a running instance — each invocation creates a new goal + task tree + reputation evidence, all appended to the ledger.

## What happens under the hood

`./run_task.sh` invokes `./bin/docgardener run` which:

1. **Bootstraps**: creates user `algis` (password `algis-demo-pw`), creates the `approvals` and `requests` channels, and materializes the pre-built coordinator agent (`doc-gardener-coordinator`) with its `config_hash` computed from its system prompt and tool scope.
2. **Creates a goal** via `goals.Service.CreateGoal` — slug `keep-docs-mcpproxy-app-accurate-against-source`, budget `$50.00`, `max_spawn_depth=3`. Auto-creates the `#goal-...` backing channel.
3. **Decomposes** the goal into a 4-node task tree (root + `scan-docs` + `verify-cli` + `drift-report` leaves) via `goaltasks.Service.CreateTree`, which denormalizes the full ancestry onto each child task in a single transaction.
4. **Spawns specialists** — three agents (`docs-scanner`, `cli-verifier`, `drift-reporter`), each one running through `trust.DelegationCap()` to verify its proposed grant does not exceed the coordinator's, then computing a deterministic `trust.ConfigHash(...)` and seeding its reputation ledger at **70 % of the parent's rolling score** via `trust.Ledger.SeedFromParent()`.
5. **Atomically claims tasks** — each specialist invokes `goaltasks.Service.Claim()` which runs the optimistic-lock `UPDATE ... WHERE assignee_agent_id IS NULL AND status='approved'` pattern. A concurrent-claim race test in `internal/goaltasks/service_test.go` verifies exactly-one-winner over 50 goroutine rounds.
6. **Runs specialists** — simulated for the v1 demo. Each task:
   - transitions `claimed → in_progress → awaiting_verification → done`
   - increments leaf spend (`tokens`, `dollars_cents`)
   - posts an artifact message (`#finding`, `#verified`, `#summary`) to the goal channel with `metadata.kind="artifact"`
   - appends a **positive evidence row** to the reputation ledger with `score_delta=+0.15` (auto verifier) or `+0.2` (command verifier)
7. **Marks the goal completed**.

`./report.sh` then invokes `./bin/docgardener report`, which:

1. reads the goal id from `.last_goal_id`
2. queries all tasks, agents, reputation, messages, billing codes for that goal
3. computes rolling reputation via `trust.Ledger.RollingScore()` (exponential decay, `half_life_days=30`)
4. builds a recursive task tree + a chronological timeline
5. renders `report.html.tmpl` into `report.html`
6. opens it in the default browser

## Inspect during / after the run

- **Web UI**: http://localhost:18089 — log in as `algis` / `algis-demo-pw`. The existing channels, messages, and agents views all work on the new data.
- **DB shell**:
  ```bash
  sqlite3 ./data/synapbus.db -header -column "
    SELECT id, title, status, spent_dollars_cents, assignee_agent_id FROM goal_tasks;
  "
  ```
- **Trust ledger**:
  ```bash
  sqlite3 ./data/synapbus.db -header -column "
    SELECT substr(config_hash,1,12) AS hash, score_delta, evidence_ref, created_at
      FROM reputation_evidence ORDER BY created_at;
  "
  ```
- **Cost rollup**:
  ```bash
  sqlite3 ./data/synapbus.db -header -column "
    SELECT COALESCE(billing_code,''), SUM(spent_tokens), SUM(spent_dollars_cents)
      FROM goal_tasks GROUP BY billing_code;
  "
  ```

## Expected HTML report

`report.html` contains six sections:

1. **Header** — goal title, status, budget, owner, backing channel
2. **Spend metrics** — total dollars / tokens / agents spawned
3. **Goal description**
4. **Task tree** — recursive, collapsible, status badges, per-task spend, verifier kind
5. **Spawned agents** — each with name, `config_hash` (first 12 chars), parent agent, spawn depth, autonomy tier, rolling reputation bar, tool-scope chips, truncated system prompt
6. **Cost breakdown by billing code** — per-code task count, tokens, dollars
7. **Artifacts posted by specialists** — the raw `#finding`, `#verified`, `#summary` messages
8. **Timeline** — every message in the goal channel, chronologically, annotated with actor and kind

Screenshot-equivalent output (minus images):

```
Doc-gardener run — Keep docs.mcpproxy.app accurate against source
Goal #4 · slug keep-docs-... · owner algis · backing channel #goal-... · [completed]

Spend         Tokens        Agents spawned
$1.05         6000          4

Task tree
├─ Verify docs.mcpproxy.app against source         [approved]
│  ├─ Scan docs for CLI flags                      [done] $0.45 · 1500 tok · auto
│  ├─ Verify flags exist in mcpproxy binary        [done] $0.25 · 2000 tok · auto
│  └─ Produce drift report                         [done] $0.35 · 2500 tok · command

Spawned agents
  • Doc-gardener Coordinator   config_hash 70a9a06e9595…  root · assisted · rep 80%
  • Docs Scanner               config_hash a0b5c6538b2d…  parent=coordinator · depth 1 · assisted · rep 58%
  • CLI Verifier               config_hash 47c6839eed73…  parent=coordinator · depth 1 · assisted · rep 58%
  • Drift Reporter             config_hash ceaa7816aa42…  parent=coordinator · depth 1 · assisted · rep 59%

Cost breakdown
  doc-gardener            1 task    0 tok  $0.00
  doc-gardener/report     1 task 2500 tok  $0.35
  doc-gardener/scan       1 task 1500 tok  $0.45
  doc-gardener/verify     1 task 2000 tok  $0.25
```

## Tests for the primitives

The feature ships with passing test suites for every critical invariant:

```bash
go test ./internal/goals/... ./internal/goaltasks/... ./internal/trust/... ./internal/secrets/...
```

- `internal/goaltasks/service_test.go`
  - `TestCreateTree_AncestryAndDepth` — recursive tree build with correct depth + ancestry
  - `TestCreateTree_AncestryOverflow` — 16 KB cap enforcement
  - `TestClaimAtomic_Race` — 50 rounds × 2 racing goroutines, exactly one winner per round
  - `TestRollupCosts` — recursive CTE over 4-level tree
  - `TestTransition_StateMachine` — legal and illegal transitions
- `internal/trust/config_hash_test.go` — determinism under shuffled inputs, sensitivity to capability changes
- `internal/trust/delegation_test.go` — full tier-matrix + tool-scope subset enforcement
- `internal/trust/ledger_test.go` — exponential decay, 70%-of-parent seed, clamping
- `internal/secrets/store_test.go` — NaCl roundtrip, scope precedence, name sanitization

## Troubleshooting

| Symptom | Fix |
|---|---|
| `./start.sh` fails at "admin socket never appeared" | Another instance on port 18089 — set `SYNAPBUS_PORT=18090 ./start.sh` |
| `./run_task.sh` fails with "DB not found" | `./start.sh` hasn't run — run it first |
| Report page is empty or missing sections | `.last_goal_id` is stale — rerun `./run_task.sh` then `./report.sh` |
| Stale binary | `rm -rf bin && ./start.sh` — forces rebuild |

## Next steps (out of scope for this MVP)

The spec at `specs/018-dynamic-agent-spawning/` lays out what comes after this demo, including the full MCP tool surface, reactor integration for real subprocess runs, the Svelte `/goals` page, the resource-request protocol, quarantine on low reputation, and the LLM-driven coordinator. This example establishes that the foundational primitives work; the follow-up work layers on top.
