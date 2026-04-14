// docgardener is a self-contained demo driver for the dynamic
// agent spawning feature (spec 018). It talks directly to the
// SynapBus SQLite database that a running `synapbus serve` instance
// created, drives a goal → task tree → spawned specialists flow
// through the new primitives, and renders a rich HTML report.
//
// It is intentionally NOT wired through the MCP tool layer or the
// reactor — the MVP's goal is to prove the core data primitives
// (goals, goal_tasks, config_hash, delegation cap, reputation ledger,
// atomic claim, cost rollup) work end-to-end and produce a
// human-readable report. Real LLM autonomy + subprocess execution
// is a follow-up PR (see specs/018-dynamic-agent-spawning/tasks.md).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
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
		Short: "Dynamic-agent-spawning demo driver",
	}

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Execute the doc-gardener demo flow end-to-end",
		RunE:  runDemo,
	}
	runCmd.Flags().StringVar(&flagDBPath, "db", "./data/synapbus.db", "Path to SynapBus SQLite DB")

	reportCmd := &cobra.Command{
		Use:   "report",
		Short: "Render the HTML report for a completed run",
		RunE:  renderReport,
	}
	reportCmd.Flags().StringVar(&flagDBPath, "db", "./data/synapbus.db", "Path to SynapBus SQLite DB")
	reportCmd.Flags().Int64Var(&flagGoalID, "goal", 0, "Goal id to report on (0 = latest)")
	reportCmd.Flags().StringVar(&flagOutputPath, "out", "./report.html", "Output HTML file path")

	root.AddCommand(runCmd, reportCmd)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// openDB opens the SynapBus SQLite DB with the same settings the
// server uses (WAL, foreign keys on) so direct writes interleave
// safely with the running process.
func openDB(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("db not found at %s (did you run ./start.sh?): %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_foreign_keys=on&_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)", abs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func runDemo(_ *cobra.Command, _ []string) error {
	db, err := openDB(flagDBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger = logger.With("component", "docgardener")

	flow := newFlow(db, logger)
	if err := flow.bootstrap(ctx); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	goalID, err := flow.run(ctx)
	if err != nil {
		return fmt.Errorf("demo run: %w", err)
	}

	// Leave a marker so report.sh knows which goal is "latest".
	if err := os.WriteFile(".last_goal_id", []byte(fmt.Sprintf("%d\n", goalID)), 0644); err != nil {
		logger.Warn("could not write .last_goal_id", "err", err)
	}

	fmt.Printf("\n✓ Demo run complete. Goal id: %d\n", goalID)
	fmt.Printf("  Render report:  ./report.sh\n")
	return nil
}
