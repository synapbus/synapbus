// Package push provides Web Push notification support for SynapBus.
package push

import (
	"context"
	"database/sql"
)

// Store defines the persistence interface for push subscriptions.
type Store interface {
	// Subscribe registers a push subscription for a user.
	// If the endpoint already exists, it updates the keys and user agent.
	Subscribe(ctx context.Context, userID int64, endpoint, keyP256dh, keyAuth, userAgent string) error

	// Unsubscribe removes a push subscription by endpoint, scoped to user.
	Unsubscribe(ctx context.Context, userID int64, endpoint string) error

	// GetSubscriptions returns all push subscriptions for a user.
	GetSubscriptions(ctx context.Context, userID int64) ([]Subscription, error)

	// DeleteSubscription removes a push subscription by endpoint.
	// This is used to clean up stale subscriptions (e.g., 410 Gone responses).
	DeleteSubscription(ctx context.Context, endpoint string) error
}

// Subscription represents a Web Push subscription stored in the database.
type Subscription struct {
	ID        int64
	UserID    int64
	Endpoint  string
	KeyP256dh string
	KeyAuth   string
	UserAgent string
	CreatedAt string
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite-backed push subscription store.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// Subscribe registers or updates a push subscription for a user.
func (s *SQLiteStore) Subscribe(ctx context.Context, userID int64, endpoint, keyP256dh, keyAuth, userAgent string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (user_id, endpoint, key_p256dh, key_auth, user_agent)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET
		   key_p256dh = excluded.key_p256dh,
		   key_auth = excluded.key_auth,
		   user_agent = excluded.user_agent,
		   user_id = excluded.user_id`,
		userID, endpoint, keyP256dh, keyAuth, userAgent,
	)
	return err
}

// Unsubscribe removes a push subscription by endpoint, scoped to the owning user.
func (s *SQLiteStore) Unsubscribe(ctx context.Context, userID int64, endpoint string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE user_id = ? AND endpoint = ?`,
		userID, endpoint,
	)
	return err
}

// GetSubscriptions returns all push subscriptions for a user.
func (s *SQLiteStore) GetSubscriptions(ctx context.Context, userID int64) ([]Subscription, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, endpoint, key_p256dh, key_auth, user_agent, created_at
		 FROM push_subscriptions
		 WHERE user_id = ?
		 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []Subscription
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.ID, &sub.UserID, &sub.Endpoint, &sub.KeyP256dh, &sub.KeyAuth, &sub.UserAgent, &sub.CreatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// DeleteSubscription removes a push subscription by endpoint (any user).
// Used for stale subscription cleanup (e.g., 410 Gone responses).
func (s *SQLiteStore) DeleteSubscription(ctx context.Context, endpoint string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE endpoint = ?`,
		endpoint,
	)
	return err
}
