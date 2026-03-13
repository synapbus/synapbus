package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

// NewRouter creates a chi router with all API routes configured.
// metricsInstance may be nil if metrics are disabled.
func NewRouter(traceStore trace.TraceStore, metricsInstance *trace.Metrics) chi.Router {
	r := chi.NewRouter()

	// Global middleware
	r.Use(RequestIDMiddleware)
	r.Use(LoggingMiddleware)

	tracesHandler := NewTracesHandler(traceStore)

	// Authenticated API routes
	r.Group(func(r chi.Router) {
		r.Use(OwnerAuthMiddleware)

		r.Get("/api/traces", tracesHandler.ListTraces)
		r.Get("/api/traces/export", tracesHandler.ExportTraces)
		r.Get("/api/traces/stats", tracesHandler.TraceStats)
	})

	// Metrics endpoint (unauthenticated, only registered when enabled)
	if metricsInstance != nil {
		r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			metricsInstance.WritePrometheus(w)
		})
	}

	return r
}
