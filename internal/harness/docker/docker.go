package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/harness/subprocess"
	"github.com/synapbus/synapbus/internal/messaging"
)

// Harness runs agents inside ephemeral Docker containers. One container
// per Execute call, bind-mounted workdir, --rm cleanup, no warm pool.
type Harness struct {
	cfg    Config
	logger *slog.Logger
}

// New builds a docker harness with sensible defaults.
func New(cfg Config, logger *slog.Logger) *Harness {
	if cfg.LogsCap <= 0 {
		cfg.LogsCap = 64 * 1024
	}
	if cfg.HostGatewayName == "" {
		cfg.HostGatewayName = "host.docker.internal"
	}
	if cfg.DockerBin == "" {
		cfg.DockerBin = "docker"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Harness{
		cfg:    cfg,
		logger: logger.With("harness", "docker"),
	}
}

// Name is the registered backend identifier. Match it in agent rows via
// harness_name = "docker".
func (h *Harness) Name() string { return "docker" }

// Capabilities mirrors subprocess: same workdir convention, same MCP
// support, same OTel env-var injection. Skills aren't materialized
// today (subprocess doesn't either).
func (h *Harness) Capabilities() harness.Capabilities {
	return harness.Capabilities{
		SystemPrompt:   true,
		SessionResume:  true,
		Skills:         false,
		OTelNative:     true,
		MaxConcurrency: 4,
	}
}

// TestEnvironment runs `docker version --format {{.Server.Version}}`
// and fails fast if the daemon isn't reachable.
func (h *Harness) TestEnvironment(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, h.cfg.DockerBin, "version", "--format", "{{.Server.Version}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker: daemon unreachable: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Provision is a no-op. Image pulls happen lazily on the first Execute
// (docker run will pull missing images automatically).
func (h *Harness) Provision(ctx context.Context, agent *agents.Agent) error { return nil }

// Cancel asks the docker daemon to kill the container for runID.
// Best-effort: returns nil even if no container exists.
func (h *Harness) Cancel(ctx context.Context, runID string) error {
	name := containerName(runID)
	_ = exec.CommandContext(ctx, h.cfg.DockerBin, "kill", name).Run()
	return nil
}

// Execute is the hot path. Materializes the workdir, runs `docker run
// --rm` synchronously, captures exit + stdout/stderr.
func (h *Harness) Execute(ctx context.Context, req *harness.ExecRequest) (*harness.ExecResult, error) {
	if req == nil {
		return nil, errors.New("docker: nil ExecRequest")
	}
	if req.Agent == nil {
		return nil, errors.New("docker: ExecRequest.Agent is required")
	}

	dockerCfg, err := ParseDockerConfig(req.Agent.HarnessConfigJSON)
	if err != nil {
		return nil, err
	}
	if dockerCfg.Image == "" {
		return nil, errors.New("docker: agent's harness_config_json.docker.image is required")
	}

	subCfg, err := subprocess.ParseAgentConfig(req.Agent.HarnessConfigJSON)
	if err != nil {
		return nil, err
	}

	workdir, err := h.makeWorkdir(req.RunID)
	if err != nil {
		return nil, err
	}

	if req.Message != nil {
		if err := writeMessageFile(workdir, req.Message); err != nil {
			return nil, err
		}
	}

	if err := subprocess.MaterialiseAgentConfig(workdir, subCfg); err != nil {
		return nil, err
	}

	// Rewrite MCP host in .gemini/settings.json so the agent inside the
	// container can reach SynapBus on the host. The host writes
	// 127.0.0.1:<port> by default; the container sees that loopback as
	// itself, not the host.
	if h.cfg.HostMCPPort > 0 {
		if err := rewriteGeminiMCPHost(workdir, h.cfg.HostGatewayName, h.cfg.HostMCPPort); err != nil {
			h.logger.Warn("rewrite gemini MCP host failed",
				"workdir", workdir, "error", err)
		}
	}

	runCtx := ctx
	if req.Budget.MaxWallClock > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Budget.MaxWallClock)
		defer cancel()
	}

	args, err := h.buildRunArgs(req, dockerCfg, subCfg, workdir)
	if err != nil {
		return nil, err
	}

	h.logger.Info("docker launching",
		"run_id", req.RunID,
		"agent", req.AgentName,
		"image", dockerCfg.Image,
		"workdir", workdir,
		"network", argOr(dockerCfg.Network, "bridge"),
	)

	cmd := exec.CommandContext(runCtx, h.cfg.DockerBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(&stdout, fileWriter(workdir, "stdout.log"))
	cmd.Stderr = io.MultiWriter(&stderr, fileWriter(workdir, "stderr.log"))

	startedAt := time.Now()
	runErr := cmd.Run()
	duration := time.Since(startedAt)

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	var resultJSON json.RawMessage
	if raw, readErr := os.ReadFile(filepath.Join(workdir, "result.json")); readErr == nil && len(raw) > 0 {
		if json.Valid(raw) {
			resultJSON = raw
		}
	}

	promptText := readFileSafe(filepath.Join(workdir, "prompt.txt"))
	responseText := readFileSafe(filepath.Join(workdir, "response.txt"))
	logs := mergeLogs(&stdout, &stderr, h.cfg.LogsCap)

	if exitCode == 0 && !h.cfg.KeepWorkdirOnSuccess {
		_ = os.RemoveAll(workdir)
	}

	h.logger.Info("docker finished",
		"run_id", req.RunID,
		"agent", req.AgentName,
		"exit", exitCode,
		"duration_ms", duration.Milliseconds(),
	)

	result := &harness.ExecResult{
		ExitCode:   exitCode,
		Logs:       logs,
		ResultJSON: resultJSON,
		Prompt:     promptText,
		Response:   responseText,
	}

	if runErr != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		_ = h.Cancel(context.Background(), req.RunID)
		return result, fmt.Errorf("docker: wall-clock budget %s exceeded", req.Budget.MaxWallClock)
	}
	if runErr != nil && errors.Is(runCtx.Err(), context.Canceled) {
		_ = h.Cancel(context.Background(), req.RunID)
		return result, runCtx.Err()
	}
	return result, nil
}

