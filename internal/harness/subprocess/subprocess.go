// Package subprocess is the local-process implementation of
// harness.Harness. It runs an agent as a child process of synapbus
// using os/exec, captures stdout+stderr, and reads an optional
// result.json the child may have written into its workdir.
//
// Works on Mac and Linux identically (no CGO, no platform-specific
// syscalls). Windows is not targeted because synapbus itself does not
// target Windows.
package subprocess

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
	"strings"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
)

// Config tunes the subprocess harness. Zero-value defaults are sensible
// for development; production callers typically set BaseDir to a
// predictable location under SYNAPBUS_DATA_DIR so forensics are easy.
type Config struct {
	// BaseDir is the parent directory under which a per-run workdir is
	// created. Defaults to os.TempDir() when empty.
	BaseDir string

	// LogsCap bounds the number of bytes kept in ExecResult.Logs. The
	// full stdout/stderr stream is written to `stdout.log` /
	// `stderr.log` inside the workdir for forensics. Defaults to 64 KiB.
	LogsCap int

	// KeepWorkdirOnSuccess leaves the workdir behind even for
	// zero-exit runs. Useful for debugging test flakes.
	KeepWorkdirOnSuccess bool
}

// Harness runs agents as local subprocesses.
type Harness struct {
	cfg    Config
	logger *slog.Logger
}

// New constructs a subprocess harness. Pass zero Config for defaults.
func New(cfg Config, logger *slog.Logger) *Harness {
	if cfg.LogsCap <= 0 {
		cfg.LogsCap = 64 * 1024
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Harness{
		cfg:    cfg,
		logger: logger.With("harness", "subprocess"),
	}
}

// Name returns the registered harness name.
func (h *Harness) Name() string { return "subprocess" }

// Capabilities advertises backend features.
func (h *Harness) Capabilities() harness.Capabilities {
	return harness.Capabilities{
		SystemPrompt:   true,
		SessionResume:  true,
		Skills:         false,
		OTelNative:     true,
		MaxConcurrency: 4,
	}
}

// TestEnvironment is a cheap sanity check: BaseDir (or os.TempDir) must
// exist and be writable. Per-agent binary reachability is checked at
// Execute time because the binary is agent-specific.
func (h *Harness) TestEnvironment(ctx context.Context) error {
	base := h.cfg.BaseDir
	if base == "" {
		base = os.TempDir()
	}
	info, err := os.Stat(base)
	if err != nil {
		return fmt.Errorf("subprocess: base dir %q: %w", base, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("subprocess: base dir %q is not a directory", base)
	}
	return nil
}

// Provision is a no-op for subprocess. All per-run state goes into the
// workdir Execute creates on the fly.
func (h *Harness) Provision(ctx context.Context, agent *agents.Agent) error {
	return nil
}

// ErrNoLocalCommand is returned when an agent has no LocalCommand
// configured so the subprocess backend cannot know what to run.
var ErrNoLocalCommand = errors.New("subprocess: agent has no local_command configured")

// Execute launches the child, waits for it to exit (or for ctx /
// Budget to fire), and returns its output.
func (h *Harness) Execute(ctx context.Context, req *harness.ExecRequest) (*harness.ExecResult, error) {
	if req == nil {
		return nil, errors.New("subprocess: nil ExecRequest")
	}
	if req.Agent == nil {
		return nil, errors.New("subprocess: ExecRequest.Agent is required")
	}

	argv, err := parseLocalCommand(req.Agent.LocalCommand)
	if err != nil {
		return nil, err
	}

	// Per-run workdir
	base := h.cfg.BaseDir
	if base == "" {
		base = os.TempDir()
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("subprocess: mkdir base: %w", err)
	}
	runDirName := sanitizeRunDir(req.RunID)
	if runDirName == "" {
		runDirName = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	workdir := filepath.Join(base, runDirName)
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return nil, fmt.Errorf("subprocess: mkdir workdir: %w", err)
	}

	// Context with Budget timeout if set.
	runCtx := ctx
	if req.Budget.MaxWallClock > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Budget.MaxWallClock)
		defer cancel()
	}

	// Write the triggering message to message.json for the child to
	// read if it cares. Simple, explicit, no stdin-piping ambiguity.
	if req.Message != nil {
		raw, _ := json.Marshal(req.Message)
		_ = os.WriteFile(filepath.Join(workdir, "message.json"), raw, 0o644)
	}

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = workdir
	cmd.Env = buildEnv(req, workdir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(&stdout, limitedFileWriter(workdir, "stdout.log"))
	cmd.Stderr = io.MultiWriter(&stderr, limitedFileWriter(workdir, "stderr.log"))

	h.logger.Info("subprocess launching",
		"run_id", req.RunID,
		"agent", req.AgentName,
		"cmd", argv[0],
		"workdir", workdir,
	)

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

	// Load optional result.json
	var resultJSON json.RawMessage
	if raw, readErr := os.ReadFile(filepath.Join(workdir, "result.json")); readErr == nil && len(raw) > 0 {
		if json.Valid(raw) {
			resultJSON = json.RawMessage(raw)
		}
	}

	logs := mergeLogs(&stdout, &stderr, h.cfg.LogsCap)

	// Cleanup policy: remove workdir on success unless configured to
	// keep it; always keep on failure so users can inspect stdout.log /
	// stderr.log / message.json / result.json.
	if exitCode == 0 && !h.cfg.KeepWorkdirOnSuccess {
		_ = os.RemoveAll(workdir)
	}

	h.logger.Info("subprocess finished",
		"run_id", req.RunID,
		"agent", req.AgentName,
		"exit", exitCode,
		"duration_ms", duration.Milliseconds(),
	)

	result := &harness.ExecResult{
		ExitCode:   exitCode,
		Logs:       logs,
		ResultJSON: resultJSON,
	}

	// Distinguish context timeout from plain failures so the caller
	// can tell "budget exceeded" from "the agent crashed".
	if runErr != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("subprocess: wall-clock budget %s exceeded", req.Budget.MaxWallClock)
	}
	if runErr != nil && errors.Is(runCtx.Err(), context.Canceled) {
		return result, runCtx.Err()
	}
	return result, nil
}

