package webhooks

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/synapbus/synapbus/internal/storage"
	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.RunMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

// seedAgent inserts prerequisite user and agent rows so that foreign key
// constraints on webhooks (agent_name -> agents.name) are satisfied.
func seedAgent(t *testing.T, db *sql.DB, agentName string) {
	t.Helper()
	ctx := context.Background()
	// Insert a user (owner)
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO users (username, password_hash, display_name) VALUES (?, 'hash', 'Test User')`,
		"owner-"+agentName,
	)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var ownerID int64
	err = db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, "owner-"+agentName).Scan(&ownerID)
	if err != nil {
		t.Fatalf("get owner id: %v", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES (?, ?, 'ai', '{}', ?, 'hash', 'active')`,
		agentName, agentName, ownerID,
	)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
}

func makeTestWebhook(agentName, url string, events []string) *Webhook {
	return &Webhook{
		AgentName:  agentName,
		URL:        url,
		Events:     events,
		SecretHash: "testhash",
		Status:     WebhookStatusActive,
	}
}

func TestInsertWebhook(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-a")

	tests := []struct {
		name    string
		webhook *Webhook
		wantErr bool
	}{
		{
			name:    "insert valid webhook",
			webhook: makeTestWebhook("agent-a", "https://example.com/hook1", []string{"message.received"}),
			wantErr: false,
		},
		{
			name:    "insert with multiple events",
			webhook: makeTestWebhook("agent-a", "https://example.com/hook2", []string{"message.received", "message.mentioned"}),
			wantErr: false,
		},
		{
			name:    "duplicate url for same agent fails",
			webhook: makeTestWebhook("agent-a", "https://example.com/hook1", []string{"message.received"}),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := store.InsertWebhook(ctx, tt.webhook)
			if (err != nil) != tt.wantErr {
				t.Fatalf("InsertWebhook() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && id <= 0 {
				t.Errorf("expected positive ID, got %d", id)
			}
		})
	}
}

func TestGetWebhookByID(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-b")

	wh := makeTestWebhook("agent-b", "https://example.com/hook", []string{"message.received", "channel.message"})
	id, err := store.InsertWebhook(ctx, wh)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		id      int64
		wantErr bool
	}{
		{name: "existing webhook", id: id, wantErr: false},
		{name: "non-existent webhook", id: 9999, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.GetWebhookByID(ctx, tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetWebhookByID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if got.ID != id {
					t.Errorf("ID = %d, want %d", got.ID, id)
				}
				if got.AgentName != "agent-b" {
					t.Errorf("AgentName = %q, want %q", got.AgentName, "agent-b")
				}
				if got.URL != "https://example.com/hook" {
					t.Errorf("URL = %q, want %q", got.URL, "https://example.com/hook")
				}
				if len(got.Events) != 2 {
					t.Errorf("Events length = %d, want 2", len(got.Events))
				}
				if got.Status != WebhookStatusActive {
					t.Errorf("Status = %q, want %q", got.Status, WebhookStatusActive)
				}
				if got.ConsecutiveFailures != 0 {
					t.Errorf("ConsecutiveFailures = %d, want 0", got.ConsecutiveFailures)
				}
			}
		})
	}
}

func TestGetWebhooksByAgent(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-c")
	seedAgent(t, db, "agent-d")

	// Insert webhooks for agent-c
	for i, url := range []string{"https://example.com/c1", "https://example.com/c2"} {
		wh := makeTestWebhook("agent-c", url, []string{"message.received"})
		if _, err := store.InsertWebhook(ctx, wh); err != nil {
			t.Fatalf("insert webhook %d: %v", i, err)
		}
	}
	// Insert webhook for agent-d
	wh := makeTestWebhook("agent-d", "https://example.com/d1", []string{"message.received"})
	if _, err := store.InsertWebhook(ctx, wh); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		agentName string
		wantCount int
	}{
		{name: "agent with two webhooks", agentName: "agent-c", wantCount: 2},
		{name: "agent with one webhook", agentName: "agent-d", wantCount: 1},
		{name: "agent with no webhooks", agentName: "nonexistent", wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webhooks, err := store.GetWebhooksByAgent(ctx, tt.agentName)
			if err != nil {
				t.Fatalf("GetWebhooksByAgent() error = %v", err)
			}
			if len(webhooks) != tt.wantCount {
				t.Errorf("got %d webhooks, want %d", len(webhooks), tt.wantCount)
			}
		})
	}
}

