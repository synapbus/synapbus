// Package agentquery provides a sandboxed SQL query executor for agents.
// Agents can run read-only SELECT queries against curated views with
// per-agent access control, automatic LIMIT enforcement, and timeouts.
package agentquery

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const (
	// MaxRows is the maximum number of rows returned by a query.
	MaxRows = 100
	// QueryTimeout is the maximum duration for a query.
	QueryTimeout = 5 * time.Second
)

// Allowed view names that agents can query.
var allowedTables = map[string]bool{
	"my_messages":      true,
	"my_channels":      true,
	"channel_messages": true,
}

// Executor runs sandboxed SQL queries on behalf of agents.
type Executor struct {
	db     *sql.DB // read-only pool (query_only=ON)
	logger *slog.Logger
}

// New creates a new query executor using the provided read-only database connection.
func New(readDB *sql.DB, logger *slog.Logger) *Executor {
	return &Executor{
		db:     readDB,
		logger: logger.With("component", "agentquery"),
	}
}

// QueryResult holds the results of a SQL query.
type QueryResult struct {
	Columns   []string        `json:"columns"`
	Rows      [][]interface{} `json:"rows"`
	RowCount  int             `json:"row_count"`
	Truncated bool            `json:"truncated"`
}

// Execute runs a SQL query on behalf of an agent with access control.
func (e *Executor) Execute(ctx context.Context, agentName, sqlQuery string) (*QueryResult, error) {
	// 1. Validate the SQL statement
	if err := validateSQL(sqlQuery); err != nil {
		return nil, fmt.Errorf("query validation failed: %w", err)
	}

	// 2. Rewrite the query to inject access control and enforce LIMIT
	rewritten := rewriteQuery(agentName, sqlQuery)

	// 3. Execute with timeout
	queryCtx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()

	rows, err := e.db.QueryContext(queryCtx, rewritten)
	if err != nil {
		if queryCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("query timed out after %s", QueryTimeout)
		}
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	// 4. Collect results
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("get columns: %w", err)
	}

	var resultRows [][]interface{}
	truncated := false

	for rows.Next() {
		if len(resultRows) >= MaxRows {
			truncated = true
			break
		}

		values := make([]interface{}, len(columns))
		scanArgs := make([]interface{}, len(columns))
		for i := range values {
			scanArgs[i] = &values[i]
		}

		if err := rows.Scan(scanArgs...); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		// Convert []byte to string for JSON serialization
		row := make([]interface{}, len(columns))
		for i, v := range values {
			if b, ok := v.([]byte); ok {
				row[i] = string(b)
			} else {
				row[i] = v
			}
		}
		resultRows = append(resultRows, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	if resultRows == nil {
		resultRows = [][]interface{}{}
	}

	e.logger.Info("agent query executed",
		"agent", agentName,
		"rows", len(resultRows),
		"truncated", truncated,
	)

	return &QueryResult{
		Columns:   columns,
		Rows:      resultRows,
		RowCount:  len(resultRows),
		Truncated: truncated,
	}, nil
}

// validateSQL checks that the query is a read-only SELECT statement.
func validateSQL(query string) error {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return fmt.Errorf("empty query")
	}

	// Remove comments
	upper := strings.ToUpper(trimmed)

	// Must start with SELECT or WITH (CTEs)
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return fmt.Errorf("only SELECT statements are allowed (got %q)", firstWord(upper))
	}

	// Block dangerous keywords (check as whole words or with common delimiters)
	blocked := []string{
		"INSERT ", "UPDATE ", "DELETE ", "DROP ", "ALTER ", "CREATE ",
		"ATTACH ", "DETACH ", "PRAGMA", "REINDEX ", "VACUUM ",
		"REPLACE ", "GRANT ", "REVOKE ",
	}
	for _, kw := range blocked {
		if strings.Contains(upper, kw) {
			return fmt.Errorf("statement contains blocked keyword: %s", strings.TrimSpace(kw))
		}
	}

	// Block multiple statements (semicolon followed by non-whitespace)
	parts := strings.Split(trimmed, ";")
	nonEmpty := 0
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			nonEmpty++
		}
	}
	if nonEmpty > 1 {
		return fmt.Errorf("multiple statements not allowed")
	}

	return nil
}

// rewriteQuery wraps the agent's query with access control CTEs.
// It replaces references to my_messages, my_channels, channel_messages
// with CTEs that filter by the agent's access.
func rewriteQuery(agentName, query string) string {
	// Build access-control CTEs that the agent's query can reference
	cte := fmt.Sprintf(`
WITH my_messages AS (
    SELECT v.* FROM v_agent_messages v
    LEFT JOIN channel_members cm ON cm.channel_id = v.channel_id AND cm.agent_name = %[1]s
    WHERE v.to_agent = %[1]s
       OR v.from_agent = %[1]s
       OR (v.channel_id IS NOT NULL AND cm.agent_name IS NOT NULL)
),
my_channels AS (
    SELECT c.id, c.name, c.description, c.type, c.topic, c.is_private, c.created_at,
           cm.joined_at AS member_since
    FROM channels c
    JOIN channel_members cm ON cm.channel_id = c.id AND cm.agent_name = %[1]s
),
channel_messages AS (
    SELECT v.* FROM v_channel_messages v
    WHERE v.channel_id IN (
        SELECT channel_id FROM channel_members WHERE agent_name = %[1]s
    )
)
`, quoteSQLString(agentName))

	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)

	// Remove trailing semicolon if present
	trimmed = strings.TrimRight(trimmed, "; \t\n")

	if strings.HasPrefix(upper, "WITH") {
		// User has their own CTEs. We need to merge them.
		// Strategy: our CTEs come first, then append user's CTEs after a comma.
		// Remove the user's "WITH " prefix since our CTE block already has WITH.
		userCTEs := strings.TrimSpace(trimmed[4:]) // skip "WITH"
		return cte + ", " + userCTEs + " LIMIT " + fmt.Sprintf("%d", MaxRows+1)
	}

	// Simple SELECT — prepend our CTEs
	return cte + trimmed + " LIMIT " + fmt.Sprintf("%d", MaxRows+1)
}

// quoteSQLString safely quotes a string for use in SQL.
func quoteSQLString(s string) string {
	escaped := strings.ReplaceAll(s, "'", "''")
	return "'" + escaped + "'"
}

func firstWord(s string) string {
	for i, c := range s {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '(' {
			return s[:i]
		}
	}
	if len(s) > 20 {
		return s[:20]
	}
	return s
}
