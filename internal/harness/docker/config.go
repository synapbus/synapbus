// Package docker is the container-isolation implementation of
// harness.Harness. Each Execute call materializes the agent's per-run
// workdir on the host (CLAUDE.md / GEMINI.md / .mcp.json / message.json
// — same layout as the subprocess backend), then runs an ephemeral
// `docker run --rm` with that workdir bind-mounted at /workspace.
//
// Inspired by scion's pkg/runtime/docker.go: per-task ephemeral
// containers, host-side scratch dir, secret/env injection via -e flags,
// shell-out to the docker CLI (no Docker SDK dependency, zero CGO,
// trivial cross-compile).
package docker

import (
	"encoding/json"
	"fmt"
)

// Config tunes the docker harness at process-startup time. Per-agent
// overrides go into the agent's harness_config_json (parsed by
// ParseDockerConfig below).
type Config struct {
	// BaseDir is the parent directory under which a per-run workdir is
	// created on the host. The host writes config files here and bind-
	// mounts the directory at /workspace inside the container.
	BaseDir string

	// LogsCap bounds the number of bytes kept in ExecResult.Logs. The
	// full stdout/stderr stream is written to stdout.log / stderr.log
	// inside the workdir for forensics.
	LogsCap int

	// KeepWorkdirOnSuccess leaves the workdir behind even for zero-exit
	// runs. Useful when debugging MCP traces or container exit codes.
	KeepWorkdirOnSuccess bool

	// HostGatewayName is the hostname the agent inside the container
	// uses to reach the SynapBus MCP server on the host. Defaults to
	// "host.docker.internal" which works on Docker Desktop (mac/win)
	// natively and on Linux when --add-host=host.docker.internal:
	// host-gateway is supplied (we add it automatically).
	HostGatewayName string

	// HostMCPPort is the port SynapBus listens on. Used to rewrite the
	// .gemini/settings.json materialized by the harness so MCP URLs
	// like http://127.0.0.1:18090/mcp become
	// http://host.docker.internal:18090/mcp inside the container.
	// When 0 the harness leaves the URL alone (the agent prompt may
	// reference an externally addressable URL already).
	HostMCPPort int

	// DockerBin is the path to the docker CLI. Empty = "docker" from
	// PATH. Override for podman or a wrapper script.
	DockerBin string
}

// AgentConfig is the per-agent docker block parsed from
// harness_config_json. Fields named alongside the existing subprocess
// AgentConfig so the same JSON file can carry both backends:
//
//	{
//	  "gemini_md": "...",
//	  "mcp_servers": [...],
//	  "env": {...},
//	  "docker": {
//	    "image": "synapbus-agent:latest",
//	    "memory": "1g",
//	    "cpus": "1.0",
//	    "network": "bridge",
//	    "extra_mounts": [{"source": "/host/path", "target": "/in/container", "read_only": true}],
//	    "cap_add": [],
//	    "extra_args": []
//	  }
//	}
type AgentConfig struct {
	// Image is the container image to run. Required. May be a local tag
	// ("synapbus-agent:latest") or a fully-qualified registry path.
	Image string `json:"image"`

	// Memory is the --memory limit, e.g. "1g", "512m". Empty = no limit.
	Memory string `json:"memory,omitempty"`

	// CPUs is the --cpus quota, e.g. "1.0", "0.5". Empty = no limit.
	CPUs string `json:"cpus,omitempty"`

	// PIDsLimit is --pids-limit. Defaults to 512 when zero.
	PIDsLimit int `json:"pids_limit,omitempty"`

	// Network is the --network mode. Empty defaults to "bridge". Use
	// "none" for fully air-gapped runs.
	Network string `json:"network,omitempty"`

	// ExtraMounts is a list of additional host bind-mounts. The
	// per-run workdir is always mounted at /workspace; this is for
	// extra read-only resources like CA bundles or shared caches.
	ExtraMounts []ExtraMount `json:"extra_mounts,omitempty"`

	// CapAdd is the list of Linux capabilities to grant on top of the
	// default --cap-drop=ALL. Most agents need none.
	CapAdd []string `json:"cap_add,omitempty"`

	// ReadOnlyRoot makes the container's root filesystem read-only.
	// Defaults to true. The harness always tmpfs-mounts /tmp so the
	// agent has a writable scratch dir.
	ReadOnlyRoot *bool `json:"read_only_root,omitempty"`

	// User is the --user flag value, e.g. "1000:1000". Empty leaves the
	// container's default user. Set explicitly when the host has
	// permission constraints on the bind-mounted workdir.
	User string `json:"user,omitempty"`

	// Entrypoint overrides the image ENTRYPOINT. Empty leaves it alone.
	// The harness always passes /workspace/wrapper.sh as the first arg
	// after entrypoint, so the image's ENTRYPOINT must accept a script
	// path (e.g. ["/usr/bin/dumb-init", "--"] then args become argv[1:]).
	Entrypoint []string `json:"entrypoint,omitempty"`

	// Command overrides what the harness passes after the entrypoint.
	// Defaults to ["/workspace/wrapper.sh"] — the convention every
	// example in this repo follows.
	Command []string `json:"command,omitempty"`

	// ExtraArgs are passed verbatim to `docker run` between the
	// security flags and the image name. Use sparingly; prefer the
	// typed fields above.
	ExtraArgs []string `json:"extra_args,omitempty"`
}

// ExtraMount describes one additional bind-mount.
type ExtraMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// ParseDockerConfig pulls the `docker` block out of the agent's
// harness_config_json. Tolerates an absent block (returns zero value
// + ErrNoDockerConfig so callers can decide whether to error or
// fall through).
func ParseDockerConfig(raw string) (AgentConfig, error) {
	var cfg AgentConfig
	if raw == "" {
		return cfg, ErrNoDockerConfig
	}
	var envelope struct {
		Docker *AgentConfig `json:"docker"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return cfg, fmt.Errorf("docker: parse harness_config_json: %w", err)
	}
	if envelope.Docker == nil {
		return cfg, ErrNoDockerConfig
	}
	return *envelope.Docker, nil
}

// ErrNoDockerConfig signals that the agent's harness_config_json had no
// `docker` block. The Registry uses this to fall through to a different
// backend rather than failing the dispatch.
var ErrNoDockerConfig = fmt.Errorf("docker: agent has no docker config block")
