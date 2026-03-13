package mcp

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/mark3labs/mcp-go/server"

	"github.com/smart-mcp-proxy/synapbus/internal/agents"
	"github.com/smart-mcp-proxy/synapbus/internal/attachments"
	"github.com/smart-mcp-proxy/synapbus/internal/channels"
	"github.com/smart-mcp-proxy/synapbus/internal/messaging"
	"github.com/smart-mcp-proxy/synapbus/internal/search"
	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

// MCPServer wraps the mcp-go server with SynapBus services.
type MCPServer struct {
	mcpServer    *server.MCPServer
	httpServer   *server.StreamableHTTPServer
	connMgr      *ConnectionManager
	agentService *agents.AgentService
	logger       *slog.Logger
}

// NewMCPServer creates and configures a new MCP server with all tools registered.
func NewMCPServer(
	msgService *messaging.MessagingService,
	agentService *agents.AgentService,
	channelService *channels.Service,
	swarmService *channels.SwarmService,
	attachmentService *attachments.Service,
	searchService *search.Service,
) *MCPServer {
	logger := slog.Default().With("component", "mcp-server")

	// Create the mcp-go server
	mcpSrv := server.NewMCPServer(
		"SynapBus",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	// Register all tools
	registrar := NewToolRegistrar(msgService, agentService)
	if searchService != nil {
		registrar.SetSearchService(searchService)
	}
	registrar.RegisterAll(mcpSrv)

	// Register channel tools
	if channelService != nil {
		channelRegistrar := NewChannelToolRegistrar(channelService)
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
		connMgr:      NewConnectionManager(),
		agentService: agentService,
		logger:       logger,
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
