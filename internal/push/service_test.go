package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// mockStore implements Store for testing without a database.
type mockStore struct {
	subscriptions map[int64][]Subscription
	deleted       []string
}

func newMockStore() *mockStore {
	return &mockStore{
		subscriptions: make(map[int64][]Subscription),
	}
}

func (m *mockStore) Subscribe(_ context.Context, userID int64, endpoint, keyP256dh, keyAuth, userAgent string) error {
	// Upsert: remove existing endpoint first
	subs := m.subscriptions[userID]
	for i, s := range subs {
		if s.Endpoint == endpoint {
			subs = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	m.subscriptions[userID] = append(subs, Subscription{
		UserID:    userID,
		Endpoint:  endpoint,
		KeyP256dh: keyP256dh,
		KeyAuth:   keyAuth,
		UserAgent: userAgent,
	})
	return nil
}

func (m *mockStore) Unsubscribe(_ context.Context, userID int64, endpoint string) error {
	subs := m.subscriptions[userID]
	for i, s := range subs {
		if s.Endpoint == endpoint {
			m.subscriptions[userID] = append(subs[:i], subs[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *mockStore) GetSubscriptions(_ context.Context, userID int64) ([]Subscription, error) {
	return m.subscriptions[userID], nil
}

func (m *mockStore) DeleteSubscription(_ context.Context, endpoint string) error {
	m.deleted = append(m.deleted, endpoint)
	// Find and remove from any user's subscriptions
	for uid, subs := range m.subscriptions {
		for i, s := range subs {
			if s.Endpoint == endpoint {
				m.subscriptions[uid] = append(subs[:i], subs[i+1:]...)
				return nil
			}
		}
	}
	return nil
}

func TestVAPIDKeyGeneration(t *testing.T) {
	keys, err := generateVAPIDKeys()
	if err != nil {
		t.Fatalf("generateVAPIDKeys: %v", err)
	}

	if keys.PublicKey == "" {
		t.Error("public key is empty")
	}
	if keys.PrivateKey == "" {
		t.Error("private key is empty")
	}

	// Public key should be 65 bytes (uncompressed point) base64url encoded
	// 65 bytes -> ceil(65*4/3) = 87 chars without padding
	if len(keys.PublicKey) < 80 {
		t.Errorf("public key too short: %d chars", len(keys.PublicKey))
	}

	// Private key should be 32 bytes base64url encoded
	// 32 bytes -> ceil(32*4/3) = 43 chars without padding
	if len(keys.PrivateKey) < 40 {
		t.Errorf("private key too short: %d chars", len(keys.PrivateKey))
	}

	// Should be parseable back into an ECDSA key
	privKey, err := ParseVAPIDPrivateKey(keys.PrivateKey)
	if err != nil {
		t.Fatalf("ParseVAPIDPrivateKey: %v", err)
	}
	if privKey.Curve == nil {
		t.Error("parsed key has nil curve")
	}
}

func TestVAPIDKeyPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	logger := slog.Default()
	store := newMockStore()

	// First service creation should generate keys
	svc1, err := NewService(store, tmpDir, logger)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	key1 := svc1.GetVAPIDPublicKey()
	if key1 == "" {
		t.Fatal("VAPID public key is empty")
	}

	// Verify file was written
	keysPath := filepath.Join(tmpDir, "vapid_keys.json")
	data, err := os.ReadFile(keysPath)
	if err != nil {
		t.Fatalf("read VAPID keys file: %v", err)
	}

	var savedKeys VAPIDKeys
	if err := json.Unmarshal(data, &savedKeys); err != nil {
		t.Fatalf("unmarshal saved keys: %v", err)
	}
	if savedKeys.PublicKey != key1 {
		t.Errorf("saved public key = %q, service key = %q", savedKeys.PublicKey, key1)
	}

	// Second service creation should load the same keys
	svc2, err := NewService(store, tmpDir, logger)
	if err != nil {
		t.Fatalf("NewService (second): %v", err)
	}
	key2 := svc2.GetVAPIDPublicKey()
	if key2 != key1 {
		t.Errorf("second service has different key: %q vs %q", key2, key1)
	}
}

func TestVAPIDKeyFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	logger := slog.Default()
	store := newMockStore()

	_, err := NewService(store, tmpDir, logger)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	keysPath := filepath.Join(tmpDir, "vapid_keys.json")
	info, err := os.Stat(keysPath)
	if err != nil {
		t.Fatalf("stat VAPID keys file: %v", err)
	}

	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		t.Errorf("VAPID keys file has overly permissive mode: %o (want 0600)", perm)
	}
}

func TestSendToUser_NoSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	logger := slog.Default()
	store := newMockStore()

	svc, err := NewService(store, tmpDir, logger)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Sending to a user with no subscriptions should not error
	err = svc.SendToUser(context.Background(), 999, Notification{
		Title: "Test",
		Body:  "Test body",
	})
	if err != nil {
		t.Errorf("SendToUser with no subs: %v", err)
	}
}

// generateTestSubscriptionKeys creates a valid ECDSA P-256 key pair
// encoded as base64url for use as Web Push subscription keys in tests.
func generateTestSubscriptionKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	pubBytes := elliptic.Marshal(elliptic.P256(), privKey.PublicKey.X, privKey.PublicKey.Y)
	p256dh = base64.RawURLEncoding.EncodeToString(pubBytes)

	authBytes := make([]byte, 16)
	rand.Read(authBytes)
	auth = base64.RawURLEncoding.EncodeToString(authBytes)
	return
}

func TestSendToUser_GoneSubscription(t *testing.T) {
	// Set up a fake push endpoint that returns 410 Gone
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusGone)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	logger := slog.Default()
	store := newMockStore()

	svc, err := NewService(store, tmpDir, logger)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	p256dh, authKey := generateTestSubscriptionKeys(t)
	store.Subscribe(context.Background(), 1, server.URL+"/push", p256dh, authKey, "TestAgent")

	// Send notification — should succeed but remove the stale subscription
	_ = svc.SendToUser(context.Background(), 1, Notification{
		Title: "Test",
		Body:  "Test body",
	})

	if requestCount.Load() == 0 {
		t.Fatal("expected at least one request to push endpoint")
	}

	// The stale subscription should have been deleted
	if len(store.deleted) != 1 {
		t.Errorf("expected 1 deleted subscription, got %d", len(store.deleted))
	}
	if len(store.deleted) > 0 && store.deleted[0] != server.URL+"/push" {
		t.Errorf("deleted wrong endpoint: %q", store.deleted[0])
	}
}

