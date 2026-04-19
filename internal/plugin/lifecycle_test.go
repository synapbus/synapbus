package plugin_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/plugin"
	"github.com/synapbus/synapbus/internal/plugin/plugintest"
)

// --- helpers ---

func nopHostFactory(t *testing.T, db *sql.DB) func(string, plugin.CapabilityContext) plugin.Host {
	t.Helper()
	return func(name string, _ plugin.CapabilityContext) plugin.Host {
		return plugin.Host{
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)).With("plugin", name),
			DB:      db,
			Events:  plugin.NewEventBus(),
			Config:  json.RawMessage(`{}`),
			DataDir: t.TempDir(),
			Secrets: plugintest.NewScopedSecrets(name),
		}
	}
}

// --- test doubles ---

type capabilityPlugin struct {
	name    string
	actions []string
	onInit  func(context.Context, plugin.Host) error
	onStart func(context.Context) error
}

func (p *capabilityPlugin) Name() string    { return p.name }
func (p *capabilityPlugin) Version() string { return "0.1.0" }
func (p *capabilityPlugin) Init(ctx context.Context, host plugin.Host) error {
	if p.onInit != nil {
		return p.onInit(ctx, host)
	}
	return nil
}
func (p *capabilityPlugin) Migrations() []plugin.Migration {
	return []plugin.Migration{{
		Version: 1, Name: "001_initial",
		SQL: `CREATE TABLE IF NOT EXISTS plugin_` + p.name + `_t (id INTEGER);`,
	}}
}
func (p *capabilityPlugin) Actions() []plugin.ActionRegistration {
	out := make([]plugin.ActionRegistration, 0, len(p.actions))
	for _, name := range p.actions {
		n := name
		out = append(out, plugin.ActionRegistration{
			Name:          n,
			RequiredScope: plugin.ScopeRead,
			Handler: func(ctx context.Context, args map[string]any) (any, error) {
				return map[string]string{"from": p.name, "action": n}, nil
			},
		})
	}
	return out
}
func (p *capabilityPlugin) RegisterRoutes(r plugin.Router) {
	r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"plugin":"` + p.name + `","ok":true}`))
	})
}
func (p *capabilityPlugin) WebPanels() []plugin.PanelManifest {
	return []plugin.PanelManifest{{ID: p.name, Title: p.name, Route: "/ui/plugins/" + p.name, Scope: "member"}}
}
func (p *capabilityPlugin) PanelHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<h1>" + p.name + "</h1>"))
	})
}
func (p *capabilityPlugin) Start(ctx context.Context) error {
	if p.onStart != nil {
		return p.onStart(ctx)
	}
	return nil
}
func (p *capabilityPlugin) Shutdown(ctx context.Context) error { return nil }

// --- tests ---

func TestInitAll_HappyPath(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	reg, err := plugin.NewRegistry(
		[]plugin.Plugin{
			&capabilityPlugin{name: "alpha", actions: []string{"alpha_say"}},
			&capabilityPlugin{name: "beta", actions: []string{"beta_hello"}},
		},
		&plugin.FileConfig{Plugins: map[string]plugin.PluginConfig{
			"alpha": {Enabled: true},
			"beta":  {Enabled: true},
		}},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := reg.InitAll(context.Background(), nopHostFactory(t, db)); err != nil {
		t.Fatalf("InitAll: %v", err)
	}
	plugintest.HasAction(t, reg, "alpha_say")
	plugintest.HasAction(t, reg, "beta_hello")
	plugintest.PluginStarted(t, reg, "alpha")
	plugintest.PluginStarted(t, reg, "beta")
	plugintest.HasMigration(t, db, "alpha", 1)
	plugintest.HasMigration(t, db, "beta", 1)
	plugintest.HasPanel(t, reg, "alpha")
	plugintest.HasPanel(t, reg, "beta")
}

func TestInitAll_FailurePerPluginIsolated(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	reg, err := plugin.NewRegistry(
		[]plugin.Plugin{
			&capabilityPlugin{name: "good", actions: []string{"good_hi"}},
			&capabilityPlugin{
				name: "bad", actions: []string{"bad_hi"},
				onInit: func(context.Context, plugin.Host) error { return errors.New("boom") },
			},
		},
		&plugin.FileConfig{Plugins: map[string]plugin.PluginConfig{
			"good": {Enabled: true},
			"bad":  {Enabled: true},
		}},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	_ = reg.InitAll(context.Background(), nopHostFactory(t, db))

	plugintest.PluginStarted(t, reg, "good")
	plugintest.HasAction(t, reg, "good_hi")
	plugintest.PluginFailed(t, reg, "bad")
	if _, ok := reg.Action("bad_hi"); ok {
		t.Fatalf("failed plugin's action must not be registered")
	}
}

func TestInitAll_PanicIsolated(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	reg, _ := plugin.NewRegistry(
		[]plugin.Plugin{
			&capabilityPlugin{name: "good", actions: []string{"ok"}},
			&capabilityPlugin{
				name: "panicky", actions: []string{"never"},
				onInit: func(context.Context, plugin.Host) error { panic("bad") },
			},
		},
		&plugin.FileConfig{Plugins: map[string]plugin.PluginConfig{
			"good": {Enabled: true}, "panicky": {Enabled: true},
		}},
	)
	_ = reg.InitAll(context.Background(), nopHostFactory(t, db))
	plugintest.PluginStarted(t, reg, "good")
	plugintest.PluginFailed(t, reg, "panicky")
}

func TestInitAll_DisabledPluginRegistersNothing(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	reg, _ := plugin.NewRegistry(
		[]plugin.Plugin{&capabilityPlugin{name: "off", actions: []string{"off_hi"}}},
		&plugin.FileConfig{Plugins: map[string]plugin.PluginConfig{"off": {Enabled: false}}},
	)
	_ = reg.InitAll(context.Background(), nopHostFactory(t, db))
	if _, ok := reg.Action("off_hi"); ok {
		t.Fatalf("disabled plugin must not register actions")
	}
	s, _ := reg.Status().Get("off")
	if s.Status != plugin.StatusDisabled {
		t.Fatalf("expected status disabled, got %q", s.Status)
	}
}

func TestInitAll_RouteMountsAreRegistered(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	reg, _ := plugin.NewRegistry(
		[]plugin.Plugin{&capabilityPlugin{name: "routes", actions: []string{"r"}}},
		&plugin.FileConfig{Plugins: map[string]plugin.PluginConfig{"routes": {Enabled: true}}},
	)
	_ = reg.InitAll(context.Background(), nopHostFactory(t, db))
	mounts := reg.RouteMounts()
	if len(mounts) != 1 {
		t.Fatalf("expected 1 route mount, got %d", len(mounts))
	}
	// Serve through a stub router to verify wiring.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	router := newStubRouter()
	for _, m := range mounts {
		m.Setup(router)
	}
	router.routes["GET /ping"](rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// stubRouter is a tiny plugin.Router implementation for tests.
type stubRouter struct{ routes map[string]http.HandlerFunc }

func newStubRouter() *stubRouter { return &stubRouter{routes: map[string]http.HandlerFunc{}} }

func (s *stubRouter) Handle(p string, h http.Handler) {
	s.routes["ANY "+p] = h.ServeHTTP
}
func (s *stubRouter) Method(m, p string, h http.Handler) {
	s.routes[m+" "+p] = h.ServeHTTP
}
func (s *stubRouter) Get(p string, h http.HandlerFunc)    { s.routes["GET "+p] = h }
func (s *stubRouter) Post(p string, h http.HandlerFunc)   { s.routes["POST "+p] = h }
func (s *stubRouter) Put(p string, h http.HandlerFunc)    { s.routes["PUT "+p] = h }
func (s *stubRouter) Delete(p string, h http.HandlerFunc) { s.routes["DELETE "+p] = h }
