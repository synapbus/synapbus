package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StalemateConfig holds stalemate detection settings.
//
// The historical reminder/escalation knobs (ReminderAfter, EscalateAfter)
// were removed when SynapBus moved to internal-only mode (no human
// approval loop). See migration 027_remove_approval_noise.sql.
type StalemateConfig struct {
	// ProcessingTimeout is how long a message can stay in "processing"
	// before auto-fail (default 24h). Protects the inbox queue from
	// agents that crash after claiming a message.
	ProcessingTimeout time.Duration
	// Interval is how often the worker checks for stale messages (default 15m).
	Interval time.Duration
}

// DefaultStalemateConfig returns the default stalemate configuration.
func DefaultStalemateConfig() StalemateConfig {
	return StalemateConfig{
		ProcessingTimeout: 24 * time.Hour,
		Interval:          15 * time.Minute,
	}
}

// parseDurationWithDays parses a duration string supporting "Nd" format for days
// in addition to standard Go duration formats.
func parseDurationWithDays(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}

	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}

	return time.ParseDuration(s)
}

// ParseStalemateConfig reads stalemate configuration from environment variables.
func ParseStalemateConfig() StalemateConfig {
	cfg := DefaultStalemateConfig()

	if v := os.Getenv("SYNAPBUS_STALEMATE_PROCESSING_TIMEOUT"); v != "" {
		if d, err := parseDurationWithDays(v); err == nil && d > 0 {
			cfg.ProcessingTimeout = d
		}
	}
	if v := os.Getenv("SYNAPBUS_STALEMATE_INTERVAL"); v != "" {
		if d, err := parseDurationWithDays(v); err == nil && d > 0 {
			cfg.Interval = d
		}
	}

	return cfg
}

// StalemateWorker periodically auto-fails messages whose claim has timed out.
type StalemateWorker struct {
	db         *sql.DB
	msgService *MessagingService
	config     StalemateConfig
	logger     *slog.Logger
	done       chan struct{}
	wg         sync.WaitGroup

	// memoryInjections, when non-nil, drives an hourly cleanup of the
	// 24h proactive-injection audit ring (feature 020). Plumbed via
	// SetMemoryInjections after construction so adding the feature is
	// non-breaking for existing call sites.
	memoryInjections *MemoryInjections
	tickCount        int
}

// SetMemoryInjections registers the audit-ring store the worker will
// cleanup hourly. Pass nil to disable; safe to call before Start.
func (w *StalemateWorker) SetMemoryInjections(store *MemoryInjections) {
	w.memoryInjections = store
}

// NewStalemateWorker creates a new stalemate detection worker.
func NewStalemateWorker(db *sql.DB, msgService *MessagingService, config StalemateConfig) *StalemateWorker {
	return &StalemateWorker{
		db:         db,
		msgService: msgService,
		config:     config,
		logger:     slog.Default().With("component", "stalemate-worker"),
		done:       make(chan struct{}),
	}
}

// Start begins the background stalemate check loop.
func (w *StalemateWorker) Start() {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.logger.Info("stalemate worker started",
			"interval", w.config.Interval.String(),
			"processing_timeout", w.config.ProcessingTimeout.String(),
		)

		ticker := time.NewTicker(w.config.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				w.checkStaleMessages(ctx)
				w.maybeCleanupInjections(ctx)
				cancel()
			case <-w.done:
				w.logger.Info("stalemate worker stopped")
				return
			}
		}
	}()
}

// Stop stops the stalemate worker and waits for it to finish.
func (w *StalemateWorker) Stop() {
	close(w.done)
	w.wg.Wait()
}

// checkStaleMessages runs the auto-fail check for timed-out claimed messages.
func (w *StalemateWorker) checkStaleMessages(ctx context.Context) {
	if failed := w.failTimedOutProcessing(ctx); failed > 0 {
		w.logger.Info("stalemate check complete", "auto_failed", failed)
	}
}

