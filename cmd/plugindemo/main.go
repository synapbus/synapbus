// Command plugindemo is a minimal end-to-end server that wires the plugin
// framework to a real HTTP listener. It is the executable used by the
// integration tests and by operators exercising the plugin toggle flow.
//
// Design notes:
//   - SIGHUP reloads config and rebuilds the registry in place. The HTTP
//     listener is kept; the mux is swapped atomically. This approximates
//     tableflip's socket-preserving restart without the cross-process
//     handoff — adequate for the in-process enable/disable use case.
//   - SIGTERM / SIGINT triggers graceful shutdown: lifecycle plugins are
//     stopped in reverse order, then the HTTP server drains.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/plugin"
	"github.com/synapbus/synapbus/internal/plugin/plugintest"
	"github.com/synapbus/synapbus/internal/plugins/demo"
)

// defaultPlugins is the explicit list of compiled-in plugins.
// Adding a new plugin is one line here.
func defaultPlugins() []plugin.Plugin {
	return []plugin.Plugin{
		demo.New(),
	}
}

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath string
		dataDir    string
		addr       string
	)
	flag.StringVar(&configPath, "config", "synapbus.yaml", "path to config file")
	flag.StringVar(&dataDir, "data", "./data", "data directory")
	flag.StringVar(&addr, "addr", ":8080", "HTTP listen address")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("mkdir data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "plugindemo.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	state := &serverState{
		logger:     logger,
		db:         db,
		dataDir:    dataDir,
		configPath: configPath,
		addr:       addr,
	}
	if err := state.reload(rootCtx); err != nil {
		return fmt.Errorf("initial reload: %w", err)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           state.muxHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	go func() {
		logger.Info("http listen", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
		}
	}()

	for {
		select {
		case <-rootCtx.Done():
			return rootCtx.Err()
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGHUP:
				start := time.Now()
				logger.Info("SIGHUP received, reloading config", "config", configPath)
				if err := state.reload(rootCtx); err != nil {
					logger.Error("reload failed", "err", err)
					continue
				}
				srv.Handler = state.muxHandler()
				logger.Info("reload complete", "duration_ms", time.Since(start).Milliseconds())
			case syscall.SIGTERM, syscall.SIGINT:
				logger.Info("shutdown signal", "sig", sig.String())
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				state.shutdown(shutdownCtx)
				_ = srv.Shutdown(shutdownCtx)
				cancel()
				return nil
			}
		}
	}
}

// serverState holds everything that can be swapped on reload.
type serverState struct {
	mu sync.RWMutex

	logger     *slog.Logger
	db         *sql.DB
	dataDir    string
	configPath string
	addr       string

	reg *plugin.Registry

	// mux is the composed chi router. atomic.Pointer lets muxHandler return
	// a closure that always sees the latest mux without locking.
	mux atomic.Pointer[http.Handler]
}

func (s *serverState) muxHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := s.mux.Load()
		if h == nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		(*h).ServeHTTP(w, r)
	})
}

// reload reads config, builds a new registry, initializes all enabled
// plugins, and swaps the HTTP mux atomically. On error, the previous mux
// stays in place.
func (s *serverState) reload(ctx context.Context) error {
	cfg, err := plugin.LoadConfig(s.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.ValidatePluginNames(); err != nil {
		return err
	}
	// Shutdown old registry before swapping, so lifecycle goroutines stop.
	s.mu.Lock()
	old := s.reg
	s.mu.Unlock()
	if old != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		old.ShutdownAll(shutdownCtx)
		cancel()
	}

	reg, err := plugin.NewRegistry(defaultPlugins(), cfg)
	if err != nil {
		return fmt.Errorf("new registry: %w", err)
	}
	factory := s.hostFactory()
	if err := reg.InitAll(ctx, factory); err != nil {
		return fmt.Errorf("init plugins: %w", err)
	}

	s.mu.Lock()
	s.reg = reg
	s.mu.Unlock()

	mux := s.buildRouter(reg)
	s.mux.Store(&mux)
	return nil
}

func (s *serverState) shutdown(ctx context.Context) {
	s.mu.RLock()
	reg := s.reg
	s.mu.RUnlock()
	if reg != nil {
		reg.ShutdownAll(ctx)
	}
}

