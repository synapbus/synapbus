// memory_status query helpers — read the SQL view defined in migration
// 028 and turn it into a map[message_id]MemoryStatus suitable for the
// injection retrieval filter. The view itself derives state from
// `memory_consolidation_jobs.actions` rows (data-model.md §`memory_status`)
// so callers never have to mutate a status column directly.
package messaging

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Memory status constants — value of `memory_status.status`.
const (
	MemoryStatusActive      = "active"
	MemoryStatusSoftDeleted = "soft_deleted"
	MemoryStatusSuperseded  = "superseded"
)

// MemoryStatus is one row derived from the `memory_status` view.
// SupersededBy / SoftDeletedAt are nil when the message is active.
type MemoryStatus struct {
	Status        string     `json:"status"`
	SupersededBy  *int64     `json:"superseded_by,omitempty"`
	SoftDeletedAt *time.Time `json:"soft_deleted_at,omitempty"`
	Reason        string     `json:"reason,omitempty"`
}

// MemoryStatuses returns the status of each message id in msgIDs. Ids
// absent from the result map are implicitly `active` (the view only
// contains rows that have at least one consolidation action against
// them).
func MemoryStatuses(ctx context.Context, db *sql.DB, msgIDs []int64) (map[int64]MemoryStatus, error) {
	out := map[int64]MemoryStatus{}
	if len(msgIDs) == 0 {
		return out, nil
	}
	if db == nil {
		return out, fmt.Errorf("memory status: nil db")
	}

	placeholders := strings.Repeat("?,", len(msgIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(msgIDs))
	for _, id := range msgIDs {
		args = append(args, id)
	}

	q := `SELECT message_id, status, superseded_by, soft_deleted_at, COALESCE(reason, '')
	        FROM memory_status
	       WHERE message_id IN (` + placeholders + `)`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory status: query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id        int64
			status    string
			supersede sql.NullInt64
			deletedAt sql.NullTime
			reason    string
		)
		if err := rows.Scan(&id, &status, &supersede, &deletedAt, &reason); err != nil {
			return nil, fmt.Errorf("memory status: scan: %w", err)
		}
		ms := MemoryStatus{Status: status, Reason: reason}
		if supersede.Valid {
			v := supersede.Int64
			ms.SupersededBy = &v
		}
		if deletedAt.Valid {
			t := deletedAt.Time
			ms.SoftDeletedAt = &t
		}
		out[id] = ms
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory status: iterate: %w", err)
	}
	return out, nil
}

// StatusByID returns just the string status for each id; callers that
// only need active/non-active (e.g. the injection retrieval filter) can
// use this to avoid pulling in MemoryStatus's optional fields.
func StatusByID(ctx context.Context, db *sql.DB, msgIDs []int64) (map[int64]string, error) {
	full, err := MemoryStatuses(ctx, db, msgIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(full))
	for id, st := range full {
		out[id] = st.Status
	}
	return out, nil
}
