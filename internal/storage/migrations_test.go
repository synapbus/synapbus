package storage

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	// Enable foreign keys for test DB
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRunMigrations(t *testing.T) {
	tests := []struct {
		name       string
		files      fstest.MapFS
		wantErr    bool
		wantCount  int
	}{
		{
			name: "apply single migration",
			files: fstest.MapFS{
				"migrations/001_create_test.sql": &fstest.MapFile{
					Data: []byte(`CREATE TABLE IF NOT EXISTS test_table (
						id INTEGER PRIMARY KEY,
						name TEXT NOT NULL
					);`),
				},
			},
			wantErr:   false,
			wantCount: 1,
		},
		{
			name: "apply multiple migrations in order",
			files: fstest.MapFS{
				"migrations/001_first.sql": &fstest.MapFile{
					Data: []byte(`CREATE TABLE IF NOT EXISTS first_table (id INTEGER PRIMARY KEY);`),
				},
				"migrations/002_second.sql": &fstest.MapFile{
					Data: []byte(`CREATE TABLE IF NOT EXISTS second_table (id INTEGER PRIMARY KEY);`),
				},
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name:      "empty directory succeeds",
			files:     fstest.MapFS{
				"migrations/readme.txt": &fstest.MapFile{Data: []byte("not a sql file")},
			},
			wantErr:   false,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			ctx := context.Background()

			err := runMigrationsFromFS(ctx, db, tt.files, "migrations")
			if (err != nil) != tt.wantErr {
				t.Fatalf("RunMigrations() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}

			// Verify migrations were recorded
			var count int
			err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
			if err != nil {
				t.Fatalf("failed to count migrations: %v", err)
			}
			if count != tt.wantCount {
				t.Errorf("migration count = %d, want %d", count, tt.wantCount)
			}
		})
	}
}

func TestRunMigrations_Idempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	files := fstest.MapFS{
		"migrations/001_create.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE IF NOT EXISTS idempotent_test (id INTEGER PRIMARY KEY);`),
		},
	}

	// Run migrations twice
	if err := runMigrationsFromFS(ctx, db, files, "migrations"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := runMigrationsFromFS(ctx, db, files, "migrations"); err != nil {
		t.Fatalf("second run: %v", err)
	}

	// Verify only one migration recorded
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 1 {
		t.Errorf("migration count = %d after two runs, want 1", count)
	}
}

func TestRunMigrations_SequentialOrder(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	files := fstest.MapFS{
		"migrations/003_third.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE IF NOT EXISTS third (id INTEGER PRIMARY KEY);`),
		},
		"migrations/001_first.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE IF NOT EXISTS first (id INTEGER PRIMARY KEY);`),
		},
		"migrations/002_second.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE IF NOT EXISTS second (id INTEGER PRIMARY KEY);`),
		},
	}

	if err := runMigrationsFromFS(ctx, db, files, "migrations"); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Verify all three migrations were applied
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 3 {
		t.Errorf("migration count = %d, want 3", count)
	}

	// Verify order by checking applied_at ordering matches version ordering
	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY applied_at")
	if err != nil {
		t.Fatalf("query versions: %v", err)
	}
	defer rows.Close()

	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan version: %v", err)
		}
		versions = append(versions, v)
	}

	if len(versions) != 3 {
		t.Fatalf("got %d versions, want 3", len(versions))
	}

	for i := 1; i < len(versions); i++ {
		if versions[i] <= versions[i-1] {
			t.Errorf("migrations not in order: %v", versions)
			break
		}
	}
}

func TestRunMigrations_EmbeddedSchema(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Run the actual embedded migrations
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations (embedded): %v", err)
	}

	// Verify key tables exist
	tables := []string{"messages", "conversations", "agents", "traces", "inbox_state", "channels"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}
}

