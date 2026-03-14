package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/attachments"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/console"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/search"
	"github.com/synapbus/synapbus/internal/trace"
)

// MCPServer wraps the mcp-go server with SynapBus services.
type MCPServer struct {
	mcpServer    *server.MCPServer
	httpServer   *server.StreamableHTTPServer
	connMgr      *ConnectionManager
	agentService *agents.AgentService
	logger       *slog.Logger
	console      *console.Printer
}

// NewMCPServer creates and configures a new MCP server with all tools registered.
func NewMCPServer(
	msgService *messaging.MessagingService,
	agentService *agents.AgentService,
	channelService *channels.Service,
	swarmService *channels.SwarmService,
	attachmentService *attachments.Service,
	searchService *search.Service,
	consolePrinter *console.Printer,
) *MCPServer {
	logger := slog.Default().With("component", "mcp-server")
	connMgr := NewConnectionManager()

	// Set up hooks for client info capture and connection tracking
	hooks := &server.Hooks{}
	hooks.AddAfterInitialize(func(ctx context.Context, id any, msg *mcplib.InitializeRequest, result *mcplib.InitializeResult) {
		clientName := msg.Params.ClientInfo.Name
		clientVersion := msg.Params.ClientInfo.Version
		protocolVersion := msg.Params.ProtocolVersion

		// Build capabilities list
		var caps []string
		if msg.Params.Capabilities.Roots != nil {
			caps = append(caps, "roots")
		}
		if msg.Params.Capabilities.Sampling != nil {
			caps = append(caps, "sampling")
		}
		if msg.Params.Capabilities.Elicitation != nil {
			caps = append(caps, "elicitation")
		}
		if len(msg.Params.Capabilities.Experimental) > 0 {
			for k := range msg.Params.Capabilities.Experimental {
				caps = append(caps, "experimental/"+k)
			}
		}

		// Extract agent name from context (set by HTTP auth middleware)
		agentName, _ := extractAgentName(ctx)

		// Get session ID for connection tracking
		session := server.ClientSessionFromContext(ctx)
		sessionID := ""
		if session != nil {
			sessionID = session.SessionID()
		}

		// Register connection
		if sessionID != "" {
			conn := &Connection{
				ID:                 sessionID,
				AgentName:          agentName,
				Transport:          "streamable-http",
				ConnectedAt:        time.Now(),
				LastActivity:       time.Now(),
				ClientName:         clientName,
				ClientVersion:      clientVersion,
				ProtocolVersion:    protocolVersion,
				ClientCapabilities: caps,
			}
			connMgr.Add(conn)
		}

		// Structured log (always)
		logger.Info("client initialized",
			"agent", agentName,
			"client_name", clientName,
			"client_version", clientVersion,
			"protocol_version", protocolVersion,
			"capabilities", caps,
			"session_id", sessionID,
		)

		// Pretty console output
		if consolePrinter != nil {
			if agentName != "" {
				consolePrinter.AgentConnected(agentName, clientName, clientVersion)
			} else {
				consolePrinter.ClientConnected(clientName, clientVersion)
			}
		}
	})

	hooks.AddOnUnregisterSession(func(ctx context.Context, session server.ClientSession) {
		sessionID := session.SessionID()
		conn, ok := connMgr.Get(sessionID)
		if ok {
			if consolePrinter != nil && conn.AgentName != "" {
				consolePrinter.AgentDisconnected(conn.AgentName)
			}
			logger.Info("client disconnected",
				"agent", conn.AgentName,
				"client_name", conn.ClientName,
				"session_id", sessionID,
				"duration", fmt.Sprintf("%s", time.Since(conn.ConnectedAt).Truncate(time.Second)),
			)
			connMgr.Remove(sessionID)
		}
	})

	// Create the mcp-go server
	mcpSrv := server.NewMCPServer(
		"SynapBus",
		"0.1.0",
		server.WithToolCapabilities(true),
		server.WithHooks(hooks),
	)

	// Register all tools
	registrar := NewToolRegistrar(msgService, agentService)
	if searchService != nil {
		registrar.SetSearchService(searchService)
	}
	registrar.RegisterAll(mcpSrv)

	// Register channel tools
	if channelService != nil {
		channelRegistrar := NewChannelToolRegistrar(channelService, msgService)
		channelRegistrar.RegisterAll(mcpSrv)
	}

	// Register swarm tools
	if swarmService != nil && channelService != nil {
		swarmRegistrar := NewSwarmToolRegistrar(swarmService, channelService)
		swarmRegistrar.RegisterAll(mcpSrv)
	}

	// Register attachment tools
	if attachmentService != nil {
		attachmentRegistrar := NewAttachmentToolRegistrar(attachmentService)
		attachmentRegistrar.RegisterAll(mcpSrv)
	}

	// Create Streamable HTTP transport with context func for auth propagation
	httpServer := server.NewStreamableHTTPServer(mcpSrv,
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			// Propagate agent identity from HTTP auth to MCP context
			if agent, ok := agents.AgentFromContext(r.Context()); ok {
				ctx = ContextWithAgentName(ctx, agent.Name)
				// Propagate owner ID for trace recording
				if ownerID, ok := trace.OwnerIDFromContext(r.Context()); ok {
					ctx = trace.ContextWithOwnerID(ctx, ownerID)
				}
			}
			return ctx
		}),
	)

	s := &MCPServer{
		mcpServer:    mcpSrv,
		httpServer:   httpServer,
		connMgr:      connMgr,
		agentService: agentService,
		logger:       logger,
		console:      consolePrinter,
	}

	logger.Info("MCP server initialized (streamable HTTP transport)")
	return s
}

// Handler returns the HTTP handler for mounting on a router.
func (s *MCPServer) Handler() http.Handler {
	return s.httpServer
}

// ConnectionManager returns the connection manager.
func (s *MCPServer) ConnectionManager() *ConnectionManager {
	return s.connMgr
}

// Shutdown gracefully shuts down the MCP server.
func (s *MCPServer) Shutdown(ctx context.Context) error {
	s.logger.Info("MCP server shutting down")
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}
