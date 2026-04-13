# cold-topic-explainer

Toy multi-agent task that exercises the subprocess harness end-to-end.
Three Gemini agents on different models collaborate via SynapBus DMs
to produce a 3-paragraph explainer for a topic, with a
writer ↔ critic refinement loop.

## Roles

| Agent             | Model                  | Job |
|-------------------|------------------------|---|
| `decomposer-pro`  | `gemini-2.5-pro`       | Receives the topic, splits it into what / why / how, DMs `writer-flash` |
| `writer-flash`    | `gemini-2.5-flash`     | Drafts (or revises) the 3-paragraph explainer, DMs `critic-lite` |
| `critic-lite`     | `gemini-2.5-flash-lite` | Rates each paragraph 1–10. Scores all ≥ 8 → DMs `algis` with `FINAL:`. Else DMs `writer-flash` with `REVISE:` and specific fixes |

This exercises:

- **Decomposition** — `decomposer-pro` splits one request into 3 sub-questions
- **Delegation** — each agent DMs the next, routed by the SynapBus reactor
- **Recursive update** — the writer↔critic loop runs until convergence or
  `max_trigger_depth` fires (default 6, giving ~3 full refinement rounds)

Every hop is a subprocess reactive run, subject to the same depth /
budget / cooldown guards as a K8s reactive run. Each hop writes a
`harness_runs` row with usage, cost, duration, and trace id.

## Prereqs

- `gemini` CLI installed and authenticated (`gemini auth login` done once)
- Go 1.25+
- `jq`, `curl`, `sqlite3` available on PATH
- An unused TCP port (default 18088)

## Run it

```bash
./start.sh
./run_task.sh "how does the SynapBus reactor's pending_work flag coalesce bursts of DMs?"
./stop.sh
```

## What happens

- `start.sh` builds `synapbus` from the current checkout, launches a
  separate instance on port **18088** with a local `./data` directory,
  creates user `algis` (password `algis`), creates three AI agents, and
  configures each agent's `harness_config_json` with GEMINI.md, MCP
  pointer, role env, and the wrapper script invocation.
- `run_task.sh` kicks off the chain by sending an initial DM from
  `algis` to `decomposer-pro` via the admin socket, then polls for a
  DM **to** `algis` whose body starts with `FINAL:`. Prints the body
  when it arrives (or gives up after 4 min).
- `stop.sh` signals the synapbus PID and waits for it to exit
  cleanly.

## View during the run

- **Web UI**: <http://localhost:18088> — log in as `algis` / `algis-demo-pw`
- **Agent detail** (see Harness panel + traces):
  - <http://localhost:18088/agents/decomposer-pro>
  - <http://localhost:18088/agents/writer-flash>
  - <http://localhost:18088/agents/critic-lite>
- **Live slog JSON**: `tail -f synapbus.log | jq -c 'select(.component=="reactor" or .harness)'`
- **All DMs in order**: `./bin/synapbus --socket ./data/synapbus.sock messages list --limit 50`
- **Harness runs**: `sqlite3 ./data/synapbus.db 'SELECT run_id, agent_name, backend, status, duration_ms, tokens_in, tokens_out, cost_usd FROM harness_runs ORDER BY id'`

### OpenTelemetry

Off by default. To ship spans to a collector while you run the task:

```bash
SYNAPBUS_OTEL_ENABLED=1 SYNAPBUS_OTEL_ENDPOINT=otel-collector.synapbus.svc.cluster.local:4318 ./start.sh
```

Or stand up a local collector first using `deploy/kubic/otel-collector.yaml`.
Without a collector, the same information is available in `synapbus.log`
as slog JSON and in the `harness_runs` table.

## Cost

Rough cost per successful run, assuming 2 writer-critic iterations:

| Hops | Model         | Cost |
|------|---------------|------|
| 1    | gemini-2.5-pro | ~$0.01 |
| 2    | gemini-2.5-flash | ~$0.01 |
| 3    | gemini-2.5-flash-lite | ~$0.002 |
| **Total** |           | ~$0.02 |

The daily trigger budget per agent is capped at 20 (see `start.sh`) so
this example cannot accidentally spend more than pennies per day even
if the reactor loops on a bug.

## Files

- `start.sh` — launch separate synapbus + configure agents
- `run_task.sh` — kickoff DM + poll for final
- `stop.sh` — graceful shutdown
- `wrapper.sh` — shell wrapper used as the agents' `local_command`;
  reads `message.json`, calls `gemini`, routes the result back via the
  admin socket
- `configs/*.json` — per-agent `harness_config_json` blobs
