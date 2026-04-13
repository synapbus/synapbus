package webhook_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/harness/webhook"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/webhooks"
)

func newAgent(url, secret string) *agents.Agent {
	cfg, _ := json.Marshal(map[string]any{"url": url, "secret": secret})
	return &agents.Agent{Name: "webhook-agent", HarnessConfigJSON: string(cfg)}
}

func TestWebhook_ImplementsInterface(t *testing.T) {
	var _ harness.Harness = (*webhook.Harness)(nil)
}

func TestWebhook_NameAndCapabilities(t *testing.T) {
	h := webhook.New(webhook.Config{}, nil)
	if h.Name() != "webhook" {
		t.Fatalf("Name = %q", h.Name())
	}
	caps := h.Capabilities()
	if caps.OTelNative {
		t.Errorf("OTelNative should be false (trace context via headers)")
	}
	if !caps.SessionResume {
		t.Error("SessionResume should be true")
	}
}

func TestWebhook_TestEnvironment_NoOp(t *testing.T) {
	h := webhook.New(webhook.Config{}, nil)
	if err := h.TestEnvironment(context.Background()); err != nil {
		t.Fatalf("TestEnvironment err = %v", err)
	}
}

func TestWebhook_Execute_Success(t *testing.T) {
	var gotBody []byte
	var gotSig string
	var gotRunID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-SynapBus-Signature")
		gotRunID = r.Header.Get("X-SynapBus-Run-Id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"exit_code":0,"logs":"hi","result":{"answer":42},"usage":{"tokens_in":5,"tokens_out":10,"cost_usd":0.003}}`))
	}))
	defer srv.Close()

	h := webhook.New(webhook.Config{}, nil)
	req := &harness.ExecRequest{
		RunID:     "run-ok",
		AgentName: "webhook-agent",
		Agent:     newAgent(srv.URL, "s3cret"),
		Message:   &messaging.Message{ID: 1, FromAgent: "alice", Body: "hi"},
	}
	res, err := h.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d", res.ExitCode)
	}
	if res.Logs != "hi" {
		t.Errorf("Logs = %q", res.Logs)
	}
	if string(res.ResultJSON) != `{"answer":42}` {
		t.Errorf("ResultJSON = %s", res.ResultJSON)
	}
	if res.Usage.TokensIn != 5 || res.Usage.TokensOut != 10 {
		t.Errorf("usage tokens not mapped: %+v", res.Usage)
	}
	if res.Usage.CostUSD != 0.003 {
		t.Errorf("cost not mapped: %v", res.Usage.CostUSD)
	}

	// Request-side assertions
	if gotRunID != "run-ok" {
		t.Errorf("X-SynapBus-Run-Id header = %q", gotRunID)
	}
	if gotSig == "" {
		t.Error("expected HMAC signature header")
	}
	expectedSig := webhooks.ComputeHMACSignature([]byte("s3cret"), gotBody)
	if gotSig != expectedSig {
		t.Errorf("signature mismatch: got=%q want=%q", gotSig, expectedSig)
	}

	var sent map[string]any
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if sent["run_id"] != "run-ok" {
		t.Errorf("sent body run_id = %v", sent["run_id"])
	}
}

func TestWebhook_Execute_HTTPErrorMapsToNonZeroExit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("kaboom"))
	}))
	defer srv.Close()

	h := webhook.New(webhook.Config{}, nil)
	res, err := h.Execute(context.Background(), &harness.ExecRequest{
		RunID: "r", AgentName: "a", Agent: newAgent(srv.URL, ""),
	})
	if err != nil {
		t.Fatalf("Execute err = %v (want graceful mapping)", err)
	}
	if res.ExitCode == 0 {
		t.Errorf("ExitCode = 0, want non-zero")
	}
	if !strings.Contains(res.Logs, "kaboom") {
		t.Errorf("logs missing body: %q", res.Logs)
	}
}

func TestWebhook_Execute_NoConfig(t *testing.T) {
	h := webhook.New(webhook.Config{}, nil)
	_, err := h.Execute(context.Background(), &harness.ExecRequest{
		RunID: "r", AgentName: "a", Agent: &agents.Agent{Name: "a"},
	})
	if !errors.Is(err, webhook.ErrNoWebhookConfig) {
		t.Fatalf("err = %v, want ErrNoWebhookConfig", err)
	}
}

func TestWebhook_Execute_InvalidConfigJSON(t *testing.T) {
	h := webhook.New(webhook.Config{}, nil)
	a := &agents.Agent{Name: "a", HarnessConfigJSON: "not-json"}
	_, err := h.Execute(context.Background(), &harness.ExecRequest{
		RunID: "r", AgentName: "a", Agent: a,
	})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestWebhook_Execute_Timeout(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	h := webhook.New(webhook.Config{}, nil)
	req := &harness.ExecRequest{
		RunID:     "r",
		AgentName: "a",
		Agent:     newAgent(srv.URL, ""),
		Budget:    harness.Budget{MaxWallClock: 30 * time.Millisecond},
	}
	start := time.Now()
	_, err := h.Execute(context.Background(), req)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > time.Second {
		t.Errorf("timeout not enforced; elapsed=%s", elapsed)
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Errorf("attempts = %d, want 1 (no retries in harness)", attempts)
	}
}

func TestWebhook_Execute_NilAgent(t *testing.T) {
	h := webhook.New(webhook.Config{}, nil)
	_, err := h.Execute(context.Background(), &harness.ExecRequest{RunID: "r"})
	if err == nil {
		t.Fatal("expected error for nil agent")
	}
}

func TestWebhook_Execute_PerAgentTimeoutOverridesDefault(t *testing.T) {
	var seenDeadline time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d, ok := r.Context().Deadline(); ok {
			seenDeadline = d
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg, _ := json.Marshal(map[string]any{"url": srv.URL, "timeout_seconds": 2})
	a := &agents.Agent{Name: "a", HarnessConfigJSON: string(cfg)}

	h := webhook.New(webhook.Config{DefaultTimeout: 30 * time.Second}, nil)
	_, err := h.Execute(context.Background(), &harness.ExecRequest{
		RunID: "r", AgentName: "a", Agent: a,
	})
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if seenDeadline.IsZero() {
		t.Skip("server did not capture deadline")
	}
	// Approximate: per-agent timeout (2s) should override the 30s default.
	// We expect the deadline to be well under 10 seconds from now.
	if time.Until(seenDeadline) > 10*time.Second {
		t.Errorf("deadline too far out: %s", time.Until(seenDeadline))
	}
}
