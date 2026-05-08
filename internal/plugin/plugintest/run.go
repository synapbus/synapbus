package plugintest

import (
	"context"
	"testing"

	"github.com/synapbus/synapbus/internal/plugin"
)

// Run exercises a plugin's full lifecycle against a NopHost:
//  1. Apply migrations.
//  2. Init.
//  3. If HasLifecycle: Start then Shutdown.
// Fails the test on any error. This is the canonical smoke-test a plugin
// author runs.
func Run(t *testing.T, p plugin.Plugin) {
	t.Helper()
	host := NopHost(t)
	if !plugin.ValidateName(p.Name()) {
		t.Fatalf("invalid plugin name %q", p.Name())
	}
	ctx := context.Background()
	if _, err := plugin.ApplyMigrations(ctx, host.DB, p); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	if err := p.Init(ctx, host); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if hl, ok := p.(plugin.HasLifecycle); ok {
		if err := hl.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := hl.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	}
}

// RunRegistry drives an entire Registry through InitAll with a factory that
// produces a NopHost per plugin. Useful for testing lifecycle interactions
// between multiple plugins (failure isolation, ordering, etc.).
func RunRegistry(t *testing.T, reg *plugin.Registry) {
	t.Helper()
	host := NopHost(t)
	ctx := context.Background()
	factory := func(name string, _ plugin.CapabilityContext) plugin.Host {
		h := host
		h.Secrets = NewScopedSecrets(name)
		h.Logger = h.Logger.With("plugin", name)
		return h
	}
	if err := reg.InitAll(ctx, factory); err != nil {
		t.Fatalf("InitAll: %v", err)
	}
}
