package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// SessionStore defines the storage interface for session operations.
type SessionStore interface {
	CreateSession(ctx context.Context, userID int64, lifetime time.Duration) (*Session, error)
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	DeleteSessionsByUser(ctx context.Context, userID int64) error
	DeleteSessionsByUserExcept(ctx context.Context, userID int64, exceptSessionID string) error
	CleanupExpired(ctx context.Context) (int64, error)
}

// SQLiteSessionStore implements SessionStore using SQLite.
type SQLiteSessionStore struct {
	db *sql.DB
}

// NewSQLiteSessionStore creates a new SQLite-backed session store.
func NewSQLiteSessionStore(db *sql.DB) *SQLiteSessionStore {
	return &SQLiteSessionStore{db: db}
}

// CreateSession creates a new session with a cryptographically random session ID.
func (s *SQLiteSessionStore) CreateSession(ctx context.Context, userID int64, lifetime time.Duration) (*Session, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(lifetime)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sessions (session_id, user_id, created_at, expires_at, last_active_at)
		 VALUES (?, ?, ?, ?, ?)`,
		sessionID, userID, now, expiresAt, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	return &Session{
		SessionID:    sessionID,
		UserID:       userID,
		CreatedAt:    now,
		ExpiresAt:    expiresAt,
		LastActiveAt: now,
	}, nil
}

// GetSession retrieves a session by its ID. Returns ErrSessionExpired if the session has expired.
func (s *SQLiteSessionStore) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	session := &Session{}
	err := s.db.QueryRowContext(ctx,
		`SELECT session_id, user_id, created_at, expires_at, last_active_at
		 FROM sessions WHERE session_id = ?`, sessionID,
	).Scan(&session.SessionID, &session.UserID, &session.CreatedAt,
		&session.ExpiresAt, &session.LastActiveAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("query session: %w", err)
	}

	if time.Now().After(session.ExpiresAt) {
		// Clean up the expired session
		s.DeleteSession(ctx, sessionID)
		return nil, ErrSessionExpired
	}

	// Update last_active_at
	s.db.ExecContext(ctx,
		`UPDATE sessions SET last_active_at = CURRENT_TIMESTAMP WHERE session_id = ?`,
		sessionID,
	)

	return session, nil
}

// DeleteSession removes a session.
func (s *SQLiteSessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE session_id = ?", sessionID,
	)
	return err
}

// DeleteSessionsByUser removes all sessions for a user.
func (s *SQLiteSessionStore) DeleteSessionsByUser(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE user_id = ?", userID,
	)
	return err
}

// DeleteSessionsByUserExcept removes all sessions for a user except the specified one.
func (s *SQLiteSessionStore) DeleteSessionsByUserExcept(ctx context.Context, userID int64, exceptSessionID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE user_id = ? AND session_id != ?", userID, exceptSessionID,
	)
	return err
}

// CleanupExpired removes all expired sessions and returns the count removed.
func (s *SQLiteSessionStore) CleanupExpired(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE expires_at < ?", time.Now(),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// generateSessionID creates a cryptographically random session identifier.
func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
