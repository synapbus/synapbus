package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// MemoryChannelNames lists exact channel names that are always treated
// as memory channels regardless of metadata.
var MemoryChannelNames = []string{"open-brain"}

// MemoryChannelPrefixes lists channel-name prefixes that are always
// treated as memory channels. Matching uses HasPrefix (e.g.
// "reflections-personal-brand" matches "reflections-").
var MemoryChannelPrefixes = []string{"reflections-"}

// MemoryChannel is the lightweight shape this package needs to decide
// memory-pool participation. We use a local type rather than
// `channels.Channel` to avoid an import cycle: package `channels`
// already imports `messaging`. Callers that hold a `*channels.Channel`
// can construct a `MemoryChannel{Name: ch.Name}` and pass it in.
type MemoryChannel struct {
	ID       int64
	Name     string
	Metadata string // raw JSON; empty when unknown
}

// IsMemoryChannel reports whether the given channel participates in
// the proactive-memory pool.
//
// A channel is a memory channel when ANY of:
//
//  1. Its name appears in MemoryChannelNames (currently `open-brain`).
//  2. Its name starts with a MemoryChannelPrefixes entry (currently
//     `reflections-*`).
//  3. Its raw metadata JSON contains `"is_memory": true`.
//
// The metadata path is documented in 020-data-model.md but the
// `channels` table does not yet expose a metadata column; the third
// rule is wired up so it just-works once that lands and is harmless
// in the meantime (empty Metadata always returns false).
func IsMemoryChannel(ch *MemoryChannel) bool {
	if ch == nil {
		return false
	}
	if matchesMemoryChannelName(ch.Name) {
		return true
	}
	return isMemoryChannelMetadata(ch.Metadata)
}

func matchesMemoryChannelName(name string) bool {
	for _, n := range MemoryChannelNames {
		if name == n {
			return true
		}
	}
	for _, p := range MemoryChannelPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func isMemoryChannelMetadata(metadata string) bool {
	m := strings.TrimSpace(metadata)
	if m == "" || m == "{}" {
		return false
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(m), &parsed); err != nil {
		return false
	}
	v, ok := parsed["is_memory"]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// MemoryChannelIDs returns the IDs of every channel that participates
// in the memory pool. Driven by name patterns today; will pick up the
// metadata flag automatically once `channels.metadata` ships (this
// function would then SELECT metadata as well and apply
// IsMemoryChannel per row).
func MemoryChannelIDs(ctx context.Context, db *sql.DB) ([]int64, error) {
	var clauses []string
	var args []any
	for _, n := range MemoryChannelNames {
		clauses = append(clauses, "name = ?")
		args = append(args, n)
	}
	for _, p := range MemoryChannelPrefixes {
		clauses = append(clauses, "name LIKE ?")
		args = append(args, p+"%")
	}
	if len(clauses) == 0 {
		return nil, nil
	}

	query := "SELECT id FROM channels WHERE " + strings.Join(clauses, " OR ")
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query memory channels: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan memory channel id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory channel ids: %w", err)
	}
	return ids, nil
}
