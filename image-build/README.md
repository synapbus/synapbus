# SynapBus container images

The `docker` harness backend (`internal/harness/docker/`) runs each agent
inside an ephemeral container. This directory holds the canonical agent
image SynapBus's bundled examples reference.

## synapbus-agent

The default image. Debian bookworm-slim base with:

- `gemini` CLI (`@google/gemini-cli`)
- `claude` CLI (`@anthropic-ai/claude-code`)
- `tini` as PID 1 (signal forwarding + zombie reaping)
- Standard tooling the example wrappers use: `jq`, `sqlite3`, `curl`, `git`, `python3`
- Non-root `agent` user (uid 1000, gid 1000) matching the typical host user

No SynapBus binary lives in the image. Agents reach the SynapBus MCP
server on the host at `host.docker.internal:<port>` — the harness
rewrites `.gemini/settings.json` URLs from `127.0.0.1` to the gateway
hostname automatically.

### Build

Local single-arch:

```bash
docker build -t synapbus-agent:latest image-build/synapbus-agent
```

Multi-arch via buildx (recommended for sharing the image):

```bash
docker buildx build \
    --platform linux/amd64,linux/arm64 \
    -t synapbus-agent:latest \
    --load \
    image-build/synapbus-agent
```

Pin specific CLI versions with build args:

```bash
docker build \
    --build-arg GEMINI_CLI_VERSION=0.37.1 \
    --build-arg CLAUDE_CODE_VERSION=1.0.0 \
    -t synapbus-agent:0.37.1 \
    image-build/synapbus-agent
```

### Wire an agent to use it

In `harness_config_json` add a `docker` block:

```json
{
  "gemini_md": "...",
  "mcp_servers": [...],
  "env": {...},
  "docker": {
    "image": "synapbus-agent:latest",
    "memory": "1g",
    "cpus": "1.0",
    "network": "bridge"
  }
}
```

The reactor will pick the docker backend automatically when it sees the
`docker.image` field. Default security posture: `--cap-drop=ALL`,
`--security-opt=no-new-privileges`, `--read-only` root with tmpfs
`/tmp`, `--pids-limit=512`, `--user=<host uid:gid>`. Override via the
typed fields in the `docker` block (`memory`, `cpus`, `cap_add`,
`extra_mounts`, `read_only_root`, `user`).
