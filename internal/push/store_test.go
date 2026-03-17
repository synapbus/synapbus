package push

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/storage"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
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

	// Seed a test user
	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testuser', 'hash', 'Test User')`)
	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (2, 'otheruser', 'hash', 'Other User')`)

	return db
}

func TestSQLiteStore_Subscribe(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	err := store.Subscribe(ctx, 1, "https://push.example.com/sub1", "p256dh-key-1", "auth-key-1", "TestAgent/1.0")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	subs, err := store.GetSubscriptions(ctx, 1)
	if err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d subscriptions, want 1", len(subs))
	}

	sub := subs[0]
	if sub.Endpoint != "https://push.example.com/sub1" {
		t.Errorf("endpoint = %q, want %q", sub.Endpoint, "https://push.example.com/sub1")
	}
	if sub.KeyP256dh != "p256dh-key-1" {
		t.Errorf("key_p256dh = %q, want %q", sub.KeyP256dh, "p256dh-key-1")
	}
	if sub.KeyAuth != "auth-key-1" {
		t.Errorf("key_auth = %q, want %q", sub.KeyAuth, "auth-key-1")
	}
	if sub.UserAgent != "TestAgent/1.0" {
		t.Errorf("user_agent = %q, want %q", sub.UserAgent, "TestAgent/1.0")
	}
	if sub.UserID != 1 {
		t.Errorf("user_id = %d, want 1", sub.UserID)
	}
}

func TestSQLiteStore_SubscribeUpsert(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	// First subscription
	err := store.Subscribe(ctx, 1, "https://push.example.com/sub1", "old-p256dh", "old-auth", "OldAgent")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Update same endpoint with new keys
	err = store.Subscribe(ctx, 1, "https://push.example.com/sub1", "new-p256dh", "new-auth", "NewAgent")
	if err != nil {
		t.Fatalf("Subscribe upsert: %v", err)
	}

	subs, err := store.GetSubscriptions(ctx, 1)
	if err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d subscriptions, want 1 (should upsert)", len(subs))
	}
	if subs[0].KeyP256dh != "new-p256dh" {
		t.Errorf("key_p256dh = %q, want %q", subs[0].KeyP256dh, "new-p256dh")
	}
	if subs[0].KeyAuth != "new-auth" {
		t.Errorf("key_auth = %q, want %q", subs[0].KeyAuth, "new-auth")
	}
	if subs[0].UserAgent != "NewAgent" {
		t.Errorf("user_agent = %q, want %q", subs[0].UserAgent, "NewAgent")
	}
}

func TestSQLiteStore_MultipleSubscriptions(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Register multiple subscriptions for same user
	for i := 0; i < 3; i++ {
		endpoint := fmt.Sprintf("https://push.example.com/sub%d", i)
		err := store.Subscribe(ctx, 1, endpoint, fmt.Sprintf("p256dh-%d", i), fmt.Sprintf("auth-%d", i), "Agent")
		if err != nil {
			t.Fatalf("Subscribe(%d): %v", i, err)
		}
	}

	subs, err := store.GetSubscriptions(ctx, 1)
	if err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if len(subs) != 3 {
		t.Errorf("got %d subscriptions, want 3", len(subs))
	}
}

func TestSQLiteStore_Unsubscribe(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	store.Subscribe(ctx, 1, "https://push.example.com/sub1", "p256dh", "auth", "Agent")
	store.Subscribe(ctx, 1, "https://push.example.com/sub2", "p256dh2", "auth2", "Agent")

	err := store.Unsubscribe(ctx, 1, "https://push.example.com/sub1")
	if err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	subs, err := store.GetSubscriptions(ctx, 1)
	if err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d subscriptions, want 1", len(subs))
	}
	if subs[0].Endpoint != "https://push.example.com/sub2" {
		t.Errorf("remaining endpoint = %q, want sub2", subs[0].Endpoint)
	}
}

func TestSQLiteStore_DeleteSubscription(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	store.Subscribe(ctx, 1, "https://push.example.com/sub1", "p256dh", "auth", "Agent")

	err := store.DeleteSubscription(ctx, "https://push.example.com/sub1")
	if err != nil {
		t.Fatalf("DeleteSubscription: %v", err)
	}

	subs, err := store.GetSubscriptions(ctx, 1)
	if err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("got %d subscriptions, want 0", len(subs))
	}
}

func TestSQLiteStore_UserIsolation(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	store.Subscribe(ctx, 1, "https://push.example.com/user1-sub", "p256dh1", "auth1", "Agent")
	store.Subscribe(ctx, 2, "https://push.example.com/user2-sub", "p256dh2", "auth2", "Agent")

	subs1, err := store.GetSubscriptions(ctx, 1)
	if err != nil {
		t.Fatalf("GetSubscriptions(1): %v", err)
	}
	if len(subs1) != 1 {
		t.Errorf("user 1: got %d subscriptions, want 1", len(subs1))
	}

	subs2, err := store.GetSubscriptions(ctx, 2)
	if err != nil {
		t.Fatalf("GetSubscriptions(2): %v", err)
	}
	if len(subs2) != 1 {
		t.Errorf("user 2: got %d subscriptions, want 1", len(subs2))
	}
}

func TestSQLiteStore_GetSubscriptionsEmpty(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	subs, err := store.GetSubscriptions(ctx, 999)
	if err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if subs != nil {
		t.Errorf("got %v, want nil for empty result", subs)
	}
}

func TestSQLiteStore_UnsubscribeNonexistent(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Should not error when deleting non-existent endpoint
	err := store.Unsubscribe(ctx, 1, "https://push.example.com/nonexistent")
	if err != nil {
		t.Fatalf("Unsubscribe non-existent: %v", err)
	}
}
