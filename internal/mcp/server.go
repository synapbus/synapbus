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
)

// MCPServer wraps the mcp-go server with SynapBus services.
type MCPServer struct {
	mcpServer    *server.MCPServer
	sseServer    *server.SSEServer
	connMgr      *ConnectionManager
	agentService *agents.AgentService
	logger       *slog.Logger
}

// NewMCPServer creates and configures a new MCP server with all tools registered.
func NewMCPServer(
	msgService *messaging.MessagingService,
	agentService *agents.AgentService,
	channelService *channels.Service,
	attachmentService *attachments.Service,
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
	registrar.RegisterAll(mcpSrv)

	// Register channel tools
	if channelService != nil {
		channelRegistrar := NewChannelToolRegistrar(channelService)
		channelRegistrar.RegisterAll(mcpSrv)
	}

	// Register attachment tools
	if attachmentService != nil {
		attachmentRegistrar := NewAttachmentToolRegistrar(attachmentService)
		attachmentRegistrar.RegisterAll(mcpSrv)
	}

	// Create SSE transport with context func for auth propagation
	sseServer := server.NewSSEServer(mcpSrv,
		server.WithSSEContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			// Propagate agent identity from HTTP auth to MCP context
			if agent, ok := agents.AgentFromContext(r.Context()); ok {
				return ContextWithAgentName(ctx, agent.Name)
			}
			return ctx
		}),
	)

	s := &MCPServer{
		mcpServer:    mcpSrv,
		sseServer:    sseServer,
		connMgr:      NewConnectionManager(),
		agentService: agentService,
		logger:       logger,
	}

	logger.Info("MCP server initialized")
	return s
}

// SSEHandler returns the SSE handler for mounting on a router.
func (s *MCPServer) SSEHandler() http.Handler {
	return s.sseServer
}

// ConnectionManager returns the connection manager.
func (s *MCPServer) ConnectionManager() *ConnectionManager {
	return s.connMgr
}

// Shutdown gracefully shuts down the MCP server.
func (s *MCPServer) Shutdown(ctx context.Context) error {
	s.logger.Info("MCP server shutting down")
	if err := s.sseServer.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}
