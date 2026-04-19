// Package plugin provides the compile-in plugin framework for SynapBus.
//
// Plugins implement the minimal Plugin interface and optionally one or more
// HasX capability sub-interfaces. The registry constructs a Host for each
// plugin at Init time, applies migrations, wires capabilities, and drives
// the three-phase lifecycle (Migrate -> Init -> Start).
package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
)

// Plugin is the minimum contract that every compiled-in plugin implements.
type Plugin interface {
	// Name returns the plugin's globally unique identifier.
	// Must match ^[a-z][a-z0-9_]{1,31}$.
	Name() string
	// Version returns the plugin's semver version.
	Version() string
	// Init is called once after migrations have applied. The plugin should
	// register capabilities via the returned capability interfaces and may
	// store the host handle for later use (e.g. in Handler funcs).
	Init(ctx context.Context, host Host) error
}

// Scope controls who can invoke a bridged action.
type Scope string

const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
	ScopeAdmin Scope = "admin"
)

// Migration is a single numbered schema change owned by one plugin.
type Migration struct {
	Version int    // 1..N, monotonic within the plugin
	Name    string // "001_initial"
	SQL     string // single file, runs in one transaction
}

// ActionRegistration is a bridged action exposed via the core execute() tool.
type ActionRegistration struct {
	Name          string
	Description   string
	InputSchema   json.RawMessage
	RequiredScope Scope
	Handler       func(ctx context.Context, args map[string]any) (any, error)
}

// MCPTool is a first-class MCP tool contributed by a plugin.
type MCPTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Handler     func(ctx context.Context, args map[string]any) (any, error)
}

// PanelManifest describes a Web UI panel contributed by a plugin.
type PanelManifest struct {
	ID    string // "wiki"
	Title string // "Wiki"
	Icon  string // lucide-icon name
	Route string // "/ui/plugins/wiki"
	Scope string // "owner" | "member"
}

// ChannelTypeDef registers a new channel type with optional hooks.
type ChannelTypeDef struct {
	Name       string
	OnMessage  func(ctx context.Context, channelID, msgID int64) error
	OnReaction func(ctx context.Context, channelID, msgID int64, reaction string) error
}

// Event is a payload delivered to plugins that implement HasEventHook.
type Event struct {
	Topic   string
	Payload any
	Meta    map[string]string
}

// Router is the minimal subset of go-chi/chi.Router that plugins need.
// Declared locally to keep the plugin package dependency-free.
type Router interface {
	Handle(pattern string, h http.Handler)
	Method(method, pattern string, h http.Handler)
	Get(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
	Put(pattern string, h http.HandlerFunc)
	Delete(pattern string, h http.HandlerFunc)
}

// CLICommand is the minimal subset of spf13/cobra.Command that plugins need.
// Declared as a generic value so cobra does not leak into this package.
type CLICommand interface {
	Use() string
	Execute() error
}

// --- Optional capability interfaces ---

// HasMigrations: plugin owns a numbered chain of SQL migrations.
type HasMigrations interface {
	Migrations() []Migration
}

// HasMCPTools: plugin adds first-class MCP tools.
type HasMCPTools interface {
	MCPTools() []MCPTool
}

// HasActions: plugin adds bridged actions callable via the core execute() tool.
type HasActions interface {
	Actions() []ActionRegistration
}

// HasHTTPRoutes: plugin mounts REST routes under /api/plugins/<name>/*.
type HasHTTPRoutes interface {
	RegisterRoutes(r Router)
}

// HasWebPanels: plugin contributes one or more UI panels served under /ui/plugins/<name>/*.
type HasWebPanels interface {
	WebPanels() []PanelManifest
	PanelHandler() http.Handler
}

// HasCLICommands: plugin adds subcommands under `synapbus plugin <name>`.
type HasCLICommands interface {
	CLICommands() []CLICommand
}

// HasChannelType: plugin defines a channel behavior.
type HasChannelType interface {
	ChannelTypes() []ChannelTypeDef
}

// HasEventHook: plugin subscribes to internal events.
type HasEventHook interface {
	OnEvent(ctx context.Context, e Event) error
}

// HasLifecycle: plugin runs background work and needs explicit start/shutdown.
type HasLifecycle interface {
	Start(ctx context.Context) error
	Shutdown(ctx context.Context) error
}

// HasConfigSchema: plugin publishes a JSON Schema for its config.
type HasConfigSchema interface {
	ConfigSchema() json.RawMessage
}

// HasStability: plugin declares stability level. Default: "stable".
type HasStability interface {
	Stability() string
}

// nameRE enforces the plugin name pattern. Exported via ValidateName below.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// ValidateName reports whether s is a syntactically valid plugin name.
func ValidateName(s string) bool { return nameRE.MatchString(s) }
