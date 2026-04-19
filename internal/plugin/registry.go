package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
)

// Registry is the core-owned catalog of compiled-in plugins.
// One Registry is constructed per process from (list of plugins, config).
type Registry struct {
	mu sync.RWMutex

	plugins  map[string]Plugin
	enabled  map[string]bool
	configs  map[string]json.RawMessage
	order    []string
	statuses *StatusStore

	// Capability indexes — populated during Init.
	mcpTools     map[string]MCPTool
	actions      map[string]ActionRegistration
	panels       []PanelManifest
	panelHandlers map[string]http.Handler
	channelTypes map[string]ChannelTypeDef
	eventSubs    []HasEventHook
	cliCommands  []CLICommand
	routeMounts  []routeMount
}

type routeMount struct {
	Plugin string
	Setup  func(Router)
}

// NewRegistry builds a registry from an explicit plugin list and a config.
// Returns an error if any plugin name is invalid or duplicated.
func NewRegistry(plugins []Plugin, cfg *FileConfig) (*Registry, error) {
	r := &Registry{
		plugins:       map[string]Plugin{},
		enabled:       map[string]bool{},
		configs:       map[string]json.RawMessage{},
		statuses:      NewStatusStore(),
		mcpTools:      map[string]MCPTool{},
		actions:       map[string]ActionRegistration{},
		panelHandlers: map[string]http.Handler{},
		channelTypes:  map[string]ChannelTypeDef{},
	}
	for _, p := range plugins {
		name := p.Name()
		if !ValidateName(name) {
			return nil, fmt.Errorf("plugin %q: name must match ^[a-z][a-z0-9_]{1,31}$", name)
		}
		if _, exists := r.plugins[name]; exists {
			return nil, fmt.Errorf("plugin %q: duplicate registration", name)
		}
		r.plugins[name] = p
		r.order = append(r.order, name)
		if cfg != nil {
			r.enabled[name] = cfg.IsEnabled(name)
			r.configs[name] = cfg.ConfigFor(name)
		} else {
			r.configs[name] = json.RawMessage("{}")
		}
		stab := "stable"
		if hs, ok := p.(HasStability); ok {
			if s := hs.Stability(); s != "" {
				stab = s
			}
		}
		st := StatusRegistered
		if !r.enabled[name] {
			st = StatusDisabled
		}
		r.statuses.Set(name, StatusEntry{
			Name: name, Version: p.Version(), Stability: stab,
			Enabled: r.enabled[name], Status: st,
		})
	}
	return r, nil
}

// Status returns the status store (for /api/plugins/status).
func (r *Registry) Status() *StatusStore { return r.statuses }

// All returns plugin names in registration order.
func (r *Registry) All() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Enabled returns the names of enabled plugins in registration order.
func (r *Registry) Enabled() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []string{}
	for _, n := range r.order {
		if r.enabled[n] {
			out = append(out, n)
		}
	}
	return out
}

// Plugin returns the named plugin, or nil.
func (r *Registry) Plugin(name string) Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.plugins[name]
}

// --- Capability accessors ---

// MCPTool returns the registered MCP tool with the given name.
func (r *Registry) MCPTool(name string) (MCPTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.mcpTools[name]
	return t, ok
}

// Action returns the registered action with the given name.
func (r *Registry) Action(name string) (ActionRegistration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.actions[name]
	return a, ok
}

// Actions returns a sorted list of all registered action names.
func (r *Registry) Actions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.actions))
	for k := range r.actions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MCPTools returns a sorted list of all registered MCP tool names.
func (r *Registry) MCPTools() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.mcpTools))
	for k := range r.mcpTools {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Panels returns all registered panel manifests in registration order.
func (r *Registry) Panels() []PanelManifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PanelManifest, len(r.panels))
	copy(out, r.panels)
	return out
}

// PanelHandler returns the HTTP handler for a plugin's UI panel, or nil.
func (r *Registry) PanelHandler(plugin string) http.Handler {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.panelHandlers[plugin]
}

// ChannelType returns the registered channel type, or zero value + false.
func (r *Registry) ChannelType(name string) (ChannelTypeDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.channelTypes[name]
	return t, ok
}

// EventSubscribers returns all plugins that opted in to event delivery.
// Intended for the event bus wiring.
func (r *Registry) EventSubscribers() []HasEventHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]HasEventHook, len(r.eventSubs))
	copy(out, r.eventSubs)
	return out
}

// CLICommands returns all registered CLI commands across plugins.
func (r *Registry) CLICommands() []CLICommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CLICommand, len(r.cliCommands))
	copy(out, r.cliCommands)
	return out
}

// RouteMounts returns registered route-mount configs; the caller is expected
// to hand each one a chi.Router scoped to `/api/plugins/<name>/`.
func (r *Registry) RouteMounts() []routeMount {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]routeMount, len(r.routeMounts))
	copy(out, r.routeMounts)
	return out
}

// CallAction invokes a registered action with args, returning its result.
// Returns a "not registered" error if the action name is unknown.
// Note: scope enforcement is the responsibility of the caller (the MCP bridge),
// which has access to the token context.
func (r *Registry) CallAction(ctx context.Context, name string, args map[string]any) (any, error) {
	a, ok := r.Action(name)
	if !ok {
		return nil, fmt.Errorf("action %q: not registered", name)
	}
	return a.Handler(ctx, args)
}
