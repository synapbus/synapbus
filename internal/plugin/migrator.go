package plugin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

const createPluginMigrationsTable = `
CREATE TABLE IF NOT EXISTS plugin_migrations (
    plugin     TEXT NOT NULL,
    version    INTEGER NOT NULL,
    name       TEXT NOT NULL,
    checksum   TEXT NOT NULL,
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin, version)
);`

// tablePrefixRE matches any CREATE TABLE or ALTER TABLE referring to a table
// not prefixed with plugin_<name>_ or the allow-listed plugin_migrations.
var createTableRE = regexp.MustCompile(`(?i)CREATE\s+TABLE(?:\s+IF\s+NOT\s+EXISTS)?\s+([a-zA-Z_][a-zA-Z0-9_]*)`)

// MigrationResult is a record of what was applied for a plugin.
type MigrationResult struct {
	Plugin  string
	Applied []int // versions newly applied this run
}

// ApplyMigrations runs all unapplied migrations for the given plugin.
// Migrations for a plugin are applied inside one transaction per version.
// A previously-applied migration whose SQL changed (checksum mismatch)
// is refused.
func ApplyMigrations(ctx context.Context, db *sql.DB, p Plugin) (MigrationResult, error) {
	out := MigrationResult{Plugin: p.Name()}
	hm, ok := p.(HasMigrations)
	if !ok {
		return out, nil
	}
	if _, err := db.ExecContext(ctx, createPluginMigrationsTable); err != nil {
		return out, fmt.Errorf("create plugin_migrations table: %w", err)
	}
	// Collect already-applied versions + checksums.
	applied := map[int]string{}
	rows, err := db.QueryContext(ctx,
		`SELECT version, checksum FROM plugin_migrations WHERE plugin = ?`, p.Name())
	if err != nil {
		return out, fmt.Errorf("load applied migrations: %w", err)
	}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			rows.Close()
			return out, err
		}
		applied[v] = sum
	}
	rows.Close()

	migs := hm.Migrations()
	// Guard: enforce table prefix. Also: allow FTS virtual tables and indexes;
	// reject only CREATE TABLE referencing names outside plugin_<name>_*.
	for _, m := range migs {
		if err := validateMigrationSQL(p.Name(), m); err != nil {
			return out, err
		}
	}
	// Sort by version for deterministic apply order.
	sortMigrations(migs)
	for _, m := range migs {
		sum := checksumSQL(m.SQL)
		if priorSum, wasApplied := applied[m.Version]; wasApplied {
			if priorSum != sum {
				return out, fmt.Errorf(
					"plugin %q migration %d (%s): checksum mismatch (was %s, now %s). "+
						"Migrations are immutable once applied",
					p.Name(), m.Version, m.Name, priorSum, sum)
			}
			continue // already applied, no-op
		}
		if err := applyOne(ctx, db, p.Name(), m, sum); err != nil {
			return out, err
		}
		out.Applied = append(out.Applied, m.Version)
	}
	return out, nil
}

func applyOne(ctx context.Context, db *sql.DB, plugin string, m Migration, checksum string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for %s:%d: %w", plugin, m.Version, err)
	}
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply %s:%d (%s): %w", plugin, m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO plugin_migrations (plugin, version, name, checksum) VALUES (?, ?, ?, ?)`,
		plugin, m.Version, m.Name, checksum,
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record %s:%d: %w", plugin, m.Version, err)
	}
	return tx.Commit()
}

func checksumSQL(s string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(sum[:])
}

func sortMigrations(ms []Migration) {
	// tiny insertion sort; plugin chains are short
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0 && ms[j-1].Version > ms[j].Version; j-- {
			ms[j-1], ms[j] = ms[j], ms[j-1]
		}
	}
}

// validateMigrationSQL rejects CREATE TABLE statements referring to names
// not prefixed with plugin_<name>_. Indexes, views, triggers, and virtual-
// table clauses are not enforced here; they are expected to reference
// plugin-owned tables anyway.
func validateMigrationSQL(pluginName string, m Migration) error {
	prefix := "plugin_" + pluginName + "_"
	for _, match := range createTableRE.FindAllStringSubmatch(m.SQL, -1) {
		tbl := strings.ToLower(match[1])
		// Allow the core table; no plugin should try to create it but
		// we tolerate being defensive.
		if tbl == "plugin_migrations" {
			continue
		}
		if !strings.HasPrefix(tbl, strings.ToLower(prefix)) {
			return fmt.Errorf(
				"plugin %q migration %d (%s): table %q must be prefixed %q",
				pluginName, m.Version, m.Name, tbl, prefix)
		}
	}
	return nil
}

// AppliedVersions returns the migration versions already recorded for the plugin.
func AppliedVersions(ctx context.Context, db *sql.DB, plugin string) ([]int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT version FROM plugin_migrations WHERE plugin = ? ORDER BY version`, plugin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
