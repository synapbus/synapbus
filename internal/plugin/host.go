package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
)

// --- Ambient service interfaces, defined here so this package stays
// --- independent of any specific core implementation. Core packages supply
// --- concrete implementations; tests use in-package nop stubs.

// Owner identifies the human user who owns the default agent.
// Used by the failure-notification path.
type Owner struct {
	ID       int64
	Username string
	Email    string
}

// Messenger is the subset of the messaging API that plugins need.
type Messenger interface {
	SendDM(ctx context.Context, toAgent, body string, priority int) error
}

// Channels is the subset of the channels API that plugins need.
type Channels interface {
	Post(ctx context.Context, channel, body string) (int64, error)
}

// Attachments is the subset of the attachment store.
type Attachments interface {
	Put(ctx context.Context, r []byte, mime string) (hash string, err error)
	Get(ctx context.Context, hash string) ([]byte, string, error)
}

// Search is a read-only view of the core search index.
type Search interface {
	Query(ctx context.Context, text string, limit int) ([]SearchHit, error)
}

type SearchHit struct {
	MessageID int64
	Score     float64
	Excerpt   string
}

// ErrSecretNotFound is returned when a scoped secret lookup fails.
// Cross-plugin access also returns this error to avoid leaking scope info.
var ErrSecretNotFound = errors.New("secret not found")

// Secrets is a plugin-scoped secret accessor.
// Calling Get with a name not owned by the calling plugin returns
// ErrSecretNotFound, identical to a truly missing secret.
type Secrets interface {
	Get(ctx context.Context, name string) ([]byte, error)
	Set(ctx context.Context, name string, value []byte) error
}

// Events is the internal event bus.
type Events interface {
	Publish(ctx context.Context, e Event) error
	Subscribe(topic string, fn func(ctx context.Context, e Event) error) (cancel func())
}

// Metrics is the subset of prometheus.Registerer the plugin uses.
// Kept as any to avoid forcing a prom dep on the plugin package.
type Metrics interface {
	Register(c any) error
}

// Tracer is the subset of otel/trace.Tracer used by plugins.
type Tracer interface {
	Start(ctx context.Context, name string) (context.Context, TraceSpan)
}

type TraceSpan interface {
	End()
	SetAttribute(key string, value any)
}

// Host is the bundle of core services handed to a plugin at Init.
// Plugins MUST treat it as read-only and MUST NOT share it between
// goroutines beyond what its embedded services allow.
type Host struct {
	Logger       *slog.Logger
	DB           *sql.DB
	Messenger    Messenger
	Channels     Channels
	Attachments  Attachments
	Search       Search
	Secrets      Secrets
	Events       Events
	Config       json.RawMessage
	DataDir      string
	Tracer       Tracer
	Metrics      Metrics
	DefaultOwner *Owner
	// BaseURL is the externally-reachable URL of the SynapBus instance,
	// useful when a plugin serves HTML that contains absolute links.
	BaseURL string
}

// ExecTx runs fn inside a database transaction. Returns the fn's error or
// any begin/commit error. Rolls back automatically if fn errors.
func (h Host) ExecTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if h.DB == nil {
		return errors.New("plugin.Host: DB is nil")
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ensureDataDir makes sure the per-plugin data directory exists with 0700 perms.
// Called by the registry before Init; exposed here for plugintest.
func ensureDataDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

// joinDataDir is a helper for constructing per-plugin sub-paths.
func joinDataDir(base, plugin string) string { return filepath.Join(base, "plugins", plugin) }
