package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "synapbus",
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "synapbus",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request duration in seconds",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	MessagesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "synapbus",
		Name:      "messages_total",
		Help:      "Total number of messages sent",
	})

	AgentsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "synapbus",
		Name:      "agents_active",
		Help:      "Number of active registered agents",
	})

	ActiveConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "synapbus",
		Name:      "active_connections",
		Help:      "Number of active connections",
	})

	// Reactive agent triggering metrics
	ReactiveTriggersTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "synapbus",
			Subsystem: "reactor",
			Name:      "triggers_total",
			Help:      "Total reactive trigger evaluations by agent and outcome",
		},
		[]string{"agent", "status"},
	)

	ReactiveRunDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "synapbus",
			Subsystem: "reactor",
			Name:      "run_duration_seconds",
			Help:      "Duration of reactive agent runs in seconds",
			Buckets:   []float64{10, 30, 60, 120, 300, 600, 1200, 1800, 3600},
		},
		[]string{"agent"},
	)

	ReactiveAgentState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "synapbus",
			Subsystem: "reactor",
			Name:      "agent_running",
			Help:      "Whether a reactive agent is currently running (1) or idle (0)",
		},
		[]string{"agent"},
	)

	ReactiveBudgetUsed = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "synapbus",
			Subsystem: "reactor",
			Name:      "budget_used_today",
			Help:      "Number of reactive runs used today per agent",
		},
		[]string{"agent"},
	)
)
