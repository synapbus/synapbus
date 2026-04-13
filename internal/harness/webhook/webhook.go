// Package webhook is the HTTP-backed implementation of harness.Harness.
// It POSTs the run request to a configured URL and waits synchronously
// for a JSON response. Distinct from internal/webhooks, which is the
// fire-and-forget outbound event fan-out — this harness is a real
// request/response channel used when an agent lives behind an HTTP
// endpoint instead of a K8s Job or a local process.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/webhooks"
)

// DefaultTimeout is applied when neither Config.DefaultTimeout nor
// ExecRequest.Budget.MaxWallClock is set.
const DefaultTimeout = 60 * time.Second

// Config tunes the webhook harness.
type Config struct {
	// HTTPClient is the outbound client. When nil, a default is used
	// with a sensible timeout and no redirect following.
	HTTPClient *http.Client

	// DefaultTimeout applies when the request has no budget set.
	DefaultTimeout time.Duration

	// UserAgent overrides the User-Agent header. Defaults to
	// "synapbus-harness-webhook/1".
	UserAgent string
}

// Harness is the webhook implementation of harness.Harness.
type Harness struct {
	cfg    Config
	client *http.Client
	logger *slog.Logger
}

// agentWebhookConfig is the per-agent config blob the webhook harness
// reads from agents.harness_config_json. Callers populate this via the
// admin CLI or migration data.
type agentWebhookConfig struct {
	URL            string `json:"url"`
	Secret         string `json:"secret,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// payload is what the harness POSTs to the configured URL. Kept
// deliberately flat — the receiver can ignore fields it doesn't need.
type payload struct {
	RunID     string          `json:"run_id"`
	AgentName string          `json:"agent_name"`
	Message   any             `json:"message,omitempty"`
	Context   []any           `json:"context,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Skills    []string        `json:"skills,omitempty"`
}

// responseEnvelope is what the harness expects back. Every field is
// optional so a minimally-compliant receiver only needs to return an
// HTTP 200 to signal success.
type responseEnvelope struct {
	ExitCode   int             `json:"exit_code"`
	Logs       string          `json:"logs,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	SessionID  string          `json:"session_id,omitempty"`
	Usage      struct {
		TokensIn     int64   `json:"tokens_in"`
		TokensOut    int64   `json:"tokens_out"`
		TokensCached int64   `json:"tokens_cached"`
		CostUSD      float64 `json:"cost_usd"`
	} `json:"usage,omitempty"`
}

// New constructs a webhook harness.
func New(cfg Config, logger *slog.Logger) *Harness {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 0, // per-request timeout is set via context
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "synapbus-harness-webhook/1"
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = DefaultTimeout
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Harness{
		cfg:    cfg,
		client: client,
		logger: logger.With("harness", "webhook"),
	}
}

// Name returns the registered harness name.
func (h *Harness) Name() string { return "webhook" }

// Capabilities advertises backend features.
func (h *Harness) Capabilities() harness.Capabilities {
	return harness.Capabilities{
		SystemPrompt:   true,
		SessionResume:  true,
		Skills:         true,
		OTelNative:     false, // trace context goes via headers, not env
		MaxConcurrency: 10,
	}
}

// TestEnvironment is a no-op for the webhook harness — we can't
// preflight without a target URL, which is per-agent config.
func (h *Harness) TestEnvironment(ctx context.Context) error {
	return nil
}

// Provision is a no-op for webhook.
func (h *Harness) Provision(ctx context.Context, agent *agents.Agent) error {
	return nil
}

// ErrNoWebhookConfig is returned when an agent's harness_config_json
// doesn't carry a URL the webhook harness can POST to.
var ErrNoWebhookConfig = errors.New("webhook: agent has no webhook config (harness_config_json.url missing)")

// Execute builds the payload, signs it if a secret is configured,
// POSTs it, and maps the response back to an ExecResult.
func (h *Harness) Execute(ctx context.Context, req *harness.ExecRequest) (*harness.ExecResult, error) {
	if req == nil {
		return nil, errors.New("webhook: nil ExecRequest")
	}
	if req.Agent == nil {
		return nil, errors.New("webhook: ExecRequest.Agent is required")
	}

	cfg, err := parseAgentConfig(req.Agent.HarnessConfigJSON)
	if err != nil {
		return nil, err
	}

	timeout := h.cfg.DefaultTimeout
	if req.Budget.MaxWallClock > 0 {
		timeout = req.Budget.MaxWallClock
	}
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body := payload{
		RunID:     req.RunID,
		AgentName: req.AgentName,
		SessionID: req.SessionID,
		Env:       req.Env,
		Skills:    req.Skills,
	}
	if req.Message != nil {
		body.Message = req.Message
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("webhook: marshal payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(runCtx, http.MethodPost, cfg.URL, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("webhook: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", h.cfg.UserAgent)
	httpReq.Header.Set("X-SynapBus-Run-Id", req.RunID)
	httpReq.Header.Set("X-SynapBus-Agent", req.AgentName)
	if cfg.Secret != "" {
		httpReq.Header.Set("X-SynapBus-Signature",
			webhooks.ComputeHMACSignature([]byte(cfg.Secret), raw))
	}

	h.logger.Info("webhook dispatching",
		"run_id", req.RunID,
		"agent", req.AgentName,
		"url", cfg.URL,
	)

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("webhook: POST %s: %w", cfg.URL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB
	if err != nil {
		return nil, fmt.Errorf("webhook: read response: %w", err)
	}

	var env responseEnvelope
	if len(respBody) > 0 && json.Valid(respBody) {
		// Best-effort parse. A non-JSON response is acceptable for
		// receivers that just want to signal success via HTTP status.
		_ = json.Unmarshal(respBody, &env)
	}

	exitCode := env.ExitCode
	if exitCode == 0 && resp.StatusCode >= 400 {
		exitCode = 1
	}
	logs := env.Logs
	if logs == "" {
		logs = string(respBody)
	}

	return &harness.ExecResult{
		ExitCode:   exitCode,
		Logs:       logs,
		ResultJSON: env.Result,
		SessionID:  env.SessionID,
		Usage: harness.Usage{
			TokensIn:     env.Usage.TokensIn,
			TokensOut:    env.Usage.TokensOut,
			TokensCached: env.Usage.TokensCached,
			CostUSD:      env.Usage.CostUSD,
		},
	}, nil
}

// Cancel is a no-op — HTTP POSTs are cancelled by context, not by
// out-of-band signalling.
func (h *Harness) Cancel(ctx context.Context, runID string) error {
	return nil
}

func parseAgentConfig(raw string) (agentWebhookConfig, error) {
	var cfg agentWebhookConfig
	if raw == "" {
		return cfg, ErrNoWebhookConfig
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, fmt.Errorf("webhook: parse harness_config_json: %w", err)
	}
	if cfg.URL == "" {
		return cfg, ErrNoWebhookConfig
	}
	return cfg, nil
}