// buildRunArgs constructs the full `docker run` argv. Defaults are
// security-conservative: --rm, --cap-drop=ALL, no-new-privileges, pids
// limit, read-only root with tmpfs /tmp, no privilege escalation, no
// host networking. Per-agent config layers on top.
func (h *Harness) buildRunArgs(
	req *harness.ExecRequest,
	dockerCfg AgentConfig,
	subCfg subprocess.AgentConfig,
	workdir string,
) ([]string, error) {
	name := containerName(req.RunID)
	args := []string{
		"run",
		"--rm",
		"--name", name,
		"--workdir", "/workspace",
		"--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace", workdir),
		"--security-opt", "no-new-privileges",
		"--cap-drop", "ALL",
		"--pids-limit", fmt.Sprintf("%d", pidsLimit(dockerCfg.PIDsLimit)),
	}

	// Read-only root + tmpfs scratch unless explicitly disabled.
	if dockerCfg.ReadOnlyRoot == nil || *dockerCfg.ReadOnlyRoot {
		args = append(args, "--read-only", "--tmpfs", "/tmp:rw,size=64m")
	}

	if dockerCfg.Memory != "" {
		args = append(args, "--memory", dockerCfg.Memory)
		// Match memory-swap to memory so swap doesn't silently double
		// the effective limit. -1 would mean unlimited; equal disables.
		args = append(args, "--memory-swap", dockerCfg.Memory)
	}
	if dockerCfg.CPUs != "" {
		args = append(args, "--cpus", dockerCfg.CPUs)
	}

	network := dockerCfg.Network
	if network == "" {
		network = "bridge"
	}
	args = append(args, "--network", network)

	// Add host.docker.internal pointer on Linux so the agent can reach
	// the SynapBus MCP server at the same hostname as on Docker Desktop.
	// Skipped for --network=host (not needed) and --network=none
	// (would fail the gateway lookup).
	if network != "host" && network != "none" && runtime.GOOS == "linux" {
		args = append(args, "--add-host", h.cfg.HostGatewayName+":host-gateway")
	}

	// User namespacing — host UID/GID injection so files written into
	// the bind-mounted workdir end up owned by the SynapBus user. If
	// the agent overrides User explicitly use that.
	if dockerCfg.User != "" {
		args = append(args, "--user", dockerCfg.User)
	} else {
		args = append(args, "--user", currentUserSpec())
	}

	for _, c := range dockerCfg.CapAdd {
		if c == "" {
			continue
		}
		args = append(args, "--cap-add", c)
	}

	for _, m := range dockerCfg.ExtraMounts {
		if m.Source == "" || m.Target == "" {
			continue
		}
		spec := fmt.Sprintf("type=bind,source=%s,target=%s", m.Source, m.Target)
		if m.ReadOnly {
			spec += ",readonly"
		}
		args = append(args, "--mount", spec)
	}

	// Environment variables — caller-provided + harness-injected. Pass
	// through as -e KEY=VALUE; sort for deterministic output.
	envMap := buildEnvMap(req, subCfg)
	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--env", k+"="+envMap[k])
	}

	args = append(args, dockerCfg.ExtraArgs...)

	if len(dockerCfg.Entrypoint) > 0 {
		args = append(args, "--entrypoint", dockerCfg.Entrypoint[0])
	}

	args = append(args, dockerCfg.Image)

	// Args after the image become the container's CMD. If Entrypoint is
	// set, prepend the rest of its argv first, then the Command.
	if len(dockerCfg.Entrypoint) > 1 {
		args = append(args, dockerCfg.Entrypoint[1:]...)
	}
	if len(dockerCfg.Command) > 0 {
		args = append(args, dockerCfg.Command...)
	} else {
		args = append(args, "/workspace/wrapper.sh")
	}

	return args, nil
}

