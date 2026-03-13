// Package storage provides SQLite storage layer for SynapBus.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps a *sql.DB with SynapBus-specific configuration.
type DB struct {
	*sql.DB
}

// New opens a SQLite database with WAL mode, busy_timeout, and foreign keys enabled.
// If dataDir is empty or ":memory:", an in-memory database is used.
func New(ctx context.Context, dataDir string) (*DB, error) {
	var dsn string

	if dataDir == "" || dataDir == ":memory:" {
		dsn = ":memory:"
	} else {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
		dsn = filepath.Join(dataDir, "synapbus.db")
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Configure SQLite pragmas
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}

	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("execute %s: %w", pragma, err)
		}
	}

	// Verify settings
	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		db.Close()
		return nil, fmt.Errorf("verify journal_mode: %w", err)
	}

	slog.Info("database opened",
		"dsn", dsn,
		"journal_mode", journalMode,
	)

	return &DB{DB: db}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.DB.Close()
}
