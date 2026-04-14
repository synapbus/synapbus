package secrets

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/nacl/secretbox"
)

const (
	// nonceSize is the NaCl secretbox nonce size in bytes.
	nonceSize = 24
	// keySize is the NaCl secretbox key size in bytes.
	keySize = 32
	// masterKeyFilename is the file inside the data dir holding the 32-byte
	// master key. Stored with 0600 permissions.
	masterKeyFilename = "secrets.key"
)

// Store provides CRUD over encrypted secrets backed by SQLite.
type Store struct {
	db        *sql.DB
	logger    *slog.Logger
	masterKey [keySize]byte
}

// NewStore constructs a Store. It bootstraps the master key from
// <dataDir>/secrets.key, generating a fresh 32-byte key (0600 perms) if the
// file does not yet exist.
func NewStore(db *sql.DB, dataDir string, logger *slog.Logger) (*Store, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if db == nil {
		return nil, fmt.Errorf("secrets: db is required")
	}
	if dataDir == "" {
		return nil, fmt.Errorf("secrets: dataDir is required")
	}

	key, err := loadOrCreateMasterKey(dataDir, logger)
	if err != nil {
		return nil, err
	}

	s := &Store{db: db, logger: logger}
	copy(s.masterKey[:], key)
	return s, nil
}

func loadOrCreateMasterKey(dataDir string, logger *slog.Logger) ([]byte, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: mkdir %s: %v", ErrMasterKeyMissing, dataDir, err)
	}
	path := filepath.Join(dataDir, masterKeyFilename)

	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != keySize {
			return nil, fmt.Errorf("%w: %s has wrong size %d (want %d)", ErrMasterKeyMissing, path, len(data), keySize)
		}
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: read %s: %v", ErrMasterKeyMissing, path, err)
	}

	// Generate a new key.
	buf := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, fmt.Errorf("%w: generate: %v", ErrMasterKeyMissing, err)
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return nil, fmt.Errorf("%w: write %s: %v", ErrMasterKeyMissing, path, err)
	}
	logger.Info("generated new secrets master key", "path", path)
	return buf, nil
}

