package apikeys

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// keyPrefix is prepended to all generated API keys.
const keyPrefixTag = "sb_"

// Store defines the storage interface for API key operations.
type Store interface {
	CreateKey(ctx context.Context, req CreateKeyRequest) (*APIKey, string, error)
	ListKeys(ctx context.Context, userID int64) ([]APIKey, error)
	GetByID(ctx context.Context, id int64) (*APIKey, error)
	Authenticate(ctx context.Context, rawKey string) (*APIKey, error)
	RevokeKey(ctx context.Context, id int64) error
	UpdateLastUsed(ctx context.Context, id int64) error
	DeleteKey(ctx context.Context, id int64) error
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite-backed API key store.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// CreateKey generates a new API key, hashes it, and stores it.
// Returns the APIKey record and the raw key (shown once).
func (s *SQLiteStore) CreateKey(ctx context.Context, req CreateKeyRequest) (*APIKey, string, error) {
	// Generate random key: sb_ + 48 hex chars (24 random bytes)
	rawBytes := make([]byte, 24)
	if _, err := rand.Read(rawBytes); err != nil {
		return nil, "", fmt.Errorf("generate random key: %w", err)
	}
	rawHex := hex.EncodeToString(rawBytes)
	fullKey := keyPrefixTag + rawHex
	prefix := keyPrefixTag + rawHex[:8]

	// Hash the full key with bcrypt
	hash, err := bcrypt.GenerateFromPassword([]byte(fullKey), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash API key: %w", err)
	}

	// Marshal permissions and allowed channels to JSON
	permsJSON, err := json.Marshal(req.Permissions)
	if err != nil {
		return nil, "", fmt.Errorf("marshal permissions: %w", err)
	}

	channels := req.AllowedChannels
	if channels == nil {
		channels = []string{}
	}
	channelsJSON, err := json.Marshal(channels)
	if err != nil {
		return nil, "", fmt.Errorf("marshal allowed channels: %w", err)
	}

	var agentID sql.NullInt64
	if req.AgentID != nil {
		agentID = sql.NullInt64{Int64: *req.AgentID, Valid: true}
	}

	var expiresAt sql.NullTime
	if req.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *req.ExpiresAt, Valid: true}
	}

	readOnlyInt := 0
	if req.ReadOnly {
		readOnlyInt = 1
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO api_keys (user_id, agent_id, name, key_prefix, key_hash, permissions, allowed_channels, read_only, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		req.UserID, agentID, req.Name, prefix, string(hash),
		string(permsJSON), string(channelsJSON), readOnlyInt, expiresAt,
	)
	if err != nil {
		return nil, "", fmt.Errorf("insert api_key: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, "", fmt.Errorf("get api_key id: %w", err)
	}

	key := &APIKey{
		ID:              id,
		UserID:          req.UserID,
		AgentID:         req.AgentID,
		Name:            req.Name,
		KeyPrefix:       prefix,
		Permissions:     req.Permissions,
		AllowedChannels: channels,
		ReadOnly:        req.ReadOnly,
		ExpiresAt:       req.ExpiresAt,
		CreatedAt:       time.Now(),
	}

	return key, fullKey, nil
}

// ListKeys returns all API keys for a user (excluding revoked keys' hashes).
func (s *SQLiteStore) ListKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, agent_id, name, key_prefix, permissions, allowed_channels,
		        read_only, expires_at, last_used_at, created_at, revoked_at
		 FROM api_keys
		 WHERE user_id = ? AND revoked_at IS NULL
		 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query api_keys: %w", err)
	}
	defer rows.Close()

	return scanAPIKeys(rows)
}

// GetByID returns a single API key by ID.
func (s *SQLiteStore) GetByID(ctx context.Context, id int64) (*APIKey, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, agent_id, name, key_prefix, permissions, allowed_channels,
		        read_only, expires_at, last_used_at, created_at, revoked_at
		 FROM api_keys
		 WHERE id = ?`,
		id,
	)
	return scanAPIKey(row)
}

// Authenticate verifies a raw API key against all non-revoked, non-expired keys.
// Returns the matching APIKey or an error.
func (s *SQLiteStore) Authenticate(ctx context.Context, rawKey string) (*APIKey, error) {
	// Only try keys with matching prefix for efficiency
	if len(rawKey) < 11 {
		return nil, fmt.Errorf("invalid API key format")
	}
	prefix := rawKey[:11] // "sb_" + 8 hex chars

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, agent_id, name, key_prefix, key_hash, permissions, allowed_channels,
		        read_only, expires_at, last_used_at, created_at, revoked_at
		 FROM api_keys
		 WHERE key_prefix = ? AND revoked_at IS NULL
		   AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`,
		prefix,
	)
	if err != nil {
		return nil, fmt.Errorf("query api_keys for auth: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key APIKey
		var agentID sql.NullInt64
		var expiresAt, lastUsedAt, revokedAt sql.NullTime
		var permsStr, channelsStr, keyHash string
		var readOnlyInt int

		err := rows.Scan(
			&key.ID, &key.UserID, &agentID, &key.Name, &key.KeyPrefix,
			&keyHash, &permsStr, &channelsStr,
			&readOnlyInt, &expiresAt, &lastUsedAt, &key.CreatedAt, &revokedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan api_key: %w", err)
		}

		// bcrypt compare
		if err := bcrypt.CompareHashAndPassword([]byte(keyHash), []byte(rawKey)); err != nil {
			continue
		}

		// Match found - populate fields
		if agentID.Valid {
			key.AgentID = &agentID.Int64
		}
		if expiresAt.Valid {
			key.ExpiresAt = &expiresAt.Time
		}
		if lastUsedAt.Valid {
			key.LastUsedAt = &lastUsedAt.Time
		}
		if revokedAt.Valid {
			key.RevokedAt = &revokedAt.Time
		}
		key.ReadOnly = readOnlyInt != 0

		if err := json.Unmarshal([]byte(permsStr), &key.Permissions); err != nil {
			return nil, fmt.Errorf("unmarshal permissions: %w", err)
		}
		if err := json.Unmarshal([]byte(channelsStr), &key.AllowedChannels); err != nil {
			key.AllowedChannels = []string{}
		}

		return &key, nil
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate api_keys: %w", err)
	}

	return nil, fmt.Errorf("invalid API key")
}

