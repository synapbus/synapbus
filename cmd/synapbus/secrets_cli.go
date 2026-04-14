package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/secrets"
)

// registerSecretsCLI returns the `secrets` cobra command tree for the
// resource-request protocol. Unlike most admin commands it does NOT go
// through the admin socket — it opens the SQLite DB directly. This
// keeps the demo simple, avoids adding socket handlers, and works
// equally well when the server is not running.
func registerSecretsCLI() *cobra.Command {
	var (
		dbPath string
		scope  string
	)

	resolveDB := func() (string, error) {
		if dbPath != "" {
			return dbPath, nil
		}
		if env := os.Getenv("SYNAPBUS_DATA_DIR"); env != "" {
			return filepath.Join(env, "synapbus.db"), nil
		}
		return "./data/synapbus.db", nil
	}

	openDirect := func() (*sql.DB, string, error) {
		path, err := resolveDB()
		if err != nil {
			return nil, "", err
		}
		if _, err := os.Stat(path); err != nil {
			return nil, "", fmt.Errorf("db not found at %s — set --db or SYNAPBUS_DATA_DIR", path)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, "", err
		}
		dsn := fmt.Sprintf("file:%s?_foreign_keys=on&_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)", abs)
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, "", err
		}
		db.SetMaxOpenConns(1)
		return db, filepath.Dir(abs), nil
	}

	parseScope := func(s string) (string, int64, error) {
		// forms: user:<name>, agent:<name>, task:<id>
		if !strings.Contains(s, ":") {
			return "", 0, fmt.Errorf("scope must be user:NAME, agent:NAME, or task:ID")
		}
		typ, ident, _ := strings.Cut(s, ":")
		db, _, err := openDirect()
		if err != nil {
			return "", 0, err
		}
		defer db.Close()
		switch typ {
		case "user":
			var id int64
			err := db.QueryRowContext(context.Background(), `SELECT id FROM users WHERE username=?`, ident).Scan(&id)
			if err != nil {
				return "", 0, fmt.Errorf("user %q not found: %w", ident, err)
			}
			return secrets.ScopeUser, id, nil
		case "agent":
			var id int64
			err := db.QueryRowContext(context.Background(), `SELECT id FROM agents WHERE name=?`, ident).Scan(&id)
			if err != nil {
				return "", 0, fmt.Errorf("agent %q not found: %w", ident, err)
			}
			return secrets.ScopeAgent, id, nil
		case "task":
			id, err := strconv.ParseInt(ident, 10, 64)
			if err != nil {
				return "", 0, fmt.Errorf("task scope id must be an integer")
			}
			return secrets.ScopeTask, id, nil
		}
		return "", 0, fmt.Errorf("unknown scope type %q", typ)
	}

	root := &cobra.Command{
		Use:   "secrets",
		Short: "Manage encrypted scoped secrets (resource-request protocol)",
	}
	root.PersistentFlags().StringVar(&dbPath, "db", "", "Path to synapbus.db (defaults to ./data or SYNAPBUS_DATA_DIR)")

	setCmd := &cobra.Command{
		Use:   "set NAME VALUE",
		Short: "Store a secret under a scope",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, value := args[0], args[1]
			scopeType, scopeID, err := parseScope(scope)
			if err != nil {
				return err
			}
			db, dataDir, err := openDirect()
			if err != nil {
				return err
			}
			defer db.Close()
			store, err := secrets.NewStore(db, dataDir, slog.Default())
			if err != nil {
				return err
			}
			s, err := store.Set(cmd.Context(), name, scopeType, scopeID, 0, value)
			if err != nil {
				return err
			}
			fmt.Printf("stored secret id=%d name=%s scope=%s:%d\n", s.ID, s.Name, scopeType, scopeID)
			return nil
		},
	}
	setCmd.Flags().StringVar(&scope, "scope", "", "Scope (user:NAME, agent:NAME, task:ID)")
	_ = setCmd.MarkFlagRequired("scope")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List secrets visible to a scope (names only — never values)",
		RunE: func(cmd *cobra.Command, args []string) error {
			scopeType, scopeID, err := parseScope(scope)
			if err != nil {
				return err
			}
			db, dataDir, err := openDirect()
			if err != nil {
				return err
			}
			defer db.Close()
			store, err := secrets.NewStore(db, dataDir, slog.Default())
			if err != nil {
				return err
			}
			infos, err := store.List(cmd.Context(), []secrets.Scope{{Type: scopeType, ID: scopeID}})
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSCOPE\tLAST USED")
			for _, i := range infos {
				last := "—"
				if i.LastUsedAt != nil {
					last = i.LastUsedAt.Format("2006-01-02 15:04")
				}
				fmt.Fprintf(tw, "%s\t%s:%d\t%s\n", i.Name, i.ScopeType, i.ScopeID, last)
			}
			return tw.Flush()
		},
	}
	listCmd.Flags().StringVar(&scope, "scope", "", "Scope (user:NAME, agent:NAME, task:ID)")
	_ = listCmd.MarkFlagRequired("scope")

	root.AddCommand(setCmd, listCmd)
	return root
}
