package storage

import (
	"context"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		dataDir string
		wantErr bool
	}{
		{
			name:    "in-memory database",
			dataDir: ":memory:",
			wantErr: false,
		},
		{
			name:    "empty string creates in-memory",
			dataDir: "",
			wantErr: false,
		},
		{
			name:    "temp directory",
			dataDir: t.TempDir(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := New(ctx, tt.dataDir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			defer db.Close()

			// Verify WAL mode (in-memory uses "memory" journal mode)
			if tt.dataDir != "" && tt.dataDir != ":memory:" {
				var journalMode string
				err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
				if err != nil {
					t.Fatalf("failed to query journal_mode: %v", err)
				}
				if journalMode != "wal" {
					t.Errorf("journal_mode = %s, want wal", journalMode)
				}
			}

			// Verify foreign keys are enabled
			var fk int
			err = db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
			if err != nil {
				t.Fatalf("failed to query foreign_keys: %v", err)
			}
			if fk != 1 {
				t.Errorf("foreign_keys = %d, want 1", fk)
			}

			// Verify busy_timeout
			var timeout int
			err = db.QueryRow("PRAGMA busy_timeout").Scan(&timeout)
			if err != nil {
				t.Fatalf("failed to query busy_timeout: %v", err)
			}
			if timeout != 15000 {
				t.Errorf("busy_timeout = %d, want 15000", timeout)
			}

			// Verify database is usable via write pool
			_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY)")
			if err != nil {
				t.Fatalf("failed to create test table: %v", err)
			}
		})
	}
}

func TestSplitPools(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	db, err := New(ctx, dir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer db.Close()

	// Run migrations to create tables
	if err := RunMigrations(ctx, db.DB); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	// Verify read pool exists for file-based DB
	if db.ReadDB == nil {
		t.Fatal("expected ReadDB to be non-nil for file-based database")
	}

	// Verify QueryDB returns read pool
	if db.QueryDB() != db.ReadDB {
		t.Error("QueryDB() should return ReadDB when available")
	}

	// Create user first (FK requirement)
	_, err = db.Exec("INSERT INTO users (id, username, password_hash, display_name) VALUES (1, 'testuser', 'hash', 'Test')")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Verify write pool can write
	_, err = db.Exec("INSERT INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES ('test-agent', 'Test', 'ai', '{}', 1, 'hash', 'active')")
	if err != nil {
		t.Fatalf("write pool should allow writes: %v", err)
	}

	// Verify read pool can read
	var name string
	err = db.ReadDB.QueryRow("SELECT name FROM agents WHERE name = 'test-agent'").Scan(&name)
	if err != nil {
		t.Fatalf("read pool should allow reads: %v", err)
	}
	if name != "test-agent" {
		t.Errorf("expected 'test-agent', got %q", name)
	}

	// Verify read pool rejects writes
	_, err = db.ReadDB.Exec("INSERT INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES ('bad', 'Bad', 'ai', '{}', 1, 'hash', 'active')")
	if err == nil {
		t.Fatal("read pool should reject writes (query_only=ON)")
	}
}

func TestInMemoryNoSplitPool(t *testing.T) {
	ctx := context.Background()

	db, err := New(ctx, ":memory:")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer db.Close()

	// In-memory DB should NOT have a separate read pool
	if db.ReadDB != nil {
		t.Error("in-memory DB should not have a separate ReadDB")
	}

	// QueryDB should fall back to write pool
	if db.QueryDB() != db.DB {
		t.Error("QueryDB() should return write pool for in-memory DB")
	}
}
