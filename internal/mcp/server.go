package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/synapbus/synapbus/internal/actions"
	"github.com/synapbus/synapbus/internal/agentquery"
	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/attachments"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/console"
	"github.com/synapbus/synapbus/internal/jsruntime"
	"github.com/synapbus/synapbus/internal/marketplace"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/reactions"
	"github.com/synapbus/synapbus/internal/search"
	"github.com/synapbus/synapbus/internal/trace"
	"github.com/synapbus/synapbus/internal/trust"
	"github.com/synapbus/synapbus/internal/wiki"
)

// MCPServer wraps the mcp-go server with SynapBus services.
type MCPServer struct {
	mcpServer       *server.MCPServer
	httpServer      *server.StreamableHTTPServer
	connMgr         *ConnectionManager
	agentService    *agents.AgentService
	hybridRegistrar *HybridToolRegistrar
	logger          *slog.Logger
	console         *console.Printer
}

// NewMCPServer creates and configures a new MCP server with 5 hybrid tools registered.
func NewMCPServer(
	msgService *messaging.MessagingService,
	agentService *agents.AgentService,
	channelService *channels.Service,
	swarmService *channels.SwarmService,
	attachmentService *attachments.Service,
	searchService *search.Service,
	reactionService *reactions.Service,
	trustService *trust.Service,
	wikiService *wiki.Service,
	consolePrinter *console.Printer,
	jsPool *jsruntime.Pool,
	actionRegistry *actions.Registry,
	actionIndex *actions.Index,
	db *sql.DB,
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
		server.WithPromptCapabilities(true),
		server.WithHooks(hooks),
	)

	// Register the 4 hybrid tools
	hybridRegistrar := NewHybridToolRegistrar(
		msgService,
		agentService,
		channelService,
		swarmService,
		attachmentService,
		searchService,
		reactionService,
		trustService,
		wikiService,
		jsPool,
		actionRegistry,
		actionIndex,
		db,
	)
	hybridRegistrar.RegisterAllOnServer(mcpSrv)

	// Register the 4 MCP prompts
	traceStore := trace.NewSQLiteTraceStore(db)
	promptRegistrar := NewPromptRegistrar(db, agentService, channelService, traceStore)
	promptRegistrar.RegisterAllOnServer(mcpSrv)

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
		mcpServer:       mcpSrv,
		httpServer:      httpServer,
		connMgr:         connMgr,
		agentService:    agentService,
		hybridRegistrar: hybridRegistrar,
		logger:          logger,
		console:         consolePrinter,
	}

	logger.Info("MCP server initialized (4 hybrid tools, 4 prompts, streamable HTTP transport)")
	return s
}

// SetInjection wires the proactive-memory injection middleware
// (feature 020) into the hybrid tool registrar and re-registers the
// hybrid tools so the wrappers take effect. Must be called after
// NewMCPServer and before the server starts handling traffic.
//
// `coreProvider` is consulted only on session-start tools (currently
// `my_status`). Pass nil when US2 has not yet been wired.
func (s *MCPServer) SetInjection(cfg messaging.MemoryConfig, store *messaging.MemoryInjections, coreProvider search.CoreMemoryProvider) {
	if s.hybridRegistrar == nil || s.mcpServer == nil {
		return
	}
	s.hybridRegistrar.SetInjection(cfg, store, coreProvider)
	// Re-register the hybrid tools so the new InjectionEnabled / Core
	// wiring takes effect. AddTool overwrites by name (see mcp-go's
	// `MCPServer.AddTools`), so this swaps in the wrapped handlers
	// without leaking the original registrations.
	s.hybridRegistrar.RegisterAllOnServer(s.mcpServer)
}

// WireGoalsTools registers the spec-018 tool surface (create_goal,
// propose_task_tree, claim_task, request_resource, list_resources,
// complete_goal) on the MCP server. Must be called after NewMCPServer.
// Note: propose_agent was removed in the internal-only mode change.
func (s *MCPServer) WireGoalsTools(r *GoalsToolRegistrar) {
	if r == nil || s.mcpServer == nil {
		return
	}
	r.RegisterAllOnServer(s.mcpServer)
}

// SetQueryExecutor sets the SQL query executor for agent queries via the execute tool.
func (s *MCPServer) SetQueryExecutor(exec *agentquery.Executor) {
	if s.hybridRegistrar != nil {
		s.hybridRegistrar.SetQueryExecutor(exec)
	}
}

// SetMarketplaceService wires the marketplace service (spec 016) into the
// hybrid tool registrar so execute-tool calls can reach marketplace actions.
func (s *MCPServer) SetMarketplaceService(m *marketplace.Service) {
	if s.hybridRegistrar != nil {
		s.hybridRegistrar.SetMarketplaceService(m)
	}
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
