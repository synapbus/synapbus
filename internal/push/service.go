package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Service manages Web Push notifications and VAPID key lifecycle.
type Service struct {
	store     Store
	vapidKeys *VAPIDKeys
	dataDir   string
	logger    *slog.Logger
	mu        sync.RWMutex
}

// VAPIDKeys holds the VAPID key pair for Web Push authentication.
type VAPIDKeys struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// Notification represents a push notification payload.
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag,omitempty"`
	URL   string `json:"url,omitempty"`
}

// NewService creates a new push notification service.
// It loads or generates VAPID keys from the data directory.
func NewService(store Store, dataDir string, logger *slog.Logger) (*Service, error) {
	s := &Service{
		store:   store,
		dataDir: dataDir,
		logger:  logger.With("component", "push"),
	}

	keys, err := s.loadOrGenerateVAPIDKeys()
	if err != nil {
		return nil, fmt.Errorf("load or generate VAPID keys: %w", err)
	}
	s.vapidKeys = keys

	s.logger.Info("push notification service initialized",
		"vapid_public_key", keys.PublicKey[:16]+"...",
	)

	return s, nil
}

// GetVAPIDPublicKey returns the VAPID public key for client subscription.
func (s *Service) GetVAPIDPublicKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vapidKeys.PublicKey
}

// Subscribe registers a push subscription for a user.
func (s *Service) Subscribe(ctx context.Context, userID int64, endpoint, keyP256dh, keyAuth, userAgent string) error {
	return s.store.Subscribe(ctx, userID, endpoint, keyP256dh, keyAuth, userAgent)
}

// Unsubscribe removes a push subscription by endpoint, scoped to user.
func (s *Service) Unsubscribe(ctx context.Context, userID int64, endpoint string) error {
	return s.store.Unsubscribe(ctx, userID, endpoint)
}

// SendToUser sends a push notification to all subscriptions for a user.
// It automatically removes subscriptions that return 410 Gone (unsubscribed).
func (s *Service) SendToUser(ctx context.Context, userID int64, notification Notification) error {
	subs, err := s.store.GetSubscriptions(ctx, userID)
	if err != nil {
		return fmt.Errorf("get subscriptions: %w", err)
	}

	if len(subs) == 0 {
		s.logger.Debug("no push subscriptions for user", "user_id", userID)
		return nil
	}

	payload, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	s.mu.RLock()
	vapidPrivate := s.vapidKeys.PrivateKey
	vapidPublic := s.vapidKeys.PublicKey
	s.mu.RUnlock()

	var sendErrors []error
	for _, sub := range subs {
		wpSub := &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys: webpush.Keys{
				P256dh: sub.KeyP256dh,
				Auth:   sub.KeyAuth,
			},
		}

		resp, err := webpush.SendNotification(payload, wpSub, &webpush.Options{
			VAPIDPrivateKey: vapidPrivate,
			VAPIDPublicKey:  vapidPublic,
			Subscriber:      "mailto:noreply@synapbus.local",
			TTL:             86400, // 24 hours
		})
		if err != nil {
			s.logger.Warn("push notification send failed",
				"user_id", userID,
				"endpoint", truncateEndpoint(sub.Endpoint),
				"error", err,
			)
			sendErrors = append(sendErrors, err)
			continue
		}
		resp.Body.Close()

		// Remove stale subscriptions
		if resp.StatusCode == http.StatusGone {
			s.logger.Info("removing stale push subscription",
				"user_id", userID,
				"endpoint", truncateEndpoint(sub.Endpoint),
			)
			if delErr := s.store.DeleteSubscription(ctx, sub.Endpoint); delErr != nil {
				s.logger.Warn("failed to delete stale subscription",
					"endpoint", truncateEndpoint(sub.Endpoint),
					"error", delErr,
				)
			}
			continue
		}

		if resp.StatusCode >= 400 {
			s.logger.Warn("push notification rejected",
				"user_id", userID,
				"endpoint", truncateEndpoint(sub.Endpoint),
				"status", resp.StatusCode,
			)
			sendErrors = append(sendErrors, fmt.Errorf("push endpoint returned %d", resp.StatusCode))

			// Also remove subscriptions that return 404 (endpoint no longer valid)
			if resp.StatusCode == http.StatusNotFound {
				if delErr := s.store.DeleteSubscription(ctx, sub.Endpoint); delErr != nil {
					s.logger.Warn("failed to delete invalid subscription",
						"endpoint", truncateEndpoint(sub.Endpoint),
						"error", delErr,
					)
				}
			}
			continue
		}

		s.logger.Debug("push notification sent",
			"user_id", userID,
			"endpoint", truncateEndpoint(sub.Endpoint),
			"status", resp.StatusCode,
		)
	}

	if len(sendErrors) > 0 {
		return fmt.Errorf("failed to send to %d/%d subscriptions", len(sendErrors), len(subs))
	}
	return nil
}

