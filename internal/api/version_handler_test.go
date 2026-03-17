package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetVersion(t *testing.T) {
	handler := NewVersionHandler("v0.7.0")

	req := httptest.NewRequest("GET", "/api/version", nil)
	rr := httptest.NewRecorder()
	handler.GetVersion(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp struct {
		Version string `json:"version"`
		Repo    string `json:"repo"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Version != "v0.7.0" {
		t.Errorf("version = %q, want v0.7.0", resp.Version)
	}
	if resp.Repo != "https://github.com/synapbus/synapbus" {
		t.Errorf("repo = %q, want https://github.com/synapbus/synapbus", resp.Repo)
	}
}

func TestGetVersion_DevBuild(t *testing.T) {
	handler := NewVersionHandler("dev")

	req := httptest.NewRequest("GET", "/api/version", nil)
	rr := httptest.NewRecorder()
	handler.GetVersion(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp struct {
		Version string `json:"version"`
		Repo    string `json:"repo"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp.Version != "dev" {
		t.Errorf("version = %q, want dev", resp.Version)
	}
}

func TestGetVersion_ResponseFormat(t *testing.T) {
	handler := NewVersionHandler("v1.2.3")

	req := httptest.NewRequest("GET", "/api/version", nil)
	rr := httptest.NewRecorder()
	handler.GetVersion(rr, req)

	// Verify the response is valid JSON with exactly the expected keys
	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(raw) != 2 {
		t.Errorf("response has %d keys, want 2", len(raw))
	}

	if _, ok := raw["version"]; !ok {
		t.Error("response missing 'version' key")
	}
	if _, ok := raw["repo"]; !ok {
		t.Error("response missing 'repo' key")
	}
}
