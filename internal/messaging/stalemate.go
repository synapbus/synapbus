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
type StalemateConfig struct {
	// ProcessingTimeout is how long a message can stay in "processing" before auto-fail (default 24h).
	ProcessingTimeout time.Duration
	// ReminderAfter is how long a pending DM waits before a system reminder is sent (default 4h).
	ReminderAfter time.Duration
	// EscalateAfter is how long a pending DM waits before escalation to #approvals (default 48h).
	EscalateAfter time.Duration
	// Interval is how often the worker checks for stale messages (default 15m).
	Interval time.Duration
}

// DefaultStalemateConfig returns the default stalemate configuration.
func DefaultStalemateConfig() StalemateConfig {
	return StalemateConfig{
		ProcessingTimeout: 24 * time.Hour,
		ReminderAfter:     4 * time.Hour,
		EscalateAfter:     48 * time.Hour,
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

	// Try "Nd" format (days)
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}

	// Try standard Go duration
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
	if v := os.Getenv("SYNAPBUS_STALEMATE_REMINDER_AFTER"); v != "" {
		if d, err := parseDurationWithDays(v); err == nil && d > 0 {
			cfg.ReminderAfter = d
		}
	}
	if v := os.Getenv("SYNAPBUS_STALEMATE_ESCALATE_AFTER"); v != "" {
		if d, err := parseDurationWithDays(v); err == nil && d > 0 {
			cfg.EscalateAfter = d
		}
	}
	if v := os.Getenv("SYNAPBUS_STALEMATE_INTERVAL"); v != "" {
		if d, err := parseDurationWithDays(v); err == nil && d > 0 {
			cfg.Interval = d
		}
	}

	return cfg
}

// ChannelLookup provides channel lookup by name without importing the channels package.
type ChannelLookup interface {
	// GetChannelIDByName returns a channel ID by name, or 0 if not found.
	GetChannelIDByName(ctx context.Context, name string) (int64, error)
}

// StalemateWorker periodically checks for and handles stale messages.
type StalemateWorker struct {
	db         *sql.DB
	msgService *MessagingService
	channelLookup ChannelLookup
	config     StalemateConfig
	logger     *slog.Logger
	done       chan struct{}
	wg         sync.WaitGroup
}

