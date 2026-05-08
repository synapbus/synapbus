package plugin_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/plugin"
)

type migPlugin struct {
	name string
	migs []plugin.Migration
}

func (p *migPlugin) Name() string                                   { return p.name }
func (p *migPlugin) Version() string                                { return "0.1.0" }
func (p *migPlugin) Init(ctx context.Context, host plugin.Host) error { return nil }
func (p *migPlugin) Migrations() []plugin.Migration                 { return p.migs }

func openMem(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestApplyMigrations_AppliesOnce(t *testing.T) {
	db := openMem(t)
	p := &migPlugin{name: "demo", migs: []plugin.Migration{
		{Version: 1, Name: "001", SQL: `CREATE TABLE plugin_demo_t (id INTEGER);`},
	}}
	res, err := plugin.ApplyMigrations(context.Background(), db, p)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(res.Applied) != 1 || res.Applied[0] != 1 {
		t.Fatalf("expected applied=[1], got %+v", res.Applied)
	}
	// Running twice is a no-op.
	res2, err := plugin.ApplyMigrations(context.Background(), db, p)
	if err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if len(res2.Applied) != 0 {
		t.Fatalf("expected no-op on second apply, got %+v", res2.Applied)
	}
}

func TestApplyMigrations_RefusesChecksumMismatch(t *testing.T) {
	db := openMem(t)
	p := &migPlugin{name: "demo", migs: []plugin.Migration{
		{Version: 1, Name: "001", SQL: `CREATE TABLE plugin_demo_t (id INTEGER);`},
	}}
	if _, err := plugin.ApplyMigrations(context.Background(), db, p); err != nil {
		t.Fatal(err)
	}
	// Modify the SQL for the same version.
	p.migs[0].SQL = `CREATE TABLE plugin_demo_t (id INTEGER, name TEXT);`
	_, err := plugin.ApplyMigrations(context.Background(), db, p)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}

func TestApplyMigrations_RejectsUnprefixedTable(t *testing.T) {
	db := openMem(t)
	p := &migPlugin{name: "demo", migs: []plugin.Migration{
		{Version: 1, Name: "001", SQL: `CREATE TABLE wrong_table (id INTEGER);`},
	}}
	_, err := plugin.ApplyMigrations(context.Background(), db, p)
	if err == nil {
		t.Fatal("expected rejection for unprefixed table name")
	}
}

func TestApplyMigrations_AppliedVersionsReflectsState(t *testing.T) {
	db := openMem(t)
	p := &migPlugin{name: "demo", migs: []plugin.Migration{
		{Version: 1, Name: "001", SQL: `CREATE TABLE plugin_demo_a (id INTEGER);`},
		{Version: 2, Name: "002", SQL: `CREATE TABLE plugin_demo_b (id INTEGER);`},
	}}
	if _, err := plugin.ApplyMigrations(context.Background(), db, p); err != nil {
		t.Fatal(err)
	}
	vs, err := plugin.AppliedVersions(context.Background(), db, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0] != 1 || vs[1] != 2 {
		t.Fatalf("expected [1,2], got %+v", vs)
	}
}
