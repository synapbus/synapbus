package plugintest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/synapbus/synapbus/internal/plugin"
)

// HasTool asserts the registry exposes a tool with the given name.
func HasTool(t *testing.T, reg *plugin.Registry, name string) {
	t.Helper()
	if _, ok := reg.MCPTool(name); !ok {
		t.Fatalf("expected MCP tool %q to be registered; got %v", name, reg.MCPTools())
	}
}

// HasAction asserts the registry exposes an action with the given name.
func HasAction(t *testing.T, reg *plugin.Registry, name string) {
	t.Helper()
	if _, ok := reg.Action(name); !ok {
		t.Fatalf("expected action %q to be registered; got %v", name, reg.Actions())
	}
}

// HasPanel asserts the registry exposes a panel with the given ID.
func HasPanel(t *testing.T, reg *plugin.Registry, id string) {
	t.Helper()
	for _, p := range reg.Panels() {
		if p.ID == id {
			return
		}
	}
	t.Fatalf("expected panel with id %q", id)
}

// HasChannelType asserts the registry exposes a channel type with the given name.
func HasChannelType(t *testing.T, reg *plugin.Registry, name string) {
	t.Helper()
	if _, ok := reg.ChannelType(name); !ok {
		t.Fatalf("expected channel type %q to be registered", name)
	}
}

// HasMigration asserts the named plugin has applied a migration at the given version.
func HasMigration(t *testing.T, db *sql.DB, plugin string, version int) {
	t.Helper()
	versions, err := applied(db, plugin)
	if err != nil {
		t.Fatalf("HasMigration: %v", err)
	}
	for _, v := range versions {
		if v == version {
			return
		}
	}
	t.Fatalf("plugin %q: migration %d not applied (have %v)", plugin, version, versions)
}

// PluginStarted asserts the named plugin has status == started.
func PluginStarted(t *testing.T, reg *plugin.Registry, name string) {
	t.Helper()
	s, ok := reg.Status().Get(name)
	if !ok {
		t.Fatalf("plugin %q not in status store", name)
	}
	if s.Status != plugin.StatusStarted {
		t.Fatalf("plugin %q: expected status started, got %q (error=%q)", name, s.Status, s.ErrorMessage)
	}
}

// PluginFailed asserts the named plugin has status == failed and an error message.
func PluginFailed(t *testing.T, reg *plugin.Registry, name string) {
	t.Helper()
	s, ok := reg.Status().Get(name)
	if !ok {
		t.Fatalf("plugin %q not in status store", name)
	}
	if s.Status != plugin.StatusFailed {
		t.Fatalf("plugin %q: expected status failed, got %q", name, s.Status)
	}
	if s.ErrorMessage == "" {
		t.Fatalf("plugin %q: failed status should carry an error message", name)
	}
}

func applied(db *sql.DB, plugin string) ([]int, error) {
	rows, err := db.QueryContext(context.Background(),
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
