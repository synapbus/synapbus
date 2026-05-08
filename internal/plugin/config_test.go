package plugin_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/synapbus/synapbus/internal/plugin"
)

const sampleYAML = `
port: 8080
plugins:
  wiki:
    enabled: true
    config:
      max_revisions: 100
  marketplace:
    enabled: false
`

func TestLoadConfig_ReadsPlugins(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "synapbus.yaml")
	if err := os.WriteFile(p, []byte(sampleYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := plugin.LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.IsEnabled("wiki") {
		t.Fatalf("expected wiki enabled")
	}
	if cfg.IsEnabled("marketplace") {
		t.Fatalf("expected marketplace disabled")
	}
	if cfg.IsEnabled("ghost") {
		t.Fatalf("expected unknown plugin disabled")
	}
}

func TestLoadConfig_RoundTripPreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "synapbus.yaml")
	if err := os.WriteFile(p, []byte(sampleYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := plugin.LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	// Flip marketplace enabled and save.
	cfg.SetEnabled("marketplace", true)
	if err := cfg.Save(p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg2, err := plugin.LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg2.IsEnabled("marketplace") {
		t.Fatal("round-trip lost the flip")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !containsBytes(raw, []byte("port: 8080")) {
		t.Fatalf("unknown top-level key 'port' not preserved:\n%s", raw)
	}
}

func TestLoadConfig_MissingFileIsEmptyNotError(t *testing.T) {
	cfg, err := plugin.LoadConfig("/nonexistent/path/synapbus.yaml")
	if err != nil {
		t.Fatalf("missing file should not be an error: %v", err)
	}
	if cfg.IsEnabled("anything") {
		t.Fatalf("empty config should not enable anything")
	}
}

func TestValidatePluginNames_RejectsInvalid(t *testing.T) {
	cfg := &plugin.FileConfig{Plugins: map[string]plugin.PluginConfig{
		"BadName": {Enabled: true},
	}}
	if err := cfg.ValidatePluginNames(); err == nil {
		t.Fatal("expected rejection of invalid plugin name")
	}
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}
