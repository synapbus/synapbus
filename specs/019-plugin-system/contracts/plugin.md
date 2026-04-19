# Contract — `internal/plugin` interfaces

## Base interface

```go
package plugin

import (
    "context"
)

// Plugin is the minimum contract. Every compiled-in plugin implements this.
type Plugin interface {
    // Name returns the plugin's globally unique identifier.
    // Must match ^[a-z][a-z0-9_]{1,31}$.
    Name() string

    // Version returns the plugin's semver version.
    Version() string

    // Init is called once during startup after migrations have applied.
    // The plugin registers its capabilities here. Must be fast (<100 ms)
    // and idempotent. Returning an error marks the plugin failed; the
    // core continues to start.
    Init(ctx context.Context, host Host) error
}
```

## Optional capability interfaces

Plugins implement any subset. Host uses type assertion at Init time.

```go
// HasMigrations: plugin owns a numbered chain of SQL migrations.
type HasMigrations interface {
    Migrations() []Migration
}

type Migration struct {
    Version int    // 1..N, monotonic
    Name    string // "001_initial"
    SQL     string // single file, runs in one transaction
}

// HasMCPTools: plugin adds first-class MCP tools (rare; most plugins use HasActions).
type HasMCPTools interface {
    MCPTools() []MCPTool
}

type MCPTool struct {
    Name        string          // "linkedin_post"
    Description string
    InputSchema json.RawMessage
    Handler     func(ctx context.Context, args map[string]any) (any, error)
}

// HasActions: plugin adds bridged actions callable via the core execute() tool.
type HasActions interface {
    Actions() []ActionRegistration
}

type ActionRegistration struct {
    Name          string          // "create_article"
    Description   string
    InputSchema   json.RawMessage
    RequiredScope Scope           // read | write | admin
    Handler       func(ctx context.Context, args map[string]any) (any, error)
}

type Scope string
const (
    ScopeRead  Scope = "read"
    ScopeWrite Scope = "write"
    ScopeAdmin Scope = "admin"
)

// HasHTTPRoutes: plugin mounts REST routes under /api/plugins/<name>/*.
type HasHTTPRoutes interface {
    RegisterRoutes(r chi.Router)
}

// HasWebPanels: plugin contributes one or more UI panels, served under /ui/plugins/<name>/*.
type HasWebPanels interface {
    WebPanels() []PanelManifest
    PanelHandler() http.Handler     // serves /ui/plugins/<name>/*
}

type PanelManifest struct {
    ID    string // "wiki"
    Title string // "Wiki"
    Icon  string // lucide-icon name
    Route string // "/ui/plugins/wiki"
    Scope string // "owner" | "member"
}

// HasCLICommands: plugin adds subcommands under `synapbus plugin <name>`.
type HasCLICommands interface {
    CLICommands() []*cobra.Command
}

// HasChannelType: plugin defines a channel behavior (blackboard/auction-like).
type HasChannelType interface {
    ChannelTypes() []ChannelTypeDef
}

type ChannelTypeDef struct {
    Name       string
    OnMessage  func(ctx context.Context, channelID int64, msgID int64) error
    OnReaction func(ctx context.Context, channelID int64, msgID int64, reaction string) error
}

// HasEventHook: plugin subscribes to internal events.
type HasEventHook interface {
    OnEvent(ctx context.Context, e Event) error
}

type Event struct {
    Topic   string         // "message.created", "reaction.added", "plugin.status.changed"
    Payload any            // typed by topic
    Meta    map[string]string
}

// HasLifecycle: plugin runs background work and needs explicit start/shutdown.
type HasLifecycle interface {
    Start(ctx context.Context) error
    Shutdown(ctx context.Context) error
}

// HasConfigSchema: plugin publishes a JSON Schema for its config so the Web UI can generate a form.
type HasConfigSchema interface {
    ConfigSchema() json.RawMessage
}
```

## Stability hint (optional)

```go
// HasStability: plugin declares stability level. Default: "stable".
type HasStability interface {
    Stability() string // "stable" | "beta" | "experimental"
}
```

## Invariants

- `Plugin.Name()` MUST be constant over the plugin's lifetime (memoized is fine).
- `Init` MUST NOT block for more than 100 ms; long-running work goes in `HasLifecycle.Start`.
- `Init` MUST be safe to call exactly once; calling twice is a host bug, not a plugin bug.
- Plugins MUST NOT hold a package-level reference to the `Host`; only the `Init` ctx-scoped copy.
- `Shutdown` MUST be idempotent; the host may call it more than once on chained signals.