// vapidKeysPath returns the file path for the VAPID keys JSON file.
func (s *Service) vapidKeysPath() string {
	return filepath.Join(s.dataDir, "vapid_keys.json")
}

// loadOrGenerateVAPIDKeys loads existing VAPID keys from disk or generates new ones.
func (s *Service) loadOrGenerateVAPIDKeys() (*VAPIDKeys, error) {
	keysPath := s.vapidKeysPath()

	// Try to load existing keys
	data, err := os.ReadFile(keysPath)
	if err == nil {
		var keys VAPIDKeys
		if err := json.Unmarshal(data, &keys); err == nil && keys.PublicKey != "" && keys.PrivateKey != "" {
			s.logger.Info("loaded existing VAPID keys", "path", keysPath)
			return &keys, nil
		}
		s.logger.Warn("invalid VAPID keys file, regenerating", "path", keysPath)
	}

	// Generate new VAPID keys
	keys, err := generateVAPIDKeys()
	if err != nil {
		return nil, fmt.Errorf("generate VAPID keys: %w", err)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	// Save to disk
	data, err = json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal VAPID keys: %w", err)
	}

	if err := os.WriteFile(keysPath, data, 0o600); err != nil {
		return nil, fmt.Errorf("write VAPID keys: %w", err)
	}

	s.logger.Info("generated new VAPID keys", "path", keysPath)
	return keys, nil
}

// generateVAPIDKeys creates a new ECDSA P-256 key pair and encodes them
// as uncompressed (public) and raw (private) base64url strings for VAPID.
func generateVAPIDKeys() (*VAPIDKeys, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ECDSA key: %w", err)
	}

	// Encode public key as uncompressed point (0x04 || x || y)
	pubBytes := elliptic.Marshal(elliptic.P256(), privateKey.PublicKey.X, privateKey.PublicKey.Y)
	publicKeyB64 := base64.RawURLEncoding.EncodeToString(pubBytes)

	// Encode private key as raw big-endian bytes (32 bytes, zero-padded)
	privBytes := privateKey.D.Bytes()
	// Pad to 32 bytes if needed
	padded := make([]byte, 32)
	copy(padded[32-len(privBytes):], privBytes)
	privateKeyB64 := base64.RawURLEncoding.EncodeToString(padded)

	return &VAPIDKeys{
		PublicKey:  publicKeyB64,
		PrivateKey: privateKeyB64,
	}, nil
}

// ParseVAPIDPrivateKey decodes a base64url-encoded VAPID private key
// into an *ecdsa.PrivateKey. Useful for testing.
func ParseVAPIDPrivateKey(b64 string) (*ecdsa.PrivateKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}

	d := new(big.Int).SetBytes(raw)
	curve := elliptic.P256()
	x, y := curve.ScalarBaseMult(raw)

	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: curve,
			X:     x,
			Y:     y,
		},
		D: d,
	}, nil
}

// truncateEndpoint returns a truncated version of the endpoint URL for logging.
func truncateEndpoint(endpoint string) string {
	if len(endpoint) <= 40 {
		return endpoint
	}
	return endpoint[:37] + "..."
}
