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
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/synapbus/internal/agents"
	"github.com/smart-mcp-proxy/synapbus/internal/auth"
	mcpserver "github.com/smart-mcp-proxy/synapbus/internal/mcp"
	"github.com/smart-mcp-proxy/synapbus/internal/messaging"
	"github.com/smart-mcp-proxy/synapbus/internal/storage"
	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

var (
	port    int
	dataDir string
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

	rootCmd.AddCommand(serveCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runServe(cmd *cobra.Command, args []string) error {
	// Check for environment variable overrides
	if p := os.Getenv("SYNAPBUS_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}
	if d := os.Getenv("SYNAPBUS_DATA_DIR"); d != "" {
		dataDir = d
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slog.Info("starting SynapBus",
		"port", port,
		"data_dir", dataDir,
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

	// Create services
	msgStore := messaging.NewSQLiteMessageStore(db.DB)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	agentStore := agents.NewSQLiteAgentStore(db.DB)
	agentService := agents.NewAgentService(agentStore, tracer)

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
	mcpSrv := mcpserver.NewMCPServer(msgService, agentService)
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
