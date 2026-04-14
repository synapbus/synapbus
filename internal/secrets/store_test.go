package secrets_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/secrets"
	"github.com/synapbus/synapbus/internal/storage"
)

// newTestStore opens an in-memory SQLite DB, runs all migrations, seeds a
// user, and returns a ready-to-use *secrets.Store.
func newTestStore(t *testing.T) (*secrets.Store, *sql.DB, int64) {
	t.Helper()

	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Single connection so the in-memory DB persists across queries.
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	if err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Seed a user so created_by FK is satisfied.
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, owner_id, role)
		 VALUES ('tester', 'x', 1, 'admin')`,
	)
	if err != nil {
		// Schema may differ slightly across migrations; try the minimal column set.
		res, err = db.ExecContext(ctx,
			`INSERT INTO users (username, password_hash) VALUES ('tester', 'x')`,
		)
		if err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	uid, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}

	store, err := secrets.NewStore(db, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, db, uid
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	store, _, uid := newTestStore(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"simple", "API_KEY", "sk-abc123"},
		{"empty", "EMPTY", ""},
		{"unicode", "GREETING", "héllo, wörld"},
		{"long", "LONG", strings.Repeat("x", 8192)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sec, err := store.Set(ctx, tc.key, secrets.ScopeUser, uid, uid, tc.value)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			if sec.Name != strings.ToUpper(tc.key) {
				t.Errorf("name: got %q want %q", sec.Name, strings.ToUpper(tc.key))
			}
			got, err := store.Get(ctx, tc.key, secrets.ScopeUser, uid)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != tc.value {
				t.Errorf("Get value: got %q want %q", got, tc.value)
			}
		})
	}
}

func TestSanitizeNameViaSet(t *testing.T) {
	store, _, uid := newTestStore(t)
	ctx := context.Background()

	cases := []struct {
		input   string
		want    string // expected stored name; "" means expect ErrInvalidName
		wantErr bool
	}{
		{"api_key", "API_KEY", false},
		{"OPENAI_KEY", "OPENAI_KEY", false},
		{"Token1", "TOKEN1", false},
		{"FOO_2", "FOO_2", false},
		{"", "", true},
		{"BAD-NAME", "", true},
		{"with space", "", true},
		{"dot.name", "", true},
		{"unicode_é", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			sec, err := store.Set(ctx, tc.input, secrets.ScopeUser, uid, uid, "v")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			if sec.Name != tc.want {
				t.Errorf("name: got %q want %q", sec.Name, tc.want)
			}
		})
	}
}

func TestListHidesValues(t *testing.T) {
	store, _, uid := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Set(ctx, "SECRET1", secrets.ScopeUser, uid, uid, "value-one"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	infos, err := store.List(ctx, []secrets.Scope{{Type: secrets.ScopeUser, ID: uid}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("len: got %d want 1", len(infos))
	}

	// Reflectively assert that Info has no field named like a value.
	tInfo := reflect.TypeOf(infos[0])
	for i := 0; i < tInfo.NumField(); i++ {
		f := tInfo.Field(i)
		lower := strings.ToLower(f.Name)
		if lower == "value" || lower == "plaintext" || lower == "secret" {
			t.Errorf("Info exposes sensitive field %q", f.Name)
		}
	}

	if infos[0].Name != "SECRET1" || !infos[0].Available {
		t.Errorf("unexpected info: %+v", infos[0])
	}
}

func TestRevokeHidesFromList(t *testing.T) {
	store, _, uid := newTestStore(t)
	ctx := context.Background()

	sec, err := store.Set(ctx, "TO_REVOKE", secrets.ScopeUser, uid, uid, "v")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := store.Revoke(ctx, sec.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	infos, err := store.List(ctx, []secrets.Scope{{Type: secrets.ScopeUser, ID: uid}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, i := range infos {
		if i.Name == "TO_REVOKE" {
			t.Fatalf("revoked secret should not appear in List")
		}
	}

	if _, err := store.Get(ctx, "TO_REVOKE", secrets.ScopeUser, uid); err == nil {
		t.Fatalf("Get should fail for revoked secret")
	}

	env, err := store.BuildEnvMap(ctx, uid, 0, 0)
	if err != nil {
		t.Fatalf("BuildEnvMap: %v", err)
	}
	if _, ok := env["TO_REVOKE"]; ok {
		t.Fatalf("revoked secret leaked into env map")
	}

	// Revoking again should return ErrAlreadyRevoked.
	if err := store.Revoke(ctx, sec.ID); err == nil {
		t.Fatalf("expected ErrAlreadyRevoked, got nil")
	}
	// Revoking unknown id should return ErrNotFound.
	if err := store.Revoke(ctx, 99999); err == nil {
		t.Fatalf("expected ErrNotFound, got nil")
	}
}

func TestScopePrecedence(t *testing.T) {
	store, _, uid := newTestStore(t)
	ctx := context.Background()

	const (
		agentID = int64(42)
		taskID  = int64(7)
		name    = "OPENAI_API_KEY"
	)

	if _, err := store.Set(ctx, name, secrets.ScopeUser, uid, uid, "user-val"); err != nil {
		t.Fatalf("user Set: %v", err)
	}
	if _, err := store.Set(ctx, name, secrets.ScopeAgent, agentID, uid, "agent-val"); err != nil {
		t.Fatalf("agent Set: %v", err)
	}
	if _, err := store.Set(ctx, name, secrets.ScopeTask, taskID, uid, "task-val"); err != nil {
		t.Fatalf("task Set: %v", err)
	}

	env, err := store.BuildEnvMap(ctx, uid, agentID, taskID)
	if err != nil {
		t.Fatalf("BuildEnvMap: %v", err)
	}
	if env[name] != "task-val" {
		t.Errorf("task wins: got %q want %q", env[name], "task-val")
	}

	// Without task: agent wins.
	env, err = store.BuildEnvMap(ctx, uid, agentID, 0)
	if err != nil {
		t.Fatalf("BuildEnvMap: %v", err)
	}
	if env[name] != "agent-val" {
		t.Errorf("agent wins: got %q want %q", env[name], "agent-val")
	}

	// Only user.
	env, err = store.BuildEnvMap(ctx, uid, 0, 0)
	if err != nil {
		t.Fatalf("BuildEnvMap: %v", err)
	}
	if env[name] != "user-val" {
		t.Errorf("user wins: got %q want %q", env[name], "user-val")
	}

	// last_used_at should be set after BuildEnvMap.
	infos, err := store.List(ctx, []secrets.Scope{{Type: secrets.ScopeUser, ID: uid}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, i := range infos {
		if i.Name == name {
			found = true
			if i.LastUsedAt == nil {
				t.Errorf("expected LastUsedAt to be set after BuildEnvMap")
			}
		}
	}
	if !found {
		t.Errorf("user-scoped secret missing from List")
	}
}

func TestSetReplacesPrevious(t *testing.T) {
	store, db, uid := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Set(ctx, "ROTATE", secrets.ScopeUser, uid, uid, "v1"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if _, err := store.Set(ctx, "ROTATE", secrets.ScopeUser, uid, uid, "v2"); err != nil {
		t.Fatalf("Set v2: %v", err)
	}

	got, err := store.Get(ctx, "ROTATE", secrets.ScopeUser, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "v2" {
		t.Errorf("got %q want %q", got, "v2")
	}

	// Two history rows should exist; one revoked, one active.
	var total, active int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM secrets WHERE name='ROTATE'`).Scan(&total); err != nil {
		t.Fatalf("count total: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM secrets WHERE name='ROTATE' AND revoked_at IS NULL`).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if total != 2 {
		t.Errorf("total rows: got %d want 2", total)
	}
	if active != 1 {
		t.Errorf("active rows: got %d want 1", active)
	}
}
