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

// DB wraps a write-only *sql.DB and an optional read-only *sql.DB
// for split connection pool architecture. The write pool has MaxOpenConns=1
// to serialize writes and eliminate SQLITE_BUSY errors. The read pool has
// MaxOpenConns=8 and query_only=ON for safe concurrent reads.
type DB struct {
	*sql.DB          // Write pool (MaxOpenConns=1)
	ReadDB *sql.DB   // Read pool (MaxOpenConns=8, query_only=ON) — nil for :memory: DBs
}

// New opens a SQLite database with WAL mode, split read/write pools, and foreign keys.
// If dataDir is empty or ":memory:", an in-memory database is used (single pool, no split).
func New(ctx context.Context, dataDir string) (*DB, error) {
	var dsn string
	isMemory := dataDir == "" || dataDir == ":memory:"

	if isMemory {
		dsn = ":memory:"
	} else {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
		dsn = filepath.Join(dataDir, "synapbus.db")
	}

	// Open WRITE pool (single connection, serializes all writes)
	writeDB, err := openPool(ctx, dsn, poolConfig{
		maxOpen:   1,
		maxIdle:   1,
		queryOnly: false,
		label:     "write",
	})
	if err != nil {
		return nil, fmt.Errorf("open write pool: %w", err)
	}

	result := &DB{DB: writeDB}

	// For file-based databases, open a separate READ pool
	if !isMemory {
		readDB, err := openPool(ctx, dsn, poolConfig{
			maxOpen:   8,
			maxIdle:   4,
			queryOnly: true,
			label:     "read",
		})
		if err != nil {
			writeDB.Close()
			return nil, fmt.Errorf("open read pool: %w", err)
		}
		result.ReadDB = readDB
	}

	// Verify settings on write pool
	var journalMode string
	if err := writeDB.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		result.Close()
		return nil, fmt.Errorf("verify journal_mode: %w", err)
	}

	slog.Info("database opened",
		"dsn", dsn,
		"journal_mode", journalMode,
		"write_pool", "MaxOpenConns=1",
		"read_pool_enabled", result.ReadDB != nil,
	)

	return result, nil
}

type poolConfig struct {
	maxOpen   int
	maxIdle   int
	queryOnly bool
	label     string
}

func openPool(ctx context.Context, dsn string, cfg poolConfig) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s pool: %w", cfg.label, err)
	}

	db.SetMaxOpenConns(cfg.maxOpen)
	db.SetMaxIdleConns(cfg.maxIdle)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=15000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA wal_autocheckpoint=1000",
	}
	if cfg.queryOnly {
		pragmas = append(pragmas, "PRAGMA query_only=ON")
	}

	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("execute %s: %w", pragma, err)
		}
	}

	return db, nil
}

// QueryDB returns the read pool if available, otherwise falls back to the write pool.
// Use this for all SELECT queries to avoid blocking writers.
func (db *DB) QueryDB() *sql.DB {
	if db.ReadDB != nil {
		return db.ReadDB
	}
	return db.DB
}

// Close closes both the write and read database connections.
func (db *DB) Close() error {
	var errs []error
	if db.ReadDB != nil {
		if err := db.ReadDB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close read pool: %w", err))
		}
	}
	if err := db.DB.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close write pool: %w", err))
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}
