package trace

import (
	"context"
	"log/slog"
	"time"
)

// RetentionCleaner periodically deletes traces older than the retention period.
type RetentionCleaner struct {
	store     TraceStore
	retention time.Duration
	interval  time.Duration
	logger    *slog.Logger
	cancel    context.CancelFunc
	done      chan struct{}
}

// NewRetentionCleaner creates a new retention cleaner.
// retention is the maximum age of traces to keep.
// interval is how often to run the cleanup (defaults to 1 hour).
func NewRetentionCleaner(store TraceStore, retention time.Duration, interval time.Duration) *RetentionCleaner {
	if interval == 0 {
		interval = 1 * time.Hour
	}
	return &RetentionCleaner{
		store:     store,
		retention: retention,
		interval:  interval,
		logger:    slog.Default().With("component", "trace-retention"),
		done:      make(chan struct{}),
	}
}

// Start begins the background retention cleanup goroutine.
func (rc *RetentionCleaner) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	rc.cancel = cancel
	go rc.loop(ctx)
}

// Stop stops the retention cleaner and waits for the goroutine to finish.
func (rc *RetentionCleaner) Stop() {
	if rc.cancel != nil {
		rc.cancel()
		<-rc.done
	}
}

func (rc *RetentionCleaner) loop(ctx context.Context) {
	defer close(rc.done)

	// Run once immediately
	rc.cleanup(ctx)

	ticker := time.NewTicker(rc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rc.cleanup(ctx)
		}
	}
}

func (rc *RetentionCleaner) cleanup(ctx context.Context) {
	cutoff := time.Now().Add(-rc.retention)
	deleted, err := rc.store.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		rc.logger.Error("trace retention cleanup failed",
			"error", err,
			"cutoff", cutoff,
		)
		return
	}
	if deleted > 0 {
		rc.logger.Info("trace retention cleanup completed",
			"deleted", deleted,
			"cutoff", cutoff,
		)
	}
}