func (s *serverState) hostFactory() func(string, plugin.CapabilityContext) plugin.Host {
	cfg, _ := plugin.LoadConfig(s.configPath)
	return func(name string, _ plugin.CapabilityContext) plugin.Host {
		return plugin.Host{
			Logger:  s.logger.With("plugin", name),
			DB:      s.db,
			Events:  plugin.NewEventBus(),
			Config:  cfg.ConfigFor(name),
			DataDir: filepath.Join(s.dataDir, "plugins", name),
			Secrets: plugintest.NewScopedSecrets(name),
			BaseURL: "http://localhost" + s.addr,
			DefaultOwner: &plugin.Owner{
				ID: 1, Username: "admin", Email: "admin@example.test",
			},
		}
	}
}

func (s *serverState) buildRouter(reg *plugin.Registry) http.Handler {
	var r http.Handler
	root := chi.NewRouter()
	root.Use(middleware.Recoverer)

	// /api/plugins/status
	root.Get("/api/plugins/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reg.Status())
	})

	// Admin toggle endpoints live under /api/admin/plugins/ to avoid colliding
	// with plugins' own REST routes mounted under /api/plugins/<name>/.
	root.Post("/api/admin/plugins/{name}/enable", s.toggleHandler(true))
	root.Post("/api/admin/plugins/{name}/disable", s.toggleHandler(false))

	// /api/actions/{name} — invoke a registered action
	root.Post("/api/actions/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		var args map[string]any
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
				http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		result, err := reg.CallAction(r.Context(), name, args)
		if err != nil {
			if strings.Contains(err.Error(), "not registered") {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})

	// Per-plugin REST routes mounted under /api/plugins/<name>/
	for _, mount := range reg.RouteMounts() {
		sub := chi.NewRouter()
		mount.Setup(chiRouter{r: sub})
		root.Mount("/api/plugins/"+mount.Plugin, sub)
	}

	// Per-plugin Web UI panel mounted under /ui/plugins/<name>/
	for _, panelName := range enabledPanels(reg) {
		handler := reg.PanelHandler(panelName)
		if handler == nil {
			continue
		}
		// Strip the prefix so the plugin's handler sees "/".
		prefix := "/ui/plugins/" + panelName
		root.Handle(prefix, http.StripPrefix(prefix, handler))
		root.Handle(prefix+"/", http.StripPrefix(prefix+"/", handler))
		root.Handle(prefix+"/*", http.StripPrefix(prefix, handler))
	}

	// Fallback index page.
	root.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "SynapBus plugindemo · %d plugins started · see /api/plugins/status\n",
			countStarted(reg))
	})

	r = root
	return r
}

func enabledPanels(reg *plugin.Registry) []string {
	seen := map[string]struct{}{}
	for _, panel := range reg.Panels() {
		// Panels[i].ID is the plugin name for our demo; we look up the handler by panel ID.
		// When multiple panels per plugin land, this needs a panel->plugin map in the registry.
		seen[panel.ID] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}

func countStarted(reg *plugin.Registry) int {
	n := 0
	for _, e := range reg.Status().All() {
		if e.Status == plugin.StatusStarted {
			n++
		}
	}
	return n
}

func (s *serverState) toggleHandler(enable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if !plugin.ValidateName(name) {
			http.Error(w, "invalid plugin name", http.StatusBadRequest)
			return
		}
		cfg, err := plugin.LoadConfig(s.configPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		cfg.SetEnabled(name, enable)
		if err := cfg.Save(s.configPath); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Trigger reload via SIGHUP so we exercise the same code path an
		// external operator would.
		proc, err := os.FindProcess(os.Getpid())
		if err == nil {
			_ = proc.Signal(syscall.SIGHUP)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":    name,
			"enabled": enable,
			"restart": true,
		})
	}
}

// chiRouter adapts chi.Router to the plugin.Router interface.
type chiRouter struct{ r chi.Router }

func (a chiRouter) Handle(p string, h http.Handler)    { a.r.Handle(p, h) }
func (a chiRouter) Method(m, p string, h http.Handler) { a.r.Method(m, p, h) }
func (a chiRouter) Get(p string, h http.HandlerFunc)    { a.r.Get(p, h) }
func (a chiRouter) Post(p string, h http.HandlerFunc)   { a.r.Post(p, h) }
func (a chiRouter) Put(p string, h http.HandlerFunc)    { a.r.Put(p, h) }
func (a chiRouter) Delete(p string, h http.HandlerFunc) { a.r.Delete(p, h) }

// unused imports guard (io) for future log-to-file feature.
var _ = io.Discard