// Set encrypts value and writes a new secret row. If an active secret with the
// same (scope_type, scope_id, name) already exists, it is revoked first so a
// new immutable history row can be inserted.
func (s *Store) Set(ctx context.Context, name, scopeType string, scopeID, createdBy int64, value string) (*Secret, error) {
	clean, err := sanitizeName(name)
	if err != nil {
		return nil, err
	}
	if !validScope(scopeType) {
		return nil, fmt.Errorf("secrets: invalid scope_type %q", scopeType)
	}

	blob, err := s.encrypt([]byte(value))
	if err != nil {
		return nil, fmt.Errorf("secrets: encrypt: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("secrets: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Revoke any existing active row for the same (scope, name).
	if _, err := tx.ExecContext(ctx,
		`UPDATE secrets
		    SET revoked_at = CURRENT_TIMESTAMP
		  WHERE name = ?
		    AND scope_type = ?
		    AND scope_id = ?
		    AND revoked_at IS NULL`,
		clean, scopeType, scopeID,
	); err != nil {
		return nil, fmt.Errorf("secrets: revoke previous: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO secrets (name, scope_type, scope_id, value_blob, created_by)
		 VALUES (?, ?, ?, ?, ?)`,
		clean, scopeType, scopeID, blob, createdBy,
	)
	if err != nil {
		return nil, fmt.Errorf("secrets: insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("secrets: last insert id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("secrets: commit: %w", err)
	}

	// Re-read to populate created_at consistently.
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, scope_type, scope_id, created_by, created_at, revoked_at, last_used_at
		   FROM secrets WHERE id = ?`,
		id,
	)
	sec, err := scanSecret(row)
	if err != nil {
		return nil, fmt.Errorf("secrets: read back: %w", err)
	}
	s.logger.Info("secret set",
		"id", sec.ID,
		"name", sec.Name,
		"scope_type", sec.ScopeType,
		"scope_id", sec.ScopeID,
	)
	return sec, nil
}

// Get decrypts and returns the plaintext for the active secret matching
// (name, scope_type, scope_id). Caller must treat the returned string as
// sensitive — never log it.
func (s *Store) Get(ctx context.Context, name, scopeType string, scopeID int64) (string, error) {
	clean, err := sanitizeName(name)
	if err != nil {
		return "", err
	}
	if !validScope(scopeType) {
		return "", fmt.Errorf("secrets: invalid scope_type %q", scopeType)
	}

	var blob []byte
	err = s.db.QueryRowContext(ctx,
		`SELECT value_blob
		   FROM secrets
		  WHERE name = ?
		    AND scope_type = ?
		    AND scope_id = ?
		    AND revoked_at IS NULL`,
		clean, scopeType, scopeID,
	).Scan(&blob)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("secrets: query: %w", err)
	}

	plain, err := s.decrypt(blob)
	if err != nil {
		return "", fmt.Errorf("secrets: decrypt: %w", err)
	}
	return string(plain), nil
}

// List returns Info entries for all active secrets in the given scopes.
// Values are never returned. Order is stable: by (scope_type, scope_id, name).
func (s *Store) List(ctx context.Context, scopes []Scope) ([]Info, error) {
	if len(scopes) == 0 {
		return []Info{}, nil
	}

	// Build dynamic IN clause: (scope_type=? AND scope_id=?) OR (...)
	var (
		parts []string
		args  []any
	)
	for _, sc := range scopes {
		if !validScope(sc.Type) {
			return nil, fmt.Errorf("secrets: invalid scope_type %q", sc.Type)
		}
		parts = append(parts, "(scope_type = ? AND scope_id = ?)")
		args = append(args, sc.Type, sc.ID)
	}
	query := `SELECT name, scope_type, scope_id, last_used_at
	            FROM secrets
	           WHERE revoked_at IS NULL
	             AND (` + strings.Join(parts, " OR ") + `)
	        ORDER BY scope_type, scope_id, name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("secrets: list query: %w", err)
	}
	defer rows.Close()

	var out []Info
	for rows.Next() {
		var (
			info     Info
			lastUsed sql.NullTime
		)
		if err := rows.Scan(&info.Name, &info.ScopeType, &info.ScopeID, &lastUsed); err != nil {
			return nil, fmt.Errorf("secrets: scan: %w", err)
		}
		info.Available = true
		if lastUsed.Valid {
			t := lastUsed.Time
			info.LastUsedAt = &t
		}
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Info{}
	}
	return out, nil
}

// Revoke marks a secret revoked by primary key. It is idempotent in the sense
// that a non-existent row returns ErrNotFound and an already-revoked row
// returns ErrAlreadyRevoked.
func (s *Store) Revoke(ctx context.Context, id int64) error {
	var revokedAt sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT revoked_at FROM secrets WHERE id = ?`, id,
	).Scan(&revokedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("secrets: lookup: %w", err)
	}
	if revokedAt.Valid {
		return ErrAlreadyRevoked
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE secrets SET revoked_at = CURRENT_TIMESTAMP WHERE id = ?`, id,
	); err != nil {
		return fmt.Errorf("secrets: revoke: %w", err)
	}
	s.logger.Info("secret revoked", "id", id)
	return nil
}

// encrypt returns nonce(24) || ciphertext.
func (s *Store) encrypt(plain []byte) ([]byte, error) {
	var nonce [nonceSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, err
	}
	out := make([]byte, 0, nonceSize+len(plain)+secretbox.Overhead)
	out = append(out, nonce[:]...)
	out = secretbox.Seal(out, plain, &nonce, &s.masterKey)
	return out, nil
}

// decrypt parses nonce(24) || ciphertext and returns the plaintext.
func (s *Store) decrypt(blob []byte) ([]byte, error) {
	if len(blob) < nonceSize+secretbox.Overhead {
		return nil, fmt.Errorf("secrets: ciphertext too short (%d bytes)", len(blob))
	}
	var nonce [nonceSize]byte
	copy(nonce[:], blob[:nonceSize])
	plain, ok := secretbox.Open(nil, blob[nonceSize:], &nonce, &s.masterKey)
	if !ok {
		return nil, fmt.Errorf("secrets: decryption failed (key mismatch or corruption)")
	}
	return plain, nil
}

// sanitizeName uppercases raw and validates that it contains only [A-Z0-9_].
// Empty input is rejected. Lowercase letters are folded to uppercase before
// validation so callers may pass either case.
func sanitizeName(raw string) (string, error) {
	if raw == "" {
		return "", ErrInvalidName
	}
	upper := strings.ToUpper(raw)
	for i := 0; i < len(upper); i++ {
		c := upper[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return "", ErrInvalidName
		}
	}
	return upper, nil
}

func validScope(t string) bool {
	switch t {
	case ScopeUser, ScopeAgent, ScopeTask:
		return true
	default:
		return false
	}
}

// scanSecret scans a single secret row from a *sql.Row.
func scanSecret(row *sql.Row) (*Secret, error) {
	var (
		s         Secret
		revoked   sql.NullTime
		lastUsed  sql.NullTime
		createdAt time.Time
	)
	if err := row.Scan(&s.ID, &s.Name, &s.ScopeType, &s.ScopeID, &s.CreatedBy, &createdAt, &revoked, &lastUsed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.CreatedAt = createdAt
	if revoked.Valid {
		t := revoked.Time
		s.RevokedAt = &t
	}
	if lastUsed.Valid {
		t := lastUsed.Time
		s.LastUsedAt = &t
	}
	return &s, nil
}
