package main

import (
	"context"
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
	"github.com/smart-mcp-proxy/synapbus/internal/channels"
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

	channelStore := channels.NewSQLiteChannelStore(db.DB)
	channelService := channels.NewService(channelStore, msgService, tracer)

	// Create MCP server
	mcpSrv := mcpserver.NewMCPServer(msgService, agentService, channelService)
	startTime := time.Now()

	// Set up chi router
	r := chi.NewRouter()

	// Health endpoint (no auth)
	r.Get("/health", mcpserver.NewHealthHandler(mcpSrv.ConnectionManager(), "0.1.0", startTime))

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
