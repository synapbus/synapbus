package health

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestHealthz(t *testing.T) {
	db := setupTestDB(t)
	checker := NewChecker(db, "1.0.0-test")

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	checker.Healthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", resp["status"])
	}
}

func TestReadyz_Healthy(t *testing.T) {
	db := setupTestDB(t)
	checker := NewChecker(db, "1.0.0-test")

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	checker.Readyz(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["status"] != "ready" {
		t.Errorf("expected status=ready, got %q", resp["status"])
	}
	if resp["version"] != "1.0.0-test" {
		t.Errorf("expected version=1.0.0-test, got %q", resp["version"])
	}
}

func TestReadyz_Unhealthy(t *testing.T) {
	db := setupTestDB(t)
	checker := NewChecker(db, "1.0.0-test")

	// Close the DB to simulate an unavailable database.
	db.Close()

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()

	checker.Readyz(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["status"] != "not ready" {
		t.Errorf("expected status='not ready', got %q", resp["status"])
	}
	if resp["error"] == "" {
		t.Error("expected non-empty error field")
	}
}