// NewStalemateWorker creates a new stalemate detection worker.
func NewStalemateWorker(db *sql.DB, msgService *MessagingService, channelLookup ChannelLookup, config StalemateConfig) *StalemateWorker {
	return &StalemateWorker{
		db:            db,
		msgService:    msgService,
		channelLookup: channelLookup,
		config:        config,
		logger:        slog.Default().With("component", "stalemate-worker"),
		done:          make(chan struct{}),
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
			"reminder_after", w.config.ReminderAfter.String(),
			"escalate_after", w.config.EscalateAfter.String(),
		)

		ticker := time.NewTicker(w.config.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				w.checkStaleMessages(ctx)
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

// checkStaleMessages runs all stalemate checks.
func (w *StalemateWorker) checkStaleMessages(ctx context.Context) {
	failed := w.failTimedOutProcessing(ctx)
	reminded := w.sendPendingReminders(ctx)
	escalated := w.escalatePendingMessages(ctx)

	if failed > 0 || reminded > 0 || escalated > 0 {
		w.logger.Info("stalemate check complete",
			"auto_failed", failed,
			"reminders_sent", reminded,
			"escalations_sent", escalated,
		)
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

		// Update directly via DB since the store's UpdateMessageStatus requires the claiming agent
		_, err := w.db.ExecContext(ctx,
			`UPDATE messages SET status = ?, metadata = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND status = 'processing'`,
			StatusFailed, string(metaBytes), dm.ID,
		)
		if err != nil {
			w.logger.Error("auto-fail message failed",
				"message_id", dm.ID,
				"error", err,
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

// sendPendingReminders sends system DM reminders for pending messages older than ReminderAfter.
func (w *StalemateWorker) sendPendingReminders(ctx context.Context) int64 {
	cutoff := time.Now().Add(-w.config.ReminderAfter)

	rows, err := w.db.QueryContext(ctx,
		`SELECT id, from_agent, to_agent, body, created_at
		 FROM messages
		 WHERE status = 'pending'
		 AND to_agent IS NOT NULL
		 AND to_agent != ''
		 AND from_agent != 'system'
		 AND to_agent != 'system'
		 AND created_at < ?`,
		cutoff,
	)
	if err != nil {
		w.logger.Error("query pending reminder candidates failed", "error", err)
		return 0
	}
	defer rows.Close()

	type pendingMsg struct {
		ID        int64
		FromAgent string
		ToAgent   string
		Body      string
		CreatedAt time.Time
	}

	var pending []pendingMsg
	for rows.Next() {
		var pm pendingMsg
		if err := rows.Scan(&pm.ID, &pm.FromAgent, &pm.ToAgent, &pm.Body, &pm.CreatedAt); err != nil {
			w.logger.Error("scan pending message failed", "error", err)
			continue
		}
		pending = append(pending, pm)
	}

	count := int64(0)
	for _, pm := range pending {
		// Check if a reminder already exists for this message
		if w.reminderExists(ctx, pm.ID, pm.ToAgent) {
			continue
		}

		age := formatAge(time.Since(pm.CreatedAt))
		truncBody := truncate(pm.Body, 100)

		body := fmt.Sprintf(
			"**Reminder**: You have a pending message from %s (%s old). Message: \"%s\". Please claim and process it.",
			pm.FromAgent, age, truncBody,
		)

		_, err := w.msgService.SendMessage(ctx, "system", pm.ToAgent, body, SendOptions{
			Subject:  fmt.Sprintf("stalemate-reminder:%d", pm.ID),
			Priority: 7,
			Metadata: fmt.Sprintf(`{"stalemate_reminder_for":%d}`, pm.ID),
		})
		if err != nil {
			w.logger.Error("send stalemate reminder failed",
				"message_id", pm.ID,
				"to_agent", pm.ToAgent,
				"error", err,
			)
			continue
		}
		w.logger.Info("sent stalemate reminder",
			"message_id", pm.ID,
			"to_agent", pm.ToAgent,
			"from_agent", pm.FromAgent,
			"age", age,
		)
		count++
	}
	return count
}

// escalatePendingMessages escalates pending messages older than EscalateAfter to #approvals.
func (w *StalemateWorker) escalatePendingMessages(ctx context.Context) int64 {
	cutoff := time.Now().Add(-w.config.EscalateAfter)

	rows, err := w.db.QueryContext(ctx,
		`SELECT id, from_agent, to_agent, body, created_at
		 FROM messages
		 WHERE status = 'pending'
		 AND to_agent IS NOT NULL
		 AND to_agent != ''
		 AND from_agent != 'system'
		 AND to_agent != 'system'
		 AND created_at < ?`,
		cutoff,
	)
	if err != nil {
		w.logger.Error("query escalation candidates failed", "error", err)
		return 0
	}
	defer rows.Close()

	type pendingMsg struct {
		ID        int64
		FromAgent string
		ToAgent   string
		Body      string
		CreatedAt time.Time
	}

	var pending []pendingMsg
	for rows.Next() {
		var pm pendingMsg
		if err := rows.Scan(&pm.ID, &pm.FromAgent, &pm.ToAgent, &pm.Body, &pm.CreatedAt); err != nil {
			w.logger.Error("scan escalation candidate failed", "error", err)
			continue
		}
		pending = append(pending, pm)
	}

	if len(pending) == 0 {
		return 0
	}

	// Look up #approvals channel
	channelID, err := w.channelLookup.GetChannelIDByName(ctx, "approvals")
	if err != nil {
		w.logger.Warn("cannot escalate: #approvals channel not found", "error", err)
		return 0
	}

	count := int64(0)
	for _, pm := range pending {
		// Check if already escalated
		if w.escalationExists(ctx, pm.ID) {
			continue
		}

		age := formatAge(time.Since(pm.CreatedAt))
		truncBody := truncate(pm.Body, 100)

		body := fmt.Sprintf(
			"**ESCALATION**: Pending message for @%s from %s has been unprocessed for %s. Message: \"%s\". Manual intervention may be required.",
			pm.ToAgent, pm.FromAgent, age, truncBody,
		)

		_, err := w.msgService.SendMessage(ctx, "system", "", body, SendOptions{
			Subject:   fmt.Sprintf("stalemate-escalation:%d", pm.ID),
			Priority:  9,
			Metadata:  fmt.Sprintf(`{"stalemate_escalation_for":%d}`, pm.ID),
			ChannelID: &channelID,
		})
		if err != nil {
			w.logger.Error("send escalation to #approvals failed",
				"message_id", pm.ID,
				"error", err,
			)
			continue
		}
		w.logger.Info("escalated stale message to #approvals",
			"message_id", pm.ID,
			"to_agent", pm.ToAgent,
			"from_agent", pm.FromAgent,
			"age", age,
		)
		count++
	}
	return count
}

// reminderExists checks if a system reminder already exists for a given message ID.
func (w *StalemateWorker) reminderExists(ctx context.Context, messageID int64, toAgent string) bool {
	var count int
	err := w.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages
		 WHERE from_agent = 'system'
		 AND to_agent = ?
		 AND metadata LIKE ?`,
		toAgent, fmt.Sprintf(`%%"stalemate_reminder_for":%d%%`, messageID),
	).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// escalationExists checks if an escalation already exists for a given message ID.
func (w *StalemateWorker) escalationExists(ctx context.Context, messageID int64) bool {
	var count int
	err := w.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages
		 WHERE from_agent = 'system'
		 AND metadata LIKE ?`,
		fmt.Sprintf(`%%"stalemate_escalation_for":%d%%`, messageID),
	).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// truncate truncates a string to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// formatAge returns a human-readable age string.
func formatAge(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	if hours < 24 {
		return fmt.Sprintf("%dh", hours)
	}
	days := hours / 24
	remainingHours := hours % 24
	if remainingHours == 0 {
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	if days == 1 {
		return fmt.Sprintf("1 day %dh", remainingHours)
	}
	return fmt.Sprintf("%d days %dh", days, remainingHours)
}
