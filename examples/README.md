# SynapBus examples

Runnable demos of SynapBus features. Each example is self-contained under its own directory, launches an isolated synapbus instance on a distinct port, and cleans up after itself.

| Example | Feature | Real LLM? | Port |
|---|---|---|---|
| [`cold-topic-explainer/`](./cold-topic-explainer/) | Reactive agent triggers + subprocess harness — three Gemini agents (decomposer → writer → critic) collaborate via DMs to produce a 3-paragraph explainer, with real LLM calls end-to-end. | ✅ yes (`gemini` CLI) | 18088 |
| [`doc-gardener/`](./doc-gardener/) | Dynamic agent spawning (spec 018) — a coordinator meta-agent decomposes a goal into a task tree, spawns specialists with `config_hash`-rooted trust + delegation-cap enforcement, runs them through the state machine, generates a rich HTML report. | ❌ v1 is synthetic (primitives demo); real LLM coordinator is a follow-up PR | 18089 |

## Quick start

Pick an example, `cd` into it, and follow its README. In general:

```bash
cd examples/<name>
./start.sh        # rebuild + launch an isolated synapbus instance
./run_task.sh     # drive the demo flow
./report.sh       # (where applicable) render an HTML report
./stop.sh         # shut down
```

Both examples use the same layout for consistency:

```
examples/<name>/
├── start.sh             # build & launch
├── run_task.sh          # execute the demo flow
├── stop.sh              # shut down
├── report.sh            # (doc-gardener only) render HTML report
├── bin/
│   ├── synapbus         # built from the current checkout
│   └── <helper>         # example-specific driver binary
├── configs/             # per-agent JSON configs (harness_config, prompts, etc.)
├── data/                # isolated SQLite DB + attachment store + sockets
├── synapbus.log         # server stdout+stderr
└── README.md            # example-specific docs
```

## What each example proves

- **cold-topic-explainer** proves that the SynapBus reactor + subprocess harness can drive a real multi-agent loop with three distinct LLMs, with depth and budget guards, OpenTelemetry tracing, and harness_runs accounting.
- **doc-gardener** proves that the dynamic-agent-spawning data primitives — `goals`, `goal_tasks` with denormalized ancestry, atomic optimistic-lock claim, `config_hash`-keyed reputation ledger, delegation-cap enforcement, per-billing-code cost rollup — work end-to-end against real SQLite, and feed a rich HTML report.

The two examples are complementary: cold-topic-explainer exercises the **runtime path** (reactor → harness → LLM → DMs), doc-gardener exercises the **work-tracking path** (goals → tasks → trust → report). A future example will combine them into a full LLM-driven coordinator loop.

## Global prereqs

- Go 1.25+
- `sqlite3`, `curl`, `jq` on `$PATH`
- A free TCP port per example (see table above)
- For `cold-topic-explainer` only: `gemini` CLI authenticated via `gemini auth login`

## Troubleshooting

- Port already in use: set `SYNAPBUS_PORT=18090 ./start.sh` (each example honors the env var).
- Web UI is blank: rebuild the embedded Svelte SPA with `make web` from the repo root once, then re-run `./start.sh`.
- Stale binary: delete the example's `bin/` directory and rerun `./start.sh` to force a rebuild.
