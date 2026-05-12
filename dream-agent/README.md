# synapbus-dream-agent

A slim Python container that performs **memory consolidation** for
SynapBus, dispatched on demand by the in-server `ConsolidatorWorker`
via the `k8sjob` harness backend.

## What it does

1. Reads its job context from env vars (dispatch token, job id, job
   type, owner id, prompt).
2. Connects to SynapBus's MCP endpoint over streamable-http, passing
   the agent's API key (`Authorization: Bearer ...`) **and** the
   dispatch token (`X-Synapbus-Dispatch-Token: ...`) on every request.
3. Runs Claude Code (via `claude-agent-sdk`) constrained to the six
   `memory_*` MCP tools defined in
   `specs/020-proactive-memory-dream-worker/contracts/mcp-memory-tools.md`.
4. Streams structured JSON logs to stdout (Loki-friendly) and emits a
   final `{"final": true, ...}` envelope so the harness can parse Usage.

## How the worker invokes it

`internal/messaging/consolidator.go` builds an `HarnessExecRequest`
with:

| Env var                         | Set by               |
|---------------------------------|----------------------|
| `SYNAPBUS_DISPATCH_TOKEN`       | ConsolidatorWorker   |
| `SYNAPBUS_CONSOLIDATION_JOB_ID` | ConsolidatorWorker   |
| `SYNAPBUS_JOB_TYPE`             | ConsolidatorWorker   |
| `SYNAPBUS_OWNER_ID`             | ConsolidatorWorker   |
| `SYNAPBUS_DREAM_PROMPT`         | ConsolidatorWorker   |
| `SYNAPBUS_RUN_ID`               | k8sjob harness       |
| `SYNAPBUS_URL`, `SYNAPBUS_API_KEY`, `ANTHROPIC_API_KEY` | Pod spec / Secret |

## Build

```bash
docker buildx build --platform=linux/amd64 \
  -t kubic.home.arpa:32000/synapbus-dream-agent:v0.1.0 \
  --load /Users/user/repos/synapbus/dream-agent/
```

Push:

```bash
docker push kubic.home.arpa:32000/synapbus-dream-agent:v0.1.0
```

## Local smoke test

The `--mock` flag validates the env contract and exits without
invoking the SDK or hitting the network:

```bash
SYNAPBUS_URL=http://localhost:8080 \
SYNAPBUS_API_KEY=fake \
SYNAPBUS_DISPATCH_TOKEN=fake \
SYNAPBUS_CONSOLIDATION_JOB_ID=1 \
SYNAPBUS_JOB_TYPE=reflection \
SYNAPBUS_OWNER_ID=algis \
SYNAPBUS_DREAM_PROMPT="test" \
SYNAPBUS_RUN_ID=r-test \
python3 dream_runner.py --mock
```

## Deploy

See `k8s-job-template.yaml`. The harness clones the template and
overlays `req.Env` into `containers[0].env`.
