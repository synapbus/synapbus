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

	"github.com/smart-mcp-proxy/synapbus/internal/agents"
	"github.com/smart-mcp-proxy/synapbus/internal/api"
	"github.com/smart-mcp-proxy/synapbus/internal/attachments"
	"github.com/smart-mcp-proxy/synapbus/internal/auth"
	"github.com/smart-mcp-proxy/synapbus/internal/channels"
	mcpserver "github.com/smart-mcp-proxy/synapbus/internal/mcp"
	"github.com/smart-mcp-proxy/synapbus/internal/messaging"
	"github.com/smart-mcp-proxy/synapbus/internal/storage"
	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

var (
	port           int
	dataDir        string
	logLevel       string
	metricsEnabled bool
	traceRetention string
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

	serveCmd.Flags().IntVar(&port, "port", 8080, "HTTP server port")
	serveCmd.Flags().StringVar(&dataDir, "data", "./data", "Data directory for storage")
	serveCmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	serveCmd.Flags().BoolVar(&metricsEnabled, "metrics", false, "Enable Prometheus metrics endpoint at /metrics")
	serveCmd.Flags().StringVar(&traceRetention, "trace-retention", "0", "Trace retention period (e.g. 30d, 90d, 0 for unlimited)")

	rootCmd.AddCommand(serveCmd)

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

	// Configure slog with JSON handler
	level := parseLogLevel(logLevel)
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slog.Info("starting SynapBus",
		"port", port,
		"data_dir", dataDir,
		"log_level", logLevel,
		"metrics_enabled", metricsEnabled,
		"trace_retention", traceRetention,
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

	// Create MCP server
	mcpSrv := mcpserver.NewMCPServer(msgService, agentService, channelService, attachmentService)
	startTime := time.Now()

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

	// MCP SSE endpoint
	r.Mount("/mcp", mcpSrv.SSEHandler())

	// Mount API routes (traces, export, stats, metrics, attachments)
	apiRouter := api.NewRouter(traceStore, metrics, attachmentService)
	r.Mount("/", apiRouter)

	// Start HTTP server
	addr := fmt.Sprintf(":%d", port)
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
