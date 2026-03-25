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

			// Verify database is usable
			_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY)")
			if err != nil {
				t.Fatalf("failed to create test table: %v", err)
			}
		})
	}
}
