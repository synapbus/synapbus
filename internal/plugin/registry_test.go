package plugin_test

import (
	"context"
	"testing"

	"github.com/synapbus/synapbus/internal/plugin"
)

// minimalPlugin is the smallest plugin that satisfies the Plugin interface.
type minimalPlugin struct{ name, version string }

func (p *minimalPlugin) Name() string                                   { return p.name }
func (p *minimalPlugin) Version() string                                { return p.version }
func (p *minimalPlugin) Init(ctx context.Context, host plugin.Host) error { return nil }

func TestNewRegistry_RejectsInvalidName(t *testing.T) {
	_, err := plugin.NewRegistry([]plugin.Plugin{&minimalPlugin{name: "Bad-Name", version: "0.1"}}, nil)
	if err == nil {
		t.Fatal("expected error for invalid plugin name")
	}
}

func TestNewRegistry_RejectsDuplicate(t *testing.T) {
	_, err := plugin.NewRegistry([]plugin.Plugin{
		&minimalPlugin{name: "alpha", version: "0.1"},
		&minimalPlugin{name: "alpha", version: "0.2"},
	}, nil)
	if err == nil {
		t.Fatal("expected error for duplicate plugin name")
	}
}

func TestNewRegistry_EnabledByConfig(t *testing.T) {
	cfg := &plugin.FileConfig{
		Plugins: map[string]plugin.PluginConfig{
			"alpha": {Enabled: true, Config: map[string]any{"x": 1}},
			"beta":  {Enabled: false},
		},
	}
	reg, err := plugin.NewRegistry([]plugin.Plugin{
		&minimalPlugin{name: "alpha", version: "0.1"},
		&minimalPlugin{name: "beta", version: "0.1"},
		&minimalPlugin{name: "gamma", version: "0.1"}, // not in config, so disabled
	}, cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if got := reg.Enabled(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("expected only [alpha] enabled, got %v", got)
	}
	if all := reg.All(); len(all) != 3 {
		t.Fatalf("expected 3 registered, got %v", all)
	}
}

func TestRegistry_RespectsRegistrationOrder(t *testing.T) {
	cfg := &plugin.FileConfig{
		Plugins: map[string]plugin.PluginConfig{
			"zeta":  {Enabled: true},
			"alpha": {Enabled: true},
			"mu":    {Enabled: true},
		},
	}
	reg, err := plugin.NewRegistry([]plugin.Plugin{
		&minimalPlugin{name: "zeta", version: "0.1"},
		&minimalPlugin{name: "alpha", version: "0.1"},
		&minimalPlugin{name: "mu", version: "0.1"},
	}, cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	got := reg.Enabled()
	want := []string{"zeta", "alpha", "mu"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("registration order broken: got %v, want %v", got, want)
		}
	}
}
