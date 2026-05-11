package messaging

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// DispatchTokenTTL is the lifetime of a freshly-issued dispatch token.
// Covers the dream-worker wallclock budget (10m default) plus dispatch
// slack.
const DispatchTokenTTL = 15 * time.Minute

// DispatchTokenStore manages single-use, owner-bound, job-bound tokens
// passed to a dream-worker agent through harness.Execute env vars. The
// agent presents the token on every memory_* MCP call; Validate is the
// single point where authorization for a consolidation action is
// resolved.
type DispatchTokenStore struct {
	db *sql.DB
	// now is overridable in tests.
	now func() time.Time
}

// NewDispatchTokenStore wraps a *sql.DB.
func NewDispatchTokenStore(db *sql.DB) *DispatchTokenStore {
	return &DispatchTokenStore{db: db, now: time.Now}
}

// Issue mints a new 32-byte random token bound to (ownerID, jobID) and
// inserts it into `memory_dispatch_tokens` with expires_at = now() + TTL.
func (s *DispatchTokenStore) Issue(ctx context.Context, ownerID string, jobID int64) (string, time.Time, error) {
	if ownerID == "" {
		return "", time.Time{}, fmt.Errorf("dispatch token: ownerID required")
	}
	if jobID == 0 {
		return "", time.Time{}, fmt.Errorf("dispatch token: jobID required")
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("dispatch token: read random: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	now := s.now().UTC()
	expiresAt := now.Add(DispatchTokenTTL)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_dispatch_tokens (token, owner_id, consolidation_job_id, issued_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		token, ownerID, jobID, now, expiresAt,
	)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("dispatch token: insert: %w", err)
	}
	return token, expiresAt, nil
}

// Validate returns true when the token row exists, is not revoked, has
// not expired, and matches the provided ownerID and jobID. On the first
// successful validate it stamps `used_at = now()` (informational; subsequent
// validates within the same job are still allowed per R7).
func (s *DispatchTokenStore) Validate(ctx context.Context, token, ownerID string, jobID int64) (bool, error) {
	if token == "" {
		return false, nil
	}
	var (
		dbOwner   string
		dbJob     int64
		expiresAt time.Time
		usedAt    sql.NullTime
		revokedAt sql.NullTime
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT owner_id, consolidation_job_id, expires_at, used_at, revoked_at
		   FROM memory_dispatch_tokens
		  WHERE token = ?`, token,
	).Scan(&dbOwner, &dbJob, &expiresAt, &usedAt, &revokedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("dispatch token: query: %w", err)
	}
	if revokedAt.Valid {
		return false, nil
	}
	if !expiresAt.After(s.now().UTC()) {
		return false, nil
	}
	if dbOwner != ownerID {
		return false, nil
	}
	if dbJob != jobID {
		return false, nil
	}

	// Stamp used_at on first successful validate (idempotent: COALESCE
	// keeps the original timestamp on later calls).
	if !usedAt.Valid {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE memory_dispatch_tokens SET used_at = ? WHERE token = ? AND used_at IS NULL`,
			s.now().UTC(), token,
		); err != nil {
			return false, fmt.Errorf("dispatch token: stamp used_at: %w", err)
		}
	}
	return true, nil
}

// Revoke marks the given token as revoked. Idempotent; revoking a
// non-existent token is not an error (so the dream worker can revoke on
// best-effort cleanup without races).
func (s *DispatchTokenStore) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE memory_dispatch_tokens SET revoked_at = ?
		  WHERE token = ? AND revoked_at IS NULL`,
		s.now().UTC(), token,
	)
	if err != nil {
		return fmt.Errorf("dispatch token: revoke: %w", err)
	}
	return nil
}
