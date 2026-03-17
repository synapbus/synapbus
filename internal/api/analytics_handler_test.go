package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/storage"
)

func newAnalyticsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:analytics_%s?mode=memory&cache=shared", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	ctx := context.Background()
	if err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Seed test users
	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testuser', 'hash', 'Test User')`)

	return db
}

func seedAnalyticsAgent(t *testing.T, db *sql.DB, name, displayName string, ownerID int64) {
	t.Helper()
	_, err := db.Exec(
		`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES (?, ?, 'ai', '{}', ?, 'testhash', 'active')`,
		name, displayName, ownerID,
	)
	if err != nil {
		t.Fatalf("seed agent %s: %v", name, err)
	}
}

func seedAnalyticsChannel(t *testing.T, db *sql.DB, name, createdBy string) int64 {
	t.Helper()
	result, err := db.Exec(
		`INSERT INTO channels (name, description, type, is_private, created_by, created_at, updated_at) VALUES (?, '', 'standard', 0, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		name, createdBy,
	)
	if err != nil {
		t.Fatalf("seed channel %s: %v", name, err)
	}
	id, _ := result.LastInsertId()
	return id
}

func seedAnalyticsMessage(t *testing.T, db *sql.DB, from, to, body string, channelID *int64, createdAt time.Time) {
	t.Helper()

	// Ensure a conversation exists
	result, err := db.Exec(
		`INSERT INTO conversations (subject, created_by, created_at, updated_at) VALUES ('test', ?, ?, ?)`,
		from, createdAt.Format("2006-01-02 15:04:05"), createdAt.Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	convID, _ := result.LastInsertId()

	_, err = db.Exec(
		`INSERT INTO messages (conversation_id, from_agent, to_agent, channel_id, body, priority, status, metadata, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 5, 'pending', '{}', ?, ?)`,
		convID, from, to, channelID, body, createdAt.Format("2006-01-02 15:04:05"), createdAt.Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}
}

func setupAnalyticsRouter(t *testing.T, db *sql.DB) chi.Router {
	t.Helper()

	agentStore := agents.NewSQLiteAgentStore(db)
	agentService := agents.NewAgentService(agentStore, nil)

	channelStore := channels.NewSQLiteChannelStore(db)
	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, nil)
	channelService := channels.NewService(channelStore, msgService, nil)

	analyticsHandler := NewAnalyticsHandler(db, agentService, channelService)

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(OwnerAuthMiddleware)
		r.Get("/api/analytics/timeline", analyticsHandler.Timeline)
		r.Get("/api/analytics/top-agents", analyticsHandler.TopAgents)
		r.Get("/api/analytics/top-channels", analyticsHandler.TopChannels)
		r.Get("/api/analytics/summary", analyticsHandler.Summary)
	})

	return router
}

func analyticsRequest(t *testing.T, router chi.Router, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Owner-ID", "1")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestAnalyticsTimeline(t *testing.T) {
	db := newAnalyticsTestDB(t)
	seedAnalyticsAgent(t, db, "agent-a", "Agent A", 1)
	seedAnalyticsAgent(t, db, "agent-b", "Agent B", 1)

	now := time.Now().UTC()

	// Seed messages at various times within the last 24h
	for i := 0; i < 5; i++ {
		seedAnalyticsMessage(t, db, "agent-a", "agent-b", fmt.Sprintf("msg-%d", i), nil, now.Add(-time.Duration(i)*time.Hour))
	}

	router := setupAnalyticsRouter(t, db)

	tests := []struct {
		name         string
		span         string
		wantStatus   int
		wantNonEmpty bool
	}{
		{
			name:         "default span (24h)",
			span:         "",
			wantStatus:   http.StatusOK,
			wantNonEmpty: true,
		},
		{
			name:         "1h span",
			span:         "1h",
			wantStatus:   http.StatusOK,
			wantNonEmpty: true,
		},
		{
			name:         "4h span",
			span:         "4h",
			wantStatus:   http.StatusOK,
			wantNonEmpty: true,
		},
		{
			name:         "24h span",
			span:         "24h",
			wantStatus:   http.StatusOK,
			wantNonEmpty: true,
		},
		{
			name:         "7d span",
			span:         "7d",
			wantStatus:   http.StatusOK,
			wantNonEmpty: true,
		},
		{
			name:         "30d span",
			span:         "30d",
			wantStatus:   http.StatusOK,
			wantNonEmpty: true,
		},
		{
			name:       "invalid span",
			span:       "99x",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "/api/analytics/timeline"
			if tt.span != "" {
				path += "?span=" + tt.span
			}

			rr := analyticsRequest(t, router, "GET", path)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body: %s", rr.Code, tt.wantStatus, rr.Body.String())
			}

			if tt.wantStatus != http.StatusOK {
				return
			}

			var resp struct {
				Span    string `json:"span"`
				Buckets []struct {
					Time  string `json:"time"`
					Count int    `json:"count"`
				} `json:"buckets"`
				Total int `json:"total"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			expectedSpan := tt.span
			if expectedSpan == "" {
				expectedSpan = "24h"
			}
			if resp.Span != expectedSpan {
				t.Errorf("span = %q, want %q", resp.Span, expectedSpan)
			}

			if tt.wantNonEmpty && resp.Total == 0 {
				t.Error("expected non-zero total")
			}

			// Verify total matches sum of bucket counts
			bucketSum := 0
			for _, b := range resp.Buckets {
				bucketSum += b.Count
			}
			if bucketSum != resp.Total {
				t.Errorf("bucket sum %d != total %d", bucketSum, resp.Total)
			}
		})
	}
}

