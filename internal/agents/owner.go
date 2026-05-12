package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// Sentinel errors returned by OwnerFor.
//
// Callers in the proactive-memory / dream-worker path use these to
// distinguish "no such agent" from "agent exists but has no owner"
// without parsing error strings.
var (
	// ErrAgentNotFound is returned when no row exists in `agents` for
	// the requested name.
	ErrAgentNotFound = errors.New("agent not found")

	// ErrAgentUnowned is returned when an agent row exists but its
	// owner_id is zero / empty. In the current schema owner_id is
	// declared NOT NULL, so this is effectively an integrity guard for
	// rows backfilled with 0.
	ErrAgentUnowned = errors.New("agent has no owner")
)

// OwnerFor returns the string-encoded owner_id of the named agent.
//
// The schema stores `agents.owner_id` as INTEGER (FK to `users.id`), but
// the proactive-memory tables and the request-context `owner_id`
// (populated by auth middleware via `trace.ContextWithOwnerID`) carry it
// as a string. OwnerFor canonicalizes to that string form so call sites
// can compare without re-converting.
//
// Returns ("", ErrAgentNotFound) when no row matches; ("",
// ErrAgentUnowned) when a row exists but owner_id is 0.
func OwnerFor(ctx context.Context, db *sql.DB, agentName string) (string, error) {
	var ownerID int64
	err := db.QueryRowContext(ctx,
		`SELECT owner_id FROM agents WHERE name = ?`, agentName,
	).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrAgentNotFound
		}
		return "", fmt.Errorf("query owner for agent %q: %w", agentName, err)
	}
	if ownerID == 0 {
		return "", ErrAgentUnowned
	}
	return strconv.FormatInt(ownerID, 10), nil
}