// RevokeKey soft-deletes an API key by setting revoked_at.
func (s *SQLiteStore) RevokeKey(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = CURRENT_TIMESTAMP WHERE id = ? AND revoked_at IS NULL`,
		id,
	)
	if err != nil {
		return fmt.Errorf("revoke api_key: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("API key not found or already revoked")
	}
	return nil
}

// UpdateLastUsed updates the last_used_at timestamp.
func (s *SQLiteStore) UpdateLastUsed(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	return err
}

// DeleteKey permanently removes an API key.
func (s *SQLiteStore) DeleteKey(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM api_keys WHERE id = ?`,
		id,
	)
	if err != nil {
		return fmt.Errorf("delete api_key: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("API key not found")
	}
	return nil
}

// scanAPIKeys scans multiple API key rows.
func scanAPIKeys(rows *sql.Rows) ([]APIKey, error) {
	var keys []APIKey
	for rows.Next() {
		var key APIKey
		var agentID sql.NullInt64
		var expiresAt, lastUsedAt, revokedAt sql.NullTime
		var permsStr, channelsStr string
		var readOnlyInt int

		err := rows.Scan(
			&key.ID, &key.UserID, &agentID, &key.Name, &key.KeyPrefix,
			&permsStr, &channelsStr,
			&readOnlyInt, &expiresAt, &lastUsedAt, &key.CreatedAt, &revokedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan api_key: %w", err)
		}

		if agentID.Valid {
			key.AgentID = &agentID.Int64
		}
		if expiresAt.Valid {
			key.ExpiresAt = &expiresAt.Time
		}
		if lastUsedAt.Valid {
			key.LastUsedAt = &lastUsedAt.Time
		}
		if revokedAt.Valid {
			key.RevokedAt = &revokedAt.Time
		}
		key.ReadOnly = readOnlyInt != 0

		if err := json.Unmarshal([]byte(permsStr), &key.Permissions); err != nil {
			return nil, fmt.Errorf("unmarshal permissions: %w", err)
		}
		if err := json.Unmarshal([]byte(channelsStr), &key.AllowedChannels); err != nil {
			key.AllowedChannels = []string{}
		}

		keys = append(keys, key)
	}
	if keys == nil {
		keys = []APIKey{}
	}
	return keys, rows.Err()
}

// scanAPIKey scans a single API key from sql.Row.
func scanAPIKey(row *sql.Row) (*APIKey, error) {
	var key APIKey
	var agentID sql.NullInt64
	var expiresAt, lastUsedAt, revokedAt sql.NullTime
	var permsStr, channelsStr string
	var readOnlyInt int

	err := row.Scan(
		&key.ID, &key.UserID, &agentID, &key.Name, &key.KeyPrefix,
		&permsStr, &channelsStr,
		&readOnlyInt, &expiresAt, &lastUsedAt, &key.CreatedAt, &revokedAt,
	)
	if err != nil {
		return nil, err
	}

	if agentID.Valid {
		key.AgentID = &agentID.Int64
	}
	if expiresAt.Valid {
		key.ExpiresAt = &expiresAt.Time
	}
	if lastUsedAt.Valid {
		key.LastUsedAt = &lastUsedAt.Time
	}
	if revokedAt.Valid {
		key.RevokedAt = &revokedAt.Time
	}
	key.ReadOnly = readOnlyInt != 0

	if err := json.Unmarshal([]byte(permsStr), &key.Permissions); err != nil {
		return nil, fmt.Errorf("unmarshal permissions: %w", err)
	}
	if err := json.Unmarshal([]byte(channelsStr), &key.AllowedChannels); err != nil {
		key.AllowedChannels = []string{}
	}

	return &key, nil
}
