package messaging

import (
	"time"

	"github.com/synapbus/synapbus/internal/metrics"
)

// recordJobMetric increments the synapbus_dream_jobs_total counter for
// (owner, job_type, status). Pulled into a helper so callers don't have
// to import the metrics package directly and so test builds can stub it
// later if needed.
func recordJobMetric(ownerID, jobType, status string) {
	metrics.DreamJobsTotal.WithLabelValues(ownerID, jobType, status).Inc()
}

// recordTokensMetric adds to the synapbus_dream_tokens_total counter
// for both in and out directions. Zero deltas are no-ops.
func recordTokensMetric(ownerID string, tokensIn, tokensOut int64) {
	if tokensIn > 0 {
		metrics.DreamTokensTotal.WithLabelValues(ownerID, "in").Add(float64(tokensIn))
	}
	if tokensOut > 0 {
		metrics.DreamTokensTotal.WithLabelValues(ownerID, "out").Add(float64(tokensOut))
	}
}

// recordJobDurationMetric observes one dream-job wallclock duration in
// the synapbus_dream_job_duration_seconds histogram.
func recordJobDurationMetric(ownerID, jobType string, d time.Duration) {
	metrics.DreamJobDuration.WithLabelValues(ownerID, jobType).Observe(d.Seconds())
}

// recordCircuitBrokenMetric bumps synapbus_dream_circuit_broken_total
// for (owner, reason). Used when the UsageGate denies a dispatch.
func recordCircuitBrokenMetric(ownerID, jobType, reason string) {
	// jobType is intentionally unused as a label here — the gate
	// decision is per-owner per-day, not per-job-type. Keeping the
	// parameter in the signature so call sites stay symmetric with
	// recordJobMetric.
	_ = jobType
	metrics.DreamCircuitBrokenTotal.WithLabelValues(ownerID, reason).Inc()
}
