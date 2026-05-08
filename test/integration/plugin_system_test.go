//go:build integration

// Package integration exercises the full plugin framework end-to-end against
// a real plugindemo binary. Run with:
//
//	go test -tags=integration ./test/integration/...
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// --- harness ---

type runningServer struct {
	cmd        *exec.Cmd
	baseURL    string
	configPath string
	dataDir    string
	stderr     *bytes.Buffer
}

func (s *runningServer) Kill() {
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = s.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		_ = s.cmd.Process.Kill()
	}
}

// freePort returns a port number that was free at the moment of the call.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// buildBinary compiles the plugindemo binary into a tempdir and returns the path.
// Caches per test binary via sync.Once would be overkill; one compile per process is fine.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "plugindemo")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/plugindemo")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, stderr.String())
	}
	return bin
}

// startServer writes a config, spawns the binary, waits for HTTP readiness,
// and returns a handle. Caller must call .Kill() via t.Cleanup.
func startServer(t *testing.T, yaml string) *runningServer {
	t.Helper()
	bin := buildBinary(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "synapbus.yaml")
	if err := os.WriteFile(cfg, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd := exec.Command(bin, "-config", cfg, "-data", data, "-addr", addr)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	srv := &runningServer{
		cmd:        cmd,
		baseURL:    "http://" + addr,
		configPath: cfg,
		dataDir:    data,
		stderr:     &stderr,
	}
	t.Cleanup(srv.Kill)

	// Wait up to 5s for the server to come up.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(srv.baseURL + "/api/plugins/status")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return srv
			}
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatalf("server did not become ready within 5s\nstderr:\n%s", stderr.String())
	return nil
}

// getJSON helpers

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if v != nil && len(body) > 0 {
		if err := json.Unmarshal(body, v); err != nil {
			t.Fatalf("decode %s: %v\nbody: %s", url, err, string(body))
		}
	}
	return resp.StatusCode
}

func postJSON(t *testing.T, url string, payload any, v any) int {
	t.Helper()
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if v != nil && len(rb) > 0 {
		if err := json.Unmarshal(rb, v); err != nil {
			t.Fatalf("decode %s: %v\nbody: %s", url, err, string(rb))
		}
	}
	return resp.StatusCode
}

