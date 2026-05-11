package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"github.com/synapbus/synapbus/internal/a2a"
	"github.com/synapbus/synapbus/internal/actions"
	"github.com/synapbus/synapbus/internal/admin"
	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/api"
	"github.com/synapbus/synapbus/internal/apikeys"
	"github.com/synapbus/synapbus/internal/attachments"
	"github.com/synapbus/synapbus/internal/auth"
	"github.com/synapbus/synapbus/internal/auth/idp"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/console"
	"github.com/synapbus/synapbus/internal/goals"
	"github.com/synapbus/synapbus/internal/goaltasks"
	"github.com/synapbus/synapbus/internal/secrets"
	"github.com/synapbus/synapbus/internal/dispatcher"
	"github.com/synapbus/synapbus/internal/health"
	"github.com/synapbus/synapbus/internal/jsruntime"
	k8spkg "github.com/synapbus/synapbus/internal/k8s"
	"github.com/synapbus/synapbus/internal/marketplace"
	mcpserver "github.com/synapbus/synapbus/internal/mcp"
	"github.com/synapbus/synapbus/internal/agentquery"
	reactorpkg "github.com/synapbus/synapbus/internal/reactor"
	"github.com/synapbus/synapbus/internal/messaging"
	prommetrics "github.com/synapbus/synapbus/internal/metrics"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/harness/docker"
	"github.com/synapbus/synapbus/internal/harness/k8sjob"
	"github.com/synapbus/synapbus/internal/harness/runs"
	"github.com/synapbus/synapbus/internal/harness/subprocess"
	"github.com/synapbus/synapbus/internal/harness/webhook"
	"github.com/synapbus/synapbus/internal/observability"
	"github.com/synapbus/synapbus/internal/reactions"
	"github.com/synapbus/synapbus/internal/search"
	"github.com/synapbus/synapbus/internal/search/embedding"
	"github.com/synapbus/synapbus/internal/storage"
	"github.com/synapbus/synapbus/internal/push"
	"github.com/synapbus/synapbus/internal/trace"
	"github.com/synapbus/synapbus/internal/trust"
	"github.com/synapbus/synapbus/internal/web"
	"github.com/synapbus/synapbus/internal/wiki"
	"github.com/synapbus/synapbus/internal/webhooks"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

