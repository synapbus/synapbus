# Quickstart — 020-proactive-memory-dream-worker

How to build, run, and verify the proactive-memory + dream-worker feature locally and on the kubic deployment.

## Local: build & test

```bash
cd ~/repos/synapbus
make test                 # full test suite — should be green
go build -o ./synapbus ./cmd/synapbus
```

## Local: run with feature flags on

```bash
export SYNAPBUS_DATA_DIR=/tmp/synapbus-dream
export SYNAPBUS_INJECTION_ENABLED=1
export SYNAPBUS_DREAM_ENABLED=1
export SYNAPBUS_EMBEDDING_PROVIDER=openai
export OPENAI_API_KEY=<key>
mkdir -p $SYNAPBUS_DATA_DIR

./synapbus serve --port 8080 --data $SYNAPBUS_DATA_DIR
```

## Verify Story 1 (injection)

```bash
# Create an agent and an owner
./synapbus --socket $SYNAPBUS_DATA_DIR/synapbus.sock agents create \
  --name dogfood-1 --owner algis --description "smoke test agent"

# Get an API key for the agent
KEY=$(./synapbus --socket $SYNAPBUS_DATA_DIR/synapbus.sock apikeys create \
  --agent dogfood-1 --json | jq -r .key)

# Seed memory channel with a few messages
./synapbus --socket $SYNAPBUS_DATA_DIR/synapbus.sock channels create \
  --name open-brain --type blackboard
./synapbus --socket $SYNAPBUS_DATA_DIR/synapbus.sock messages send \
  --agent dogfood-1 --channel open-brain \
  --body "KuzuDB was archived 2025-10-10. Not viable for SynapBus."

# Call my_status via MCP — relevant_context should appear
curl -s -X POST http://localhost:8080/mcp \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"my_status","arguments":{}}}' \
  | jq '.result.content[0].text | fromjson | .relevant_context'
```

Expected: `relevant_context.memories` is non-empty, contains the seeded message, `core_memory` is null/omitted (none set yet).

Set a core memory and re-run:

```bash
# Direct DB or admin CLI:
sqlite3 $SYNAPBUS_DATA_DIR/synapbus.db \
  "INSERT INTO memory_core(owner_id, agent_name, blob, updated_by)
   VALUES('algis', 'dogfood-1', 'You are a dogfood test agent.', 'human:algis');"

# Re-call my_status — relevant_context.core_memory should now be populated
```

## Verify Story 3 (dream worker manual dispatch)

```bash
# Force a reflection job for owner=algis
./synapbus --socket $SYNAPBUS_DATA_DIR/synapbus.sock memory dream-run \
  --owner algis --job reflection

# Tail logs for the worker
# Look for:
#   component=consolidator-worker job=reflection owner=algis status=dispatched
#   component=consolidator-worker job=reflection owner=algis status=succeeded
sqlite3 $SYNAPBUS_DATA_DIR/synapbus.db \
  "SELECT id, job_type, status, summary, finished_at
   FROM memory_consolidation_jobs ORDER BY id DESC LIMIT 5;"
```

Expected: one new row in `memory_consolidation_jobs` with `status=succeeded` (or `partial` if seed pool too small). For a reflection job, expect 0–5 new `body LIKE 'REFLECTION:%'` messages in `#open-brain`.

## Verify token isolation (SC-008)

```bash
# Create a second owner + agent
./synapbus --socket ... agents create --name attacker-1 --owner mallory ...
KEY2=$(./synapbus --socket ... apikeys create --agent attacker-1 --json | jq -r .key)

# Attacker calls search — must NOT see algis's memories
curl -s -X POST http://localhost:8080/mcp -H "Authorization: Bearer $KEY2" ... \
  | jq '.result.content[0].text | fromjson | .relevant_context.memories'
```

Expected: empty array. If anything from algis's pool surfaces, that is a Constitution IV violation.

## kubic: build, push, deploy

```bash
# Build linux/amd64 from darwin/arm64 — no CGO so this just works
cd ~/repos/synapbus
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ./synapbus-linux-amd64 ./cmd/synapbus

# Build container (existing Dockerfile)
docker build --platform=linux/amd64 -t synapbus:020-proactive-memory .

# Push to the cluster's local registry (kubic uses microk8s registry on :32000)
docker tag synapbus:020-proactive-memory kubic.home.arpa:32000/synapbus:020-proactive-memory
docker push kubic.home.arpa:32000/synapbus:020-proactive-memory

# Update the deployment image
kubectl -n synapbus set image deploy/synapbus \
  synapbus=kubic.home.arpa:32000/synapbus:020-proactive-memory

# Enable feature flags via env (kubectl set env, or edit values in deploy/kubic-manifests/)
kubectl -n synapbus set env deploy/synapbus \
  SYNAPBUS_INJECTION_ENABLED=1 SYNAPBUS_DREAM_ENABLED=1

kubectl -n synapbus rollout status deploy/synapbus
```

## kubic: verify dream agent works

```bash
# Tail logs filtered to the new worker
kubectl -n synapbus logs deploy/synapbus -f | grep -E "consolidator-worker|memory-injection"
```

Expected log lines on startup:
```
{"component":"consolidator-worker","msg":"consolidator worker started","interval":"1h","deep_cron":"0 3 * * *"}
```

Expected after first watermark / cron tick:
```
{"component":"consolidator-worker","msg":"trigger fired","owner":"algis","job":"reflection","trigger":"watermark:20"}
{"component":"consolidator-worker","msg":"dispatched","owner":"algis","job":"reflection","harness_run_id":"...","dispatch_token":"redacted"}
{"component":"consolidator-worker","msg":"job completed","owner":"algis","job":"reflection","status":"succeeded","actions":3,"duration_ms":42184}
```

Force a job to test without waiting:
```bash
kubectl exec -n synapbus deploy/synapbus -- /synapbus --socket /data/synapbus.sock \
  memory dream-run --owner algis --job reflection
```

Inspect the audit log:
```bash
kubectl exec -n synapbus deploy/synapbus -- sqlite3 /data/synapbus.db \
  "SELECT id, job_type, status, json_array_length(actions) AS n_actions, summary
   FROM memory_consolidation_jobs ORDER BY id DESC LIMIT 10;"
```

## Rollback

```bash
kubectl -n synapbus set env deploy/synapbus SYNAPBUS_INJECTION_ENABLED- SYNAPBUS_DREAM_ENABLED-
# Or roll back the image:
kubectl -n synapbus rollout undo deploy/synapbus
```

The migration `028_memory_consolidation.sql` is additive — rolling the binary back leaves the new tables in place but unused. Safe.
