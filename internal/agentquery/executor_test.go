package agentquery

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	// Create the schema needed for views
	schema := `
		CREATE TABLE channels (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT DEFAULT '',
			type TEXT DEFAULT 'standard',
			topic TEXT DEFAULT '',
			is_private INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE channel_members (
			channel_id INTEGER,
			agent_name TEXT,
			joined_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (channel_id, agent_name)
		);
		CREATE TABLE messages (
			id INTEGER PRIMARY KEY,
			conversation_id INTEGER DEFAULT 0,
			from_agent TEXT,
			to_agent TEXT,
			channel_id INTEGER,
			reply_to INTEGER,
			body TEXT,
			priority INTEGER DEFAULT 5,
			status TEXT DEFAULT 'pending',
			metadata TEXT DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		-- Views matching the migration
		CREATE VIEW v_agent_messages AS
		SELECT m.id, m.body, m.from_agent, m.to_agent, m.priority, m.status, m.metadata,
			   m.created_at, m.updated_at, c.name AS channel_name, m.channel_id, m.reply_to, m.conversation_id
		FROM messages m LEFT JOIN channels c ON c.id = m.channel_id;

		CREATE VIEW v_agent_channels AS
		SELECT c.id, c.name, c.description, c.type, c.topic, c.is_private, c.created_at,
			   cm.joined_at AS member_since
		FROM channels c JOIN channel_members cm ON cm.channel_id = c.id;

		CREATE VIEW v_channel_messages AS
		SELECT m.id, m.body, m.from_agent, m.priority, m.status, m.metadata, m.created_at,
			   c.name AS channel_name, m.channel_id, m.reply_to
		FROM messages m JOIN channels c ON c.id = m.channel_id;
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	// Seed test data
	seed := `
		INSERT INTO channels (id, name) VALUES (1, 'general'), (2, 'news-mcpproxy'), (3, 'private-channel');
		INSERT INTO channel_members (channel_id, agent_name) VALUES
			(1, 'agent-a'), (1, 'agent-b'),
			(2, 'agent-a'),
			(3, 'agent-b');

		-- DMs
		INSERT INTO messages (id, from_agent, to_agent, body, priority) VALUES
			(1, 'algis', 'agent-a', 'Hello agent A', 7),
			(2, 'agent-a', 'algis', 'Hi there', 5),
			(3, 'algis', 'agent-b', 'Hello agent B', 5);

		-- Channel messages
		INSERT INTO messages (id, from_agent, channel_id, body, priority) VALUES
			(4, 'agent-a', 1, 'General post from A', 5),
			(5, 'agent-b', 1, 'General post from B', 5),
			(6, 'agent-a', 2, 'News post high prio', 8),
			(7, 'agent-b', 3, 'Private channel msg', 5);
	`
	if _, err := db.Exec(seed); err != nil {
		t.Fatalf("seed data: %v", err)
	}

	return db
}

func TestExecuteBasicQuery(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	exec := New(db, slog.Default())
	result, err := exec.Execute(context.Background(), "agent-a",
		"SELECT id, body, priority FROM my_messages ORDER BY id")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(result.Columns) != 3 {
		t.Errorf("expected 3 columns, got %d", len(result.Columns))
	}
	if result.Columns[0] != "id" || result.Columns[1] != "body" || result.Columns[2] != "priority" {
		t.Errorf("unexpected columns: %v", result.Columns)
	}

	// agent-a should see: DM to it (1), DM from it (2), general posts (4,5), news post (6)
	// Should NOT see: DM to agent-b (3), private channel msg (7)
	if result.RowCount < 4 {
		t.Errorf("expected at least 4 rows for agent-a, got %d", result.RowCount)
	}

	// Verify agent-b's DM and private channel msg are NOT visible
	for _, row := range result.Rows {
		id := row[0]
		if id == int64(3) {
			t.Error("agent-a should NOT see message 3 (DM to agent-b)")
		}
		if id == int64(7) {
			t.Error("agent-a should NOT see message 7 (private channel, not joined)")
		}
	}
}

func TestAccessControlAgentB(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	exec := New(db, slog.Default())
	result, err := exec.Execute(context.Background(), "agent-b",
		"SELECT id, body FROM my_messages ORDER BY id")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	// agent-b should see: DM to it (3), general posts (4,5), private channel (7)
	// Should NOT see: DM to agent-a (1), DM from agent-a (2), news post (6)
	hasMsg3 := false
	hasMsg7 := false
	for _, row := range result.Rows {
		id := row[0]
		if id == int64(3) {
			hasMsg3 = true
		}
		if id == int64(7) {
			hasMsg7 = true
		}
		if id == int64(1) {
			t.Error("agent-b should NOT see message 1 (DM to agent-a)")
		}
		if id == int64(6) {
			t.Error("agent-b should NOT see message 6 (news channel, not joined)")
		}
	}
	if !hasMsg3 {
		t.Error("agent-b should see message 3 (DM to it)")
	}
	if !hasMsg7 {
		t.Error("agent-b should see message 7 (private channel, joined)")
	}
}

func TestQueryChannelMessages(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	exec := New(db, slog.Default())
	result, err := exec.Execute(context.Background(), "agent-a",
		"SELECT id, body, channel_name FROM channel_messages WHERE channel_name = 'news-mcpproxy'")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if result.RowCount != 1 {
		t.Errorf("expected 1 news message, got %d", result.RowCount)
	}
}

func TestQueryMyChannels(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	exec := New(db, slog.Default())
	result, err := exec.Execute(context.Background(), "agent-a",
		"SELECT name FROM my_channels ORDER BY name")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	// agent-a is in: general, news-mcpproxy (not private-channel)
	if result.RowCount != 2 {
		t.Errorf("expected 2 channels for agent-a, got %d", result.RowCount)
	}
}

func TestValidationRejectsInsert(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	exec := New(db, slog.Default())
	_, err := exec.Execute(context.Background(), "agent-a",
		"INSERT INTO messages (body) VALUES ('evil')")
	if err == nil {
		t.Fatal("expected INSERT to be rejected")
	}
	if !contains(err.Error(), "only SELECT") {
		t.Errorf("expected 'only SELECT' error, got: %v", err)
	}
}

func TestValidationRejectsDrop(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	exec := New(db, slog.Default())
	_, err := exec.Execute(context.Background(), "agent-a",
		"SELECT 1; DROP TABLE messages")
	if err == nil {
		t.Fatal("expected multi-statement to be rejected")
	}
}

func TestValidationRejectsUpdate(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	exec := New(db, slog.Default())
	_, err := exec.Execute(context.Background(), "agent-a",
		"UPDATE messages SET body = 'hacked'")
	if err == nil {
		t.Fatal("expected UPDATE to be rejected")
	}
}

func TestValidationRejectsPragma(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	exec := New(db, slog.Default())
	_, err := exec.Execute(context.Background(), "agent-a",
		"SELECT * FROM pragma_table_info('messages')")
	if err == nil {
		t.Fatal("expected PRAGMA in SELECT to be rejected")
	}
}

func TestEmptyQuery(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	exec := New(db, slog.Default())
	_, err := exec.Execute(context.Background(), "agent-a", "")
	if err == nil {
		t.Fatal("expected empty query to be rejected")
	}
}

func TestCTEQuery(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	exec := New(db, slog.Default())
	result, err := exec.Execute(context.Background(), "agent-a",
		"WITH high_prio AS (SELECT * FROM my_messages WHERE priority >= 7) SELECT id, priority FROM high_prio")
	if err != nil {
		t.Fatalf("CTE query failed: %v", err)
	}

	// agent-a should see high-priority messages it has access to
	if result.RowCount == 0 {
		t.Error("expected at least 1 high-priority message")
	}
}

func TestEmptyResultSet(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	exec := New(db, slog.Default())
	result, err := exec.Execute(context.Background(), "agent-a",
		"SELECT * FROM my_messages WHERE body = 'nonexistent'")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if result.RowCount != 0 {
		t.Errorf("expected 0 rows, got %d", result.RowCount)
	}
	if result.Rows == nil {
		t.Error("rows should be empty array, not nil")
	}
	if result.Truncated {
		t.Error("should not be truncated")
	}
}

func TestLimitEnforcement(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert 150 messages to test limit
	for i := 100; i < 250; i++ {
		_, _ = db.Exec("INSERT INTO messages (id, from_agent, to_agent, body) VALUES (?, 'algis', 'agent-a', 'msg')", i)
	}

	exec := New(db, slog.Default())
	result, err := exec.Execute(context.Background(), "agent-a",
		"SELECT id FROM my_messages")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if result.RowCount > MaxRows {
		t.Errorf("expected max %d rows, got %d", MaxRows, result.RowCount)
	}
	if !result.Truncated {
		t.Error("expected truncated=true for large result set")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
