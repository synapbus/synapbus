package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/synapbus/internal/admin"
	"github.com/smart-mcp-proxy/synapbus/internal/agents"
	"github.com/smart-mcp-proxy/synapbus/internal/api"
	"github.com/smart-mcp-proxy/synapbus/internal/apikeys"
	"github.com/smart-mcp-proxy/synapbus/internal/attachments"
	"github.com/smart-mcp-proxy/synapbus/internal/auth"
	"github.com/smart-mcp-proxy/synapbus/internal/channels"
	mcpserver "github.com/smart-mcp-proxy/synapbus/internal/mcp"
	"github.com/smart-mcp-proxy/synapbus/internal/messaging"
	"github.com/smart-mcp-proxy/synapbus/internal/search"
	"github.com/smart-mcp-proxy/synapbus/internal/search/embedding"
	"github.com/smart-mcp-proxy/synapbus/internal/storage"
	"github.com/smart-mcp-proxy/synapbus/internal/trace"
	"github.com/smart-mcp-proxy/synapbus/internal/web"
)

var (
	host           string
	port           int
	dataDir        string
	logLevel       string
	metricsEnabled bool
	traceRetention string
	adminSocketPath string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "synapbus",
		Short: "SynapBus — MCP-native agent-to-agent messaging",
		Long:  "Local-first, MCP-native messaging service for AI agents. Single binary with embedded storage, semantic search, and a Slack-like Web UI.",
	}

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the SynapBus server",
		RunE:  runServe,
	}

	serveCmd.Flags().StringVar(&host, "host", "0.0.0.0", "HTTP server bind address")
	serveCmd.Flags().IntVar(&port, "port", 8080, "HTTP server port")
	serveCmd.Flags().StringVar(&dataDir, "data", "./data", "Data directory for storage")
	serveCmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	serveCmd.Flags().BoolVar(&metricsEnabled, "metrics", false, "Enable Prometheus metrics endpoint at /metrics")
	serveCmd.Flags().StringVar(&traceRetention, "trace-retention", "0", "Trace retention period (e.g. 30d, 90d, 0 for unlimited)")
	serveCmd.Flags().StringVar(&adminSocketPath, "admin-socket", "", "Admin Unix socket path (default: {data}/synapbus.sock)")

	rootCmd.AddCommand(serveCmd)

	// Add admin CLI subcommands.
	addAdminCommands(rootCmd)

	if err := rootCmd.Execute(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

// parseLogLevel converts a string log level to slog.Level.
func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// parseRetentionDuration parses a retention string like "30d", "90d", or "0".
func parseRetentionDuration(s string) time.Duration {
	if s == "" || s == "0" {
		return 0
	}
	// Try parsing as "Nd" format (days)
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour
		}
	}
	// Try standard duration parsing
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

