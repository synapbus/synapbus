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
