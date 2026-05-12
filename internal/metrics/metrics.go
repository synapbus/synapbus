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

	// Dream worker metrics (feature 020 follow-up).
	DreamJobsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "synapbus",
			Name:      "dream_jobs_total",
			Help:      "Dream worker jobs dispatched, labeled by owner, job_type and final status",
		},
		[]string{"owner", "job_type", "status"},
	)

	DreamTokensTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "synapbus",
			Name:      "dream_tokens_total",
			Help:      "Total tokens consumed by dream worker runs, labeled by owner and direction (in|out)",
		},
		[]string{"owner", "direction"},
	)

	DreamJobDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "synapbus",
			Name:      "dream_job_duration_seconds",
			Help:      "Wallclock duration of a single dream worker job",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"owner", "job_type"},
	)

	DreamCircuitBrokenTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "synapbus",
			Name:      "dream_circuit_broken_total",
			Help:      "Times the dream worker refused to dispatch because the daily usage gate fired",
		},
		[]string{"owner", "reason"},
	)

	// Proactive-injection metrics (feature 020 follow-up).
	InjectionPacketsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "synapbus",
			Name:      "injection_packets_total",
			Help:      "Number of relevant_context packets attached to MCP tool responses, by tool",
		},
		[]string{"tool"},
	)

	InjectionMemoriesPerPacket = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "synapbus",
			Name:      "injection_memories_per_packet",
			Help:      "Distribution of memory items per injection packet, by tool",
			Buckets:   []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		},
		[]string{"tool"},
	)

	InjectionPacketChars = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "synapbus",
			Name:      "injection_packet_chars",
			Help:      "Distribution of character size of an injection packet, by tool",
			Buckets:   []float64{0, 250, 500, 750, 1000, 1250, 1500, 1750, 2000, 2250, 2500},
		},
		[]string{"tool"},
	)

	InjectionSkippedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "synapbus",
			Name:      "injection_skipped_total",
			Help:      "Times injection was skipped, labeled by tool and reason (no_owner|empty_pool|disabled)",
		},
		[]string{"tool", "reason"},
	)
)