func runServe(cmd *cobra.Command, args []string) error {
	// Check for environment variable overrides
	if h := os.Getenv("SYNAPBUS_HOST"); h != "" {
		host = h
	}
	if p := os.Getenv("SYNAPBUS_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}
	if d := os.Getenv("SYNAPBUS_DATA_DIR"); d != "" {
		dataDir = d
	}
	if ll := os.Getenv("SYNAPBUS_LOG_LEVEL"); ll != "" {
		logLevel = ll
	}
	if me := os.Getenv("SYNAPBUS_METRICS"); me != "" {
		metricsEnabled = me == "true" || me == "1"
	}
	if tr := os.Getenv("SYNAPBUS_TRACE_RETENTION"); tr != "" {
		traceRetention = tr
	}
	if as := os.Getenv("SYNAPBUS_ADMIN_SOCKET"); as != "" {
		adminSocketPath = as
	}
	if adminSocketPath == "" {
		adminSocketPath = filepath.Join(dataDir, "synapbus.sock")
	}

	// Configure slog with JSON handler
	level := parseLogLevel(logLevel)
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slog.Info("starting SynapBus",
		"host", host,
		"port", port,
		"data_dir", dataDir,
		"log_level", logLevel,
		"metrics_enabled", metricsEnabled,
		"trace_retention", traceRetention,
		"admin_socket", adminSocketPath,
	)

	// Initialize SQLite database
	db, err := storage.New(ctx, dataDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// Run migrations
	if err := storage.RunMigrations(ctx, db.DB); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	slog.Info("migrations complete")

	// Create tracer
	tracer := trace.NewTracer(db.DB)
	defer tracer.Close()

	// Set up metrics
	var metrics *trace.Metrics
	if metricsEnabled {
		metrics = trace.NewMetrics()
		tracer.SetMetrics(metrics)
		slog.Info("prometheus metrics enabled")
	}

	// Create trace store
	traceStore := trace.NewSQLiteTraceStore(db.DB)

	// Set up trace retention cleanup
	retentionDuration := parseRetentionDuration(traceRetention)
	var retentionCleaner *trace.RetentionCleaner
	if retentionDuration > 0 {
		retentionCleaner = trace.NewRetentionCleaner(traceStore, retentionDuration, 1*time.Hour)
		retentionCleaner.Start()
		slog.Info("trace retention cleanup enabled",
			"retention", retentionDuration.String(),
		)
	}

	// Create services
	msgStore := messaging.NewSQLiteMessageStore(db.DB)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	agentStore := agents.NewSQLiteAgentStore(db.DB)
	agentService := agents.NewAgentService(agentStore, tracer)

	channelStore := channels.NewSQLiteChannelStore(db.DB)
	channelService := channels.NewService(channelStore, msgService, tracer)

	// Create swarm service (task auction + stigmergy)
	taskStore := channels.NewSQLiteTaskStore(db.DB)
	swarmService := channels.NewSwarmService(taskStore, channelStore, tracer)

	// Create attachment service
	attachmentsDir := filepath.Join(dataDir, "attachments")
	cas, err := attachments.NewCAS(attachmentsDir, slog.Default())
	if err != nil {
		return fmt.Errorf("create CAS engine: %w", err)
	}
	attachmentStore := attachments.NewSQLiteStore(db.DB, slog.Default())
	attachmentService := attachments.NewService(attachmentStore, cas, slog.Default())
	slog.Info("attachment service initialized", "dir", attachmentsDir)

	// Initialize auth subsystem
	authSecret := make([]byte, 32)
	if _, err := rand.Read(authSecret); err != nil {
		return fmt.Errorf("generate auth secret: %w", err)
	}

	authCfg := auth.DefaultConfig()
	authCfg.Secret = authSecret
	authCfg.DevMode = true
	authCfg.IssuerURL = fmt.Sprintf("http://localhost:%d", port)

	userStore := auth.NewSQLiteUserStore(db.DB, authCfg.BcryptCost)
	sessionStore := auth.NewSQLiteSessionStore(db.DB)
	clientStore := auth.NewSQLiteClientStore(db.DB, authCfg.BcryptCost)
	fositeStore := auth.NewFositeStore(db.DB, authCfg.BcryptCost)
	oauthProvider := auth.NewOAuthProvider(authCfg, fositeStore)
	authHandlers := auth.NewHandlers(userStore, sessionStore, clientStore, oauthProvider, authCfg)

	// Create initial admin user if no users exist
	userCount, err := userStore.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if userCount == 0 {
		adminPassword := generateRandomPassword()
		if _, err := userStore.CreateUser(ctx, "admin", adminPassword, "Admin"); err != nil {
			return fmt.Errorf("create admin user: %w", err)
		}
		slog.Info("initial admin user created",
			"username", "admin",
			"password", adminPassword,
		)
		fmt.Printf("\n========================================\n")
		fmt.Printf("  Initial admin account created\n")
		fmt.Printf("  Username: admin\n")
		fmt.Printf("  Password: %s\n", adminPassword)
		fmt.Printf("  (Change this password after first login)\n")
		fmt.Printf("========================================\n\n")
	}

	// Initialize search subsystem
	searchCfg := search.LoadConfigFromEnv()
	var searchService *search.Service
	var embPipeline *search.Pipeline
	var vectorIndex *search.VectorIndex

	if searchCfg.IsEnabled() {
		slog.Info("initializing semantic search",
			"provider", searchCfg.Provider,
		)

		// Create embedding provider
		embProvider, err := embedding.NewProvider(searchCfg.Provider, searchCfg.APIKey, searchCfg.OllamaURL)
		if err != nil {
			slog.Warn("failed to create embedding provider, semantic search disabled",
				"error", err,
			)
		} else {
			// Create vector index
			vectorIndex, err = search.NewVectorIndex(dataDir)
			if err != nil {
				slog.Warn("failed to create vector index, semantic search disabled",
					"error", err,
				)
			} else {
				embStore := search.NewEmbeddingStore(db.DB)

				// Check for provider change
				existingProvider, _ := embStore.GetEmbeddingProvider(ctx)
				if existingProvider != "" && existingProvider != embProvider.Name() {
					slog.Info("embedding provider changed, re-indexing",
						"old_provider", existingProvider,
						"new_provider", embProvider.Name(),
					)
					_ = embStore.DeleteAllEmbeddings(ctx)
					_ = embStore.ClearQueue(ctx)
					_ = vectorIndex.Rebuild(nil)
				}

				// Enqueue messages that need embedding
				enqueued, _ := embStore.EnqueueAllMessages(ctx)
				if enqueued > 0 {
					slog.Info("enqueued messages for embedding", "count", enqueued)
				}

				// Create and start pipeline
				embPipeline = search.NewPipeline(embProvider, embStore, vectorIndex, searchCfg)
				embPipeline.Start(ctx)

				// Create search service with semantic support
				searchService = search.NewService(db.DB, embProvider, vectorIndex, msgService)
				slog.Info("semantic search enabled",
					"provider", embProvider.Name(),
					"dimensions", embProvider.Dimensions(),
					"index_size", vectorIndex.Len(),
				)
			}
		}
	}

	// If no semantic search, create search service with FTS-only fallback
	if searchService == nil {
		searchService = search.NewService(db.DB, nil, nil, msgService)
		slog.Info("semantic search not configured, using full-text search only")
	}

	// Create API key service
	apiKeyStore := apikeys.NewSQLiteStore(db.DB)
	apiKeyService := apikeys.NewService(apiKeyStore)

	// Create MCP server (with swarm + attachment + search tools)
	mcpSrv := mcpserver.NewMCPServer(msgService, agentService, channelService, swarmService, attachmentService, searchService)
	startTime := time.Now()

	// Start task expiry worker
	expiryWorker := channels.NewExpiryWorker(swarmService, 1*time.Minute)
	expiryWorker.Start()
	slog.Info("task expiry worker started")

	// Set up chi router
	r := chi.NewRouter()

	// Health endpoint (no auth)
	r.Get("/health", mcpserver.NewHealthHandler(mcpSrv.ConnectionManager(), "0.1.0", startTime))

	// Auth endpoints (public)
	r.Post("/auth/register", authHandlers.HandleRegister)
	r.Post("/auth/login", authHandlers.HandleLogin)

	// OAuth endpoints
	r.Get("/oauth/authorize", authHandlers.HandleAuthorize)
	r.Post("/oauth/token", authHandlers.HandleToken)
	r.Post("/oauth/introspect", authHandlers.HandleIntrospect)

	// Protected auth endpoints
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireSession(userStore, sessionStore))
		r.Post("/auth/logout", authHandlers.HandleLogout)
		r.Get("/auth/me", authHandlers.HandleMe)
		r.Put("/auth/password", authHandlers.HandleChangePassword)
	})

	// MCP Streamable HTTP endpoint (with optional agent auth + API keys)
	r.Group(func(r chi.Router) {
		r.Use(agents.OptionalAuthMiddlewareWithAPIKeys(agentService, apiKeyService))
		r.Mount("/mcp", mcpSrv.Handler())
	})

	// Create SSE hub for real-time events
	sseHub := api.NewSSEHub()

	// Mount API routes (traces, export, stats, metrics, attachments, messages, agents, channels, SSE)
	sessionMiddleware := api.SessionToOwnerMiddleware(userStore, sessionStore)
	apiRouter := api.NewRouterWithConfig(api.RouterConfig{
		TraceStore:        traceStore,
		Metrics:           metrics,
		AttachmentService: attachmentService,
		MsgService:        msgService,
		AgentService:      agentService,
		ChannelService:    channelService,
		APIKeyService:     apiKeyService,
		SSEHub:            sseHub,
		SessionMiddleware: sessionMiddleware,
	})
	r.Mount("/", apiRouter)

	// Serve embedded Web UI SPA (catch-all for non-API routes)
	r.NotFound(web.NewSPAHandler().ServeHTTP)

	// Start admin socket server
	adminSvcs := &admin.Services{
		Users:    userStore,
		Sessions: sessionStore,
		Agents:   agentService,
		Messages: msgService,
		Channels: channelService,
		Traces:   traceStore,
		DataDir:  dataDir,
	}
	adminServer := admin.NewServer(adminSocketPath, db.DB, adminSvcs, logger)
	if err := adminServer.Start(); err != nil {
		return fmt.Errorf("start admin socket: %w", err)
	}
	slog.Info("admin socket listening", "path", adminServer.SocketPath())

	// Start HTTP server
	addr := fmt.Sprintf("%s:%d", host, port)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("HTTP server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			cancel()
		}
	}()

	// Wait for shutdown signal
	select {
	case sig := <-sigCh:
		slog.Info("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
	}

	// Shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop admin socket
	adminServer.Stop()

	// Stop expiry worker
	expiryWorker.Stop()

	// Stop embedding pipeline
	if embPipeline != nil {
		embPipeline.Stop()
	}

	// Save vector index
	if vectorIndex != nil {
		if err := vectorIndex.Save(); err != nil {
			slog.Error("failed to save vector index", "error", err)
		} else {
			slog.Info("vector index saved", "size", vectorIndex.Len())
		}
	}

	// Stop retention cleaner
	if retentionCleaner != nil {
		retentionCleaner.Stop()
	}

	if err := mcpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("MCP server shutdown error", "error", err)
	}

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	slog.Info("SynapBus stopped")
	return nil
}

// generateRandomPassword creates a cryptographically random password.
func generateRandomPassword() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
