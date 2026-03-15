# Quickstart: Admin CLI & Docker Fixes

**Feature**: 006-admin-cli-docker-fixes

## Build & Deploy

```bash
# Build Docker image (now uses alpine base)
docker build -t synapbus:dev .

# Run locally
./synapbus serve --port 8080 --data ./data
```

## New CLI Commands

### Create a channel
```bash
# With description
synapbus channels create --name news-feed --description "News feed channel"

# Without description
synapbus channels create --name alerts
```

### Join an agent to a channel
```bash
synapbus channels join --channel news-feed --agent research-mcpproxy
```

### In Kubernetes
```bash
# Now works because alpine base image provides /bin/sh
kubectl exec -n synapbus deploy/synapbus -- /synapbus channels create --name news-feed
kubectl exec -n synapbus deploy/synapbus -- /synapbus channels join --channel news-feed --agent my-agent
kubectl exec -n synapbus deploy/synapbus -- /synapbus channels list
```

## Socket Path

The default socket path is now `/data/synapbus.sock` (absolute). Override with:

```bash
# Environment variable
export SYNAPBUS_SOCKET=/custom/path/synapbus.sock

# CLI flag
synapbus --socket /custom/path/synapbus.sock channels list
```

## Verification

```bash
# Verify Docker image base
docker run --rm synapbus:dev sh -c "cat /etc/os-release"
# Should show Alpine Linux

# Verify socket path default
synapbus --help | grep socket
# Should show: --socket string   Path to admin Unix socket (default "/data/synapbus.sock")
```
