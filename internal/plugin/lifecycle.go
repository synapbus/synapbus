package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// InitAll runs the three-phase boot sequence against an existing registry.
// 1. Migrate all enabled plugins (failure marks only that plugin failed).
// 2. Init each enabled plugin, collecting capabilities.
// 3. Start lifecycle-enabled plugins.
//
// The hostFactory produces a Host specialized for a given plugin name — it
// is responsible for scoping the logger, datadir, metrics, tracer, and
// secrets accessor. This indirection keeps the plugin package independent
// of concrete core types.
func (r *Registry) InitAll(ctx context.Context, hostFactory func(name string, cfg CapabilityContext) Host) error {
	// Phase 1: Migrate
	for _, name := range r.Enabled() {
		p := r.Plugin(name)
		if p == nil {
			continue
		}
		// Use a temporary host to get the DB handle.
		ctxHost := hostFactory(name, CapabilityContext{PhaseMigrate: true})
		if ctxHost.DB == nil {
			r.markFailed(name, fmt.Errorf("migrate: Host.DB is nil"))
			continue
		}
		if _, err := ApplyMigrations(ctx, ctxHost.DB, p); err != nil {
			r.markFailed(name, fmt.Errorf("migrate: %w", err))
			continue
		}
		r.statuses.Update(name, func(e *StatusEntry) {
			e.Status = StatusMigrated
			// Populate migration version list for the status endpoint.
			if versions, vErr := AppliedVersions(ctx, ctxHost.DB, name); vErr == nil {
				e.MigrationVersions = versions
			}
		})
	}

	// Phase 2: Init
	for _, name := range r.Enabled() {
		if s, ok := r.statuses.Get(name); ok && s.Status == StatusFailed {
			continue
		}
		p := r.Plugin(name)
		host := hostFactory(name, CapabilityContext{PhaseInit: true})
		if err := ensureDataDir(host.DataDir); err != nil {
			r.markFailed(name, fmt.Errorf("prepare datadir: %w", err))
			continue
		}
		if err := safeInit(ctx, p, host); err != nil {
			r.markFailed(name, fmt.Errorf("init: %w", err))
			continue
		}
		r.collectCapabilities(name, p)
		r.statuses.Update(name, func(e *StatusEntry) {
			e.Status = StatusInitialized
			e.Capabilities = capabilityList(p)
			e.ToolsRegistered = toolsList(p)
			e.ActionsRegistered = actionsList(p)
		})
	}

	// Phase 3: Start
	for _, name := range r.Enabled() {
		if s, ok := r.statuses.Get(name); ok && s.Status == StatusFailed {
			continue
		}
		p := r.Plugin(name)
		hl, ok := p.(HasLifecycle)
		if !ok {
			// Nothing to start; mark "started" to reflect "fully live".
			r.statuses.Update(name, func(e *StatusEntry) {
				e.Status = StatusStarted
				e.StartedAt = time.Now()
			})
			continue
		}
		if err := safeStart(ctx, hl); err != nil {
			r.markFailed(name, fmt.Errorf("start: %w", err))
			continue
		}
		r.statuses.Update(name, func(e *StatusEntry) {
			e.Status = StatusStarted
			e.StartedAt = time.Now()
		})
	}
	return nil
}

// CapabilityContext is a tiny struct the host factory can use to know which
// phase it is being invoked for. Allows the factory to populate only the
// subset needed for the phase (e.g. skip metrics registration during migrate).
type CapabilityContext struct {
	PhaseMigrate bool
	PhaseInit    bool
}

// ShutdownAll calls Shutdown on lifecycle plugins in reverse start order.
func (r *Registry) ShutdownAll(ctx context.Context) {
	enabled := r.Enabled()
	for i := len(enabled) - 1; i >= 0; i-- {
		name := enabled[i]
		p := r.Plugin(name)
		if hl, ok := p.(HasLifecycle); ok {
			_ = safeShutdown(ctx, hl)
		}
		r.statuses.Update(name, func(e *StatusEntry) { e.Status = StatusStopped })
	}
}

func (r *Registry) markFailed(name string, err error) {
	r.statuses.Update(name, func(e *StatusEntry) {
		e.Status = StatusFailed
		if err != nil {
			e.ErrorMessage = err.Error()
		}
	})
	// Emit via slog at warn level. Host-level observers may do more.
	slog.Warn("plugin failed", "plugin", name, "error", err)
}

