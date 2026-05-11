// Memory-pin store for feature 020 — owner-pinned message ids that
// bypass the relevance floor on injection retrieval (data-model.md
// §`memory_pins`). Pins are always set by the human owner; the
// dream-agent does not write here. Pinned memories are loaded by
// `search.BuildContextPacket` and overlaid on top of hybrid retrieval
// so they appear even when their similarity score is below
// SYNAPBUS_INJECTION_MIN_SCORE.
package messaging

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Pin is one row in `memory_pins`.
type Pin struct {
	OwnerID   string    `json:"owner_id"`
	MessageID int64     `json:"message_id"`
	PinnedBy  string    `json:"pinned_by"`
	Note      string    `json:"note,omitempty"`
	PinnedAt  time.Time `json:"pinned_at"`
}

// PinStore wraps the `memory_pins` table.
type PinStore struct {
	db *sql.DB
}

// NewPinStore returns a store rooted at db.
func NewPinStore(db *sql.DB) *PinStore {
	return &PinStore{db: db}
}

// Pin pins (owner, msgID) for retrieval overlay. Idempotent — re-pinning
// updates `pinned_by` / `note` in place (the primary key is the
// (owner_id, message_id) tuple).
func (s *PinStore) Pin(ctx context.Context, ownerID string, msgID int64, pinnedBy, note string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("pin store: nil store")
	}
	if ownerID == "" {
		return fmt.Errorf("pin store: empty owner_id")
	}
	if msgID == 0 {
		return fmt.Errorf("pin store: message_id required")
	}
	if pinnedBy == "" {
		pinnedBy = "human:" + ownerID
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_pins (owner_id, message_id, pinned_by, note)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(owner_id, message_id) DO UPDATE SET
		     pinned_by = excluded.pinned_by,
		     note = excluded.note,
		     pinned_at = CURRENT_TIMESTAMP`,
		ownerID, msgID, pinnedBy, note,
	)
	if err != nil {
		return fmt.Errorf("pin store: insert: %w", err)
	}
	return nil
}

// Unpin removes the (owner, msgID) row. Returns nil even when no row
// matched — callers treat "unpinned" and "did not exist" the same way.
func (s *PinStore) Unpin(ctx context.Context, ownerID string, msgID int64) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_pins WHERE owner_id = ? AND message_id = ?`,
		ownerID, msgID,
	)
	if err != nil {
		return fmt.Errorf("pin store: delete: %w", err)
	}
	return nil
}

// ListForOwner returns the pinned message ids for the given owner. Used
// by the injection overlay to splice these in regardless of search
// score.
func (s *PinStore) ListForOwner(ctx context.Context, ownerID string) ([]int64, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT message_id FROM memory_pins WHERE owner_id = ? ORDER BY pinned_at DESC`,
		ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("pin store: list ids: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("pin store: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pin store: iterate: %w", err)
	}
	return out, nil
}

// pinProviderAdapter wraps *PinStore so it satisfies the
// search.PinProvider interface without leaking the wider PinStore
// surface area. Methods accept exactly the (ctx, ownerID) signature
// search.InjectionOpts requires.
type pinProviderAdapter struct{ store *PinStore }

// NewPinProvider returns a search.PinProvider over the given store.
func NewPinProvider(store *PinStore) *pinProviderAdapter { return &pinProviderAdapter{store: store} }

// ListForOwner implements search.PinProvider.
func (a *pinProviderAdapter) ListForOwner(ctx context.Context, ownerID string) ([]int64, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	return a.store.ListForOwner(ctx, ownerID)
}

// ListPinsForOwner returns full pin rows. Used by future audit UI.
func (s *PinStore) ListPinsForOwner(ctx context.Context, ownerID string) ([]Pin, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT owner_id, message_id, pinned_by, COALESCE(note, ''), pinned_at
		   FROM memory_pins WHERE owner_id = ? ORDER BY pinned_at DESC`,
		ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("pin store: list: %w", err)
	}
	defer rows.Close()
	var out []Pin
	for rows.Next() {
		var p Pin
		if err := rows.Scan(&p.OwnerID, &p.MessageID, &p.PinnedBy, &p.Note, &p.PinnedAt); err != nil {
			return nil, fmt.Errorf("pin store: scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pin store: iterate: %w", err)
	}
	return out, nil
}
