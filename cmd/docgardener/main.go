// docgardener is the rich HTML report renderer for the doc-gardener
// example. It queries an existing SynapBus SQLite DB (the one that the
// docker-isolated agents wrote into) and produces a single-file HTML
// snapshot of the most recent goal: task tree, spawned agents, spend
// per billing code, trust deltas, and a timeline of events.
//
// The orchestration that USED to live in this binary (`docgardener
// agent` per-role subprocess entry, hardcoded task tree, gemini fall-
// back) has been replaced by the MCP-native flow at
// examples/doc-gardener/. All this binary does now is render reports.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	_ "modernc.org/sqlite"
)

var (
	flagDBPath     string
	flagGoalID     int64
	flagOutputPath string
)

func main() {
	root := &cobra.Command{
		Use:   "docgardener",
		Short: "doc-gardener report renderer (queries SynapBus goals/goal_tasks)",
	}

	reportCmd := &cobra.Command{
		Use:   "report",
		Short: "Render the HTML report for a completed run",
		RunE:  renderReport,
	}
	reportCmd.Flags().StringVar(&flagDBPath, "db", "./data/synapbus.db", "Path to SynapBus SQLite DB")
	reportCmd.Flags().Int64Var(&flagGoalID, "goal", 0, "Goal id to report on (0 = latest)")
	reportCmd.Flags().StringVar(&flagOutputPath, "out", "./report.html", "Output HTML file path")

	root.AddCommand(reportCmd)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// openDB opens the SynapBus SQLite DB read-only with WAL so it
// interleaves safely with a running synapbus serve process.
func openDB(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("db not found at %s (did you run ./start.sh?): %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_foreign_keys=on&_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&mode=ro", abs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
