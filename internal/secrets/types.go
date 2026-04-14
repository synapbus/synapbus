// Package secrets provides encrypted, scoped secret storage for SynapBus.
//
// Secrets are stored in SQLite, encrypted at rest with NaCl secretbox under a
// local 32-byte master key (kept in <data-dir>/secrets.key with 0600 perms).
// Secrets are scoped to a user, agent, or task and are intended to be injected
// into subprocess environments as sanitized A-Z0-9_ variable names. The MCP
// surface never returns plaintext values — only names and availability.
package secrets

import (
	"errors"
	"time"
)

// Scope type constants. The same values are used in the secrets.scope_type
// column (CHECK constraint enforced at the SQL level).
const (
	ScopeUser  = "user"
	ScopeAgent = "agent"
	ScopeTask  = "task"
)

// Sentinel errors for the secrets package.
var (
	// ErrNotFound is returned when no active secret matches the lookup.
	ErrNotFound = errors.New("secret not found")
	// ErrAlreadyRevoked is returned when revoking a secret that is already revoked.
	ErrAlreadyRevoked = errors.New("secret already revoked")
	// ErrInvalidName is returned when a secret name fails sanitization.
	ErrInvalidName = errors.New("invalid secret name: must be non-empty and contain only A-Z, 0-9, _")
	// ErrMasterKeyMissing is returned when the master key file cannot be read or generated.
	ErrMasterKeyMissing = errors.New("secrets master key missing or unreadable")
)

// Secret is a stored, encrypted secret row. The plaintext value is never
// included — callers fetch it explicitly via Store.Get.
type Secret struct {
	ID         int64
	Name       string
	ScopeType  string
	ScopeID    int64
	CreatedBy  int64
	CreatedAt  time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
}

// Info is the public, value-free projection of a Secret used for listings
// exposed via MCP / API. It deliberately has no value field.
type Info struct {
	Name       string
	ScopeType  string
	ScopeID    int64
	Available  bool
	LastUsedAt *time.Time
}

// Scope identifies a (type, id) pair used when listing or building env maps.
type Scope struct {
	Type string
	ID   int64
}
