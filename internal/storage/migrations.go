package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
)

//go:embed schema/*.sql
var embeddedSchema embed.FS

// RunMigrations applies unapplied SQL migrations from the embedded schema directory.
// Migrations are tracked in the schema_migrations table.
func RunMigrations(ctx context.Context, db *sql.DB) error {
	return runMigrationsFromFS(ctx, db, embeddedSchema, "schema")
}

// runMigrationsFromFS applies migrations from a given filesystem and directory path.
// Exported for testing with custom migration files.
func runMigrationsFromFS(ctx context.Context, db *sql.DB, fsys fs.FS, dir string) error {
	// Create schema_migrations table if it doesn't exist
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	// Get applied versions
	applied, err := getAppliedVersions(ctx, db)
	if err != nil {
		return fmt.Errorf("get applied versions: %w", err)
	}

	// Read migration files
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("read migration directory: %w", err)
	}

	// Parse and sort migration files
	type migration struct {
		version  int
		filename string
	}
	var migrations []migration

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := parseMigrationVersion(entry.Name())
		if err != nil {
			slog.Warn("skipping non-migration file", "filename", entry.Name(), "error", err)
			continue
		}
		migrations = append(migrations, migration{version: version, filename: entry.Name()})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	// Apply unapplied migrations
	for _, m := range migrations {
		if applied[m.version] {
			slog.Debug("migration already applied", "version", m.version, "filename", m.filename)
			continue
		}

		content, err := fs.ReadFile(fsys, dir+"/"+m.filename)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", m.filename, err)
		}

		if err := applyMigration(ctx, db, m.version, string(content)); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.filename, err)
		}

		slog.Info("migration applied", "version", m.version, "filename", m.filename)
	}

	return nil
}

// parseMigrationVersion extracts the version number from a migration filename.
// Expected format: NNN_description.sql (e.g., 001_initial.sql)
func parseMigrationVersion(filename string) (int, error) {
	parts := strings.SplitN(filename, "_", 2)
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid migration filename: %s", filename)
	}
	version, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid version number in %s: %w", filename, err)
	}
	return version, nil
}

// getAppliedVersions returns a set of already-applied migration versions.
func getAppliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// applyMigration runs a migration inside a transaction and records it.
func applyMigration(ctx context.Context, db *sql.DB, version int, content string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Execute migration SQL (may contain multiple statements)
	if _, err := tx.ExecContext(ctx, content); err != nil {
		return fmt.Errorf("execute migration: %w", err)
	}

	// Record migration
	if _, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_migrations (version) VALUES (?)", version,
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	return tx.Commit()
}
