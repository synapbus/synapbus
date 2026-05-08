package demo_test

import (
	"context"
	"testing"

	"github.com/synapbus/synapbus/internal/plugin"
	"github.com/synapbus/synapbus/internal/plugin/plugintest"
	"github.com/synapbus/synapbus/internal/plugins/demo"
)

// Smoke: plugin lifecycle runs end-to-end.
func TestDemoPlugin_Smoke(t *testing.T) {
	plugintest.Run(t, demo.New())
}

// Full capability check through a Registry.
func TestDemoPlugin_AllCapabilitiesRegistered(t *testing.T) {
	reg, err := plugin.NewRegistry(
		[]plugin.Plugin{demo.New()},
		&plugin.FileConfig{Plugins: map[string]plugin.PluginConfig{
			"demo": {Enabled: true},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	plugintest.RunRegistry(t, reg)
	plugintest.HasAction(t, reg, "create_note")
	plugintest.HasAction(t, reg, "list_notes")
	plugintest.HasAction(t, reg, "get_note")
	plugintest.HasPanel(t, reg, "demo")
	plugintest.PluginStarted(t, reg, "demo")
}

// Action handlers work against the in-memory DB.
func TestDemoPlugin_Actions(t *testing.T) {
	p := demo.New()
	host := plugintest.NopHost(t)
	ctx := context.Background()
	if _, err := plugin.ApplyMigrations(ctx, host.DB, p); err != nil {
		t.Fatal(err)
	}
	if err := p.Init(ctx, host); err != nil {
		t.Fatal(err)
	}

	// create
	created, err := p.Actions()[0].Handler(ctx, map[string]any{"slug": "hello", "title": "Hello", "body": "hi"})
	if err != nil {
		t.Fatalf("create_note: %v", err)
	}
	m := created.(map[string]any)
	if m["slug"] != "hello" {
		t.Fatalf("unexpected create result: %+v", m)
	}
	// list
	listed, err := p.Actions()[2].Handler(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("list_notes: %v", err)
	}
	if listed.(map[string]any)["count"].(int) != 1 {
		t.Fatalf("expected 1 note, got %+v", listed)
	}
	// duplicate slug rejected
	if _, err := p.Actions()[0].Handler(ctx, map[string]any{"slug": "hello", "title": "Again"}); err == nil {
		t.Fatal("expected duplicate slug to be rejected")
	}
	// get
	got, err := p.Actions()[1].Handler(ctx, map[string]any{"slug": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("get returned nil")
	}
}

func TestDemoPlugin_MaxNotesLimit(t *testing.T) {
	p := demo.New()
	host := plugintest.NopHost(t)
	// Override the Host.Config so decodeConfig sees our limit.
	host.Config = []byte(`{"max_notes": 1}`)
	ctx := context.Background()
	if _, err := plugin.ApplyMigrations(ctx, host.DB, p); err != nil {
		t.Fatal(err)
	}
	if err := p.Init(ctx, host); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Actions()[0].Handler(ctx, map[string]any{"slug": "a", "title": "A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Actions()[0].Handler(ctx, map[string]any{"slug": "b", "title": "B"}); err == nil {
		t.Fatal("expected max_notes limit to reject second note")
	}
}