// maybeCleanupInjections piggybacks an hourly cleanup of the 24h
// proactive-injection audit ring (feature 020) on the stalemate worker
// tick. With the default 15-minute Interval, the cleanup fires every
// 4th tick (i.e. ~1h). A no-op when SetMemoryInjections has not been
// called or the store is nil.
func (w *StalemateWorker) maybeCleanupInjections(ctx context.Context) {
	if w.memoryInjections == nil {
		return
	}
	w.tickCount++
	// Fire roughly hourly. With Interval=15m the modulus matches the
	// 4-tick cadence called out in spec 020 T017. For non-default
	// intervals it still fires hourly-ish on a best-effort basis.
	ticksPerHour := int(time.Hour / w.config.Interval)
	if ticksPerHour <= 0 {
		ticksPerHour = 1
	}
	if w.tickCount%ticksPerHour != 0 {
		return
	}
	deleted, err := w.memoryInjections.Cleanup(ctx, 24*time.Hour)
	if err != nil {
		w.logger.Warn("memory_injections cleanup failed", "error", err)
		return
	}
	if deleted > 0 {
		w.logger.Info("memory_injections cleanup", "deleted", deleted)
	}
}

// staleDM represents a stale direct message found by the worker.
type staleDM struct {
	ID        int64
	FromAgent string
	ToAgent   string
	Body      string
	ClaimedAt *time.Time
	ClaimedBy string
	CreatedAt time.Time
}

// failTimedOutProcessing auto-fails DMs in "processing" status that have exceeded the timeout.
func (w *StalemateWorker) failTimedOutProcessing(ctx context.Context) int64 {
	cutoff := time.Now().Add(-w.config.ProcessingTimeout)

	rows, err := w.db.QueryContext(ctx,
		`SELECT id, from_agent, to_agent, body, claimed_at, claimed_by
		 FROM messages
		 WHERE status = 'processing'
		 AND to_agent IS NOT NULL
		 AND to_agent != ''
		 AND to_agent != 'system'
		 AND claimed_at < ?`,
		cutoff,
	)
	if err != nil {
		w.logger.Error("query timed-out processing messages failed", "error", err)
		return 0
	}
	defer rows.Close()

	var stale []staleDM
	for rows.Next() {
		var dm staleDM
		var claimedAt sql.NullTime
		var claimedBy sql.NullString
		if err := rows.Scan(&dm.ID, &dm.FromAgent, &dm.ToAgent, &dm.Body, &claimedAt, &claimedBy); err != nil {
			w.logger.Error("scan timed-out message failed", "error", err)
			continue
		}
		if claimedAt.Valid {
			dm.ClaimedAt = &claimedAt.Time
		}
		if claimedBy.Valid {
			dm.ClaimedBy = claimedBy.String
		}
		stale = append(stale, dm)
	}

	count := int64(0)
	for _, dm := range stale {
		metadata := map[string]any{"error": "claim timeout exceeded"}
		metaBytes, _ := json.Marshal(metadata)

		// Update directly via DB since the store's UpdateMessageStatus requires
		// the claiming agent. The UPDATE re-checks both status='processing' AND
		// claimed_at < cutoff to close a TOCTOU window between the SELECT above
		// and this UPDATE: if a legitimate claimer ran mark_done (status flip)
		// or a fresh claim_messages refreshed claimed_at between the two, this
		// no-ops instead of stomping live work. RowsAffected = 0 is the signal
		// the row escaped the stale window before the worker reached it.
		res, err := w.db.ExecContext(ctx,
			`UPDATE messages SET status = ?, metadata = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND status = 'processing' AND claimed_at < ?`,
			StatusFailed, string(metaBytes), dm.ID, cutoff,
		)
		if err != nil {
			w.logger.Error("auto-fail message failed",
				"message_id", dm.ID,
				"error", err,
			)
			continue
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			w.logger.Debug("stale processing message no longer stale; skipped",
				"message_id", dm.ID,
				"claimed_by", dm.ClaimedBy,
			)
			continue
		}
		w.logger.Info("auto-failed stale processing message",
			"message_id", dm.ID,
			"from_agent", dm.FromAgent,
			"to_agent", dm.ToAgent,
			"claimed_by", dm.ClaimedBy,
		)
		count++
	}
	return count
}
