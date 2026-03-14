// Package mcp provides the MCP server implementation for SynapBus.
package mcp

import (
	"sync"
	"time"
)

// Connection represents an active agent connection.
type Connection struct {
	ID                 string    `json:"id"`
	AgentName          string    `json:"agent_name"`
	Transport          string    `json:"transport"`
	ConnectedAt        time.Time `json:"connected_at"`
	LastActivity       time.Time `json:"last_activity"`
	ClientName         string    `json:"client_name,omitempty"`
	ClientVersion      string    `json:"client_version,omitempty"`
	ProtocolVersion    string    `json:"protocol_version,omitempty"`
	ClientCapabilities []string  `json:"client_capabilities,omitempty"`
}

// ConnectionManager tracks active MCP connections (thread-safe).
type ConnectionManager struct {
	mu    sync.RWMutex
	conns map[string]*Connection
}

// NewConnectionManager creates a new ConnectionManager.
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		conns: make(map[string]*Connection),
	}
}

// Add registers a new connection.
func (cm *ConnectionManager) Add(conn *Connection) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.conns[conn.ID] = conn
}

// Remove unregisters a connection.
func (cm *ConnectionManager) Remove(id string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	delete(cm.conns, id)
}

// Get returns a connection by ID.
func (cm *ConnectionManager) Get(id string) (*Connection, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	conn, ok := cm.conns[id]
	return conn, ok
}

// Count returns the number of active connections.
func (cm *ConnectionManager) Count() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.conns)
}

// List returns all active connections.
func (cm *ConnectionManager) List() []*Connection {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	result := make([]*Connection, 0, len(cm.conns))
	for _, conn := range cm.conns {
		result = append(result, conn)
	}
	return result
}

// UpdateActivity updates the last activity timestamp for a connection.
func (cm *ConnectionManager) UpdateActivity(id string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if conn, ok := cm.conns[id]; ok {
		conn.LastActivity = time.Now()
	}
}