func TestGetActiveWebhooksByEvent(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-e")

	// Webhook subscribed to message.received only
	wh1 := makeTestWebhook("agent-e", "https://example.com/e1", []string{"message.received"})
	if _, err := store.InsertWebhook(ctx, wh1); err != nil {
		t.Fatal(err)
	}
	// Webhook subscribed to both events
	wh2 := makeTestWebhook("agent-e", "https://example.com/e2", []string{"message.received", "message.mentioned"})
	if _, err := store.InsertWebhook(ctx, wh2); err != nil {
		t.Fatal(err)
	}
	// Disabled webhook subscribed to message.received
	wh3 := makeTestWebhook("agent-e", "https://example.com/e3", []string{"message.received"})
	wh3.Status = WebhookStatusDisabled
	if _, err := store.InsertWebhook(ctx, wh3); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		agentName string
		event     string
		wantCount int
	}{
		{
			name:      "message.received matches two active webhooks",
			agentName: "agent-e",
			event:     "message.received",
			wantCount: 2,
		},
		{
			name:      "message.mentioned matches one active webhook",
			agentName: "agent-e",
			event:     "message.mentioned",
			wantCount: 1,
		},
		{
			name:      "channel.message matches none",
			agentName: "agent-e",
			event:     "channel.message",
			wantCount: 0,
		},
		{
			name:      "non-existent agent matches none",
			agentName: "no-agent",
			event:     "message.received",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webhooks, err := store.GetActiveWebhooksByEvent(ctx, tt.agentName, tt.event)
			if err != nil {
				t.Fatalf("GetActiveWebhooksByEvent() error = %v", err)
			}
			if len(webhooks) != tt.wantCount {
				t.Errorf("got %d webhooks, want %d", len(webhooks), tt.wantCount)
			}
		})
	}
}

func TestUpdateWebhookStatus(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-f")

	wh := makeTestWebhook("agent-f", "https://example.com/f1", []string{"message.received"})
	id, err := store.InsertWebhook(ctx, wh)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		id         int64
		newStatus  string
		wantErr    bool
		wantStatus string
	}{
		{name: "disable webhook", id: id, newStatus: WebhookStatusDisabled, wantErr: false, wantStatus: WebhookStatusDisabled},
		{name: "re-enable webhook", id: id, newStatus: WebhookStatusActive, wantErr: false, wantStatus: WebhookStatusActive},
		{name: "non-existent webhook", id: 9999, newStatus: WebhookStatusDisabled, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.UpdateWebhookStatus(ctx, tt.id, tt.newStatus)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UpdateWebhookStatus() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				got, err := store.GetWebhookByID(ctx, tt.id)
				if err != nil {
					t.Fatal(err)
				}
				if got.Status != tt.wantStatus {
					t.Errorf("status = %q, want %q", got.Status, tt.wantStatus)
				}
			}
		})
	}
}

func TestIncrementConsecutiveFailures(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-g")

	wh := makeTestWebhook("agent-g", "https://example.com/g1", []string{"message.received"})
	id, err := store.InsertWebhook(ctx, wh)
	if err != nil {
		t.Fatal(err)
	}

	// Increment three times
	for i := 1; i <= 3; i++ {
		count, err := store.IncrementConsecutiveFailures(ctx, id)
		if err != nil {
			t.Fatalf("increment %d: %v", i, err)
		}
		if count != i {
			t.Errorf("after increment %d: count = %d, want %d", i, count, i)
		}
	}

	// Verify via GetWebhookByID
	got, err := store.GetWebhookByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConsecutiveFailures != 3 {
		t.Errorf("ConsecutiveFailures = %d, want 3", got.ConsecutiveFailures)
	}
}

