package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// InjectionRecord is one row in the `memory_injections` 24-hour audit
// ring. Each row captures what was attached to a single MCP tool
// response so the owner can later answer "why did my agent know this?"
// via the recent-injections debug surface (FR-025).
type InjectionRecord struct {
	ID               int64     `json:"id"`
	OwnerID          string    `json:"owner_id"`
	AgentName        string    `json:"agent_name"`
	ToolName         string    `json:"tool_name"`
	PacketSizeChars  int       `json:"packet_size_chars"`
	PacketItemsCount int       `json:"packet_items_count"`
	MessageIDs       []int64   `json:"message_ids"`
	CoreBlobIncluded bool      `json:"core_blob_included"`
	CreatedAt        time.Time `json:"created_at"`
}

// MemoryInjections is the audit-ring store for proactive injection
// (data-model.md §`memory_injections`). Each row is best-effort:
// failures are non-fatal for the injection request — callers should
// log and continue.
type MemoryInjections struct {
	db *sql.DB
}

// NewMemoryInjections wraps a *sql.DB.
func NewMemoryInjections(db *sql.DB) *MemoryInjections {
	return &MemoryInjections{db: db}
}

// Record inserts one injection row. `row.MessageIDs` is JSON-encoded.
// `created_at` defaults to CURRENT_TIMESTAMP when zero.
func (s *MemoryInjections) Record(ctx context.Context, row InjectionRecord) error {
	if s == nil || s.db == nil {
		return nil
	}
	ids := row.MessageIDs
	if ids == nil {
		ids = []int64{}
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("memory_injections: marshal message_ids: %w", err)
	}

	if row.CreatedAt.IsZero() {
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO memory_injections
			   (owner_id, agent_name, tool_name, packet_size_chars,
			    packet_items_count, message_ids, core_blob_included)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			row.OwnerID, row.AgentName, row.ToolName,
			row.PacketSizeChars, row.PacketItemsCount, string(b),
			row.CoreBlobIncluded,
		)
	} else {
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO memory_injections
			   (owner_id, agent_name, tool_name, packet_size_chars,
			    packet_items_count, message_ids, core_blob_included, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			row.OwnerID, row.AgentName, row.ToolName,
			row.PacketSizeChars, row.PacketItemsCount, string(b),
			row.CoreBlobIncluded, row.CreatedAt.UTC(),
		)
	}
	if err != nil {
		return fmt.Errorf("memory_injections: insert: %w", err)
	}
	return nil
}

// Cleanup deletes rows older than `olderThan` ago. Returns the number
// of rows removed. Safe to call from a periodic ticker.
func (s *MemoryInjections) Cleanup(ctx context.Context, olderThan time.Duration) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	if olderThan <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-olderThan).UTC()
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_injections WHERE created_at < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("memory_injections: cleanup: %w", err)
	}
	affected, _ := res.RowsAffected()
	return affected, nil
}

// ListRecent returns the most recent injections for one owner, newest
// first, up to `limit`.
func (s *MemoryInjections) ListRecent(ctx context.Context, ownerID string, limit int) ([]InjectionRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, owner_id, agent_name, tool_name, packet_size_chars,
		        packet_items_count, message_ids, core_blob_included, created_at
		   FROM memory_injections
		  WHERE owner_id = ?
		  ORDER BY created_at DESC, id DESC
		  LIMIT ?`, ownerID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("memory_injections: list recent: %w", err)
	}
	defer rows.Close()

	var out []InjectionRecord
	for rows.Next() {
		var rec InjectionRecord
		var idsJSON string
		if err := rows.Scan(
			&rec.ID, &rec.OwnerID, &rec.AgentName, &rec.ToolName,
			&rec.PacketSizeChars, &rec.PacketItemsCount, &idsJSON,
			&rec.CoreBlobIncluded, &rec.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("memory_injections: scan: %w", err)
		}
		if idsJSON == "" {
			rec.MessageIDs = []int64{}
		} else if err := json.Unmarshal([]byte(idsJSON), &rec.MessageIDs); err != nil {
			// Corrupt row — surface as empty rather than fail the whole listing.
			rec.MessageIDs = []int64{}
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory_injections: iterate: %w", err)
	}
	return out, nil
}
