package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/apikeys"
	"github.com/synapbus/synapbus/internal/attachments"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/trace"
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
	SSEHub            *SSEHub
	SessionMiddleware func(http.Handler) http.Handler
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
		agentsHandler := NewAgentsHandler(cfg.AgentService, cfg.TraceStore)

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
			r.Delete("/api/agents/{name}", agentsHandler.DeleteAgent)
			r.Post("/api/agents/{name}/revoke-key", agentsHandler.RevokeKey)
			r.Get("/api/agents/{name}/messages", messagesHandler.DMMessages)
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

		// Channels
		if cfg.ChannelService != nil {
			channelsHandler := NewChannelsHandler(cfg.ChannelService, cfg.AgentService, cfg.MsgService)
			r.Group(func(r chi.Router) {
				r.Use(authMiddleware)

				r.Get("/api/channels", channelsHandler.ListChannels)
				r.Get("/api/channels/{name}", channelsHandler.GetChannel)
				r.Post("/api/channels", channelsHandler.CreateChannel)
				r.Get("/api/channels/{name}/messages", channelsHandler.ChannelMessages)
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
