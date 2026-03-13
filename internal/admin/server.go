// Package admin provides a Unix domain socket server for local administration.
package admin

import (
	"database/sql"
	"log/slog"
	"net"

	"github.com/smart-mcp-proxy/synapbus/internal/agents"
	"github.com/smart-mcp-proxy/synapbus/internal/auth"
	"github.com/smart-mcp-proxy/synapbus/internal/channels"
	"github.com/smart-mcp-proxy/synapbus/internal/messaging"
	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

// Services holds references to all services the admin socket can control.
type Services struct {
	Users    *auth.SQLiteUserStore
	Sessions auth.SessionStore
	Agents   *agents.AgentService
	Messages *messaging.MessagingService
	Channels *channels.Service
	Traces   trace.TraceStore
	DataDir  string
}

// AdminServer is a Unix domain socket server for local administration.
type AdminServer struct {
	listener   net.Listener
	db         *sql.DB
	services   *Services
	logger     *slog.Logger
	socketPath string
	done       chan struct{}
}

// NewServer creates a new admin server bound to a Unix socket at {dataDir}/synapbus.sock.
// If socketPath is non-empty it overrides the default.
func NewServer(socketPath string, db *sql.DB, services *Services, logger *slog.Logger) *AdminServer {
	return &AdminServer{
		db:         db,
		services:   services,
		logger:     logger.With("component", "admin"),
		socketPath: socketPath,
		done:       make(chan struct{}),
	}
}

// SocketPath returns the path to the Unix socket.
func (s *AdminServer) SocketPath() string {
	return s.socketPath
}