func TestSendToUser_NotFoundSubscription(t *testing.T) {
	// Set up a fake push endpoint that returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	logger := slog.Default()
	store := newMockStore()

	svc, err := NewService(store, tmpDir, logger)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	p256dh, authKey := generateTestSubscriptionKeys(t)
	store.Subscribe(context.Background(), 1, server.URL+"/push", p256dh, authKey, "TestAgent")

	_ = svc.SendToUser(context.Background(), 1, Notification{
		Title: "Test",
		Body:  "Test body",
	})

	// 404 should also trigger deletion
	if len(store.deleted) != 1 {
		t.Errorf("expected 1 deleted subscription, got %d", len(store.deleted))
	}
}

func TestServiceSubscribeUnsubscribe(t *testing.T) {
	tmpDir := t.TempDir()
	logger := slog.Default()
	store := newMockStore()

	svc, err := NewService(store, tmpDir, logger)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	ctx := context.Background()

	// Subscribe
	err = svc.Subscribe(ctx, 1, "https://push.example.com/test", "p256dh", "auth", "Agent")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	subs := store.subscriptions[1]
	if len(subs) != 1 {
		t.Fatalf("got %d subscriptions, want 1", len(subs))
	}

	// Unsubscribe
	err = svc.Unsubscribe(ctx, 1, "https://push.example.com/test")
	if err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	subs = store.subscriptions[1]
	if len(subs) != 0 {
		t.Errorf("got %d subscriptions after unsubscribe, want 0", len(subs))
	}
}

func TestTruncateEndpoint(t *testing.T) {
	short := "https://short.url"
	long := "https://push.services.mozilla.com/wpush/v2/very-long-subscription-id-that-goes-on-and-on"

	got := truncateEndpoint(short)
	if got != short {
		t.Errorf("truncateEndpoint(short) = %q, want %q", got, short)
	}

	got = truncateEndpoint(long)
	if len(got) != 40 {
		t.Errorf("truncateEndpoint(long) length = %d, want 40", len(got))
	}
	if got[len(got)-3:] != "..." {
		t.Errorf("truncateEndpoint(long) should end with '...', got %q", got)
	}

	got = truncateEndpoint("")
	if got != "" {
		t.Errorf("truncateEndpoint('') = %q, want ''", got)
	}
}
