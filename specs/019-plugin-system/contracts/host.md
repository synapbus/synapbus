# Contract — `Host` struct

```go
package plugin

import (
    "context"
    "database/sql"
    "encoding/json"
    "log/slog"

    "github.com/jmoiron/sqlx"
    "github.com/prometheus/client_golang/prometheus"
    "go.opentelemetry.io/otel/trace"

    "github.com/synapbus/synapbus/internal/attachments"
    "github.com/synapbus/synapbus/internal/channels"
    "github.com/synapbus/synapbus/internal/eventbus"
    "github.com/synapbus/synapbus/internal/messaging"
    "github.com/synapbus/synapbus/internal/search"
    "github.com/synapbus/synapbus/internal/secrets"
    "github.com/synapbus/synapbus/internal/users"
)

// Host is the bundle of core services handed to a plugin at Init.
// Plugins MUST treat it as read-only and MUST NOT cache sub-handles
// across process lifetimes.
type Host struct {
    Logger      *slog.Logger             // pre-tagged plugin=<name>
    DB          *sqlx.DB                 // shared core handle
    Messaging   messaging.API
    Channels    channels.API
    Attachments attachments.Store
    Search      search.Index
    Secrets     secrets.Scoped           // scoped to calling plugin
    Events      eventbus.Bus
    Config      json.RawMessage          // plugins[<name>] YAML sub-tree
    DataDir     string                   // <--data>/plugins/<name>/
    Tracer      trace.Tracer
    Metrics     prometheus.Registerer
    DefaultOwner *users.User             // for failure notifications
    baseURL      string                  // unexported; plugins read via BaseURL()
}

// BaseURL returns the public base URL of this instance, for building
// absolute links in panel HTML.
func (h Host) BaseURL() string { return h.baseURL }

// ExecTx runs a function inside a database transaction against the shared
// handle. Plugins should use this for multi-statement writes.
func (h Host) ExecTx(ctx context.Context, fn func(*sqlx.Tx) error) error { … }
```

## Security / scope invariants

- `Host.Secrets` returns only secrets that were written with the caller's plugin name. Calling `.Get("X")` for a secret owned by another plugin returns `secrets.ErrNotFound`, same as missing.
- `Host.DB` gives full SQL access — but tables read/written are expected to be `plugin_<name>_*`. A static linter (part of this feature) flags unqualified reads from core tables.
- `Host.DataDir` is guaranteed to exist, to be a directory, and to be writable only by this plugin (permissions 0700).
- `Host.Metrics` is a sub-registerer; labels automatically carry `plugin="<name>"`.

## Test constructor (from plugintest)

```go
package plugintest

// NopHost returns an in-memory Host backed by an in-memory SQLite database
// and a temporary directory. Suitable for unit tests.
func NopHost(t *testing.T) *plugin.Host { … }

// Run performs a full lifecycle dry-run of a plugin against a NopHost:
//   1. Migrations are applied.
//   2. Init is called.
//   3. If HasLifecycle, Start is called.
//   4. All declared capabilities are asserted to be registered.
//   5. Shutdown + close.
// t.Fatal on any error.
func Run(t *testing.T, p plugin.Plugin) { … }
```