var (
	host              string
	port              int
	dataDir           string
	logLevel          string
	metricsEnabled    bool
	traceRetention    string
	adminSocketPath   string
	webhookWorkers    int
	messageRetention  string
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "synapbus",
		Short:   "SynapBus — MCP-native agent-to-agent messaging",
		Long:    "Local-first, MCP-native messaging service for AI agents. Single binary with embedded storage, semantic search, and a Slack-like Web UI.",
		Version: version,
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
	serveCmd.Flags().IntVar(&webhookWorkers, "webhook-workers", 8, "Number of webhook delivery worker goroutines")
	serveCmd.Flags().StringVar(&messageRetention, "message-retention", "12m", "Message retention period (e.g. 12m, 365d, 0 to disable)")

	rootCmd.AddCommand(serveCmd)

	// Add admin CLI subcommands.
	addAdminCommands(rootCmd)

	// Add wiki export/import subcommands.
	addWikiCommands(rootCmd)

	// Add secrets CLI (resource-request protocol, feature 018).
	rootCmd.AddCommand(registerSecretsCLI())

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
	if ww := os.Getenv("SYNAPBUS_WEBHOOK_WORKERS"); ww != "" {
		fmt.Sscanf(ww, "%d", &webhookWorkers)
	}
	if mr := os.Getenv("SYNAPBUS_MESSAGE_RETENTION"); mr != "" {
		messageRetention = mr
	}
	if adminSocketPath == "" {
		// Default to /tmp in containers — PVC-backed filesystems (NFS, Ceph,
		// EBS CSI) often don't support Unix domain sockets.
		if _, err := os.Stat("/.dockerenv"); err == nil {
			adminSocketPath = "/tmp/synapbus.sock"
		} else {
			adminSocketPath = filepath.Join(dataDir, "synapbus.sock")
		}
	}

	// Configure slog with JSON handler writing to stderr (stdout is for console output)
	level := parseLogLevel(logLevel)
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Create console printer for pretty output
	con := console.New()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialise OpenTelemetry tracing (opt-in via SYNAPBUS_OTEL_ENABLED=1).
	// Harmless when disabled — installs only the W3C propagator and
	// leaves the global tracer provider as the default no-op.
	otelShutdown, err := observability.Init(ctx, observability.ConfigFromEnv(os.Getenv), logger)
	if err != nil {
		return fmt.Errorf("init otel: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = otelShutdown(shutdownCtx)
	}()

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

	// Wire dead letter store into agent service for capture on deregistration
	deadLetterStore := messaging.NewDeadLetterStore(db.DB)
	agentService.SetDeadLetterStore(deadLetterStore)

	// Ensure system agent exists (used for retention warnings and system notifications)
	if _, err := agentService.EnsureSystemAgent(ctx, 1); err != nil {
		slog.Warn("failed to create system agent", "error", err)
	}

	channelStore := channels.NewSQLiteChannelStore(db.DB)
	channelService := channels.NewService(channelStore, msgService, tracer)

	// Ensure #general channel exists
	_, err = channelService.CreateChannel(ctx, channels.CreateChannelRequest{
		Name:        "general",
		Description: "General discussion",
		Type:        "standard",
		CreatedBy:   "system",
	})
	if err != nil {
		// Ignore "already exists" errors
		if !strings.Contains(err.Error(), "UNIQUE") && !strings.Contains(err.Error(), "already exists") {
			slog.Warn("failed to create #general channel", "error", err)
		}
	} else {
		slog.Info("created default #general channel")
	}

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
	msgService.SetAttachmentLinker(&attachmentLinkerAdapter{svc: attachmentService})
	slog.Info("attachment service initialized", "dir", attachmentsDir)

	// Create reaction service
	reactionStore := reactions.NewSQLiteStore(db.DB)
	reactionService := reactions.NewService(reactionStore, slog.Default())
	msgService.SetReactionEnricher(&reactionEnricherAdapter{svc: reactionService})
	slog.Info("reaction service initialized")

	// Create trust service
	trustStore := trust.NewSQLiteStore(db.DB)
	trustService := trust.NewService(trustStore, slog.Default())
	slog.Info("trust service initialized")

	// Initialize auth subsystem
	authSecret := make([]byte, 32)
	if _, err := rand.Read(authSecret); err != nil {
		return fmt.Errorf("generate auth secret: %w", err)
	}

	authCfg := auth.DefaultConfig()
	authCfg.Secret = authSecret
	authCfg.DevMode = true
	// Use SYNAPBUS_BASE_URL for remote/LAN deployments; otherwise derive from request Host header
	if baseURL := os.Getenv("SYNAPBUS_BASE_URL"); baseURL != "" {
		authCfg.IssuerURL = strings.TrimRight(baseURL, "/")
	}
	// Leave IssuerURL empty for localhost — metadata handler falls back to r.Host

	userStore := auth.NewSQLiteUserStoreWithRead(db.DB, db.QueryDB(), authCfg.BcryptCost)
	sessionStore := auth.NewSQLiteSessionStoreWithRead(db.DB, db.QueryDB())
	clientStore := auth.NewSQLiteClientStore(db.DB, authCfg.BcryptCost)
	fositeStore := auth.NewFositeStore(db.DB, authCfg.BcryptCost)
	oauthProvider := auth.NewOAuthProvider(authCfg, fositeStore)
	authHandlers := auth.NewHandlers(userStore, sessionStore, clientStore, oauthProvider, authCfg)

	// Wire agent lister into auth handlers for OAuth authorize page
	authHandlers.SetAgentLister(&agentListerAdapter{agentService: agentService})

	// Initialize external identity providers (GitHub, Google, Azure AD)
	baseURL := authCfg.IssuerURL
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://localhost:%d", port)
	}
	idpProviders := idp.LoadConfig(baseURL)

	// Register default MCP OAuth client if it doesn't already exist (T016)
	ensureDefaultMCPClient(ctx, db.DB, authCfg.BcryptCost)

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

	// Print available embedding providers
	providers := embedding.AvailableProviders(searchCfg.APIKey, searchCfg.OllamaURL)
	slog.Info("available embedding providers:")
	for _, p := range providers {
		status := "not configured"
		if p.Configured {
			status = "ready"
		}
		slog.Info("  embedding provider",
			"name", p.Name,
			"model", p.Model,
			"dimensions", p.Dimensions,
			"status", status,
		)
	}

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

				// Wire pipeline into messaging so new messages auto-enqueue
				msgService.SetEmbeddingNotifier(embPipeline)

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

	// Create webhook service
	allowHTTPWebhooks := os.Getenv("SYNAPBUS_ALLOW_HTTP_WEBHOOKS") == "true"
	allowPrivateNetworks := os.Getenv("SYNAPBUS_ALLOW_PRIVATE_NETWORKS") == "true"
	webhookStore := webhooks.NewSQLiteWebhookStore(db.DB)
	webhookService := webhooks.NewWebhookService(webhookStore, allowHTTPWebhooks, allowPrivateNetworks)
	rateLimiter := webhooks.NewAgentRateLimiter(60) // 60 deliveries/minute per agent

	// Create delivery engine (webhook dispatcher)
	deliveryEngine := webhooks.NewDeliveryEngine(webhookService, rateLimiter, allowPrivateNetworks)
	deliveryEngine.Start()
	slog.Info("webhook delivery engine started")

	// Create K8s job runner and service
	k8sRunner := k8spkg.NewJobRunner(slog.Default())
	k8sStore := k8spkg.NewSQLiteK8sStore(db.DB)
	k8sService := k8spkg.NewK8sService(k8sStore, k8sRunner) // K8s service for CLI admin commands; not passed to MCP
	k8sDispatcher := k8spkg.NewK8sDispatcher(k8sStore, k8sRunner, slog.Default())

	if k8sRunner.IsAvailable() {
		slog.Info("K8s job runner available")
	} else {
		slog.Info("K8s job runner not available (not in-cluster)")
	}

	// Create reactor engine for reactive agent triggering
	reactorStore := reactorpkg.NewStore(db.DB)
	reactorEngine := reactorpkg.New(reactorStore, agentStore, k8sRunner, slog.Default())
	reactorNotifier := reactorpkg.NewDMFailureNotifier(msgService)
	reactorEngine.SetFailureNotifier(reactorNotifier)

	// Harness registry — single seam for non-K8s reactive runs and
	// for any caller (admin CLI, MCP, future features) that wants to
	// dispatch work to an agent's configured backend. K8s agents keep
	// going through the existing createJob + poller path; subprocess
	// and webhook agents go through Registry.Execute.
	harnessRegistry := harness.NewRegistry()
	harnessRegistry.Register(k8sjob.New(k8sRunner, nil, slog.Default()))
	// SYNAPBUS_KEEP_WORKDIR=1 preserves per-run workdirs after successful
	// runs. Useful when debugging MCP tool traces, gemini stdout, or
	// materialized config files. Default off to avoid disk growth.
	keepWorkdir := os.Getenv("SYNAPBUS_KEEP_WORKDIR") == "1"
	harnessRegistry.Register(subprocess.New(subprocess.Config{
		BaseDir:              filepath.Join(dataDir, "harness", "subprocess"),
		KeepWorkdirOnSuccess: keepWorkdir,
	}, slog.Default()))
	harnessRegistry.Register(webhook.New(webhook.Config{}, slog.Default()))
	// Docker isolation backend — agents whose harness_config_json has a
	// `docker.image` block run inside ephemeral containers. Same per-run
	// workdir convention as subprocess; the workdir is bind-mounted at
	// /workspace so wrappers and config files (.gemini/settings.json,
	// CLAUDE.md, message.json) reach the container unchanged. The MCP
	// host gets rewritten from 127.0.0.1 to host.docker.internal so the
	// in-container Gemini/Claude CLI can reach the SynapBus MCP server.
	harnessRegistry.Register(docker.New(docker.Config{
		BaseDir:              filepath.Join(dataDir, "harness", "docker"),
		KeepWorkdirOnSuccess: keepWorkdir,
		HostMCPPort:          port,
		MountHostCredentials: true,
	}, slog.Default()))
	harnessRunsStore := runs.New(db.DB, slog.Default())
	harnessRegistry.Observer = harnessRunsStore
	reactorEngine.SetHarnessRegistry(harnessRegistry)
	reactorEngine.SetReactionNotifier(&reactorReactionAdapter{svc: reactionService})

	// Secrets store — feature 018. Encrypted secrets scoped to
	// user/agent/task, injected by the reactor as env vars on each
	// reactive subprocess run.
	secretsStore, err := secrets.NewStore(db.DB, dataDir, slog.Default())
	if err != nil {
		slog.Warn("secrets store unavailable — reactive runs will not receive injected secrets", "error", err)
	} else {
		reactorEngine.SetSecretProvider(secretsStore)
		slog.Info("secrets store bootstrapped and wired to reactor")
	}
	slog.Info("harness registry configured",
		"backends", harnessRegistry.Names(),
	)

	// Create event dispatcher (fans out to webhooks + K8s + reactor)
	eventDispatcher := dispatcher.NewMultiDispatcher(slog.Default(), deliveryEngine, k8sDispatcher, reactorEngine)
	msgService.SetDispatcher(eventDispatcher)

	// Start reactor poller for K8s Job status tracking
	reactorPoller := reactorpkg.NewPoller(reactorStore, agentStore, k8sRunner, reactorEngine, slog.Default())
	reactorPoller.Start()
	slog.Info("reactor engine and poller started")

	// Create JS runtime pool and action registry for hybrid MCP tools
	jsPool := jsruntime.NewPool(10)
	defer jsPool.Close()

	actionRegistry := actions.NewRegistry()
	actionIndex := actions.NewIndex(actionRegistry.List())

	// Create MCP server (4 hybrid tools: my_status, send_message, search, execute)
	wikiService := wiki.NewService(db.DB)

	// Goals + goal_tasks (feature 018 — dynamic agent spawning).
	goalsStore := goals.NewStore(db.DB)
	goalTasksStore := goaltasks.NewStore(db.DB)
	goalChannelCreator := &svcGoalChannelCreator{channels: channelService}
	goalsService := goals.NewService(goalsStore, goalChannelCreator, slog.Default())
	goalTasksService := goaltasks.NewService(goalTasksStore, slog.Default())

	mcpSrv := mcpserver.NewMCPServer(msgService, agentService, channelService, swarmService, attachmentService, searchService, reactionService, trustService, wikiService, con, jsPool, actionRegistry, actionIndex, db.DB)

	// Wire the agent marketplace (spec 016 MVP).
	marketplaceStore := marketplace.NewStore(db.DB)
	marketplaceSvc := marketplace.NewService(marketplaceStore, wikiService, swarmService, channelService, msgService, tracer)
	mcpSrv.SetMarketplaceService(marketplaceSvc)
	slog.Info("agent marketplace service initialized (spec 016)")

	// Wire the spec-018 tool surface (dynamic agent spawning). Only
	// registered if the goals/tasks services are up — which they
	// always are after the block above.
	goalsToolReg := mcpserver.NewGoalsToolRegistrar(
		goalsService,
		goalTasksService,
		agentService,
		secretsStore,
		db.DB,
	)
	mcpSrv.WireGoalsTools(goalsToolReg)
	slog.Info("spec-018 MCP tools wired (create_goal, propose_task_tree, claim_task, request_resource, list_resources, complete_goal)")

	// Set up SQL query executor for agents (uses read pool if available)
	queryDB := db.QueryDB()
	queryExec := agentquery.New(queryDB, slog.Default())
	mcpSrv.SetQueryExecutor(queryExec)
	slog.Info("agent SQL query executor initialized", "read_pool", db.ReadDB != nil)

	startTime := time.Now()

	// Start task expiry worker (gated — set SYNAPBUS_DISABLE_EXPIRY_WORKER=1
	// to skip. Used by local examples to avoid the legacy-tasks-table
	// write-pool contention bug that wedges the server over time.)
	var expiryWorker *channels.ExpiryWorker
	if os.Getenv("SYNAPBUS_DISABLE_EXPIRY_WORKER") == "1" {
		slog.Info("task expiry worker disabled by SYNAPBUS_DISABLE_EXPIRY_WORKER=1")
	} else {
		expiryWorker = channels.NewExpiryWorker(swarmService, 1*time.Minute)
		expiryWorker.Start()
		slog.Info("task expiry worker started")
	}

	// Start message retention worker (gated — set
	// SYNAPBUS_DISABLE_RETENTION_WORKER=1 to skip.)
	retentionCfg := messaging.ParseRetentionPeriod(messageRetention)
	var retentionWorker *messaging.RetentionWorker
	if os.Getenv("SYNAPBUS_DISABLE_RETENTION_WORKER") == "1" {
		slog.Info("message retention worker disabled by SYNAPBUS_DISABLE_RETENTION_WORKER=1")
	} else if retentionCfg.Enabled {
		retentionWorker = messaging.NewRetentionWorker(db.DB, retentionCfg, dataDir)
		retentionWorker.Start()
		slog.Info("message retention worker started",
			"retention_period", retentionCfg.RetentionPeriodHuman(),
			"cleanup_interval", retentionCfg.CleanupInterval.String(),
		)
	} else {
		slog.Info("message retention disabled")
	}

	// Start stalemate worker (gated — set SYNAPBUS_DISABLE_STALEMATE_WORKER=1
	// to skip.)
	var stalemateWorker *messaging.StalemateWorker
	if os.Getenv("SYNAPBUS_DISABLE_STALEMATE_WORKER") == "1" {
		slog.Info("stalemate worker disabled by SYNAPBUS_DISABLE_STALEMATE_WORKER=1")
	} else {
		stalemateConfig := messaging.ParseStalemateConfig()
		stalemateWorker = messaging.NewStalemateWorker(db.DB, msgService, stalemateConfig)
		stalemateWorker.Start()
		slog.Info("stalemate worker started",
			"processing_timeout", stalemateConfig.ProcessingTimeout.String(),
			"interval", stalemateConfig.Interval.String(),
		)
	}

	// Create health checker
	healthChecker := health.NewChecker(db.DB, version)

	// Set up chi router
	r := chi.NewRouter()

	// Add metrics middleware when enabled
	if metricsEnabled {
		r.Use(prommetrics.Middleware)
		slog.Info("prometheus HTTP metrics middleware enabled")
	}

	// Health endpoint (no auth)
	r.Get("/health", mcpserver.NewHealthHandler(mcpSrv.ConnectionManager(), version, startTime))

	// Kubernetes-style health probes (no auth, always registered)
	r.Get("/healthz", healthChecker.Healthz)
	r.Get("/readyz", healthChecker.Readyz)

	// Prometheus metrics endpoint (only when enabled)
	if metricsEnabled {
		r.Handle("/metrics", promhttp.Handler())
	}

	// Kubernetes-style health probes (no auth, always registered)
	r.Get("/healthz", healthChecker.Healthz)
	r.Get("/readyz", healthChecker.Readyz)

	// Prometheus metrics endpoint (only when enabled)
	if metricsEnabled {
		r.Handle("/metrics", promhttp.Handler())
	}

	// Auth endpoints (public)
	r.Post("/auth/register", withHumanAgent(authHandlers.HandleRegister, userStore, agentService, channelService))
	r.Post("/auth/login", withHumanAgent(authHandlers.HandleLogin, userStore, agentService, channelService))

	// External identity provider endpoints (public)
	if len(idpProviders) > 0 {
		idpStore := idp.NewUserIdentityStore(db.DB)
		idpAgentAdapter := &idpAgentProvisioner{agentService: agentService, channelService: channelService}
		idpHandlers := idp.NewHandlers(idpProviders, idpStore, userStore, sessionStore, idpAgentAdapter)
		r.Get("/auth/providers", idpHandlers.HandleListProviders)
		r.Get("/auth/login/{provider}", idpHandlers.HandleLogin)
		r.Get("/auth/callback/{provider}", idpHandlers.HandleCallback)
		slog.Info("external identity providers configured", "count", len(idpProviders))
	} else {
		// Return empty list when no providers configured
		r.Get("/auth/providers", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"providers":[]}`))
		})
	}

	// OAuth metadata (public, per RFC 8414)
	r.Get("/.well-known/oauth-authorization-server", authHandlers.HandleOAuthMetadata)

	// A2A Agent Card discovery (public, no auth required)
	agentCardBaseURL := authCfg.IssuerURL // reuse the same base URL config
	r.Get("/.well-known/agent-card.json", a2a.NewAgentCardHandler(
		&a2aAgentListerAdapter{agentService: agentService},
		agentCardBaseURL,
		version,
	))

	// OAuth endpoints
	r.Get("/oauth/authorize", authHandlers.HandleAuthorizeGet)
	r.Post("/oauth/authorize", authHandlers.HandleAuthorizePost)
	r.Post("/oauth/token", authHandlers.HandleToken)
	r.Post("/oauth/introspect", authHandlers.HandleIntrospect)
	r.Post("/oauth/register", authHandlers.HandleDynamicRegistration)

	// Protected auth endpoints
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireSession(userStore, sessionStore))
		r.Post("/auth/logout", authHandlers.HandleLogout)
		r.Get("/auth/me", authHandlers.HandleMe)
		r.Put("/auth/password", authHandlers.HandleChangePassword)
		r.Put("/api/auth/profile", authHandlers.HandleUpdateProfile)
	})

	// MCP Streamable HTTP endpoint (requires agent auth: API key, managed key, or OAuth bearer)
	r.Group(func(r chi.Router) {
		r.Use(agents.RequiredAuthMiddlewareWithOAuth(agentService, apiKeyService, oauthProvider))
		r.Mount("/mcp", mcpSrv.Handler())
	})

	// A2A Gateway (requires auth: API key, managed key, or OAuth bearer)
	a2aTaskStore := a2a.NewA2ATaskStore(db.DB)
	a2aGateway := a2a.NewGateway(a2aTaskStore, msgService, agentService)
	r.Group(func(r chi.Router) {
		r.Use(agents.RequiredAuthMiddlewareWithOAuth(agentService, apiKeyService, oauthProvider))
		r.Post("/a2a", a2aGateway.HandleJSONRPC)
	})

	// Create SSE hub and broadcaster for real-time events
	sseHub := api.NewSSEHub()
	sseBroadcaster := api.NewSSEBroadcaster(sseHub, agentService, channelService)

	// Register broadcaster as a message listener so SSE events fire
	// for messages sent via MCP (agents) as well as the REST API.
	msgService.AddMessageListener(sseBroadcaster)

	// Initialize push notification service
	pushStore := push.NewSQLiteStore(db.DB)
	pushService, err := push.NewService(pushStore, dataDir, logger)
	if err != nil {
		logger.Warn("push notification service unavailable", "error", err)
	}

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
		DeadLetterStore:   deadLetterStore,
		ReactionService:   reactionService,
		SSEHub:            sseHub,
		Broadcaster:       sseBroadcaster,
		SessionMiddleware: sessionMiddleware,
		DB:                db.DB,
		Version:           version,
		PushService:       pushService,
		TrustService:      trustService,
		ReactorStore:      reactorStore,
		ReactorEngine:     reactorEngine,
		HarnessRunsStore:  harnessRunsStore,
		GoalsService:      goalsService,
		GoalTasksService:  goalTasksService,
		BaseURL:           baseURL,
		WikiService:       wikiService,
	})
	r.Mount("/", apiRouter)

	// Serve embedded Web UI SPA (catch-all for non-API routes)
	r.NotFound(web.NewSPAHandler().ServeHTTP)

	// Start admin socket server
	adminSvcs := &admin.Services{
		Users:             userStore,
		Sessions:          sessionStore,
		Agents:            agentService,
		Messages:          msgService,
		Channels:          channelService,
		Traces:            traceStore,
		DataDir:           dataDir,
	}
	// Wire optional services into admin (may be nil if not configured)
	if searchCfg.IsEnabled() {
		adminSvcs.EmbeddingStore = search.NewEmbeddingStore(db.DB)
		adminSvcs.VectorIndex = vectorIndex
		adminSvcs.SearchService = searchService
	}
	if attachmentService != nil {
		adminSvcs.AttachmentService = attachmentService
	}
	if retentionWorker != nil {
		adminSvcs.RetentionWorker = retentionWorker
	}
	adminSvcs.WebhookService = webhookService
	adminSvcs.K8sService = k8sService
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
		// Pretty startup banner
		con.Blank()
		con.Success(fmt.Sprintf("SynapBus listening on %s", addr))
		con.Success("MCP server ready")
		con.Success(fmt.Sprintf("Web UI at http://localhost:%d", port))
		con.Blank()
		con.Info("Waiting for agents...")
		con.Blank()
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
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	// Stop admin socket
	adminServer.Stop()

	// Stop webhook delivery engine
	deliveryEngine.Stop()

	// Stop expiry worker
	if expiryWorker != nil {
		expiryWorker.Stop()
	}

	// Stop message retention worker
	if retentionWorker != nil {
		retentionWorker.Stop()
	}

	// Stop stalemate worker
	if stalemateWorker != nil {
		stalemateWorker.Stop()
	}

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

	// Close SSE connections so HTTP server can drain
	sseHub.Close()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	slog.Info("SynapBus stopped")
	return nil
}

// withHumanAgent wraps an auth handler to ensure a human-type agent and
// my-agents channel exist after successful login/register.
func withHumanAgent(next http.HandlerFunc, userStore auth.UserStore, agentSvc *agents.AgentService, channelSvc *channels.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Buffer the request body so we can read the username and still pass it to the handler
		bodyBytes, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		// Extract username from request
		var reqBody struct {
			Username string `json:"username"`
		}
		json.Unmarshal(bodyBytes, &reqBody)

		// Wrap response writer to capture status code
		rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rec, r)

		// On successful auth, ensure human agent and my-agents channel exist
		if rec.statusCode >= 200 && rec.statusCode < 300 && reqBody.Username != "" {
			go func() {
				ctx := context.Background()
				user, err := userStore.GetUserByUsername(ctx, reqBody.Username)
				if err != nil {
					return
				}
				humanAgent, err := agentSvc.EnsureHumanAgent(ctx, user.Username, user.DisplayName, user.ID)
				if err != nil || humanAgent == nil {
					return
				}
				if err := channelSvc.EnsureMyAgentsChannel(ctx, user.Username, humanAgent.Name); err != nil {
					slog.Warn("failed to ensure my-agents channel",
						"username", user.Username,
						"error", err,
					)
				}
			}()
		}
	}
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// generateRandomPassword creates a cryptographically random password.
func generateRandomPassword() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// a2aAgentListerAdapter adapts agents.AgentService to a2a.AgentLister.
type a2aAgentListerAdapter struct {
	agentService *agents.AgentService
}

func (a *a2aAgentListerAdapter) ListAllActiveAgents(ctx context.Context) ([]a2a.AgentInfo, error) {
	agentsList, err := a.agentService.ListAllActiveAgents(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]a2a.AgentInfo, 0, len(agentsList))
	for _, agent := range agentsList {
		result = append(result, a2a.AgentInfo{
			Name:         agent.Name,
			DisplayName:  agent.DisplayName,
			Type:         agent.Type,
			Capabilities: agent.Capabilities,
		})
	}
	return result, nil
}

// attachmentLinkerAdapter adapts attachments.Service to messaging.AttachmentLinker.
type attachmentLinkerAdapter struct {
	svc *attachments.Service
}

func (a *attachmentLinkerAdapter) AttachToMessage(ctx context.Context, hash string, messageID int64) error {
	return a.svc.AttachToMessage(ctx, hash, messageID)
}

func (a *attachmentLinkerAdapter) GetByMessageID(ctx context.Context, messageID int64) ([]messaging.AttachmentInfo, error) {
	atts, err := a.svc.GetByMessageID(ctx, messageID)
	if err != nil {
		return nil, err
	}
	results := make([]messaging.AttachmentInfo, len(atts))
	for i, att := range atts {
		results[i] = messaging.AttachmentInfo{
			Hash:             att.Hash,
			OriginalFilename: att.OriginalFilename,
			Size:             att.Size,
			MIMEType:         att.MIMEType,
			IsImage:          attachments.IsImageType(att.MIMEType),
		}
	}
	return results, nil
}

// reactorReactionAdapter adapts reactions.Service to the reactor's
// ReactionNotifier interface. It wraps Toggle so the reactor only
// sees one simple AddReaction call.
type reactorReactionAdapter struct {
	svc *reactions.Service
}

func (a *reactorReactionAdapter) AddReaction(ctx context.Context, messageID int64, agentName, reactionType string) error {
	_, err := a.svc.Toggle(ctx, messageID, agentName, reactionType, nil)
	return err
}

// reactionEnricherAdapter adapts reactions.Service to messaging.ReactionEnricher.
type reactionEnricherAdapter struct {
	svc *reactions.Service
}

func (a *reactionEnricherAdapter) GetByMessageIDs(ctx context.Context, messageIDs []int64) (map[int64][]messaging.ReactionInfo, error) {
	rxMap, err := a.svc.GetReactionsByMessageIDs(ctx, messageIDs)
	if err != nil {
		return nil, err
	}
	result := make(map[int64][]messaging.ReactionInfo, len(rxMap))
	for msgID, rxs := range rxMap {
		infos := make([]messaging.ReactionInfo, len(rxs))
		for i, rx := range rxs {
			infos[i] = messaging.ReactionInfo{
				AgentName: rx.AgentName,
				Reaction:  rx.Reaction,
				Metadata:  rx.Metadata,
				CreatedAt: rx.CreatedAt,
			}
		}
		result[msgID] = infos
	}
	return result, nil
}

// agentListerAdapter adapts agents.AgentService to auth.AgentLister.
type agentListerAdapter struct {
	agentService *agents.AgentService
}

func (a *agentListerAdapter) ListAgentsByOwner(ctx context.Context, ownerID int64) ([]auth.AgentInfo, error) {
	agentsList, err := a.agentService.ListAgents(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	result := make([]auth.AgentInfo, 0, len(agentsList))
	for _, agent := range agentsList {
		if agent.Status != "active" || agent.Type == "human" {
			continue
		}
		result = append(result, auth.AgentInfo{
			Name:        agent.Name,
			DisplayName: agent.DisplayName,
			Type:        agent.Type,
		})
	}
	return result, nil
}

// idpAgentProvisioner adapts agents.AgentService + channels.Service to idp.AgentProvisioner.
type idpAgentProvisioner struct {
	agentService   *agents.AgentService
	channelService *channels.Service
}

func (a *idpAgentProvisioner) ProvisionHumanAgent(ctx context.Context, username, displayName string, ownerID int64) error {
	humanAgent, err := a.agentService.EnsureHumanAgent(ctx, username, displayName, ownerID)
	if err != nil {
		return fmt.Errorf("ensure human agent: %w", err)
	}
	if humanAgent != nil {
		if chErr := a.channelService.EnsureMyAgentsChannel(ctx, username, humanAgent.Name); chErr != nil {
			slog.Warn("failed to ensure my-agents channel after IdP login",
				"username", username,
				"error", chErr,
			)
		}
	}
	return nil
}

// ensureDefaultMCPClient creates the "mcp-default" public OAuth client if it doesn't exist.
// This client is used by MCP clients connecting via OAuth 2.1.
func ensureDefaultMCPClient(ctx context.Context, db *sql.DB, bcryptCost int) {
	const clientID = "mcp-default"
	const clientName = "MCP Default Client"

	// Check if client already exists
	var exists int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM oauth_clients WHERE id = ?", clientID).Scan(&exists)
	if err != nil {
		slog.Error("check mcp-default client failed", "error", err)
		return
	}
	if exists > 0 {
		slog.Debug("mcp-default OAuth client already exists")
		return
	}

	// Create public client (empty secret hash = public)
	redirectURIs, _ := json.Marshal([]string{"http://localhost:*"})
	grantTypes, _ := json.Marshal([]string{"authorization_code", "refresh_token"})
	scopes, _ := json.Marshal([]string{"mcp"})

	_, err = db.ExecContext(ctx,
		`INSERT INTO oauth_clients (id, secret_hash, name, redirect_uris, grant_types, scopes, owner_id, created_at)
		 VALUES (?, '', ?, ?, ?, ?, NULL, CURRENT_TIMESTAMP)`,
		clientID, clientName, string(redirectURIs), string(grantTypes), string(scopes),
	)
	if err != nil {
		slog.Error("create mcp-default OAuth client failed", "error", err)
		return
	}

	slog.Info("created default MCP OAuth client",
		"client_id", clientID,
		"public", true,
		"redirect_uris", "http://localhost:*",
		"scopes", "mcp",
	)
}

// trustAdjusterAdapter adapts trust.Service to reactions.TrustAdjuster.
type trustAdjusterAdapter struct {
	svc *trust.Service
}

func (a *trustAdjusterAdapter) RecordApproval(ctx context.Context, agentName, actionType string) error {
	_, err := a.svc.RecordApproval(ctx, agentName, actionType)
	return err
}

func (a *trustAdjusterAdapter) RecordRejection(ctx context.Context, agentName, actionType string) error {
	_, err := a.svc.RecordRejection(ctx, agentName, actionType)
	return err
}

// agentTypeCheckerAdapter adapts agents.AgentService to reactions.AgentTypeChecker.
type agentTypeCheckerAdapter struct {
	agentService *agents.AgentService
}

func (a *agentTypeCheckerAdapter) GetAgentType(ctx context.Context, agentName string) (string, error) {
	agent, err := a.agentService.GetAgent(ctx, agentName)
	if err != nil {
		return "", err
	}
	return agent.Type, nil
}

// messageAuthorResolverAdapter adapts messaging.MessagingService to reactions.MessageAuthorResolver.
type messageAuthorResolverAdapter struct {
	msgService *messaging.MessagingService
}

func (a *messageAuthorResolverAdapter) GetMessageAuthor(ctx context.Context, messageID int64) (string, error) {
	msg, err := a.msgService.GetMessageByID(ctx, messageID)
	if err != nil {
		return "", err
	}
	return msg.FromAgent, nil
}
