package webhooks

import (
	"context"
	"testing"
)

func TestRegisterWebhook_Success(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	svc := NewWebhookService(store, true, true) // allow HTTP + private for testing
	ctx := context.Background()
	seedAgent(t, db, "svc-agent")

	wh, err := svc.RegisterWebhook(ctx, "svc-agent", "https://example.com/hook", []string{"message.received"}, "my-secret")
	if err != nil {
		t.Fatalf("RegisterWebhook() error = %v", err)
	}
	if wh.ID <= 0 {
		t.Errorf("expected positive ID, got %d", wh.ID)
	}
	if wh.AgentName != "svc-agent" {
		t.Errorf("AgentName = %q, want %q", wh.AgentName, "svc-agent")
	}
	if wh.URL != "https://example.com/hook" {
		t.Errorf("URL = %q, want %q", wh.URL, "https://example.com/hook")
	}
	if wh.Status != WebhookStatusActive {
		t.Errorf("Status = %q, want %q", wh.Status, WebhookStatusActive)
	}
	if len(wh.Events) != 1 || wh.Events[0] != "message.received" {
		t.Errorf("Events = %v, want [message.received]", wh.Events)
	}
	if wh.SecretHash == "" {
		t.Error("SecretHash should not be empty")
	}
}

func TestRegisterWebhook_InvalidEvent(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	svc := NewWebhookService(store, true, true)
	ctx := context.Background()
	seedAgent(t, db, "svc-agent2")

	tests := []struct {
		name   string
		events []string
	}{
		{name: "invalid event type", events: []string{"invalid.event"}},
		{name: "empty events", events: []string{}},
		{name: "mix of valid and invalid", events: []string{"message.received", "bogus"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.RegisterWebhook(ctx, "svc-agent2", "https://example.com/hook", tt.events, "secret")
			if err == nil {
				t.Error("expected error for invalid events, got nil")
			}
		})
	}
}

func TestRegisterWebhook_InvalidURL(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	// Disallow HTTP to test HTTPS enforcement
	svc := NewWebhookService(store, false, true)
	ctx := context.Background()
	seedAgent(t, db, "svc-agent3")

	tests := []struct {
		name string
		url  string
	}{
		{name: "HTTP when not allowed", url: "http://example.com/hook"},
		{name: "FTP scheme", url: "ftp://example.com/hook"},
		{name: "no scheme", url: "example.com/hook"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.RegisterWebhook(ctx, "svc-agent3", tt.url, []string{"message.received"}, "secret")
			if err == nil {
				t.Errorf("expected error for URL %q, got nil", tt.url)
			}
		})
	}
}

func TestRegisterWebhook_MaxWebhooks(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	svc := NewWebhookService(store, true, true)
	ctx := context.Background()
	seedAgent(t, db, "svc-agent4")

	// Register MaxWebhooksPerAgent webhooks
	for i := 0; i < MaxWebhooksPerAgent; i++ {
		url := "https://example.com/hook" + string(rune('a'+i))
		_, err := svc.RegisterWebhook(ctx, "svc-agent4", url, []string{"message.received"}, "secret")
		if err != nil {
			t.Fatalf("register webhook %d: %v", i, err)
		}
	}

	// Next one should fail
	_, err := svc.RegisterWebhook(ctx, "svc-agent4", "https://example.com/one-too-many", []string{"message.received"}, "secret")
	if err == nil {
		t.Error("expected error when exceeding max webhooks, got nil")
	}
}

func TestDeleteWebhook_Service(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	svc := NewWebhookService(store, true, true)
	ctx := context.Background()
	seedAgent(t, db, "svc-agent5")
	seedAgent(t, db, "svc-agent6")

	wh, err := svc.RegisterWebhook(ctx, "svc-agent5", "https://example.com/del", []string{"message.received"}, "secret")
	if err != nil {
		t.Fatal(err)
	}

	// Wrong owner
	err = svc.DeleteWebhook(ctx, "svc-agent6", wh.ID)
	if err == nil {
		t.Error("expected error when deleting with wrong owner")
	}

	// Correct owner
	err = svc.DeleteWebhook(ctx, "svc-agent5", wh.ID)
	if err != nil {
		t.Fatalf("DeleteWebhook() error = %v", err)
	}

	// Verify deleted
	webhooks, err := svc.ListWebhooks(ctx, "svc-agent5")
	if err != nil {
		t.Fatal(err)
	}
	if len(webhooks) != 0 {
		t.Errorf("expected 0 webhooks after delete, got %d", len(webhooks))
	}
}

func TestListWebhooks(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	svc := NewWebhookService(store, true, true)
	ctx := context.Background()
	seedAgent(t, db, "svc-agent7")

	// Empty list initially
	webhooks, err := svc.ListWebhooks(ctx, "svc-agent7")
	if err != nil {
		t.Fatal(err)
	}
	if len(webhooks) != 0 {
		t.Errorf("expected 0 webhooks initially, got %d", len(webhooks))
	}

	// Register two
	for _, url := range []string{"https://example.com/list1", "https://example.com/list2"} {
		if _, err := svc.RegisterWebhook(ctx, "svc-agent7", url, []string{"message.received"}, "secret"); err != nil {
			t.Fatal(err)
		}
	}

	webhooks, err = svc.ListWebhooks(ctx, "svc-agent7")
	if err != nil {
		t.Fatal(err)
	}
	if len(webhooks) != 2 {
		t.Errorf("expected 2 webhooks, got %d", len(webhooks))
	}
}

func TestRecordFailure_AutoDisable(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	svc := NewWebhookService(store, true, true)
	ctx := context.Background()
	seedAgent(t, db, "svc-agent8")

	wh, err := svc.RegisterWebhook(ctx, "svc-agent8", "https://example.com/fail", []string{"message.received"}, "secret")
	if err != nil {
		t.Fatal(err)
	}

	// Record failures up to threshold
	for i := 0; i < AutoDisableThreshold; i++ {
		if err := svc.RecordFailure(ctx, wh.ID); err != nil {
			t.Fatalf("RecordFailure iteration %d: %v", i, err)
		}
	}

	// Verify webhook is now disabled
	got, err := store.GetWebhookByID(ctx, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != WebhookStatusDisabled {
		t.Errorf("Status = %q after %d failures, want %q", got.Status, AutoDisableThreshold, WebhookStatusDisabled)
	}
	if got.ConsecutiveFailures < AutoDisableThreshold {
		t.Errorf("ConsecutiveFailures = %d, want >= %d", got.ConsecutiveFailures, AutoDisableThreshold)
	}
}

func TestRecordSuccess_ResetsFailures(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteWebhookStore(db)
	svc := NewWebhookService(store, true, true)
	ctx := context.Background()
	seedAgent(t, db, "svc-agent9")

	wh, err := svc.RegisterWebhook(ctx, "svc-agent9", "https://example.com/success", []string{"message.received"}, "secret")
	if err != nil {
		t.Fatal(err)
	}

	// Record a few failures
	for i := 0; i < 10; i++ {
		if err := svc.RecordFailure(ctx, wh.ID); err != nil {
			t.Fatal(err)
		}
	}

	// Record success
	if err := svc.RecordSuccess(ctx, wh.ID); err != nil {
		t.Fatalf("RecordSuccess() error = %v", err)
	}

	got, err := store.GetWebhookByID(ctx, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d after success, want 0", got.ConsecutiveFailures)
	}
}
