package docker_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/harness/docker"
	"github.com/synapbus/synapbus/internal/messaging"
)

// TestExecute_Hello is the smoke test for the docker backend. Skipped
// when the docker daemon isn't reachable so CI without docker won't
// fail. Builds nothing — uses `alpine:3.20` which is small and
// universally available.
func TestExecute_Hello(t *testing.T) {
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		t.Skip("docker daemon not available, skipping")
	}

	base := t.TempDir()
	h := docker.New(docker.Config{
		BaseDir:              base,
		KeepWorkdirOnSuccess: true,
		HostMCPPort:          0, // skip URL rewrite for this test
	}, nil)

	// Minimal agent with a docker block. wrapper.sh writes a marker
	// file to /workspace and prints a known string so we can assert
	// both bind-mount writeback and stdout capture.
	cfgJSON, err := json.Marshal(map[string]any{
		"env": map[string]string{
			"GREETING": "hello-from-container",
		},
		"docker": map[string]any{
			"image":      "alpine:3.20",
			"command":    []string{"sh", "/workspace/wrapper.sh"},
			"network":    "none", // air-gapped; we don't need MCP for this test
			"memory":     "128m",
			"cpus":       "0.5",
			"pids_limit": 32,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	agent := &agents.Agent{
		ID:                1,
		Name:              "smoke-test-agent",
		HarnessConfigJSON: string(cfgJSON),
	}

	req := &harness.ExecRequest{
		RunID:     "smoke-test-1",
		AgentName: agent.Name,
		Agent:     agent,
		Message: &messaging.Message{
			ID:        42,
			FromAgent: "tester",
			ToAgent:   agent.Name,
			Body:      "hello",
		},
		Budget: harness.Budget{
			MaxWallClock: 60 * time.Second,
		},
	}

	// We need a wrapper.sh staged BEFORE Execute creates the container.
	// In production the harness materializes one via the gemini_md /
	// claude_md fields, but for the test we drop a tiny shell script
	// directly into the run workdir.
	runDir := filepath.Join(base, "smoke-test-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := `#!/bin/sh
set -eu
echo "wrapper running as uid=$(id -u) gid=$(id -g) cwd=$(pwd)"
echo "GREETING=$GREETING"
echo "SYNAPBUS_RUN_ID=$SYNAPBUS_RUN_ID"
echo "SYNAPBUS_FROM_AGENT=$SYNAPBUS_FROM_AGENT"
[ -f /workspace/message.json ] && echo "message.json present"
echo '{"ok":true,"phase":"smoke"}' > /workspace/result.json
echo "wrapper done"
`
	if err := os.WriteFile(filepath.Join(runDir, "wrapper.sh"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned error: %v\nlogs:\n%s", err, func() string {
			if res != nil {
				return res.Logs
			}
			return ""
		}())
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nlogs:\n%s", res.ExitCode, res.Logs)
	}

	// Check stdout capture.
	if !strings.Contains(res.Logs, "wrapper done") {
		t.Errorf("stdout missing 'wrapper done':\n%s", res.Logs)
	}
	if !strings.Contains(res.Logs, "GREETING=hello-from-container") {
		t.Errorf("env injection missing GREETING:\n%s", res.Logs)
	}
	if !strings.Contains(res.Logs, "SYNAPBUS_RUN_ID=smoke-test-1") {
		t.Errorf("SYNAPBUS_RUN_ID not propagated:\n%s", res.Logs)
	}
	if !strings.Contains(res.Logs, "message.json present") {
		t.Errorf("message.json not bind-mounted:\n%s", res.Logs)
	}

	// Check result.json bind-mount writeback.
	if len(res.ResultJSON) == 0 {
		t.Error("result.json was not captured from bind-mount")
	} else {
		var parsed map[string]any
		if err := json.Unmarshal(res.ResultJSON, &parsed); err != nil {
			t.Errorf("result.json invalid: %v", err)
		} else if parsed["ok"] != true {
			t.Errorf("result.json content unexpected: %v", parsed)
		}
	}

	// Workdir should still exist (KeepWorkdirOnSuccess=true). Verify
	// the result.json the container wrote actually landed on the host.
	hostResult, err := os.ReadFile(filepath.Join(runDir, "result.json"))
	if err != nil {
		t.Errorf("result.json not on host post-run: %v", err)
	} else if !strings.Contains(string(hostResult), "smoke") {
		t.Errorf("host result.json content unexpected: %s", hostResult)
	}
}

// TestExecute_NoImage verifies the backend rejects agents whose
// harness_config_json lacks docker.image rather than silently picking
// some default.
func TestExecute_NoImage(t *testing.T) {
	h := docker.New(docker.Config{}, nil)
	agent := &agents.Agent{
		Name:              "no-image",
		HarnessConfigJSON: `{"docker":{}}`,
	}
	req := &harness.ExecRequest{
		RunID:     "noimg",
		AgentName: agent.Name,
		Agent:     agent,
	}
	_, err := h.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing image, got nil")
	}
	if !strings.Contains(err.Error(), "image is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestExecute_TimeoutCancel verifies the wall-clock budget kills a
// long-running container.
func TestExecute_TimeoutCancel(t *testing.T) {
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		t.Skip("docker daemon not available, skipping")
	}

	base := t.TempDir()
	h := docker.New(docker.Config{BaseDir: base, KeepWorkdirOnSuccess: true}, nil)

	cfgJSON, _ := json.Marshal(map[string]any{
		"docker": map[string]any{
			"image":   "alpine:3.20",
			"command": []string{"sh", "/workspace/wrapper.sh"},
			"network": "none",
			"memory":  "64m",
		},
	})
	agent := &agents.Agent{
		Name:              "slow",
		HarnessConfigJSON: string(cfgJSON),
	}
	runDir := filepath.Join(base, "slow")
	_ = os.MkdirAll(runDir, 0o755)
	_ = os.WriteFile(filepath.Join(runDir, "wrapper.sh"),
		[]byte("#!/bin/sh\nsleep 30\n"), 0o755)

	req := &harness.ExecRequest{
		RunID:     "slow",
		AgentName: agent.Name,
		Agent:     agent,
		Budget:    harness.Budget{MaxWallClock: 2 * time.Second},
	}
	start := time.Now()
	res, err := h.Execute(context.Background(), req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected timeout error, got nil (exit=%d)", res.ExitCode)
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Errorf("unexpected error: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Errorf("timeout took too long: %s", elapsed)
	}
}
