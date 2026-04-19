package plugintest

import (
	"context"
	"sync"

	"github.com/synapbus/synapbus/internal/plugin"
)

// sharedStore is a process-wide map of (plugin, name) → value. Multiple
// ScopedSecrets instances created with different plugin names share this
// store, so a test that wants to verify cross-plugin isolation can do so
// by instantiating two ScopedSecrets from the same process.
var (
	sharedStoreMu sync.Mutex
	sharedStore   = map[string]map[string][]byte{}
)

// ScopedSecrets is a plugin-scoped secret store backed by an in-memory map
// keyed by (plugin, name). Accessing a secret with a different plugin name
// returns plugin.ErrSecretNotFound — exactly like a missing secret. This
// lets tests verify the isolation guarantee from FR-007 / SC-006.
type ScopedSecrets struct {
	pluginName string
}

func NewScopedSecrets(pluginName string) *ScopedSecrets {
	return &ScopedSecrets{pluginName: pluginName}
}

func (s *ScopedSecrets) Get(ctx context.Context, name string) ([]byte, error) {
	sharedStoreMu.Lock()
	defer sharedStoreMu.Unlock()
	ps, ok := sharedStore[s.pluginName]
	if !ok {
		return nil, plugin.ErrSecretNotFound
	}
	v, ok := ps[name]
	if !ok {
		return nil, plugin.ErrSecretNotFound
	}
	// Copy to avoid aliasing.
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (s *ScopedSecrets) Set(ctx context.Context, name string, value []byte) error {
	sharedStoreMu.Lock()
	defer sharedStoreMu.Unlock()
	ps, ok := sharedStore[s.pluginName]
	if !ok {
		ps = map[string][]byte{}
		sharedStore[s.pluginName] = ps
	}
	cp := make([]byte, len(value))
	copy(cp, value)
	ps[name] = cp
	return nil
}

// ResetScopedSecrets wipes the shared store. Call t.Cleanup(ResetScopedSecrets)
// if your test sets secrets and doesn't want them to leak across runs.
func ResetScopedSecrets() {
	sharedStoreMu.Lock()
	defer sharedStoreMu.Unlock()
	sharedStore = map[string]map[string][]byte{}
}
