package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/dispatcher"
)

func setupDeliveryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	// Create tables
	for _, ddl := range []string{
		`CREATE TABLE webhooks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_name TEXT NOT NULL,
			url TEXT NOT NULL,
			events TEXT NOT NULL DEFAULT '[]',
			secret_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE webhook_deliveries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			webhook_id INTEGER NOT NULL,
			agent_name TEXT NOT NULL,
			event TEXT NOT NULL,
			message_id INTEGER NOT NULL,
			payload TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			http_status INTEGER,
			attempts INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 3,
			last_error TEXT,
			next_retry_at DATETIME,
			depth INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			delivered_at DATETIME,
			FOREIGN KEY (webhook_id) REFERENCES webhooks(id)
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}

	t.Cleanup(func() { db.Close() })
	return db
}

func TestDeliveryEngine_SuccessfulDelivery(t *testing.T) {
	db := setupDeliveryTestDB(t)
	store := NewSQLiteWebhookStore(db)

	var received atomic.Int32
	var receivedHeaders http.Header
	var receivedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		receivedHeaders = r.Header.Clone()
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		receivedBody = buf[:n]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Register webhook
	wh := &Webhook{
		AgentName:  "test-agent",
		URL:        srv.URL,
		Events:     []string{"message.received"},
		SecretHash: "testhash",
		Status:     WebhookStatusActive,
	}
	_, err := store.InsertWebhook(context.Background(), wh)
	if err != nil {
		t.Fatal(err)
	}

	service := NewWebhookService(store, true, true)
	rateLimiter := NewAgentRateLimiter(60)
	engine := NewDeliveryEngine(service, rateLimiter, true)
	engine.Start()
	defer engine.Stop()

	// Dispatch event
	event := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 42,
		FromAgent: "sender",
		ToAgent:   "test-agent",
		Body:      "hello world",
		Priority:  1,
		Depth:     0,
	}

	err = engine.Dispatch(context.Background(), event)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}

	// Wait for delivery
	deadline := time.After(5 * time.Second)
	for received.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for delivery")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	if received.Load() != 1 {
		t.Errorf("expected 1 delivery, got %d", received.Load())
	}

	// Verify headers
	if receivedHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", receivedHeaders.Get("Content-Type"))
	}
	if receivedHeaders.Get("X-Synapbus-Event") != "message.received" {
		t.Errorf("expected X-Synapbus-Event message.received, got %q", receivedHeaders.Get("X-Synapbus-Event"))
	}
	if receivedHeaders.Get("X-Synapbus-Signature") == "" {
		t.Error("expected X-Synapbus-Signature header to be set")
	}
	if receivedHeaders.Get("X-Synapbus-Depth") != "1" {
		t.Errorf("expected X-Synapbus-Depth 1, got %q", receivedHeaders.Get("X-Synapbus-Depth"))
	}

	// Verify payload structure
	var payload DeliveryPayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Event != "message.received" {
		t.Errorf("expected event message.received, got %q", payload.Event)
	}
	if payload.MessageID != 42 {
		t.Errorf("expected message_id 42, got %d", payload.MessageID)
	}
	if payload.FromAgent != "sender" {
		t.Errorf("expected from_agent sender, got %q", payload.FromAgent)
	}
	if payload.Body != "hello world" {
		t.Errorf("expected body 'hello world', got %q", payload.Body)
	}
}

func TestDeliveryEngine_DepthExceeded(t *testing.T) {
	db := setupDeliveryTestDB(t)
	store := NewSQLiteWebhookStore(db)

	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Register webhook
	wh := &Webhook{
		AgentName:  "test-agent",
		URL:        srv.URL,
		Events:     []string{"message.received"},
		SecretHash: "testhash",
		Status:     WebhookStatusActive,
	}
	_, err := store.InsertWebhook(context.Background(), wh)
	if err != nil {
		t.Fatal(err)
	}

	service := NewWebhookService(store, true, true)
	rateLimiter := NewAgentRateLimiter(60)
	engine := NewDeliveryEngine(service, rateLimiter, true)
	engine.Start()
	defer engine.Stop()

	// Depth 4 should succeed
	event4 := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "sender",
		ToAgent:   "test-agent",
		Body:      "depth 4",
		Depth:     4,
	}
	if err := engine.Dispatch(context.Background(), event4); err != nil {
		t.Fatalf("dispatch depth 4: %v", err)
	}

	// Wait for depth 4 delivery
	deadline := time.After(5 * time.Second)
	for received.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for depth 4 delivery")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	if received.Load() != 1 {
		t.Errorf("expected 1 delivery for depth 4, got %d", received.Load())
	}

	// Depth 5 should be dropped (MaxDepth = 5)
	event5 := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 2,
		FromAgent: "sender",
		ToAgent:   "test-agent",
		Body:      "depth 5",
		Depth:     5,
	}
	if err := engine.Dispatch(context.Background(), event5); err != nil {
		t.Fatalf("dispatch depth 5: %v", err)
	}

	// Short wait — delivery should NOT happen
	time.Sleep(500 * time.Millisecond)

	if received.Load() != 1 {
		t.Errorf("expected depth 5 to be dropped, but got %d deliveries", received.Load())
	}
}

func TestDeliveryEngine_RetryOnFailure(t *testing.T) {
	db := setupDeliveryTestDB(t)
	store := NewSQLiteWebhookStore(db)

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		if count <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Register webhook
	wh := &Webhook{
		AgentName:  "test-agent",
		URL:        srv.URL,
		Events:     []string{"message.received"},
		SecretHash: "testhash",
		Status:     WebhookStatusActive,
	}
	_, err := store.InsertWebhook(context.Background(), wh)
	if err != nil {
		t.Fatal(err)
	}

	service := NewWebhookService(store, true, true)
	rateLimiter := NewAgentRateLimiter(60)
	engine := NewDeliveryEngine(service, rateLimiter, true)

	// Use shorter retry intervals for testing
	origIntervals := retryIntervals
	retryIntervals = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond}
	defer func() { retryIntervals = origIntervals }()

	engine.Start()
	defer engine.Stop()

	event := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "sender",
		ToAgent:   "test-agent",
		Body:      "retry test",
		Depth:     0,
	}

	if err := engine.Dispatch(context.Background(), event); err != nil {
		t.Fatalf("dispatch error: %v", err)
	}

	// Wait for retries to complete (server returns 200 on 3rd attempt)
	deadline := time.After(15 * time.Second)
	for attempts.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for retries, got %d attempts", attempts.Load())
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	if attempts.Load() < 3 {
		t.Errorf("expected at least 3 attempts, got %d", attempts.Load())
	}
}

func TestDeliveryEngine_DeadLetterAfterMaxRetries(t *testing.T) {
	db := setupDeliveryTestDB(t)
	store := NewSQLiteWebhookStore(db)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Register webhook
	wh := &Webhook{
		AgentName:  "test-agent",
		URL:        srv.URL,
		Events:     []string{"message.received"},
		SecretHash: "testhash",
		Status:     WebhookStatusActive,
	}
	_, err := store.InsertWebhook(context.Background(), wh)
	if err != nil {
		t.Fatal(err)
	}

	service := NewWebhookService(store, true, true)
	rateLimiter := NewAgentRateLimiter(60)
	engine := NewDeliveryEngine(service, rateLimiter, true)

	// Use shorter retry intervals for testing
	origIntervals := retryIntervals
	retryIntervals = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond}
	defer func() { retryIntervals = origIntervals }()

	engine.Start()
	defer engine.Stop()

	event := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "sender",
		ToAgent:   "test-agent",
		Body:      "dead letter test",
		Depth:     0,
	}

	if err := engine.Dispatch(context.Background(), event); err != nil {
		t.Fatalf("dispatch error: %v", err)
	}

	// Wait for all retries to exhaust
	deadline := time.After(20 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for dead letter")
		default:
			time.Sleep(200 * time.Millisecond)
		}

		deliveries, err := store.GetDeliveriesByAgent(context.Background(), "test-agent", DeliveryStatusDeadLettered, 10)
		if err != nil {
			t.Fatalf("get deliveries: %v", err)
		}
		if len(deliveries) > 0 {
			if deliveries[0].Attempts < maxAttempts {
				t.Errorf("expected %d attempts, got %d", maxAttempts, deliveries[0].Attempts)
			}
			return // Test passes
		}
	}
}