func (r *Registry) collectCapabilities(name string, p Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if hmt, ok := p.(HasMCPTools); ok {
		for _, t := range hmt.MCPTools() {
			if _, exists := r.mcpTools[t.Name]; exists {
				r.statuses.Update(name, func(e *StatusEntry) {
					e.Status = StatusFailed
					e.ErrorMessage = fmt.Sprintf("duplicate MCP tool %q", t.Name)
				})
				return
			}
			r.mcpTools[t.Name] = t
		}
	}
	if ha, ok := p.(HasActions); ok {
		for _, a := range ha.Actions() {
			if _, exists := r.actions[a.Name]; exists {
				r.statuses.Update(name, func(e *StatusEntry) {
					e.Status = StatusFailed
					e.ErrorMessage = fmt.Sprintf("duplicate action %q", a.Name)
				})
				return
			}
			r.actions[a.Name] = a
		}
	}
	if hwp, ok := p.(HasWebPanels); ok {
		for _, m := range hwp.WebPanels() {
			r.panels = append(r.panels, m)
		}
		if h := hwp.PanelHandler(); h != nil {
			r.panelHandlers[name] = h
		}
	}
	if hct, ok := p.(HasChannelType); ok {
		for _, ct := range hct.ChannelTypes() {
			if _, exists := r.channelTypes[ct.Name]; exists {
				r.statuses.Update(name, func(e *StatusEntry) {
					e.Status = StatusFailed
					e.ErrorMessage = fmt.Sprintf("duplicate channel type %q", ct.Name)
				})
				return
			}
			r.channelTypes[ct.Name] = ct
		}
	}
	if hc, ok := p.(HasCLICommands); ok {
		r.cliCommands = append(r.cliCommands, hc.CLICommands()...)
	}
	if hr, ok := p.(HasHTTPRoutes); ok {
		mountPlugin := name
		r.routeMounts = append(r.routeMounts, routeMount{
			Plugin: mountPlugin,
			Setup:  func(rt Router) { hr.RegisterRoutes(rt) },
		})
	}
	if he, ok := p.(HasEventHook); ok {
		r.eventSubs = append(r.eventSubs, he)
	}
}

func capabilityList(p Plugin) []string {
	caps := []string{}
	if _, ok := p.(HasMigrations); ok {
		caps = append(caps, "migrations")
	}
	if _, ok := p.(HasMCPTools); ok {
		caps = append(caps, "mcp_tools")
	}
	if _, ok := p.(HasActions); ok {
		caps = append(caps, "actions")
	}
	if _, ok := p.(HasHTTPRoutes); ok {
		caps = append(caps, "http_routes")
	}
	if _, ok := p.(HasWebPanels); ok {
		caps = append(caps, "web_panel")
	}
	if _, ok := p.(HasCLICommands); ok {
		caps = append(caps, "cli")
	}
	if _, ok := p.(HasChannelType); ok {
		caps = append(caps, "channel_type")
	}
	if _, ok := p.(HasEventHook); ok {
		caps = append(caps, "event_hook")
	}
	if _, ok := p.(HasLifecycle); ok {
		caps = append(caps, "lifecycle")
	}
	if _, ok := p.(HasConfigSchema); ok {
		caps = append(caps, "config_schema")
	}
	return caps
}

func toolsList(p Plugin) []string {
	out := []string{}
	if hmt, ok := p.(HasMCPTools); ok {
		for _, t := range hmt.MCPTools() {
			out = append(out, t.Name)
		}
	}
	return out
}

func actionsList(p Plugin) []string {
	out := []string{}
	if ha, ok := p.(HasActions); ok {
		for _, a := range ha.Actions() {
			out = append(out, a.Name)
		}
	}
	return out
}

// --- panic-safe wrappers ---

func safeInit(ctx context.Context, p Plugin, host Host) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return p.Init(ctx, host)
}

func safeStart(ctx context.Context, hl HasLifecycle) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return hl.Start(ctx)
}

func safeShutdown(ctx context.Context, hl HasLifecycle) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return hl.Shutdown(ctx)
}

// --- in-process event bus (implements Events) ---

// NewEventBus returns a zero-configuration in-process bus suitable for tests
// and lightweight deployments. Subscribers are called sequentially per
// publish, each with panic-recover so one bad subscriber cannot break the
// others.
func NewEventBus() Events { return &localBus{subs: map[string][]func(context.Context, Event) error{}} }

type localBus struct {
	mu   sync.RWMutex
	subs map[string][]func(context.Context, Event) error
}

func (b *localBus) Publish(ctx context.Context, e Event) error {
	b.mu.RLock()
	handlers := append([]func(context.Context, Event) error(nil), b.subs[e.Topic]...)
	all := append(handlers, b.subs["*"]...)
	b.mu.RUnlock()
	var errs []error
	for _, h := range all {
		func() {
			defer func() {
				if r := recover(); r != nil {
					errs = append(errs, fmt.Errorf("subscriber panic: %v", r))
				}
			}()
			if err := h(ctx, e); err != nil {
				errs = append(errs, err)
			}
		}()
	}
	return errors.Join(errs...)
}

func (b *localBus) Subscribe(topic string, fn func(ctx context.Context, e Event) error) (cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], fn)
	idx := len(b.subs[topic]) - 1
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		lst := b.subs[topic]
		if idx < len(lst) {
			b.subs[topic] = append(lst[:idx], lst[idx+1:]...)
		}
	}
}
