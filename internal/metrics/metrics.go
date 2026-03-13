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
)