// Cancel is a no-op for subprocess today — cancellation happens via the
// context passed to Execute. A future iteration could track in-flight
// runs by RunID and send SIGTERM to them.
func (h *Harness) Cancel(ctx context.Context, runID string) error {
	return nil
}

// -- helpers --------------------------------------------------------------

// parseLocalCommand accepts either a JSON array (["claude", "--print"])
// or a simple space-separated string. Returns the argv slice or an
// error if neither form parses.
func parseLocalCommand(raw string) ([]string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, ErrNoLocalCommand
	}
	if strings.HasPrefix(s, "[") {
		var argv []string
		if err := json.Unmarshal([]byte(s), &argv); err != nil {
			return nil, fmt.Errorf("subprocess: parse local_command JSON: %w", err)
		}
		if len(argv) == 0 {
			return nil, ErrNoLocalCommand
		}
		return argv, nil
	}
	// Fall back to whitespace split. Suitable for simple commands.
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return nil, ErrNoLocalCommand
	}
	return parts, nil
}

// buildEnv constructs the env var list for the child. Starts from the
// parent's environment (so HOME, PATH, credentials are inherited by
// default — matches current K8s Pod behaviour). Then layers the
// agent's k8s_env_json (for consistency between backends), then caller
// overrides, then the SYNAPBUS_* run-context variables.
func buildEnv(req *harness.ExecRequest, workdir string) []string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}

	// agent env map (K8sEnvJSON is shared across backends today)
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

	// caller overrides
	for k, v := range req.Env {
		env[k] = v
	}

	// run context
	env["SYNAPBUS_RUN_ID"] = req.RunID
	env["SYNAPBUS_AGENT"] = req.AgentName
	env["SYNAPBUS_WORKDIR"] = workdir
	if req.Message != nil {
		env["SYNAPBUS_MESSAGE_ID"] = fmt.Sprintf("%d", req.Message.ID)
		env["SYNAPBUS_FROM_AGENT"] = req.Message.FromAgent
	}

	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// mergeLogs interleaves stdout then stderr with a header, bounded by cap.
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
		// Keep the tail — most informative on failure.
		s = "... [truncated " + fmt.Sprintf("%d", len(s)-cap) + " bytes] ...\n" + s[len(s)-cap:]
	}
	return s
}

// limitedFileWriter returns a writer that appends to a file inside the
// workdir. Errors are silently ignored — logs are best-effort and must
// not fail the run.
func limitedFileWriter(workdir, name string) io.Writer {
	f, err := os.OpenFile(filepath.Join(workdir, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return io.Discard
	}
	return f
}

// sanitizeRunDir strips characters that cause surprises on case-folded
// filesystems or in shell globs. Keeps the resulting name readable.
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
	name := b.String()
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}
