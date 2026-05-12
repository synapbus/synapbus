// Memory-link store for feature 020 — typed directed edges between two
// memory message ids (see data-model.md §`memory_links`).
//
// Three classes of relation types live in this table:
//
//   - Semantic types written by the dream-agent via the
//     `memory_add_link` MCP tool: `refines`, `contradicts`, `examples`,
//     `related`.
//
//   - Consolidation types written by `memory_mark_duplicate` and
//     `memory_supersede`: `duplicate_of`, `superseded_by`. These are NOT
//     valid arguments to `memory_add_link` — the contract reserves them
//     for the dedicated tools so the `memory_status` view can derive
//     soft-delete / supersede state from a single audit path.
//
//   - Auto types written by the messaging layer (post-insert hook,
//     T035): `mention`, `reply_to`, `channel_cooccurrence`. These are
//     NOT valid arguments from any agent — only the `auto:<rule>`
//     created_by prefix may use them.
//
// Add() rejects type/actor mismatches with ErrLinkTypeReserved so the
// MCP tools surface the contractual `relation_type_reserved` error
// code cleanly.
package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrLinkTypeReserved is returned when an actor tries to add a
// relation_type they are not allowed to write directly (see file
// comment for the actor/type matrix).
var ErrLinkTypeReserved = errors.New("relation type reserved for another actor")

// Link is one row in `memory_links`.
type Link struct {
	ID           int64          `json:"id"`
	SrcMessageID int64          `json:"src_message_id"`
	DstMessageID int64          `json:"dst_message_id"`
	RelationType string         `json:"relation_type"`
	OwnerID      string         `json:"owner_id"`
	CreatedBy    string         `json:"created_by"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

// LinkStore wraps the `memory_links` table.
type LinkStore struct {
	db *sql.DB
}

// NewLinkStore returns a store rooted at db.
func NewLinkStore(db *sql.DB) *LinkStore {
	return &LinkStore{db: db}
}

// Auto-generated link types (written only via auto:<rule> caller).
var autoLinkTypes = map[string]struct{}{
	"mention":              {},
	"reply_to":             {},
	"channel_cooccurrence": {},
}

// Reserved-for-tools relation types (written by memory_mark_duplicate /
// memory_supersede via their own code paths, never by memory_add_link).
var consolidationLinkTypes = map[string]struct{}{
	"duplicate_of":  {},
	"superseded_by": {},
}

// IsAutoLinkType reports whether relType is one of the auto-generated
// link types written by the messaging post-insert hook.
func IsAutoLinkType(relType string) bool {
	_, ok := autoLinkTypes[relType]
	return ok
}

// Add inserts one row into `memory_links`. Reserved-type guarding:
//
//   - When createdBy starts with `agent:`, the auto-types
//     (mention/reply_to/channel_cooccurrence) AND consolidation-types
//     (duplicate_of/superseded_by) are rejected with
//     ErrLinkTypeReserved. The MCP `memory_add_link` tool must surface
//     this as the contractual `relation_type_reserved` error.
//
//   - When createdBy starts with `auto:`, only the auto-types are
//     allowed; semantic types and consolidation-types are rejected.
//
//   - When createdBy starts with `human:` or any other prefix, no
//     reserved-type check is applied — admin tooling can backfill any
//     type for debugging / migration.
func (s *LinkStore) Add(
	ctx context.Context,
	src, dst int64,
	relType, ownerID, createdBy string,
	metadata map[string]any,
) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("link store: nil store")
	}
	if src == 0 || dst == 0 {
		return 0, fmt.Errorf("link store: src/dst message ids required")
	}
	if relType == "" || ownerID == "" || createdBy == "" {
		return 0, fmt.Errorf("link store: relation_type, owner_id, created_by required")
	}

	switch {
	case strings.HasPrefix(createdBy, "agent:"):
		if _, banned := autoLinkTypes[relType]; banned {
			return 0, ErrLinkTypeReserved
		}
		if _, banned := consolidationLinkTypes[relType]; banned {
			return 0, ErrLinkTypeReserved
		}
	case strings.HasPrefix(createdBy, "auto:"):
		if _, ok := autoLinkTypes[relType]; !ok {
			return 0, ErrLinkTypeReserved
		}
	}

	metaJSON := "{}"
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err != nil {
			return 0, fmt.Errorf("link store: marshal metadata: %w", err)
		}
		metaJSON = string(b)
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_links
		   (src_message_id, dst_message_id, relation_type, owner_id, created_by, metadata)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		src, dst, relType, ownerID, createdBy, metaJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("link store: insert: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// AddConsolidationLink inserts a `duplicate_of` or `superseded_by`
// link without running the reserved-type guard. Only the dedicated
// MCP tools `memory_mark_duplicate` and `memory_supersede` should
// call this — the `memory_add_link` path uses Add() and rejects these
// types per the contract.
func (s *LinkStore) AddConsolidationLink(
	ctx context.Context,
	src, dst int64,
	relType, ownerID, createdBy string,
	metadata map[string]any,
) (int64, error) {
	if relType != "duplicate_of" && relType != "superseded_by" {
		return 0, fmt.Errorf("link store: AddConsolidationLink rejects %q", relType)
	}
	metaJSON := "{}"
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err != nil {
			return 0, fmt.Errorf("link store: marshal metadata: %w", err)
		}
		metaJSON = string(b)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_links
		   (src_message_id, dst_message_id, relation_type, owner_id, created_by, metadata)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		src, dst, relType, ownerID, createdBy, metaJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("link store: insert: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// ListByMessage returns every link with the given message id as src OR
// dst. Useful for both outgoing edges (reflection sources) and incoming
// edges (what refines this).
func (s *LinkStore) ListByMessage(ctx context.Context, msgID int64) ([]Link, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, src_message_id, dst_message_id, relation_type, owner_id,
		        created_by, metadata, created_at
		   FROM memory_links
		  WHERE src_message_id = ? OR dst_message_id = ?
		  ORDER BY id ASC`, msgID, msgID,
	)
	if err != nil {
		return nil, fmt.Errorf("link store: list by message: %w", err)
	}
	defer rows.Close()
	return scanLinks(rows)
}

// ListByOwner returns links for the given owner, optionally filtered to
// a subset of relation types. `limit <= 0` defaults to 100.
func (s *LinkStore) ListByOwner(
	ctx context.Context,
	ownerID string,
	types []string,
	limit int,
) ([]Link, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id, src_message_id, dst_message_id, relation_type, owner_id,
	             created_by, metadata, created_at
	        FROM memory_links
	       WHERE owner_id = ?`
	args := []any{ownerID}
	if len(types) > 0 {
		placeholders := strings.Repeat("?,", len(types))
		placeholders = placeholders[:len(placeholders)-1]
		q += " AND relation_type IN (" + placeholders + ")"
		for _, t := range types {
			args = append(args, t)
		}
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("link store: list by owner: %w", err)
	}
	defer rows.Close()
	return scanLinks(rows)
}

func scanLinks(rows *sql.Rows) ([]Link, error) {
	var out []Link
	for rows.Next() {
		var l Link
		var metaJSON string
		if err := rows.Scan(
			&l.ID, &l.SrcMessageID, &l.DstMessageID, &l.RelationType,
			&l.OwnerID, &l.CreatedBy, &metaJSON, &l.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("link store: scan: %w", err)
		}
		if metaJSON != "" && metaJSON != "{}" {
			_ = json.Unmarshal([]byte(metaJSON), &l.Metadata)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("link store: iterate: %w", err)
	}
	return out, nil
}