// makeWorkdir creates BaseDir/<runID-sanitised>/ with mode 0755. The
// directory is removed on successful completion unless KeepWorkdirOn
// Success is set.
func (h *Harness) makeWorkdir(runID string) (string, error) {
	base := h.cfg.BaseDir
	if base == "" {
		base = filepath.Join(os.TempDir(), "synapbus-docker")
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("docker: mkdir base: %w", err)
	}
	name := sanitizeRunDir(runID)
	if name == "" {
		name = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	wd := filepath.Join(base, name)
	if err := os.MkdirAll(wd, 0o755); err != nil {
		return "", fmt.Errorf("docker: mkdir workdir: %w", err)
	}
	return wd, nil
}

// containerName turns a run id into a docker-safe container name.
func containerName(runID string) string {
	clean := sanitizeRunDir(runID)
	if clean == "" {
		clean = fmt.Sprintf("run%d", time.Now().UnixNano())
	}
	return "synapbus-" + clean
}

func sanitizeRunDir(runID string) string {
	if runID == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range runID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func writeMessageFile(workdir string, msg *messaging.Message) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("docker: marshal message: %w", err)
	}
	return os.WriteFile(filepath.Join(workdir, "message.json"), raw, 0o644)
}

// rewriteGeminiMCPHost reads .gemini/settings.json (written by the
// subprocess MaterialiseAgentConfig step) and rewrites every mcpServers
// URL whose host is 127.0.0.1 / localhost / 0.0.0.0 to the host
// gateway. The container can't reach the host on loopback.
func rewriteGeminiMCPHost(workdir, gateway string, port int) error {
	path := filepath.Join(workdir, ".gemini", "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var settings struct {
		MCPServers map[string]map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("docker: parse gemini settings: %w", err)
	}
	if len(settings.MCPServers) == 0 {
		return nil
	}
	dirty := false
	for name, server := range settings.MCPServers {
		urlRaw, ok := server["url"]
		if !ok {
			continue
		}
		var url string
		if err := json.Unmarshal(urlRaw, &url); err != nil {
			continue
		}
		newURL := rewriteLoopback(url, gateway, port)
		if newURL == url {
			continue
		}
		fixed, _ := json.Marshal(newURL)
		settings.MCPServers[name]["url"] = fixed
		dirty = true
	}
	if !dirty {
		return nil
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func rewriteLoopback(url, gateway string, port int) string {
	for _, host := range []string{"127.0.0.1", "localhost", "0.0.0.0"} {
		needle := "//" + host
		if i := strings.Index(url, needle); i >= 0 {
			rest := url[i+len(needle):]
			// Replace the port on the URL with the harness-known host
			// port so a misconfigured agent cant accidentally point at
			// a different listener.
			if strings.HasPrefix(rest, ":") {
				if slash := strings.IndexByte(rest, '/'); slash >= 0 {
					rest = rest[slash:]
				} else {
					rest = ""
				}
			}
			return url[:i] + "//" + gateway + ":" + fmt.Sprintf("%d", port) + rest
		}
	}
	return url
}

// buildEnvMap mirrors subprocess.buildEnv but does NOT inherit the
// parent process's environment. Containers start clean — only what we
// explicitly forward gets in. Order: agent k8s_env_json → harness
// config env → caller overrides → SYNAPBUS_* run context.
func buildEnvMap(req *harness.ExecRequest, cfg subprocess.AgentConfig) map[string]string {
	env := map[string]string{}

	if req.Agent != nil && req.Agent.K8sEnvJSON != "" {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(req.Agent.K8sEnvJSON), &m); err == nil {
			for k, v := range m {
				var s string
				if err := json.Unmarshal(v, &s); err == nil {
					env[k] = s
					continue
				}
				env[k] = strings.Trim(string(v), "\"")
			}
		}
	}

	for k, v := range cfg.Env {
		env[k] = v
	}

	for k, v := range req.Env {
		env[k] = v
	}

	env["SYNAPBUS_RUN_ID"] = req.RunID
	env["SYNAPBUS_AGENT"] = req.AgentName
	env["SYNAPBUS_WORKDIR"] = "/workspace"
	if req.Message != nil {
		env["SYNAPBUS_MESSAGE_ID"] = fmt.Sprintf("%d", req.Message.ID)
		env["SYNAPBUS_FROM_AGENT"] = req.Message.FromAgent
	}
	return env
}

// currentUserSpec returns "uid:gid" for the host user so files written
// inside the bind-mount land with sane ownership instead of root. Only
// meaningful on Linux; on Docker Desktop (mac) the bind-mount layer
// handles ownership translation transparently but passing --user is
// still a defence-in-depth measure.
func currentUserSpec() string {
	uid := os.Getuid()
	gid := os.Getgid()
	if uid <= 0 {
		// fall through to image default if we can't discover (e.g.
		// running on Windows or under a daemon without /proc).
		return ""
	}
	return fmt.Sprintf("%d:%d", uid, gid)
}

func argOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func pidsLimit(cfg int) int {
	if cfg <= 0 {
		return 512
	}
	return cfg
}

func fileWriter(workdir, name string) io.Writer {
	f, err := os.OpenFile(filepath.Join(workdir, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return io.Discard
	}
	return f
}

func readFileSafe(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func mergeLogs(out, errb *bytes.Buffer, cap int) string {
	var b strings.Builder
	if out.Len() > 0 {
		b.WriteString(out.String())
	}
	if errb.Len() > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("-- stderr --\n")
		b.WriteString(errb.String())
	}
	s := b.String()
	if cap > 0 && len(s) > cap {
		s = "... [truncated " + fmt.Sprintf("%d", len(s)-cap) + " bytes] ...\n" + s[len(s)-cap:]
	}
	return s
}
