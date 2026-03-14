package api

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/synapbus/synapbus/internal/trace"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS traces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_name TEXT NOT NULL,
			action TEXT NOT NULL,
			details TEXT NOT NULL DEFAULT '{}',
			error TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			owner_id TEXT NOT NULL DEFAULT ''
		)
	`)
	if err != nil {
		t.Fatalf("create traces table: %v", err)
	}

	return db
}

func seedTraces(t *testing.T, store *trace.SQLiteTraceStore, ownerID string, agentName string, actions []string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	for i, action := range actions {
		details, _ := json.Marshal(map[string]int{"i": i})
		tr := &trace.Trace{
			OwnerID:   ownerID,
			AgentName: agentName,
			Action:    action,
			Details:   json.RawMessage(details),
			Timestamp: now.Add(time.Duration(-i) * time.Minute),
		}
		if err := store.Insert(ctx, tr); err != nil {
			t.Fatalf("seed trace: %v", err)
		}
	}
}

func makeRequest(t *testing.T, handler http.Handler, method, path string, ownerID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if ownerID != "" {
		req.Header.Set("X-Owner-ID", ownerID)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestListTraces_OwnerIsolation(t *testing.T) {
	db := newTestDB(t)
	store := trace.NewSQLiteTraceStore(db)

	// Seed traces for two owners
	seedTraces(t, store, "1", "alice-bot", []string{"send_message", "read_inbox", "send_message"})
	seedTraces(t, store, "2", "bob-bot", []string{"send_message", "error"})

	router := NewRouter(store, nil, nil)

	t.Run("owner 1 sees only own traces", func(t *testing.T) {
		rr := makeRequest(t, router, "GET", "/api/traces", "1")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}

		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		total := int(resp["total"].(float64))
		if total != 3 {
			t.Errorf("total = %d, want 3", total)
		}

		traces := resp["traces"].([]any)
		for _, tr := range traces {
			trMap := tr.(map[string]any)
			if trMap["owner_id"] != "1" {
				t.Errorf("leaked trace from owner %v", trMap["owner_id"])
			}
		}
	})

	t.Run("owner 2 sees only own traces", func(t *testing.T) {
		rr := makeRequest(t, router, "GET", "/api/traces", "2")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}

		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		total := int(resp["total"].(float64))
		if total != 2 {
			t.Errorf("total = %d, want 2", total)
		}
	})

	t.Run("unauthenticated request rejected", func(t *testing.T) {
		rr := makeRequest(t, router, "GET", "/api/traces", "")
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})
}

func TestListTraces_Filters(t *testing.T) {
	db := newTestDB(t)
	store := trace.NewSQLiteTraceStore(db)

	// Seed various traces
	seedTraces(t, store, "1", "agent-a", []string{"send_message", "read_inbox", "send_message", "error"})
	seedTraces(t, store, "1", "agent-b", []string{"send_message", "join_channel"})

	router := NewRouter(store, nil, nil)

	t.Run("filter by agent_name", func(t *testing.T) {
		rr := makeRequest(t, router, "GET", "/api/traces?agent_name=agent-a", "1")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}

		var resp map[string]any
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if int(resp["total"].(float64)) != 4 {
			t.Errorf("total = %v, want 4", resp["total"])
		}
	})

	t.Run("filter by action", func(t *testing.T) {
		rr := makeRequest(t, router, "GET", "/api/traces?action=send_message", "1")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}

		var resp map[string]any
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if int(resp["total"].(float64)) != 3 {
			t.Errorf("total = %v, want 3", resp["total"])
		}
	})

	t.Run("filter combined", func(t *testing.T) {
		rr := makeRequest(t, router, "GET", "/api/traces?agent_name=agent-a&action=send_message", "1")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}

		var resp map[string]any
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if int(resp["total"].(float64)) != 2 {
			t.Errorf("total = %v, want 2", resp["total"])
		}
	})

	t.Run("pagination", func(t *testing.T) {
		rr := makeRequest(t, router, "GET", "/api/traces?page=1&page_size=2", "1")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}

		var resp map[string]any
		json.Unmarshal(rr.Body.Bytes(), &resp)
		traces := resp["traces"].([]any)
		if len(traces) != 2 {
			t.Errorf("got %d traces, want 2", len(traces))
		}
		if int(resp["total"].(float64)) != 6 {
			t.Errorf("total = %v, want 6", resp["total"])
		}
	})

	t.Run("empty result", func(t *testing.T) {
		rr := makeRequest(t, router, "GET", "/api/traces?action=nonexistent", "1")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}

		var resp map[string]any
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if int(resp["total"].(float64)) != 0 {
			t.Errorf("total = %v, want 0", resp["total"])
		}
		traces := resp["traces"].([]any)
		if len(traces) != 0 {
			t.Errorf("got %d traces, want 0", len(traces))
		}
	})
}

func TestListTraces_ResponseFormat(t *testing.T) {
	db := newTestDB(t)
	store := trace.NewSQLiteTraceStore(db)

	seedTraces(t, store, "1", "agent", []string{"send_message"})

	router := NewRouter(store, nil, nil)
	rr := makeRequest(t, router, "GET", "/api/traces", "1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	// Verify JSON structure
	var resp struct {
		Traces   []trace.Trace `json:"traces"`
		Total    int           `json:"total"`
		Page     int           `json:"page"`
		PageSize int           `json:"page_size"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("total = %d, want 1", resp.Total)
	}
	if resp.Page != 1 {
		t.Errorf("page = %d, want 1", resp.Page)
	}
	if resp.PageSize != 50 {
		t.Errorf("page_size = %d, want 50", resp.PageSize)
	}
	if len(resp.Traces) != 1 {
		t.Errorf("traces count = %d, want 1", len(resp.Traces))
	}

	tr := resp.Traces[0]
	if tr.AgentName != "agent" {
		t.Errorf("agent_name = %q, want %q", tr.AgentName, "agent")
	}
	if tr.Action != "send_message" {
		t.Errorf("action = %q, want %q", tr.Action, "send_message")
	}
}

