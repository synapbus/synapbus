// Package plugintest provides a Host implementation backed by in-memory
// SQLite and no-op stubs for every core dependency, plus a Run helper that
// exercises a plugin's full lifecycle.
package plugintest

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/plugin"
)

// NopHost returns an in-memory Host suitable for unit tests.
// The database, data directory, event bus, and metrics registry are all
// scoped to the calling test (via t.TempDir and t.Cleanup).
func NopHost(t *testing.T) plugin.Host {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	dir := t.TempDir()
	return plugin.Host{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:          db,
		Messenger:   &nopMessenger{},
		Channels:    &nopChannels{},
		Attachments: &nopAttachments{},
		Search:      &nopSearch{},
		Secrets:     NewScopedSecrets(""),
		Events:      plugin.NewEventBus(),
		Config:      json.RawMessage(`{}`),
		DataDir:     dir,
		Tracer:      &nopTracer{},
		Metrics:     &nopMetrics{},
		DefaultOwner: &plugin.Owner{
			ID: 1, Username: "testowner", Email: "owner@example.test",
		},
		BaseURL: "http://localhost:8080",
	}
}

// HostWithDB returns a Host backed by an existing *sql.DB (useful for
// integration tests where multiple plugins share state).
func HostWithDB(t *testing.T, db *sql.DB, pluginName string) plugin.Host {
	t.Helper()
	dir := t.TempDir()
	return plugin.Host{
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})).With("plugin", pluginName),
		DB:          db,
		Messenger:   &nopMessenger{},
		Channels:    &nopChannels{},
		Attachments: &nopAttachments{},
		Search:      &nopSearch{},
		Secrets:     NewScopedSecrets(pluginName),
		Events:      plugin.NewEventBus(),
		Config:      json.RawMessage(`{}`),
		DataDir:     dir,
		Tracer:      &nopTracer{},
		Metrics:     &nopMetrics{},
		DefaultOwner: &plugin.Owner{ID: 1, Username: "testowner", Email: "owner@example.test"},
		BaseURL:     "http://localhost:8080",
	}
}

// --- ambient stubs ---

type nopMessenger struct{}

func (nopMessenger) SendDM(ctx context.Context, toAgent, body string, priority int) error {
	return nil
}

type nopChannels struct{}

func (nopChannels) Post(ctx context.Context, channel, body string) (int64, error) {
	return 0, nil
}

type nopAttachments struct{}

func (nopAttachments) Put(ctx context.Context, r []byte, mime string) (string, error) {
	return "", nil
}

func (nopAttachments) Get(ctx context.Context, hash string) ([]byte, string, error) {
	return nil, "", nil
}

type nopSearch struct{}

func (nopSearch) Query(ctx context.Context, text string, limit int) ([]plugin.SearchHit, error) {
	return nil, nil
}

type nopTracer struct{}

func (nopTracer) Start(ctx context.Context, name string) (context.Context, plugin.TraceSpan) {
	return ctx, nopSpan{}
}

type nopSpan struct{}

func (nopSpan) End()                                    {}
func (nopSpan) SetAttribute(key string, value any)      {}

type nopMetrics struct{}

func (nopMetrics) Register(c any) error { return nil }
