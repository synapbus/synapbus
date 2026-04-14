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
//
// Uses separate write + read connection pools when available. The
// write pool has MaxOpenConns=1 so long-running writes (reactor, trace
// recorder) would otherwise serialize every session lookup and wedge
// the UI. Reads (GetSession) go through the read pool; writes
// (CreateSession, DeleteSession, bumping last_active_at) still use
// the write pool. If no read pool is wired, both fall back to the
// same handle for backward compat.
type SQLiteSessionStore struct {
	db        *sql.DB
	readDB    *sql.DB
}

// NewSQLiteSessionStore creates a new SQLite-backed session store.
// When only a write handle is provided the same handle is used for
// both reads and writes (pre-spec-018 behaviour).
func NewSQLiteSessionStore(db *sql.DB) *SQLiteSessionStore {
	return &SQLiteSessionStore{db: db, readDB: db}
}

// NewSQLiteSessionStoreWithRead creates a SessionStore that routes
// GetSession SELECTs through readDB while using writeDB for inserts,
// deletes, and the last_active_at bump.
func NewSQLiteSessionStoreWithRead(writeDB, readDB *sql.DB) *SQLiteSessionStore {
	if readDB == nil {
		readDB = writeDB
	}
	return &SQLiteSessionStore{db: writeDB, readDB: readDB}
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

// GetSession retrieves a session by its ID. Returns ErrSessionExpired
// if the session has expired.
//
// Reads go through the read pool (high MaxOpenConns, query_only=ON) so
// session validation on every authenticated request never contends
// with the single-connection write pool. The last_active_at bump is
// fire-and-forget on a background goroutine — it's a liveness
// indicator only and must not block the HTTP handler.
func (s *SQLiteSessionStore) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	session := &Session{}
	err := s.readDB.QueryRowContext(ctx,
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
		// Clean up the expired session (async — not on the hot path).
		go func(id string) {
			bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.DeleteSession(bg, id)
		}(sessionID)
		return nil, ErrSessionExpired
	}

	// Update last_active_at in the background so the HTTP handler
	// doesn't wait on the write pool for a non-critical liveness bump.
	go func(id string) {
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.db.ExecContext(bg,
			`UPDATE sessions SET last_active_at = CURRENT_TIMESTAMP WHERE session_id = ?`,
			id,
		)
	}(sessionID)

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