func TestResetConsecutiveFailures(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-h")

	wh := makeTestWebhook("agent-h", "https://example.com/h1", []string{"message.received"})
	id, err := store.InsertWebhook(ctx, wh)
	if err != nil {
		t.Fatal(err)
	}

	// Increment a few times
	for i := 0; i < 5; i++ {
		if _, err := store.IncrementConsecutiveFailures(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	// Reset
	if err := store.ResetConsecutiveFailures(ctx, id); err != nil {
		t.Fatalf("ResetConsecutiveFailures() error = %v", err)
	}

	got, err := store.GetWebhookByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", got.ConsecutiveFailures)
	}
}

func TestDeleteWebhook(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-i")
	seedAgent(t, db, "agent-j")

	wh := makeTestWebhook("agent-i", "https://example.com/i1", []string{"message.received"})
	id, err := store.InsertWebhook(ctx, wh)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		id        int64
		agentName string
		wantErr   bool
	}{
		{name: "wrong owner fails", id: id, agentName: "agent-j", wantErr: true},
		{name: "correct owner succeeds", id: id, agentName: "agent-i", wantErr: false},
		{name: "already deleted fails", id: id, agentName: "agent-i", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.DeleteWebhook(ctx, tt.id, tt.agentName)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DeleteWebhook() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCountWebhooksByAgent(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-k")

	count, err := store.CountWebhooksByAgent(ctx, "agent-k")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("initial count = %d, want 0", count)
	}

	for i, url := range []string{"https://example.com/k1", "https://example.com/k2"} {
		wh := makeTestWebhook("agent-k", url, []string{"message.received"})
		if _, err := store.InsertWebhook(ctx, wh); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	count, err = store.CountWebhooksByAgent(ctx, "agent-k")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestInsertDelivery(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-l")

	wh := makeTestWebhook("agent-l", "https://example.com/l1", []string{"message.received"})
	whID, err := store.InsertWebhook(ctx, wh)
	if err != nil {
		t.Fatal(err)
	}

	delivery := &WebhookDelivery{
		WebhookID:   whID,
		AgentName:   "agent-l",
		Event:       "message.received",
		MessageID:   100,
		Payload:     `{"test":"payload"}`,
		Status:      DeliveryStatusPending,
		MaxAttempts: 3,
		Depth:       0,
	}

	id, err := store.InsertDelivery(ctx, delivery)
	if err != nil {
		t.Fatalf("InsertDelivery() error = %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive ID, got %d", id)
	}
	if delivery.ID != id {
		t.Errorf("delivery.ID = %d, want %d (should be set by InsertDelivery)", delivery.ID, id)
	}

	// Verify via GetDeliveryByID
	got, err := store.GetDeliveryByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.WebhookID != whID {
		t.Errorf("WebhookID = %d, want %d", got.WebhookID, whID)
	}
	if got.Status != DeliveryStatusPending {
		t.Errorf("Status = %q, want %q", got.Status, DeliveryStatusPending)
	}
	if got.Payload != `{"test":"payload"}` {
		t.Errorf("Payload = %q, want %q", got.Payload, `{"test":"payload"}`)
	}
}

func TestUpdateDeliveryStatus(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-m")

	wh := makeTestWebhook("agent-m", "https://example.com/m1", []string{"message.received"})
	whID, err := store.InsertWebhook(ctx, wh)
	if err != nil {
		t.Fatal(err)
	}

	delivery := &WebhookDelivery{
		WebhookID:   whID,
		AgentName:   "agent-m",
		Event:       "message.received",
		MessageID:   101,
		Payload:     `{}`,
		Status:      DeliveryStatusPending,
		MaxAttempts: 3,
	}
	id, err := store.InsertDelivery(ctx, delivery)
	if err != nil {
		t.Fatal(err)
	}

	// Mark as delivered
	now := time.Now()
	err = store.UpdateDeliveryStatus(ctx, id, DeliveryStatusDelivered, 200, "", nil, &now)
	if err != nil {
		t.Fatalf("UpdateDeliveryStatus() error = %v", err)
	}

	got, err := store.GetDeliveryByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DeliveryStatusDelivered {
		t.Errorf("Status = %q, want %q", got.Status, DeliveryStatusDelivered)
	}
	if got.HTTPStatus != 200 {
		t.Errorf("HTTPStatus = %d, want 200", got.HTTPStatus)
	}
	if got.DeliveredAt == nil {
		t.Error("DeliveredAt should not be nil")
	}

	// Mark as retrying with error and next_retry_at
	nextRetry := time.Now().Add(5 * time.Minute)
	err = store.UpdateDeliveryStatus(ctx, id, DeliveryStatusRetrying, 500, "server error", &nextRetry, nil)
	if err != nil {
		t.Fatalf("UpdateDeliveryStatus(retrying) error = %v", err)
	}

	got, err = store.GetDeliveryByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DeliveryStatusRetrying {
		t.Errorf("Status = %q, want %q", got.Status, DeliveryStatusRetrying)
	}
	if got.LastError != "server error" {
		t.Errorf("LastError = %q, want %q", got.LastError, "server error")
	}
	if got.NextRetryAt == nil {
		t.Error("NextRetryAt should not be nil")
	}

	// Non-existent delivery
	err = store.UpdateDeliveryStatus(ctx, 9999, DeliveryStatusDelivered, 200, "", nil, nil)
	if err == nil {
		t.Error("expected error for non-existent delivery")
	}
}

func TestGetPendingDeliveries(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-n")

	wh := makeTestWebhook("agent-n", "https://example.com/n1", []string{"message.received"})
	whID, err := store.InsertWebhook(ctx, wh)
	if err != nil {
		t.Fatal(err)
	}

	// Insert 3 pending deliveries
	for i := 0; i < 3; i++ {
		d := &WebhookDelivery{
			WebhookID:   whID,
			AgentName:   "agent-n",
			Event:       "message.received",
			MessageID:   int64(200 + i),
			Payload:     `{}`,
			Status:      DeliveryStatusPending,
			MaxAttempts: 3,
		}
		if _, err := store.InsertDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	// Insert a delivered one (should not appear)
	delivered := &WebhookDelivery{
		WebhookID:   whID,
		AgentName:   "agent-n",
		Event:       "message.received",
		MessageID:   299,
		Payload:     `{}`,
		Status:      DeliveryStatusDelivered,
		MaxAttempts: 3,
	}
	if _, err := store.InsertDelivery(ctx, delivered); err != nil {
		t.Fatal(err)
	}

	pending, err := store.GetPendingDeliveries(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 3 {
		t.Errorf("got %d pending, want 3", len(pending))
	}
	for _, d := range pending {
		if d.Status != DeliveryStatusPending {
			t.Errorf("delivery %d has status %q, want %q", d.ID, d.Status, DeliveryStatusPending)
		}
	}

	// Test limit
	limited, err := store.GetPendingDeliveries(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Errorf("got %d with limit 2, want 2", len(limited))
	}
}

func TestGetRetryableDeliveries(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-o")

	wh := makeTestWebhook("agent-o", "https://example.com/o1", []string{"message.received"})
	whID, err := store.InsertWebhook(ctx, wh)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	past := now.Add(-10 * time.Minute)
	future := now.Add(10 * time.Minute)

	// Retrying delivery with past next_retry_at (should be returned)
	d1 := &WebhookDelivery{
		WebhookID:   whID,
		AgentName:   "agent-o",
		Event:       "message.received",
		MessageID:   300,
		Payload:     `{}`,
		Status:      DeliveryStatusRetrying,
		MaxAttempts: 3,
		NextRetryAt: &past,
	}
	if _, err := store.InsertDelivery(ctx, d1); err != nil {
		t.Fatal(err)
	}

	// Retrying delivery with future next_retry_at (should NOT be returned)
	d2 := &WebhookDelivery{
		WebhookID:   whID,
		AgentName:   "agent-o",
		Event:       "message.received",
		MessageID:   301,
		Payload:     `{}`,
		Status:      DeliveryStatusRetrying,
		MaxAttempts: 3,
		NextRetryAt: &future,
	}
	if _, err := store.InsertDelivery(ctx, d2); err != nil {
		t.Fatal(err)
	}

	// Pending delivery (should NOT be returned)
	d3 := &WebhookDelivery{
		WebhookID:   whID,
		AgentName:   "agent-o",
		Event:       "message.received",
		MessageID:   302,
		Payload:     `{}`,
		Status:      DeliveryStatusPending,
		MaxAttempts: 3,
	}
	if _, err := store.InsertDelivery(ctx, d3); err != nil {
		t.Fatal(err)
	}

	retryable, err := store.GetRetryableDeliveries(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(retryable) != 1 {
		t.Fatalf("got %d retryable, want 1", len(retryable))
	}
	if retryable[0].MessageID != 300 {
		t.Errorf("got message_id %d, want 300", retryable[0].MessageID)
	}
}

func TestGetDeliveriesByAgent(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-p")
	seedAgent(t, db, "agent-q")

	whP := makeTestWebhook("agent-p", "https://example.com/p1", []string{"message.received"})
	whPID, err := store.InsertWebhook(ctx, whP)
	if err != nil {
		t.Fatal(err)
	}

	whQ := makeTestWebhook("agent-q", "https://example.com/q1", []string{"message.received"})
	whQID, err := store.InsertWebhook(ctx, whQ)
	if err != nil {
		t.Fatal(err)
	}

	// Deliveries for agent-p
	for _, status := range []string{DeliveryStatusPending, DeliveryStatusDelivered, DeliveryStatusPending} {
		d := &WebhookDelivery{
			WebhookID:   whPID,
			AgentName:   "agent-p",
			Event:       "message.received",
			MessageID:   400,
			Payload:     `{}`,
			Status:      status,
			MaxAttempts: 3,
		}
		if _, err := store.InsertDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	// Delivery for agent-q
	dq := &WebhookDelivery{
		WebhookID:   whQID,
		AgentName:   "agent-q",
		Event:       "message.received",
		MessageID:   401,
		Payload:     `{}`,
		Status:      DeliveryStatusPending,
		MaxAttempts: 3,
	}
	if _, err := store.InsertDelivery(ctx, dq); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		agentName string
		status    string
		wantCount int
	}{
		{name: "all for agent-p", agentName: "agent-p", status: "", wantCount: 3},
		{name: "pending for agent-p", agentName: "agent-p", status: DeliveryStatusPending, wantCount: 2},
		{name: "delivered for agent-p", agentName: "agent-p", status: DeliveryStatusDelivered, wantCount: 1},
		{name: "all for agent-q", agentName: "agent-q", status: "", wantCount: 1},
		{name: "nonexistent agent", agentName: "nope", status: "", wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deliveries, err := store.GetDeliveriesByAgent(ctx, tt.agentName, tt.status, 50)
			if err != nil {
				t.Fatalf("GetDeliveriesByAgent() error = %v", err)
			}
			if len(deliveries) != tt.wantCount {
				t.Errorf("got %d, want %d", len(deliveries), tt.wantCount)
			}
		})
	}
}

func TestPurgeOldDeadLetters(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	ctx := context.Background()
	seedAgent(t, db, "agent-r")

	wh := makeTestWebhook("agent-r", "https://example.com/r1", []string{"message.received"})
	whID, err := store.InsertWebhook(ctx, wh)
	if err != nil {
		t.Fatal(err)
	}

	// Insert dead-lettered deliveries
	for i := 0; i < 3; i++ {
		d := &WebhookDelivery{
			WebhookID:   whID,
			AgentName:   "agent-r",
			Event:       "message.received",
			MessageID:   int64(500 + i),
			Payload:     `{}`,
			Status:      DeliveryStatusDeadLettered,
			MaxAttempts: 3,
		}
		if _, err := store.InsertDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	// Insert a pending delivery (should NOT be purged)
	pending := &WebhookDelivery{
		WebhookID:   whID,
		AgentName:   "agent-r",
		Event:       "message.received",
		MessageID:   599,
		Payload:     `{}`,
		Status:      DeliveryStatusPending,
		MaxAttempts: 3,
	}
	if _, err := store.InsertDelivery(ctx, pending); err != nil {
		t.Fatal(err)
	}

	// Purge with a future cutoff (should purge all dead-lettered)
	cutoff := time.Now().Add(1 * time.Hour)
	purged, err := store.PurgeOldDeadLetters(ctx, cutoff)
	if err != nil {
		t.Fatalf("PurgeOldDeadLetters() error = %v", err)
	}
	if purged != 3 {
		t.Errorf("purged %d, want 3", purged)
	}

	// Verify pending delivery still exists
	all, err := store.GetDeliveriesByAgent(ctx, "agent-r", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("remaining deliveries = %d, want 1", len(all))
	}
	if all[0].Status != DeliveryStatusPending {
		t.Errorf("remaining delivery status = %q, want %q", all[0].Status, DeliveryStatusPending)
	}
}
