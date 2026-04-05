package api

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/apikeys"
	"github.com/synapbus/synapbus/internal/attachments"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/k8s"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/reactor"
	"github.com/synapbus/synapbus/internal/push"
	"github.com/synapbus/synapbus/internal/reactions"
	"github.com/synapbus/synapbus/internal/trace"
	"github.com/synapbus/synapbus/internal/trust"
	"github.com/synapbus/synapbus/internal/webhooks"
	"github.com/synapbus/synapbus/internal/wiki"
)

// RouterConfig holds optional services for the API router.
// Fields may be nil if the corresponding feature is not configured.
type RouterConfig struct {
	TraceStore        trace.TraceStore
	Metrics           *trace.Metrics
	AttachmentService *attachments.Service
	MsgService        *messaging.MessagingService
	AgentService      *agents.AgentService
	ChannelService    *channels.Service
	APIKeyService     *apikeys.Service
	DeadLetterStore   *messaging.DeadLetterStore
	WebhookService    *webhooks.WebhookService
	WebhookStore      webhooks.WebhookStore
	K8sService        *k8s.K8sService
	K8sStore          k8s.K8sStore
	ReactionService   *reactions.Service
	PushService       *push.Service
	TrustService      *trust.Service
	ReactorStore      *reactor.Store
	ReactorEngine     *reactor.Reactor
	WikiService       *wiki.Service
	SSEHub            *SSEHub
	Broadcaster       *SSEBroadcaster
	SessionMiddleware func(http.Handler) http.Handler
	DB                *sql.DB
	Version           string
	BaseURL           string
}

// NewRouter creates a chi router with all API routes configured.
// metricsInstance may be nil if metrics are disabled.
// attachmentService may be nil if attachments are not configured.
func NewRouter(traceStore trace.TraceStore, metricsInstance *trace.Metrics, attachmentService *attachments.Service) chi.Router {
	return NewRouterWithConfig(RouterConfig{
		TraceStore:        traceStore,
		Metrics:           metricsInstance,
		AttachmentService: attachmentService,
	})
}

