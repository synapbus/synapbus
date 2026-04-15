# synapbus-agent: the canonical container image for SynapBus's docker
# harness backend. One image, all the agent CLIs the bundled examples
# invoke (gemini, claude — add codex/opencode here when needed). No
# SynapBus binary inside — agents reach the host's MCP server over the
# network at host.docker.internal:<port>.
#
# Build with multi-arch buildx:
#   docker buildx build \
#     --platform linux/amd64,linux/arm64 \
#     -t synapbus-agent:latest \
#     --load image-build/synapbus-agent
#
# Or for local dev (single-arch matching your host):
#   docker build -t synapbus-agent:latest image-build/synapbus-agent

FROM debian:bookworm-slim

ARG NODE_MAJOR=22
ARG GEMINI_CLI_VERSION=latest
ARG CLAUDE_CODE_VERSION=latest

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8

# Base tooling. Most demos shell out to one of these from wrapper.sh.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        gnupg \
        jq \
        sqlite3 \
        python3 \
        python3-pip \
        tini \
    && rm -rf /var/lib/apt/lists/*

# Node.js for the agent CLIs (gemini, claude). NodeSource keeps a
# pinned major version so the image is reproducible-ish across builds.
RUN curl -fsSL https://deb.nodesource.com/setup_${NODE_MAJOR}.x | bash - \
    && apt-get update && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/* \
    && npm config set update-notifier false

# Agent CLIs. Install globally so any user inside the container can
# call them. Pinned versions are accepted via build args above.
RUN npm install -g \
        @google/gemini-cli@${GEMINI_CLI_VERSION} \
        @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}

# Non-root user with UID/GID 1000 — matches the typical host user on
# Linux dev machines and lets `docker run --user 1000:1000` (which the
# harness sets by default) write into bind-mounted workdirs without
# permission errors.
RUN groupadd -g 1000 agent && useradd -u 1000 -g 1000 -m -s /bin/bash agent

# Use tini as PID 1 so:
#   * SIGTERM from `docker stop` reaches our wrapper.sh
#   * Zombie node/python child processes get reaped properly
# Wrappers can override the entrypoint via harness_config_json.docker.
ENTRYPOINT ["/usr/bin/tini", "--"]

# Default: run /workspace/wrapper.sh (the convention every example
# follows). Overridden via harness_config_json.docker.command when
# you want a different entry script.
WORKDIR /workspace
USER 1000:1000
CMD ["/workspace/wrapper.sh"]
