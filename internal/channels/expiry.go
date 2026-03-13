package channels

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ExpiryWorker periodically checks for and cancels expired tasks.
type ExpiryWorker struct {
	swarmService *SwarmService
	interval     time.Duration
	logger       *slog.Logger
	done         chan struct{}
	wg           sync.WaitGroup
}

// NewExpiryWorker creates a new expiry worker.
// The interval controls how often it checks for expired tasks (default: 1 minute).
func NewExpiryWorker(swarmService *SwarmService, interval time.Duration) *ExpiryWorker {
	if interval <= 0 {
		interval = 1 * time.Minute
	}
	return &ExpiryWorker{
		swarmService: swarmService,
		interval:     interval,
		logger:       slog.Default().With("component", "expiry-worker"),
		done:         make(chan struct{}),
	}
}

// Start begins the background expiry check loop.
func (w *ExpiryWorker) Start() {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.logger.Info("expiry worker started", "interval", w.interval.String())

		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				count, err := w.swarmService.ExpireTasks(ctx)
				if err != nil {
					w.logger.Error("expiry check failed", "error", err)
				} else if count > 0 {
					w.logger.Info("expired tasks processed", "count", count)
				}
				cancel()
			case <-w.done:
				w.logger.Info("expiry worker stopped")
				return
			}
		}
	}()
}

// Stop stops the expiry worker and waits for it to finish.
func (w *ExpiryWorker) Stop() {
	close(w.done)
	w.wg.Wait()
}