func TestAnalyticsTimeline_EmptyDB(t *testing.T) {
	db := newAnalyticsTestDB(t)
	router := setupAnalyticsRouter(t, db)

	rr := analyticsRequest(t, router, "GET", "/api/analytics/timeline?span=24h")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp struct {
		Span    string `json:"span"`
		Buckets []any  `json:"buckets"`
		Total   int    `json:"total"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp.Total != 0 {
		t.Errorf("total = %d, want 0", resp.Total)
	}
	if len(resp.Buckets) != 0 {
		t.Errorf("buckets = %d, want 0", len(resp.Buckets))
	}
}

func TestAnalyticsTopAgents(t *testing.T) {
	db := newAnalyticsTestDB(t)
	seedAnalyticsAgent(t, db, "agent-alpha", "Alpha Agent", 1)
	seedAnalyticsAgent(t, db, "agent-beta", "Beta Agent", 1)

	now := time.Now().UTC()

	// agent-alpha sends 5 messages, agent-beta sends 2
	for i := 0; i < 5; i++ {
		seedAnalyticsMessage(t, db, "agent-alpha", "agent-beta", fmt.Sprintf("msg-%d", i), nil, now.Add(-time.Duration(i)*time.Minute))
	}
	for i := 0; i < 2; i++ {
		seedAnalyticsMessage(t, db, "agent-beta", "agent-alpha", fmt.Sprintf("reply-%d", i), nil, now.Add(-time.Duration(i)*time.Minute))
	}

	router := setupAnalyticsRouter(t, db)

	t.Run("returns agents sorted by count", func(t *testing.T) {
		rr := analyticsRequest(t, router, "GET", "/api/analytics/top-agents?span=24h")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
		}

		var resp struct {
			Span   string `json:"span"`
			Agents []struct {
				Name        string `json:"name"`
				DisplayName string `json:"display_name"`
				Count       int    `json:"count"`
			} `json:"agents"`
		}
		json.Unmarshal(rr.Body.Bytes(), &resp)

		if len(resp.Agents) < 2 {
			t.Fatalf("agents = %d, want >= 2", len(resp.Agents))
		}

		// First agent should be alpha (5 messages)
		if resp.Agents[0].Name != "agent-alpha" {
			t.Errorf("top agent = %q, want agent-alpha", resp.Agents[0].Name)
		}
		if resp.Agents[0].Count != 5 {
			t.Errorf("top agent count = %d, want 5", resp.Agents[0].Count)
		}
		if resp.Agents[0].DisplayName != "Alpha Agent" {
			t.Errorf("display_name = %q, want 'Alpha Agent'", resp.Agents[0].DisplayName)
		}

		// Second agent should be beta (2 messages)
		if resp.Agents[1].Name != "agent-beta" {
			t.Errorf("second agent = %q, want agent-beta", resp.Agents[1].Name)
		}
		if resp.Agents[1].Count != 2 {
			t.Errorf("second agent count = %d, want 2", resp.Agents[1].Count)
		}
	})

	t.Run("respects limit parameter", func(t *testing.T) {
		rr := analyticsRequest(t, router, "GET", "/api/analytics/top-agents?span=24h&limit=1")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}

		var resp struct {
			Agents []struct {
				Name  string `json:"name"`
				Count int    `json:"count"`
			} `json:"agents"`
		}
		json.Unmarshal(rr.Body.Bytes(), &resp)

		if len(resp.Agents) != 1 {
			t.Errorf("agents = %d, want 1", len(resp.Agents))
		}
	})

	t.Run("invalid span returns error", func(t *testing.T) {
		rr := analyticsRequest(t, router, "GET", "/api/analytics/top-agents?span=invalid")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	})
}

func TestAnalyticsTopAgents_EmptyDB(t *testing.T) {
	db := newAnalyticsTestDB(t)
	router := setupAnalyticsRouter(t, db)

	rr := analyticsRequest(t, router, "GET", "/api/analytics/top-agents?span=24h")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp struct {
		Agents []any `json:"agents"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if len(resp.Agents) != 0 {
		t.Errorf("agents = %d, want 0", len(resp.Agents))
	}
}

func TestAnalyticsTopChannels(t *testing.T) {
	db := newAnalyticsTestDB(t)
	seedAnalyticsAgent(t, db, "agent-a", "Agent A", 1)

	chID1 := seedAnalyticsChannel(t, db, "news-mcp", "agent-a")
	chID2 := seedAnalyticsChannel(t, db, "general", "agent-a")

	now := time.Now().UTC()

	// news-mcp gets 4 messages, general gets 2
	for i := 0; i < 4; i++ {
		seedAnalyticsMessage(t, db, "agent-a", "", fmt.Sprintf("ch-msg-%d", i), &chID1, now.Add(-time.Duration(i)*time.Minute))
	}
	for i := 0; i < 2; i++ {
		seedAnalyticsMessage(t, db, "agent-a", "", fmt.Sprintf("gen-msg-%d", i), &chID2, now.Add(-time.Duration(i)*time.Minute))
	}

	router := setupAnalyticsRouter(t, db)

	t.Run("returns channels sorted by count", func(t *testing.T) {
		rr := analyticsRequest(t, router, "GET", "/api/analytics/top-channels?span=24h")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
		}

		var resp struct {
			Span     string `json:"span"`
			Channels []struct {
				Name  string `json:"name"`
				Count int    `json:"count"`
			} `json:"channels"`
		}
		json.Unmarshal(rr.Body.Bytes(), &resp)

		if len(resp.Channels) < 2 {
			t.Fatalf("channels = %d, want >= 2", len(resp.Channels))
		}

		if resp.Channels[0].Name != "news-mcp" {
			t.Errorf("top channel = %q, want news-mcp", resp.Channels[0].Name)
		}
		if resp.Channels[0].Count != 4 {
			t.Errorf("top channel count = %d, want 4", resp.Channels[0].Count)
		}

		if resp.Channels[1].Name != "general" {
			t.Errorf("second channel = %q, want general", resp.Channels[1].Name)
		}
		if resp.Channels[1].Count != 2 {
			t.Errorf("second channel count = %d, want 2", resp.Channels[1].Count)
		}
	})

	t.Run("respects limit parameter", func(t *testing.T) {
		rr := analyticsRequest(t, router, "GET", "/api/analytics/top-channels?span=24h&limit=1")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}

		var resp struct {
			Channels []struct {
				Name  string `json:"name"`
				Count int    `json:"count"`
			} `json:"channels"`
		}
		json.Unmarshal(rr.Body.Bytes(), &resp)

		if len(resp.Channels) != 1 {
			t.Errorf("channels = %d, want 1", len(resp.Channels))
		}
	})

	t.Run("invalid span returns error", func(t *testing.T) {
		rr := analyticsRequest(t, router, "GET", "/api/analytics/top-channels?span=invalid")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	})
}