// NewRouterWithConfig creates a chi router using the full configuration.
func NewRouterWithConfig(cfg RouterConfig) chi.Router {
	r := chi.NewRouter()

	// Global middleware
	r.Use(RequestIDMiddleware)
	r.Use(LoggingMiddleware)

	tracesHandler := NewTracesHandler(cfg.TraceStore)

	// Determine which auth middleware to use for API routes
	authMiddleware := OwnerAuthMiddleware
	if cfg.SessionMiddleware != nil {
		authMiddleware = cfg.SessionMiddleware
	}

	// Authenticated API routes (traces)
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)

		r.Get("/api/traces", tracesHandler.ListTraces)
		r.Get("/api/traces/export", tracesHandler.ExportTraces)
		r.Get("/api/traces/stats", tracesHandler.TraceStats)
	})

	// Attachment API routes (for Web UI)
	if cfg.AttachmentService != nil {
		attachmentsHandler := NewAttachmentsHandler(cfg.AttachmentService)
		r.Get("/api/attachments/{hash}", attachmentsHandler.Download)
		r.Get("/api/attachments/{hash}/meta", attachmentsHandler.Metadata)
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Post("/api/attachments", attachmentsHandler.Upload)
		})
	}

	// Web UI API routes (messages, agents, channels, SSE)
	if cfg.MsgService != nil && cfg.AgentService != nil {
		messagesHandler := NewMessagesHandler(cfg.MsgService, cfg.AgentService)
		agentsHandler := NewAgentsHandler(cfg.AgentService, cfg.TraceStore, cfg.ChannelService)
		notificationsHandler := NewNotificationsHandler(cfg.MsgService, cfg.AgentService, cfg.ChannelService)

		// Wire up SSE broadcaster for real-time events
		if cfg.Broadcaster != nil {
			messagesHandler.SetBroadcaster(cfg.Broadcaster)
		} else if cfg.SSEHub != nil {
			messagesHandler.SetBroadcaster(NewSSEBroadcaster(cfg.SSEHub, cfg.AgentService, cfg.ChannelService))
		}

		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)

			// Messages
			r.Get("/api/messages", messagesHandler.ListMessages)
			r.Get("/api/messages/search", messagesHandler.SearchMessages)
			r.Get("/api/messages/{id}", messagesHandler.GetMessage)
			r.Get("/api/messages/{id}/replies", messagesHandler.GetReplies)
			r.Post("/api/messages", messagesHandler.SendMessage)
			r.Post("/api/messages/{id}/done", messagesHandler.MarkDone)

			// Conversations
			r.Get("/api/conversations", messagesHandler.ListConversations)
			r.Get("/api/conversations/{id}", messagesHandler.GetConversation)

			// Agents
			r.Get("/api/agents", agentsHandler.ListAgents)
			r.Get("/api/agents/{name}", agentsHandler.GetAgent)
			r.Post("/api/agents", agentsHandler.RegisterAgent)
			r.Put("/api/agents/{name}", agentsHandler.UpdateAgent)
			r.Delete("/api/agents/{name}", agentsHandler.DeleteAgent)
			r.Post("/api/agents/{name}/revoke-key", agentsHandler.RevokeKey)
			r.Get("/api/agents/{name}/messages", messagesHandler.DMMessages)
			r.Get("/api/dm/partners", messagesHandler.DMPartners)

			// Notifications
			r.Get("/api/notifications/unread", notificationsHandler.UnreadCounts)
			r.Post("/api/notifications/mark-read", notificationsHandler.MarkRead)
		})

		// API Keys
		if cfg.APIKeyService != nil {
			apiKeysHandler := NewAPIKeysHandler(cfg.APIKeyService)
			r.Group(func(r chi.Router) {
				r.Use(authMiddleware)

				r.Get("/api/keys", apiKeysHandler.ListKeys)
				r.Post("/api/keys", apiKeysHandler.CreateKey)
				r.Get("/api/keys/{id}", apiKeysHandler.GetKey)
				r.Delete("/api/keys/{id}", apiKeysHandler.RevokeKey)
			})
		}

		// Reactions
		if cfg.ReactionService != nil {
			reactionsHandler := NewReactionsHandler(cfg.ReactionService, cfg.MsgService, cfg.AgentService)
			r.Group(func(r chi.Router) {
				r.Use(authMiddleware)

				r.Post("/api/messages/{id}/reactions", reactionsHandler.Toggle)
				r.Get("/api/messages/{id}/reactions", reactionsHandler.GetReactions)
				r.Delete("/api/messages/{id}/reactions/{reaction}", reactionsHandler.Remove)
			})
		}

		// Channels
		if cfg.ChannelService != nil {
			channelsHandler := NewChannelsHandler(cfg.ChannelService, cfg.AgentService, cfg.MsgService)
			if cfg.ReactionService != nil {
				channelsHandler.SetReactionService(cfg.ReactionService)
			}
			r.Group(func(r chi.Router) {
				r.Use(authMiddleware)

				r.Get("/api/channels", channelsHandler.ListChannels)
				r.Get("/api/channels/{name}", channelsHandler.GetChannel)
				r.Post("/api/channels", channelsHandler.CreateChannel)
				r.Get("/api/channels/{name}/messages", channelsHandler.ChannelMessages)
				r.Get("/api/channels/{name}/messages/by-state", channelsHandler.ListByState)
				r.Put("/api/channels/{name}/settings", channelsHandler.UpdateSettings)
				r.Post("/api/channels/{name}/join", channelsHandler.JoinChannel)
				r.Post("/api/channels/{name}/leave", channelsHandler.LeaveChannel)
			})
		}

		// SSE events
		if cfg.SSEHub != nil {
			r.Group(func(r chi.Router) {
				r.Use(authMiddleware)
				r.Get("/api/events", cfg.SSEHub.HandleEvents)
			})
		}

		// Dead Letters
		if cfg.DeadLetterStore != nil {
			deadLettersHandler := NewDeadLettersHandler(cfg.DeadLetterStore)
			r.Group(func(r chi.Router) {
				r.Use(authMiddleware)

				r.Get("/api/dead-letters", deadLettersHandler.List)
				r.Get("/api/dead-letters/count", deadLettersHandler.Count)
				r.Post("/api/dead-letters/{id}/acknowledge", deadLettersHandler.Acknowledge)
			})
		}

		// Webhooks
		if cfg.WebhookService != nil && cfg.WebhookStore != nil {
			webhooksHandler := NewWebhooksHandler(cfg.WebhookService, cfg.WebhookStore, cfg.AgentService)
			r.Group(func(r chi.Router) {
				r.Use(authMiddleware)

				r.Get("/api/webhooks", webhooksHandler.ListWebhooks)
				r.Get("/api/webhooks/{id}/deliveries", webhooksHandler.WebhookDeliveries)
				r.Get("/api/deliveries/dead-letters", webhooksHandler.DeadLetters)
				r.Post("/api/deliveries/{id}/retry", webhooksHandler.RetryDelivery)
			})
		}

		// K8s Handlers
		if cfg.K8sService != nil && cfg.K8sStore != nil {
			k8sHandler := NewK8sHandler(cfg.K8sService, cfg.K8sStore, cfg.AgentService)
			r.Group(func(r chi.Router) {
				r.Use(authMiddleware)

				r.Get("/api/k8s/handlers", k8sHandler.ListHandlers)
				r.Get("/api/k8s/job-runs", k8sHandler.ListJobRuns)
				r.Get("/api/k8s/job-runs/{id}/logs", k8sHandler.JobRunLogs)
			})
		}

		// Push Notifications
		if cfg.PushService != nil {
			pushHandler := NewPushHandler(cfg.PushService)
			// VAPID key endpoint is unauthenticated (needed before subscription)
			r.Get("/api/push/vapid-key", pushHandler.VAPIDKey)
			r.Group(func(r chi.Router) {
				r.Use(authMiddleware)

				r.Post("/api/push/subscribe", pushHandler.Subscribe)
				r.Delete("/api/push/subscribe", pushHandler.Unsubscribe)
			})
		}
	}

	// Reactive Runs
	if cfg.ReactorStore != nil && cfg.ReactorEngine != nil && cfg.AgentService != nil {
		runsHandler := NewRunsHandler(cfg.ReactorStore, cfg.ReactorEngine, agents.NewSQLiteAgentStore(cfg.DB))
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)

			r.Get("/api/runs", runsHandler.ListRuns)
			r.Get("/api/runs/{id}", runsHandler.GetRun)
			r.Post("/api/runs/{id}/retry", runsHandler.RetryRun)
			r.Get("/api/agents/reactive", runsHandler.ReactiveAgents)
		})
	}

	// Trust Scores
	if cfg.TrustService != nil {
		trustHandler := NewTrustHandler(cfg.TrustService)
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)

			r.Get("/api/trust/{name}", trustHandler.GetScores)
		})
	}

	// Wiki
	if cfg.WikiService != nil {
		wikiHandler := NewWikiHandler(cfg.WikiService)
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Get("/api/wiki/articles", wikiHandler.ListArticles)
			r.Get("/api/wiki/articles/{slug}", wikiHandler.GetArticle)
			r.Get("/api/wiki/articles/{slug}/history", wikiHandler.GetHistory)
			r.Get("/api/wiki/map", wikiHandler.GetMap)
		})
	}

	// Onboarding (CLAUDE.md generator, MCP config, archetypes, skills)
	if cfg.AgentService != nil {
		onboardingHandler := NewOnboardingHandler(cfg.AgentService, cfg.ChannelService, cfg.BaseURL)

		// Unauthenticated: archetypes list, skills list, skill content
		r.Get("/api/archetypes", onboardingHandler.ListArchetypes)
		r.Get("/api/skills", onboardingHandler.ListSkills)
		r.Get("/api/skills/{name}", onboardingHandler.GetSkill)

		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)

			r.Get("/api/agents/{name}/claude-md", onboardingHandler.GetCLAUDEMD)
			r.Get("/api/agents/{name}/mcp-config", onboardingHandler.GetMCPConfig)
		})
	}

	// Analytics (authenticated, requires DB)
	if cfg.DB != nil {
		analyticsHandler := NewAnalyticsHandler(cfg.DB, cfg.AgentService, cfg.ChannelService)
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)

			r.Get("/api/analytics/timeline", analyticsHandler.Timeline)
			r.Get("/api/analytics/top-agents", analyticsHandler.TopAgents)
			r.Get("/api/analytics/top-channels", analyticsHandler.TopChannels)
			r.Get("/api/analytics/summary", analyticsHandler.Summary)
		})
	}

	// Version (unauthenticated)
	if cfg.Version != "" {
		versionHandler := NewVersionHandler(cfg.Version)
		r.Get("/api/version", versionHandler.GetVersion)
	}

	// Metrics endpoint (unauthenticated, only registered when enabled)
	if cfg.Metrics != nil {
		r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			cfg.Metrics.WritePrometheus(w)
		})
	}

	return r
}
