package plugin

import (
	"encoding/json"
	"sync"
	"time"
)

// Status describes a plugin's current lifecycle state.
type Status string

const (
	StatusRegistered  Status = "registered"
	StatusDisabled    Status = "disabled"
	StatusMigrated    Status = "migrated"
	StatusInitialized Status = "initialized"
	StatusStarted     Status = "started"
	StatusFailed      Status = "failed"
	StatusStopped     Status = "stopped"
)

// StatusEntry is the runtime snapshot of one plugin's state, suitable for
// JSON serialization by /api/plugins/status.
type StatusEntry struct {
	Name              string    `json:"name"`
	Version           string    `json:"version"`
	Stability         string    `json:"stability"`
	Enabled           bool      `json:"enabled"`
	Status            Status    `json:"status"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	ErrorMessage      string    `json:"error,omitempty"`
	Capabilities      []string  `json:"capabilities"`
	ToolsRegistered   []string  `json:"tools_registered"`
	ActionsRegistered []string  `json:"actions_registered"`
	MigrationVersions []int     `json:"migration_versions"`
}

// StatusStore is a thread-safe registry of plugin status entries.
type StatusStore struct {
	mu      sync.RWMutex
	entries map[string]*StatusEntry
	order   []string
}

func NewStatusStore() *StatusStore {
	return &StatusStore{entries: map[string]*StatusEntry{}}
}

func (s *StatusStore) Set(name string, e StatusEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[name]; !ok {
		s.order = append(s.order, name)
	}
	e.Name = name
	s.entries[name] = &e
}

func (s *StatusStore) Update(name string, fn func(*StatusEntry)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[name]
	if !ok {
		return
	}
	fn(e)
}

func (s *StatusStore) Get(name string) (StatusEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[name]
	if !ok {
		return StatusEntry{}, false
	}
	return *e, true
}

// All returns a stable-ordered snapshot of all entries.
func (s *StatusStore) All() []StatusEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]StatusEntry, 0, len(s.order))
	for _, n := range s.order {
		out = append(out, *s.entries[n])
	}
	return out
}

// MarshalJSON returns {"plugins": [...]} per the REST contract.
func (s *StatusStore) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Plugins []StatusEntry `json:"plugins"`
	}{Plugins: s.All()})
}
