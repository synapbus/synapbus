package idp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// UserIdentity represents an external identity linked to a local user.
type UserIdentity struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Provider    string    `json:"provider"`
	ExternalID  string    `json:"external_id"`
	Email       string    `json:"email,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	RawClaims   string    `json:"raw_claims"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// UserIdentityStore manages the user_identities table.
type UserIdentityStore struct {
	db *sql.DB
}

// NewUserIdentityStore creates a new identity store backed by SQLite.
func NewUserIdentityStore(db *sql.DB) *UserIdentityStore {
	return &UserIdentityStore{db: db}
}

// FindByProvider looks up a user ID by provider name and external ID.
// Returns sql.ErrNoRows if not found.
func (s *UserIdentityStore) FindByProvider(ctx context.Context, provider, externalID string) (int64, error) {
	var userID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM user_identities WHERE provider = ? AND external_id = ?`,
		provider, externalID,
	).Scan(&userID)
	if err != nil {
		return 0, err
	}
	return userID, nil
}

// Create links an external identity to a local user.
func (s *UserIdentityStore) Create(ctx context.Context, userID int64, provider, externalID, email, displayName string, rawClaims map[string]any) error {
	claimsJSON, err := json.Marshal(rawClaims)
	if err != nil {
		claimsJSON = []byte("{}")
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO user_identities (user_id, provider, external_id, email, display_name, raw_claims, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		userID, provider, externalID, email, displayName, string(claimsJSON),
	)
	if err != nil {
		return fmt.Errorf("create identity: %w", err)
	}
	return nil
}

// ListByUser returns all external identities linked to a user.
func (s *UserIdentityStore) ListByUser(ctx context.Context, userID int64) ([]UserIdentity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, provider, external_id, email, display_name, raw_claims, created_at, updated_at
		 FROM user_identities WHERE user_id = ? ORDER BY created_at`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	defer rows.Close()

	var identities []UserIdentity
	for rows.Next() {
		var identity UserIdentity
		var email, displayName sql.NullString
		if err := rows.Scan(
			&identity.ID, &identity.UserID, &identity.Provider,
			&identity.ExternalID, &email, &displayName,
			&identity.RawClaims, &identity.CreatedAt, &identity.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan identity: %w", err)
		}
		identity.Email = email.String
		identity.DisplayName = displayName.String
		identities = append(identities, identity)
	}
	if identities == nil {
		identities = []UserIdentity{}
	}
	return identities, rows.Err()
}
