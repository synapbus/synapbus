package secrets

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// BuildEnvMap returns a name→plaintext map for all active secrets visible to
// the given user/agent/task, with scope precedence user < agent < task. Pass
// 0 for any scope id you wish to skip. last_used_at is bumped to
// CURRENT_TIMESTAMP for every secret returned.
//
// The returned map is intended to be merged into a subprocess env. Callers
// must treat values as sensitive and never log them.
func (s *Store) BuildEnvMap(ctx context.Context, userID, agentID, taskID int64) (map[string]string, error) {
	// Build (scope_type, scope_id, precedence) tuples; higher precedence wins.
	type scopeRow struct {
		typ        string
		id         int64
		precedence int
	}
	var scopes []scopeRow
	if userID > 0 {
		scopes = append(scopes, scopeRow{ScopeUser, userID, 1})
	}
	if agentID > 0 {
		scopes = append(scopes, scopeRow{ScopeAgent, agentID, 2})
	}
	if taskID > 0 {
		scopes = append(scopes, scopeRow{ScopeTask, taskID, 3})
	}
	if len(scopes) == 0 {
		return map[string]string{}, nil
	}

	var (
		parts []string
		args  []any
	)
	for _, sc := range scopes {
		parts = append(parts, "(scope_type = ? AND scope_id = ?)")
		args = append(args, sc.typ, sc.id)
	}

	query := `SELECT id, name, scope_type, value_blob
	            FROM secrets
	           WHERE revoked_at IS NULL
	             AND (` + strings.Join(parts, " OR ") + `)`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("secrets: env query: %w", err)
	}
	defer rows.Close()

	type winner struct {
		id         int64
		precedence int
		value      string
	}
	winners := make(map[string]winner)
	var touched []int64

	for rows.Next() {
		var (
			id        int64
			name      string
			scopeType string
			blob      []byte
		)
		if err := rows.Scan(&id, &name, &scopeType, &blob); err != nil {
			return nil, fmt.Errorf("secrets: env scan: %w", err)
		}
		var prec int
		switch scopeType {
		case ScopeUser:
			prec = 1
		case ScopeAgent:
			prec = 2
		case ScopeTask:
			prec = 3
		default:
			continue
		}
		existing, ok := winners[name]
		if ok && existing.precedence >= prec {
			continue
		}
		plain, err := s.decrypt(blob)
		if err != nil {
			return nil, fmt.Errorf("secrets: decrypt %q: %w", name, err)
		}
		winners[name] = winner{id: id, precedence: prec, value: string(plain)}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make(map[string]string, len(winners))
	for name, w := range winners {
		out[name] = w.value
		touched = append(touched, w.id)
	}

	if len(touched) > 0 {
		if err := s.bumpLastUsed(ctx, touched); err != nil {
			// Non-fatal for the caller's env, but log it.
			s.logger.Warn("failed to bump last_used_at", "error", err, "ids", touched)
		}
	}
	return out, nil
}

// bumpLastUsed updates last_used_at for the given secret ids in a single
// statement.
func (s *Store) bumpLastUsed(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `UPDATE secrets SET last_used_at = CURRENT_TIMESTAMP WHERE id IN (` +
		strings.Join(placeholders, ",") + `)`
	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	return nil
}

// Compile-time guard that *sql.DB satisfies the methods we rely on.
var _ = (*sql.DB)(nil)
