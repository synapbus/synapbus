# SynapBus on kubic

Plain Kubernetes manifests for the kubic single-node MicroK8s cluster
(`kubic.home.arpa`). No Helm — the image is built locally, imported directly
into MicroK8s containerd, and rolled with `kubectl set image`.

## Files

| File | Purpose |
|------|---------|
| `namespace.yaml` | `synapbus` namespace |
| `pvc.yaml` | 2 Gi PVC on `microk8s-hostpath` for `/data` (DB + WAL + attachments + HNSW index) |
| `secret.example.yaml` | Template for `synapbus-secrets` (OpenAI/Gemini keys, mounted via `envFrom`) |
| `deployment.yaml` | Single replica, `docker.io/library/synapbus:vX.Y.Z-amd64`, `imagePullPolicy: IfNotPresent` (image is pre-loaded into containerd) |
| `service.yaml` | NodePort 30088 on port 8080 |
| `otel-collector.yaml` | OpenTelemetry collector for traces/metrics |

## Initial install

```sh
kubectl apply -f deploy/kubic/namespace.yaml
kubectl apply -f deploy/kubic/pvc.yaml
# Edit secret.example.yaml first — never commit real keys.
kubectl apply -f deploy/kubic/secret.example.yaml
kubectl apply -f deploy/kubic/service.yaml
kubectl apply -f deploy/kubic/deployment.yaml
```

## Releasing a new version

```sh
scripts/deploy-kubic.sh v0.17.0
```

The script:

1. `docker buildx build --platform linux/amd64` with the version baked in.
2. `docker save` to a tarball.
3. `scp` to `kubic.home.arpa`.
4. `ssh kubic 'sudo microk8s ctr image import …'` (loads the image into the
   in-cluster containerd registry — the image is *not* pushed to a remote
   registry).
5. `kubectl set image deploy/synapbus synapbus=docker.io/library/synapbus:vX.Y.Z-amd64`.
6. `kubectl rollout status …` and a `/healthz` smoke test.

The `docker.io/library/` prefix is required because that's how containerd
resolves image references that don't specify a registry — `synapbus:v…`
written into the deployment is normalised to `docker.io/library/synapbus:v…`
on the node.

## Why no Helm?

The original chart under `deploy/helm/` (since deleted) was used for the very
first install (Mar 2026) and then went into a `failed` state when someone
ran `kubectl set image` for a hotfix; subsequent `helm upgrade` attempts hit
server-side-apply ownership conflicts. Rather than reconcile, we now own the
manifests directly. The deploy flow is simple enough that templating buys
nothing.

## Backups

Before any version that touches schema, snapshot `/data`:

```sh
kubectl exec -n synapbus deploy/synapbus -- \
  tar -C /data -cf - synapbus.db synapbus.db-shm synapbus.db-wal vapid_keys.json \
  | tar -xf - -C "$HOME/synapbus-backups/$(date -u +%Y%m%dT%H%M%SZ)/"
```

Then `sqlite3 synapbus.db 'PRAGMA wal_checkpoint(TRUNCATE); PRAGMA integrity_check;'`
to fold the WAL into the main file and verify integrity before archiving.