// TestMigration028_MemoryConsolidation verifies the proactive-memory + dream
// worker migration applies cleanly and all six tables plus the memory_status
// view are queryable. Doubles as a basic INSERT/SELECT round-trip for each
// new table.
func TestMigration028_MemoryConsolidation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// 1. Verify all six tables exist.
	wantTables := []string{
		"memory_core",
		"memory_links",
		"memory_consolidation_jobs",
		"memory_pins",
		"memory_dispatch_tokens",
		"memory_injections",
	}
	for _, table := range wantTables {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}

	// 2. Verify memory_status view exists.
	var viewName string
	if err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='view' AND name=?", "memory_status",
	).Scan(&viewName); err != nil {
		t.Errorf("memory_status view not found: %v", err)
	}

	// 3. INSERT/SELECT round-trip for each new table.

	// memory_core
	if _, err := db.ExecContext(ctx,
		`INSERT INTO memory_core (owner_id, agent_name, blob, updated_by)
		 VALUES (?, ?, ?, ?)`,
		"1", "agent-a", "core blob", "human:1",
	); err != nil {
		t.Fatalf("insert memory_core: %v", err)
	}
	var blob string
	if err := db.QueryRowContext(ctx,
		`SELECT blob FROM memory_core WHERE owner_id=? AND agent_name=?`, "1", "agent-a",
	).Scan(&blob); err != nil {
		t.Fatalf("select memory_core: %v", err)
	}
	if blob != "core blob" {
		t.Errorf("memory_core blob = %q, want %q", blob, "core blob")
	}

	// memory_consolidation_jobs (needed for FK on dispatch tokens)
	res, err := db.ExecContext(ctx,
		`INSERT INTO memory_consolidation_jobs (owner_id, job_type, trigger_reason)
		 VALUES (?, ?, ?)`,
		"1", "reflection", "watermark:25",
	)
	if err != nil {
		t.Fatalf("insert memory_consolidation_jobs: %v", err)
	}
	jobID, _ := res.LastInsertId()
	var status string
	if err := db.QueryRowContext(ctx,
		`SELECT status FROM memory_consolidation_jobs WHERE id=?`, jobID,
	).Scan(&status); err != nil {
		t.Fatalf("select memory_consolidation_jobs: %v", err)
	}
	if status != "pending" {
		t.Errorf("default status = %q, want pending", status)
	}

	// memory_links
	if _, err := db.ExecContext(ctx,
		`INSERT INTO memory_links (src_message_id, dst_message_id, relation_type, owner_id, created_by)
		 VALUES (?, ?, ?, ?, ?)`,
		1, 2, "refines", "1", "auto:test",
	); err != nil {
		t.Fatalf("insert memory_links: %v", err)
	}
	var relType string
	if err := db.QueryRowContext(ctx,
		`SELECT relation_type FROM memory_links WHERE src_message_id=? AND dst_message_id=?`, 1, 2,
	).Scan(&relType); err != nil {
		t.Fatalf("select memory_links: %v", err)
	}
	if relType != "refines" {
		t.Errorf("relation_type = %q, want refines", relType)
	}

	// memory_pins
	if _, err := db.ExecContext(ctx,
		`INSERT INTO memory_pins (owner_id, message_id, pinned_by, note)
		 VALUES (?, ?, ?, ?)`,
		"1", 42, "human:1", "important",
	); err != nil {
		t.Fatalf("insert memory_pins: %v", err)
	}
	var note string
	if err := db.QueryRowContext(ctx,
		`SELECT note FROM memory_pins WHERE owner_id=? AND message_id=?`, "1", 42,
	).Scan(&note); err != nil {
		t.Fatalf("select memory_pins: %v", err)
	}
	if note != "important" {
		t.Errorf("note = %q, want important", note)
	}

	// memory_dispatch_tokens
	if _, err := db.ExecContext(ctx,
		`INSERT INTO memory_dispatch_tokens (token, owner_id, consolidation_job_id, expires_at)
		 VALUES (?, ?, ?, datetime('now', '+15 minutes'))`,
		"tok-abc", "1", jobID,
	); err != nil {
		t.Fatalf("insert memory_dispatch_tokens: %v", err)
	}
	var ownerID string
	if err := db.QueryRowContext(ctx,
		`SELECT owner_id FROM memory_dispatch_tokens WHERE token=?`, "tok-abc",
	).Scan(&ownerID); err != nil {
		t.Fatalf("select memory_dispatch_tokens: %v", err)
	}
	if ownerID != "1" {
		t.Errorf("owner_id = %q, want 1", ownerID)
	}

	// memory_injections
	if _, err := db.ExecContext(ctx,
		`INSERT INTO memory_injections (owner_id, agent_name, tool_name, packet_size_chars, packet_items_count, message_ids)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"1", "agent-a", "my_status", 120, 3, "[1,2,3]",
	); err != nil {
		t.Fatalf("insert memory_injections: %v", err)
	}
	var items int
	if err := db.QueryRowContext(ctx,
		`SELECT packet_items_count FROM memory_injections WHERE owner_id=?`, "1",
	).Scan(&items); err != nil {
		t.Fatalf("select memory_injections: %v", err)
	}
	if items != 3 {
		t.Errorf("packet_items_count = %d, want 3", items)
	}

	// memory_status view: query should succeed (empty rows are fine since
	// the job we inserted is still 'pending').
	rows, err := db.QueryContext(ctx, `SELECT message_id, owner_id, status FROM memory_status`)
	if err != nil {
		t.Fatalf("select memory_status: %v", err)
	}
	rows.Close()
}