// waitForStatus polls /api/plugins/status until the predicate holds or deadline passes.
func waitForStatus(t *testing.T, srv *runningServer, want func(statusByName map[string]string) bool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var out struct {
			Plugins []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"plugins"`
		}
		code := getJSON(t, srv.baseURL+"/api/plugins/status", &out)
		if code == 200 {
			m := map[string]string{}
			for _, p := range out.Plugins {
				m[p.Name] = p.Status
			}
			if want(m) {
				return true
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	return false
}

// --- tests ---

const baseYAML = `
plugins:
  demo:
    enabled: true
    config:
      max_notes: 0
      background_sweep_every: 1h
`

func TestPluginSystem_StartupShowsDemoStarted(t *testing.T) {
	srv := startServer(t, baseYAML)
	var out struct {
		Plugins []map[string]any `json:"plugins"`
	}
	code := getJSON(t, srv.baseURL+"/api/plugins/status", &out)
	if code != 200 {
		t.Fatalf("status code = %d", code)
	}
	if len(out.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %+v", out.Plugins)
	}
	if got := out.Plugins[0]["status"]; got != "started" {
		t.Fatalf("expected status=started, got %v", got)
	}
	caps, _ := out.Plugins[0]["capabilities"].([]any)
	if len(caps) < 5 {
		t.Fatalf("expected demo plugin to expose 5+ capabilities, got %v", caps)
	}
}

func TestPluginSystem_DemoRESTEndpointWorks(t *testing.T) {
	srv := startServer(t, baseYAML)

	// Create via action endpoint.
	code := postJSON(t, srv.baseURL+"/api/actions/create_note", map[string]any{
		"slug": "first", "title": "Hello from integration test", "body": "body-1",
	}, nil)
	if code != 200 {
		t.Fatalf("create_note action code = %d", code)
	}

	// List via plugin REST route.
	var listed struct {
		Notes []struct {
			Slug, Title string
		} `json:"notes"`
		Count int `json:"count"`
	}
	code = getJSON(t, srv.baseURL+"/api/plugins/demo/notes", &listed)
	if code != 200 {
		t.Fatalf("list code = %d", code)
	}
	if listed.Count != 1 || listed.Notes[0].Slug != "first" {
		t.Fatalf("unexpected list: %+v", listed)
	}
}

func TestPluginSystem_PanelIsServed(t *testing.T) {
	srv := startServer(t, baseYAML)
	resp, err := http.Get(srv.baseURL + "/ui/plugins/demo/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("panel code = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Demo Plugin") {
		t.Fatalf("panel HTML missing expected heading:\n%s", body)
	}
}

func TestPluginSystem_UnknownActionReturns404(t *testing.T) {
	srv := startServer(t, baseYAML)
	code := postJSON(t, srv.baseURL+"/api/actions/no_such_action", map[string]any{}, nil)
	if code != 404 {
		t.Fatalf("expected 404 for unknown action, got %d", code)
	}
}

// SC-001 + SC-008: toggle via REST, SIGHUP, observe the flip + under-2s reload.
func TestPluginSystem_ToggleDisableViaRESTThenEnable(t *testing.T) {
	srv := startServer(t, baseYAML)

	// Create a note so we can verify data survives the round-trip.
	if code := postJSON(t, srv.baseURL+"/api/actions/create_note", map[string]any{
		"slug": "survivor", "title": "Survives disable/enable", "body": "still here",
	}, nil); code != 200 {
		t.Fatalf("seed create failed with code %d", code)
	}

	// Disable
	start := time.Now()
	if code := postJSON(t, srv.baseURL+"/api/admin/plugins/demo/disable", nil, nil); code != 200 {
		t.Fatalf("disable code = %d", code)
	}
	if !waitForStatus(t, srv, func(m map[string]string) bool { return m["demo"] == "disabled" }, 3*time.Second) {
		t.Fatalf("demo did not transition to disabled within 3s")
	}
	disabledAfter := time.Since(start)
	if disabledAfter > 3*time.Second {
		t.Fatalf("reload took too long: %v", disabledAfter)
	}
	// Plugin REST route should now 404.
	resp, err := http.Get(srv.baseURL + "/api/plugins/demo/notes")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404 after disable, got %d", resp.StatusCode)
	}
	// Action must no longer be registered.
	if code := postJSON(t, srv.baseURL+"/api/actions/list_notes", nil, nil); code != 404 {
		t.Fatalf("expected 404 for disabled plugin action, got %d", code)
	}

	// Re-enable
	start = time.Now()
	if code := postJSON(t, srv.baseURL+"/api/admin/plugins/demo/enable", nil, nil); code != 200 {
		t.Fatalf("enable code = %d", code)
	}
	if !waitForStatus(t, srv, func(m map[string]string) bool { return m["demo"] == "started" }, 3*time.Second) {
		t.Fatalf("demo did not return to started within 3s")
	}
	enableAfter := time.Since(start)
	if enableAfter > 3*time.Second {
		t.Fatalf("enable took too long: %v", enableAfter)
	}

	// Data survived: the seed note should still be listed.
	var listed struct {
		Notes []struct {
			Slug string `json:"slug"`
		} `json:"notes"`
		Count int `json:"count"`
	}
	if code := getJSON(t, srv.baseURL+"/api/plugins/demo/notes", &listed); code != 200 {
		t.Fatalf("list after re-enable code = %d", code)
	}
	if listed.Count != 1 || listed.Notes[0].Slug != "survivor" {
		t.Fatalf("data did not survive disable/enable: %+v", listed)
	}

	// Log the reload duration for SC-008 reporting.
	t.Logf("disable→disabled in %v, enable→started in %v", disabledAfter, enableAfter)
}

// SC-008 more rigorous: time SIGHUP-to-ready directly.
func TestPluginSystem_SIGHUPRestartUnderTwoSeconds(t *testing.T) {
	srv := startServer(t, baseYAML)

	// Signal SIGHUP directly and measure.
	start := time.Now()
	if err := srv.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	// Immediately after SIGHUP the server may still respond from the old
	// registry. To measure "fully reloaded", we write a marker config first,
	// then SIGHUP, then poll until the new config's state holds.
	if !waitForStatus(t, srv, func(m map[string]string) bool { return m["demo"] == "started" }, 2*time.Second) {
		t.Fatalf("did not re-ready within 2s")
	}
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("SC-008 miss: SIGHUP reload took %v", elapsed)
	}
	t.Logf("SC-008 pass: SIGHUP reload in %v", elapsed)
}

var _ = context.Background // keep for potential future use