func TestAnalyticsTopChannels_EmptyDB(t *testing.T) {
	db := newAnalyticsTestDB(t)
	router := setupAnalyticsRouter(t, db)

	rr := analyticsRequest(t, router, "GET", "/api/analytics/top-channels?span=24h")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp struct {
		Channels []any `json:"channels"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if len(resp.Channels) != 0 {
		t.Errorf("channels = %d, want 0", len(resp.Channels))
	}
}

func TestAnalyticsSummary(t *testing.T) {
	db := newAnalyticsTestDB(t)
	seedAnalyticsAgent(t, db, "agent-a", "Agent A", 1)
	seedAnalyticsAgent(t, db, "agent-b", "Agent B", 1)

	chID := seedAnalyticsChannel(t, db, "test-channel", "agent-a")

	now := time.Now().UTC()
	seedAnalyticsMessage(t, db, "agent-a", "agent-b", "hello", nil, now)
	seedAnalyticsMessage(t, db, "agent-b", "agent-a", "hi back", nil, now)
	seedAnalyticsMessage(t, db, "agent-a", "", "channel msg", &chID, now)

	router := setupAnalyticsRouter(t, db)

	rr := analyticsRequest(t, router, "GET", "/api/analytics/summary")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		TotalAgents   int `json:"total_agents"`
		TotalChannels int `json:"total_channels"`
		TotalMessages int `json:"total_messages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.TotalAgents != 2 {
		t.Errorf("total_agents = %d, want 2", resp.TotalAgents)
	}
	if resp.TotalChannels != 1 {
		t.Errorf("total_channels = %d, want 1", resp.TotalChannels)
	}
	if resp.TotalMessages != 3 {
		t.Errorf("total_messages = %d, want 3", resp.TotalMessages)
	}
}

func TestAnalyticsSummary_EmptyDB(t *testing.T) {
	db := newAnalyticsTestDB(t)
	router := setupAnalyticsRouter(t, db)

	rr := analyticsRequest(t, router, "GET", "/api/analytics/summary")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp struct {
		TotalAgents   int `json:"total_agents"`
		TotalChannels int `json:"total_channels"`
		TotalMessages int `json:"total_messages"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp.TotalAgents != 0 {
		t.Errorf("total_agents = %d, want 0", resp.TotalAgents)
	}
	if resp.TotalChannels != 0 {
		t.Errorf("total_channels = %d, want 0", resp.TotalChannels)
	}
	if resp.TotalMessages != 0 {
		t.Errorf("total_messages = %d, want 0", resp.TotalMessages)
	}
}

func TestAnalytics_Unauthenticated(t *testing.T) {
	db := newAnalyticsTestDB(t)
	router := setupAnalyticsRouter(t, db)

	endpoints := []string{
		"/api/analytics/timeline",
		"/api/analytics/top-agents",
		"/api/analytics/top-channels",
		"/api/analytics/summary",
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			req := httptest.NewRequest("GET", endpoint, nil)
			// No X-Owner-ID header
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
			}
		})
	}
}
