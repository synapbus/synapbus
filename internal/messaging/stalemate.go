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

	// Phase 2: Workflow stalemate checks for channel messages
	wfReminded, wfEscalated := w.checkWorkflowStalemates(ctx)

	if failed > 0 || reminded > 0 || escalated > 0 || wfReminded > 0 || wfEscalated > 0 {
		w.logger.Info("stalemate check complete",
			"auto_failed", failed,
			"reminders_sent", reminded,
			"escalations_sent", escalated,
			"workflow_reminders", wfReminded,
			"workflow_escalations", wfEscalated,
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
			// Message was reclaimed, completed, or otherwise moved out of the
			// stale window between SELECT and UPDATE. Skip silently — the
			// next worker tick will re-evaluate.
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

// workflowChannel holds channel info relevant to workflow stalemate checking.
type workflowChannel struct {
	ID                     int64
	Name                   string
	StalemateRemindAfter   string
	StalemateEscalateAfter string
}

// staleWorkflowMsg holds info about a channel message in a stale workflow state.
type staleWorkflowMsg struct {
	ID        int64
	Body      string
	FromAgent string
	ChannelID int64
	Channel   string
	State     string
	StateAge  time.Duration
}

// checkWorkflowStalemates scans workflow-enabled channels for messages stuck in
// non-terminal workflow states (proposed, approved, in_progress) and sends
// reminders to channel members or escalates to #approvals.
func (w *StalemateWorker) checkWorkflowStalemates(ctx context.Context) (reminded int64, escalated int64) {
	// Step 1: Find all workflow-enabled channels
	channels, err := w.listWorkflowChannels(ctx)
	if err != nil {
		w.logger.Error("list workflow channels failed", "error", err)
		return 0, 0
	}
	if len(channels) == 0 {
		return 0, 0
	}

	for _, ch := range channels {
		remindTimeout, err := parseDurationWithDays(ch.StalemateRemindAfter)
		if err != nil || remindTimeout <= 0 {
			remindTimeout = 24 * time.Hour // default
		}
		escalateTimeout, err := parseDurationWithDays(ch.StalemateEscalateAfter)
		if err != nil || escalateTimeout <= 0 {
			escalateTimeout = 72 * time.Hour // default
		}

		// Step 2: Find messages in non-terminal workflow states
		staleMessages, err := w.findStaleWorkflowMessages(ctx, ch)
		if err != nil {
			w.logger.Error("find stale workflow messages failed",
				"channel", ch.Name,
				"error", err,
			)
			continue
		}

		for _, msg := range staleMessages {
			// Step 3: Check escalation first (longer timeout)
			if msg.StateAge >= escalateTimeout {
				if w.workflowEscalationExists(ctx, msg.ID) {
					continue
				}
				if w.sendWorkflowEscalation(ctx, msg) {
					escalated++
				}
				continue
			}

			// Step 4: Check reminder (shorter timeout)
			if msg.StateAge >= remindTimeout {
				if w.workflowReminderExists(ctx, msg.ID) {
					continue
				}
				r := w.sendWorkflowReminders(ctx, msg, ch.ID)
				reminded += r
			}
		}
	}

	return reminded, escalated
}

// listWorkflowChannels returns all channels that have workflow_enabled = true.
func (w *StalemateWorker) listWorkflowChannels(ctx context.Context) ([]workflowChannel, error) {
	rows, err := w.db.QueryContext(ctx,
		`SELECT id, name, stalemate_remind_after, stalemate_escalate_after
		 FROM channels
		 WHERE workflow_enabled = 1`)
	if err != nil {
		return nil, fmt.Errorf("query workflow channels: %w", err)
	}
	defer rows.Close()

	var channels []workflowChannel
	for rows.Next() {
		var ch workflowChannel
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.StalemateRemindAfter, &ch.StalemateEscalateAfter); err != nil {
			return nil, fmt.Errorf("scan workflow channel: %w", err)
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// findStaleWorkflowMessages finds channel messages in non-terminal workflow states
// and computes how long they have been in their current state.
func (w *StalemateWorker) findStaleWorkflowMessages(ctx context.Context, ch workflowChannel) ([]staleWorkflowMsg, error) {
	// Get all messages in this channel that could be in a workflow state.
	// We fetch messages and their reactions, then compute state in Go.
	rows, err := w.db.QueryContext(ctx,
		`SELECT m.id, m.body, m.from_agent, m.created_at
		 FROM messages m
		 WHERE m.channel_id = ?
		 AND m.from_agent != 'system'
		 ORDER BY m.created_at ASC`,
		ch.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("query channel messages: %w", err)
	}
	defer rows.Close()

	type chanMsg struct {
		ID        int64
		Body      string
		FromAgent string
		CreatedAt time.Time
	}

	var msgs []chanMsg
	for rows.Next() {
		var m chanMsg
		if err := rows.Scan(&m.ID, &m.Body, &m.FromAgent, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan channel message: %w", err)
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(msgs) == 0 {
		return nil, nil
	}

	// Batch-fetch reactions for all messages
	msgIDs := make([]int64, len(msgs))
	for i, m := range msgs {
		msgIDs[i] = m.ID
	}

	reactionsMap, err := w.getReactionsByMessageIDs(ctx, msgIDs)
	if err != nil {
		return nil, fmt.Errorf("get reactions: %w", err)
	}

	now := time.Now()
	var stale []staleWorkflowMsg
	for _, m := range msgs {
		reactions := reactionsMap[m.ID]
		state := computeWorkflowStateFromReactions(reactions)

		// Skip terminal states
		if isTerminalWorkflowState(state) {
			continue
		}

		// Determine the "state age": how long since the state was entered.
		// If reactions exist, use the most recent reaction's created_at.
		// If no reactions (proposed state), use the message's created_at.
		stateEnteredAt := m.CreatedAt
		if len(reactions) > 0 {
			// Find the most recent reaction
			for _, r := range reactions {
				if r.CreatedAt.After(stateEnteredAt) {
					stateEnteredAt = r.CreatedAt
				}
			}
		}

		stale = append(stale, staleWorkflowMsg{
			ID:        m.ID,
			Body:      m.Body,
			FromAgent: m.FromAgent,
			ChannelID: ch.ID,
			Channel:   ch.Name,
			State:     state,
			StateAge:  now.Sub(stateEnteredAt),
		})
	}

	return stale, nil
}

// reactionRow holds a raw reaction row for workflow state computation.
type reactionRow struct {
	Reaction  string
	CreatedAt time.Time
}

// getReactionsByMessageIDs fetches reactions for a batch of message IDs.
func (w *StalemateWorker) getReactionsByMessageIDs(ctx context.Context, messageIDs []int64) (map[int64][]reactionRow, error) {
	if len(messageIDs) == 0 {
		return map[int64][]reactionRow{}, nil
	}

	placeholders := make([]string, len(messageIDs))
	args := make([]any, len(messageIDs))
	for i, id := range messageIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT message_id, reaction, created_at
		 FROM message_reactions
		 WHERE message_id IN (%s)
		 ORDER BY created_at ASC`,
		strings.Join(placeholders, ","),
	)

	rows, err := w.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query reactions: %w", err)
	}
	defer rows.Close()

	result := make(map[int64][]reactionRow)
	for rows.Next() {
		var msgID int64
		var r reactionRow
		if err := rows.Scan(&msgID, &r.Reaction, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan reaction: %w", err)
		}
		result[msgID] = append(result[msgID], r)
	}
	return result, rows.Err()
}

// computeWorkflowStateFromReactions derives workflow state from raw reaction rows.
// Mirrors the logic in reactions.ComputeWorkflowState without importing that package.
func computeWorkflowStateFromReactions(reactions []reactionRow) string {
	if len(reactions) == 0 {
		return "proposed"
	}

	// Reaction priority (same as reactions.reactionPriority)
	priority := map[string]int{
		"approve":     2,
		"in_progress": 3,
		"reject":      4,
		"done":        5,
		"published":   6,
	}

	// Reaction-to-state mapping (same as reactions.reactionToState)
	toState := map[string]string{
		"approve":     "approved",
		"reject":      "rejected",
		"in_progress": "in_progress",
		"done":        "done",
		"published":   "published",
	}

	highestPriority := 0
	highestState := "proposed"

	for _, r := range reactions {
		if p, ok := priority[r.Reaction]; ok && p > highestPriority {
			highestPriority = p
			highestState = toState[r.Reaction]
		}
	}

	return highestState
}

// isTerminalWorkflowState returns true if the state should not trigger stalemate checks.
func isTerminalWorkflowState(state string) bool {
	switch state {
	case "rejected", "done", "published":
		return true
	default:
		return false
	}
}

// sendWorkflowReminders sends DMs to channel members about a stale workflow message.
func (w *StalemateWorker) sendWorkflowReminders(ctx context.Context, msg staleWorkflowMsg, channelID int64) int64 {
	// Get channel members
	rows, err := w.db.QueryContext(ctx,
		`SELECT agent_name FROM channel_members WHERE channel_id = ?`,
		channelID,
	)
	if err != nil {
		w.logger.Error("query channel members for workflow reminder failed",
			"channel_id", channelID,
			"error", err,
		)
		return 0
	}
	defer rows.Close()

	var members []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		members = append(members, name)
	}

	age := formatAge(msg.StateAge)
	truncBody := truncate(msg.Body, 100)
	count := int64(0)

	for _, member := range members {
		body := fmt.Sprintf(
			"**STALE**: Message #%d in #%s in '%s' for %s. \"%s\" — @%s",
			msg.ID, msg.Channel, msg.State, age, truncBody, msg.FromAgent,
		)

		_, err := w.msgService.SendMessage(ctx, "system", member, body, SendOptions{
			Subject:  fmt.Sprintf("workflow-stalemate-reminder:%d", msg.ID),
			Priority: 7,
			Metadata: fmt.Sprintf(`{"workflow_stalemate_reminder_for":%d}`, msg.ID),
		})
		if err != nil {
			w.logger.Error("send workflow stalemate reminder failed",
				"message_id", msg.ID,
				"to_agent", member,
				"error", err,
			)
			continue
		}
		w.logger.Info("sent workflow stalemate reminder",
			"message_id", msg.ID,
			"channel", msg.Channel,
			"state", msg.State,
			"to_agent", member,
			"age", age,
		)
		count++
	}
	return count
}

// sendWorkflowEscalation posts an escalation to #approvals for a stale workflow message.
func (w *StalemateWorker) sendWorkflowEscalation(ctx context.Context, msg staleWorkflowMsg) bool {
	approvalsChanID, err := w.channelLookup.GetChannelIDByName(ctx, "approvals")
	if err != nil {
		w.logger.Warn("cannot escalate workflow stalemate: #approvals channel not found", "error", err)
		return false
	}

	age := formatAge(msg.StateAge)
	truncBody := truncate(msg.Body, 100)

	body := fmt.Sprintf(
		"**STALE**: Message #%d in #%s in '%s' for %s. \"%s\" — @%s",
		msg.ID, msg.Channel, msg.State, age, truncBody, msg.FromAgent,
	)

	_, err = w.msgService.SendMessage(ctx, "system", "", body, SendOptions{
		Subject:   fmt.Sprintf("workflow-stalemate-escalation:%d", msg.ID),
		Priority:  9,
		Metadata:  fmt.Sprintf(`{"workflow_stalemate_escalation_for":%d}`, msg.ID),
		ChannelID: &approvalsChanID,
	})
	if err != nil {
		w.logger.Error("send workflow escalation to #approvals failed",
			"message_id", msg.ID,
			"channel", msg.Channel,
			"error", err,
		)
		return false
	}
	w.logger.Info("escalated stale workflow message to #approvals",
		"message_id", msg.ID,
		"channel", msg.Channel,
		"state", msg.State,
		"age", age,
	)
	return true
}

// workflowReminderExists checks if a workflow stalemate reminder already exists for a message.
func (w *StalemateWorker) workflowReminderExists(ctx context.Context, messageID int64) bool {
	var count int
	err := w.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages
		 WHERE from_agent = 'system'
		 AND metadata LIKE ?`,
		fmt.Sprintf(`%%"workflow_stalemate_reminder_for":%d%%`, messageID),
	).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// workflowEscalationExists checks if a workflow stalemate escalation already exists for a message.
func (w *StalemateWorker) workflowEscalationExists(ctx context.Context, messageID int64) bool {
	var count int
	err := w.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages
		 WHERE from_agent = 'system'
		 AND metadata LIKE ?`,
		fmt.Sprintf(`%%"workflow_stalemate_escalation_for":%d%%`, messageID),
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