func TestTraceStats(t *testing.T) {
	db := newTestDB(t)
	store := trace.NewSQLiteTraceStore(db)

	seedTraces(t, store, "1", "agent", []string{"send_message", "send_message", "read_inbox", "error"})

	router := NewRouter(store, nil, nil)
	rr := makeRequest(t, router, "GET", "/api/traces/stats", "1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)

	stats := resp["stats"].(map[string]any)
	if stats["send_message"].(float64) != 2 {
		t.Errorf("send_message = %v, want 2", stats["send_message"])
	}
}

func TestExportTraces_JSON(t *testing.T) {
	db := newTestDB(t)
	store := trace.NewSQLiteTraceStore(db)

	seedTraces(t, store, "1", "agent", []string{"send_message", "read_inbox"})

	router := NewRouter(store, nil, nil)
	rr := makeRequest(t, router, "GET", "/api/traces/export?format=json", "1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "traces-") || !strings.Contains(cd, ".json") {
		t.Errorf("unexpected Content-Disposition: %q", cd)
	}

	var traces []trace.Trace
	if err := json.Unmarshal(rr.Body.Bytes(), &traces); err != nil {
		t.Fatalf("unmarshal JSON export: %v", err)
	}
	if len(traces) != 2 {
		t.Errorf("got %d traces, want 2", len(traces))
	}
}

func TestExportTraces_CSV(t *testing.T) {
	db := newTestDB(t)
	store := trace.NewSQLiteTraceStore(db)

	seedTraces(t, store, "1", "agent", []string{"send_message", "read_inbox"})

	router := NewRouter(store, nil, nil)
	rr := makeRequest(t, router, "GET", "/api/traces/export?format=csv", "1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	if ct := rr.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}

	reader := csv.NewReader(rr.Body)
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}

	// Header + 2 data rows
	if len(records) != 3 {
		t.Errorf("got %d CSV rows, want 3 (header + 2 data)", len(records))
	}

	// Verify header
	header := records[0]
	expectedHeaders := []string{"id", "agent_name", "action", "details", "timestamp"}
	for i, h := range expectedHeaders {
		if header[i] != h {
			t.Errorf("header[%d] = %q, want %q", i, header[i], h)
		}
	}
}

func TestExportTraces_Empty(t *testing.T) {
	db := newTestDB(t)
	store := trace.NewSQLiteTraceStore(db)

	router := NewRouter(store, nil, nil)

	t.Run("empty JSON export", func(t *testing.T) {
		rr := makeRequest(t, router, "GET", "/api/traces/export?format=json", "1")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}

		var traces []trace.Trace
		if err := json.Unmarshal(rr.Body.Bytes(), &traces); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(traces) != 0 {
			t.Errorf("got %d traces, want 0", len(traces))
		}
	})

	t.Run("empty CSV export", func(t *testing.T) {
		rr := makeRequest(t, router, "GET", "/api/traces/export?format=csv", "1")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}

		reader := csv.NewReader(rr.Body)
		records, err := reader.ReadAll()
		if err != nil && err != io.EOF {
			t.Fatalf("parse CSV: %v", err)
		}
		// Should have only header
		if len(records) != 1 {
			t.Errorf("got %d CSV rows, want 1 (header only)", len(records))
		}
	})
}

