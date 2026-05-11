// Per-(owner, agent) core memory store. Backs User Story 2 of feature
// 020-proactive-memory-dream-worker — small, owner-scoped, replace-wholesale
// blobs surfaced in `relevant_context.core_memory` on session-start tools
// (e.g. `my_status`).
//
// Schema lives in `internal/storage/schema/028_memory_consolidation.sql`
// (table `memory_core`). owner_id is stored as TEXT (string form of
// `users.id`) to match the proactive-memory tables and the request-context
// owner_id propagated by auth middleware.
package messaging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrCoreMemoryTooLarge is returned by CoreMemoryStore.Set when the blob
// exceeds the configured max bytes (default SYNAPBUS_CORE_MEMORY_MAX_BYTES = 2048).
// The MCP `memory_rewrite_core` tool surfaces this as the contractual
// `core_memory_too_large` error code (see contracts/mcp-memory-tools.md).
var ErrCoreMemoryTooLarge = errors.New("core memory blob exceeds max bytes")

// CoreMemoryRecord is one row of the `memory_core` table.
type CoreMemoryRecord struct {
	OwnerID   string
	AgentName string
	Blob      string
	UpdatedAt time.Time
	UpdatedBy string
}

// CoreMemoryStore wraps the `memory_core` table. All operations are
// owner-scoped — owner_id is part of the primary key — so callers cannot
// cross-read another owner's blobs.
type CoreMemoryStore struct {
	db       *sql.DB
	maxBytes int
}

// NewCoreMemoryStore returns a store rooted at db enforcing the given
// max-bytes cap on Set. When maxBytes <= 0, defaults to 2048 (the spec
// default for SYNAPBUS_CORE_MEMORY_MAX_BYTES).
func NewCoreMemoryStore(db *sql.DB, maxBytes int) *CoreMemoryStore {
	if maxBytes <= 0 {
		maxBytes = 2048
	}
	return &CoreMemoryStore{db: db, maxBytes: maxBytes}
}

// MaxBytes returns the configured upper bound for Set blobs.
func (s *CoreMemoryStore) MaxBytes() int { return s.maxBytes }

// Get returns the core memory blob for (ownerID, agentName). When no row
// exists, returns ok=false with no error. Unexpected DB errors surface as
// err.
func (s *CoreMemoryStore) Get(ctx context.Context, ownerID, agentName string) (blob string, updatedAt time.Time, ok bool, err error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT blob, updated_at FROM memory_core WHERE owner_id = ? AND agent_name = ?`,
		ownerID, agentName,
	)
	if err := row.Scan(&blob, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", time.Time{}, false, nil
		}
		return "", time.Time{}, false, fmt.Errorf("core memory get: %w", err)
	}
	return blob, updatedAt, true, nil
}

// Set wholesale-replaces the blob for (ownerID, agentName). Enforces the
// configured size cap and returns ErrCoreMemoryTooLarge when violated.
// `updatedBy` is recorded for audit (typically the caller agent name, or
// "human" for admin/web edits).
func (s *CoreMemoryStore) Set(ctx context.Context, ownerID, agentName, blob, updatedBy string) error {
	if len(blob) > s.maxBytes {
		return ErrCoreMemoryTooLarge
	}
	if ownerID == "" {
		return fmt.Errorf("core memory set: empty owner_id")
	}
	if agentName == "" {
		return fmt.Errorf("core memory set: empty agent_name")
	}
	if updatedBy == "" {
		return fmt.Errorf("core memory set: empty updated_by")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_core (owner_id, agent_name, blob, updated_at, updated_by)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP, ?)
		 ON CONFLICT(owner_id, agent_name) DO UPDATE SET
		     blob = excluded.blob,
		     updated_at = CURRENT_TIMESTAMP,
		     updated_by = excluded.updated_by`,
		ownerID, agentName, blob, updatedBy,
	)
	if err != nil {
		return fmt.Errorf("core memory set: %w", err)
	}
	return nil
}

// Delete removes the (ownerID, agentName) row. Returns nil even if no
// row matched — callers should treat "deleted" and "did not exist" the
// same way (the REST endpoint distinguishes via a separate Get).
func (s *CoreMemoryStore) Delete(ctx context.Context, ownerID, agentName string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_core WHERE owner_id = ? AND agent_name = ?`,
		ownerID, agentName,
	)
	if err != nil {
		return fmt.Errorf("core memory delete: %w", err)
	}
	return nil
}

// List returns all core memory rows for the given owner. Used by the
// future audit UI (deferred US4) and by admin tooling.
func (s *CoreMemoryStore) List(ctx context.Context, ownerID string) ([]CoreMemoryRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT owner_id, agent_name, blob, updated_at, updated_by
		   FROM memory_core
		  WHERE owner_id = ?
		  ORDER BY agent_name`,
		ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("core memory list: %w", err)
	}
	defer rows.Close()
	var out []CoreMemoryRecord
	for rows.Next() {
		var r CoreMemoryRecord
		if err := rows.Scan(&r.OwnerID, &r.AgentName, &r.Blob, &r.UpdatedAt, &r.UpdatedBy); err != nil {
			return nil, fmt.Errorf("core memory list scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("core memory list rows: %w", err)
	}
	return out, nil
}

// GetForInjection is the adapter implementing
// `search.CoreMemoryProvider.Get`. Returns "" (no error) when no row
// exists — the empty-string convention lets the injection wrapper treat a
// missing core memory the same as "field omitted", per
// `contracts/mcp-injection.md`.
//
// The matching interface contract is in
// `internal/search/injection.go`'s `CoreMemoryProvider`:
//
//	Get(ctx context.Context, ownerID, agentName string) (string, error)
//
// We expose this as a method on the store (not a separate type) so
// callers can pass `coreStore.GetForInjection` as a method value — but the
// store itself also satisfies the interface via its `Get` method below.
func (s *CoreMemoryStore) GetForInjection(ctx context.Context, ownerID, agentName string) (string, error) {
	blob, _, ok, err := s.Get(ctx, ownerID, agentName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return blob, nil
}

// coreProviderAdapter wraps a *CoreMemoryStore so it satisfies
// `search.CoreMemoryProvider` (which requires a `Get(ctx, ownerID,
// agentName) (string, error)` signature — distinct from the store's
// 4-return Get). Use NewCoreProvider to construct.
type coreProviderAdapter struct {
	store *CoreMemoryStore
}

// NewCoreProvider returns an object satisfying
// `search.CoreMemoryProvider` so callers can wire the store into
// WrapConfig.CoreProvider without leaking the store's richer Get
// signature.
func NewCoreProvider(store *CoreMemoryStore) *coreProviderAdapter {
	return &coreProviderAdapter{store: store}
}

// Get implements `search.CoreMemoryProvider.Get`. Returns "" when no row
// exists for the (owner, agent) pair.
func (a *coreProviderAdapter) Get(ctx context.Context, ownerID, agentName string) (string, error) {
	if a == nil || a.store == nil {
		return "", nil
	}
	return a.store.GetForInjection(ctx, ownerID, agentName)
}
