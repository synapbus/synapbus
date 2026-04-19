package plugin

import (
	"encoding/json"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// PluginConfig is the per-plugin YAML sub-tree.
// Config is stored as a free-form decoded value (yaml.v3 hands us back
// map[string]any); it is converted to json.RawMessage on demand via
// ConfigFor, so plugins can unmarshal into their own typed struct.
type PluginConfig struct {
	Enabled bool           `yaml:"enabled" json:"enabled"`
	Config  map[string]any `yaml:"config" json:"config,omitempty"`
}

// FileConfig is the root shape of synapbus.yaml. Only the plugins sub-tree
// is managed by this package; core configuration lives elsewhere and is
// preserved during round-trip rewrites.
type FileConfig struct {
	Plugins map[string]PluginConfig `yaml:"plugins" json:"plugins"`
	// rawRoot preserves the full YAML document so enable/disable edits
	// can round-trip without losing unrelated keys.
	rawRoot map[string]any `yaml:"-" json:"-"`
}

// LoadConfig reads and parses synapbus.yaml. If path is empty, the
// environment variable SYNAPBUS_CONFIG_PATH is consulted, falling back
// to ./synapbus.yaml. A non-existent file is not an error — it returns
// an empty config (all plugins default to disabled).
func LoadConfig(path string) (*FileConfig, error) {
	if path == "" {
		path = os.Getenv("SYNAPBUS_CONFIG_PATH")
	}
	if path == "" {
		path = "synapbus.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &FileConfig{Plugins: map[string]PluginConfig{}, rawRoot: map[string]any{}}, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	// Decode into a raw map first so we can preserve unknown top-level keys.
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	// Decode plugins sub-tree into strongly-typed form. yaml.v3 produces
	// map[string]any for nested maps; we walk and normalize manually so
	// keys always end up as strings.
	out := &FileConfig{Plugins: map[string]PluginConfig{}, rawRoot: raw}
	if rawPlugins, ok := raw["plugins"]; ok {
		pm, ok := rawPlugins.(map[string]any)
		if !ok {
			// yaml.v3 uses map[any]any in some cases; normalize.
			pm = coerceStringKeyed(rawPlugins)
		}
		for name, entry := range pm {
			m, ok := entry.(map[string]any)
			if !ok {
				m = coerceStringKeyed(entry)
			}
			pc := PluginConfig{}
			if v, ok := m["enabled"]; ok {
				if b, ok := v.(bool); ok {
					pc.Enabled = b
				}
			}
			if v, ok := m["config"]; ok {
				cm, ok := v.(map[string]any)
				if !ok {
					cm = coerceStringKeyed(v)
				}
				pc.Config = cm
			}
			out.Plugins[name] = pc
		}
	}
	return out, nil
}

// coerceStringKeyed converts a value that yaml.v3 produced as map[any]any
// (due to non-string scalar keys in YAML) into map[string]any, best-effort.
// Returns nil if the value isn't a map.
func coerceStringKeyed(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	m2, ok := v.(map[any]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for k, vv := range m2 {
		ks, ok := k.(string)
		if !ok {
			continue
		}
		out[ks] = vv
	}
	return out
}

// IsEnabled returns whether the named plugin is enabled per the config.
// Plugins not listed are considered disabled.
func (c *FileConfig) IsEnabled(name string) bool {
	if c == nil {
		return false
	}
	pc, ok := c.Plugins[name]
	return ok && pc.Enabled
}

// ConfigFor returns the raw config blob for the named plugin (never nil).
func (c *FileConfig) ConfigFor(name string) json.RawMessage {
	if c == nil {
		return json.RawMessage("{}")
	}
	pc, ok := c.Plugins[name]
	if !ok || len(pc.Config) == 0 {
		return json.RawMessage("{}")
	}
	raw, err := json.Marshal(pc.Config)
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}

// SetEnabled flips the enabled flag for the named plugin. If the plugin is
// not present, an entry is created with Enabled=enabled.
func (c *FileConfig) SetEnabled(name string, enabled bool) {
	pc := c.Plugins[name]
	pc.Enabled = enabled
	c.Plugins[name] = pc
	// Mirror into rawRoot for round-trip serialization.
	pluginsRaw, _ := c.rawRoot["plugins"].(map[string]any)
	if pluginsRaw == nil {
		pluginsRaw = map[string]any{}
	}
	entry, _ := pluginsRaw[name].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry["enabled"] = enabled
	pluginsRaw[name] = entry
	c.rawRoot["plugins"] = pluginsRaw
}

// Save writes the config back to path, preserving unknown top-level keys.
func (c *FileConfig) Save(path string) error {
	if path == "" {
		path = os.Getenv("SYNAPBUS_CONFIG_PATH")
	}
	if path == "" {
		path = "synapbus.yaml"
	}
	data, err := yaml.Marshal(c.rawRoot)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	// Atomic write: write to tmp, rename into place.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp config: %w", err)
	}
	return os.Rename(tmp, path)
}

// ValidatePluginNames checks that each plugin entry in the config has a
// syntactically valid name.
func (c *FileConfig) ValidatePluginNames() error {
	for name := range c.Plugins {
		if !ValidateName(name) {
			return fmt.Errorf("invalid plugin name %q (must match ^[a-z][a-z0-9_]{1,31}$)", name)
		}
	}
	return nil
}
