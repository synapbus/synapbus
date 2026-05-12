# SynapBus dream-agent — slim Python container that runs Claude Code via
# claude-agent-sdk against SynapBus's MCP server. Built for linux/amd64.
#
# The proven recipe (per ~/repos/searcher/agents/universal/Dockerfile)
# is a single-stage image with `uv pip install --system`. Multi-stage
# saves little since claude-agent-sdk transitively pulls anyio/httpx,
# and the heavy bit (the `claude` CLI binary) ships inside the wheel as
# a JS bundle.
FROM python:3.12-slim

# Bring in `uv` from its official image. Pure binary, no apt.
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

# System deps: git (claude-agent-sdk shells out for some workspace ops),
# ca-certs + curl for TLS / health checks. Cleanup apt lists.
RUN apt-get update && \
    apt-get install -y --no-install-recommends git ca-certificates curl && \
    apt-get clean && rm -rf /var/lib/apt/lists/* && \
    git config --global user.email "dream-agent@synapbus.dev" && \
    git config --global user.name "SynapBus Dream Agent"

WORKDIR /app

# Pinned versions — keep aligned with pyproject.toml. claude-agent-sdk
# 0.1.48 bundles the `claude` CLI Node binary inside its wheel, so no
# separate `claude-code` install step is required.
RUN uv pip install --system --no-cache \
    "claude-agent-sdk==0.1.48" \
    "httpx>=0.27" \
    "opentelemetry-api>=1.27" \
    "opentelemetry-sdk>=1.27" \
    "opentelemetry-exporter-otlp-proto-http>=1.27"

COPY dream_runner.py /app/dream_runner.py

# Non-root user (matches searcher convention)
RUN groupadd -g 1000 dream && useradd -u 1000 -g 1000 -m dream && \
    chown -R dream:dream /app
USER dream

ENTRYPOINT ["python", "/app/dream_runner.py"]