func TestMetricsEndpoint(t *testing.T) {
	db := newTestDB(t)
	store := trace.NewSQLiteTraceStore(db)

	t.Run("metrics enabled", func(t *testing.T) {
		metrics := trace.NewMetrics()
		metrics.IncTrace("send_message")
		metrics.IncTrace("send_message")
		metrics.IncTrace("read_inbox")
		metrics.IncError()
		metrics.SetActiveAgents(3)

		router := NewRouter(store, metrics, nil)
		rr := makeRequest(t, router, "GET", "/metrics", "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}

		body := rr.Body.String()
		if !strings.Contains(body, "synapbus_traces_total 3") {
			t.Error("missing synapbus_traces_total metric")
		}
		if !strings.Contains(body, `synapbus_traces_by_action{action="send_message"} 2`) {
			t.Error("missing synapbus_traces_by_action send_message metric")
		}
		if !strings.Contains(body, "synapbus_errors_total 1") {
			t.Error("missing synapbus_errors_total metric")
		}
		if !strings.Contains(body, "synapbus_active_agents 3") {
			t.Error("missing synapbus_active_agents metric")
		}

		if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
			t.Errorf("Content-Type = %q, want text/plain", ct)
		}
	})

	t.Run("metrics disabled returns 404", func(t *testing.T) {
		router := NewRouter(store, nil, nil)
		rr := makeRequest(t, router, "GET", "/metrics", "")
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
		}
	})
}

func TestMultiOwnerIsolation(t *testing.T) {
	db := newTestDB(t)
	store := trace.NewSQLiteTraceStore(db)

	// Three owners with different agents and traces
	seedTraces(t, store, "1", "alice-bot", []string{"send_message", "read_inbox"})
	seedTraces(t, store, "2", "bob-bot", []string{"send_message", "error", "join_channel"})
	seedTraces(t, store, "3", "charlie-bot", []string{"send_message"})

	router := NewRouter(store, nil, nil)

	for _, tc := range []struct {
		ownerID       string
		expectedTotal int
	}{
		{"1", 2},
		{"2", 3},
		{"3", 1},
	} {
		t.Run("owner_"+tc.ownerID, func(t *testing.T) {
			// Test list endpoint
			rr := makeRequest(t, router, "GET", "/api/traces", tc.ownerID)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d", rr.Code)
			}

			var resp map[string]any
			json.Unmarshal(rr.Body.Bytes(), &resp)
			total := int(resp["total"].(float64))
			if total != tc.expectedTotal {
				t.Errorf("owner %s: total = %d, want %d", tc.ownerID, total, tc.expectedTotal)
			}

			traces := resp["traces"].([]any)
			for _, tr := range traces {
				trMap := tr.(map[string]any)
				if trMap["owner_id"] != tc.ownerID {
					t.Errorf("owner %s: leaked trace from owner %v", tc.ownerID, trMap["owner_id"])
				}
			}

			// Test export endpoint
			rr = makeRequest(t, router, "GET", "/api/traces/export?format=json", tc.ownerID)
			if rr.Code != http.StatusOK {
				t.Fatalf("export status = %d", rr.Code)
			}

			var exported []trace.Trace
			json.Unmarshal(rr.Body.Bytes(), &exported)
			if len(exported) != tc.expectedTotal {
				t.Errorf("owner %s: exported %d, want %d", tc.ownerID, len(exported), tc.expectedTotal)
			}
			for _, tr := range exported {
				if tr.OwnerID != tc.ownerID {
					t.Errorf("owner %s: exported trace from owner %s", tc.ownerID, tr.OwnerID)
				}
			}

			// Test stats endpoint
			rr = makeRequest(t, router, "GET", "/api/traces/stats", tc.ownerID)
			if rr.Code != http.StatusOK {
				t.Fatalf("stats status = %d", rr.Code)
			}
		})
	}
}
