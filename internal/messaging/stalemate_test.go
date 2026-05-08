package messaging

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/trace"
)

// stubChannelLookup implements ChannelLookup for tests.
type stubChannelLookup struct {
	channelID int64
	err       error
}

func (s *stubChannelLookup) GetChannelIDByName(ctx context.Context, name string) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	return s.channelID, nil
}

// newStalemateTestService creates a MessagingService and DB for stalemate tests.
func newStalemateTestService(t *testing.T) (*MessagingService, *sql.DB) {
	t.Helper()
	db := newTestDB(t)

	seedAgent(t, db, "sender")
	seedAgent(t, db, "receiver")
	seedAgent(t, db, "system")

	store := NewSQLiteMessageStore(db)
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	svc := NewMessagingService(store, tracer)
	return svc, db
}

// insertStaleMessage inserts a message with a specific created_at and claimed_at for testing.
func insertStaleMessage(t *testing.T, db *sql.DB, from, to, body, status string, createdAt time.Time, claimedAt *time.Time, claimedBy string) int64 {
	t.Helper()

	// Insert conversation first
	result, err := db.Exec(
		`INSERT INTO conversations (subject, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?)`,
		"stalemate-test", from, createdAt, createdAt,
	)
	if err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
	convID, _ := result.LastInsertId()

	var claimedAtSQL interface{} = nil
	if claimedAt != nil {
		claimedAtSQL = *claimedAt
	}
	var claimedBySQL interface{} = nil
	if claimedBy != "" {
		claimedBySQL = claimedBy
	}

	result, err = db.Exec(
		`INSERT INTO messages (conversation_id, from_agent, to_agent, body, priority, status, metadata, claimed_by, claimed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 5, ?, '{}', ?, ?, ?, ?)`,
		convID, from, to, body, status, claimedBySQL, claimedAtSQL, createdAt, createdAt,
	)
	if err != nil {
		t.Fatalf("insert stale message: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestStalemateWorker_ProcessingTimeout(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Insert a message in "processing" status with old claimed_at
	oldClaimedAt := time.Now().Add(-25 * time.Hour)
	msgID := insertStaleMessage(t, db, "sender", "receiver", "stale processing task", StatusProcessing, time.Now().Add(-26*time.Hour), &oldClaimedAt, "receiver")

	config := DefaultStalemateConfig()
	config.ProcessingTimeout = 24 * time.Hour

	lookup := &stubChannelLookup{channelID: 0, err: fmt.Errorf("no channel")}
	worker := NewStalemateWorker(db, svc, lookup, config)

	worker.checkStaleMessages(ctx)

	// Verify message was auto-failed
	var status, metadata string
	err := db.QueryRowContext(ctx, `SELECT status, metadata FROM messages WHERE id = ?`, msgID).Scan(&status, &metadata)
	if err != nil {
		t.Fatalf("query message: %v", err)
	}
	if status != StatusFailed {
		t.Errorf("status = %q, want %q", status, StatusFailed)
	}
	if metadata == "{}" {
		t.Error("expected metadata to contain error info")
	}
}

func TestStalemateWorker_ProcessingTimeout_NotExpired(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Insert a message in "processing" status with recent claimed_at (should NOT be failed)
	recentClaimedAt := time.Now().Add(-1 * time.Hour)
	msgID := insertStaleMessage(t, db, "sender", "receiver", "recent processing task", StatusProcessing, time.Now().Add(-2*time.Hour), &recentClaimedAt, "receiver")

	config := DefaultStalemateConfig()
	config.ProcessingTimeout = 24 * time.Hour

	lookup := &stubChannelLookup{channelID: 0, err: fmt.Errorf("no channel")}
	worker := NewStalemateWorker(db, svc, lookup, config)

	worker.checkStaleMessages(ctx)

	// Verify message was NOT auto-failed
	var status string
	err := db.QueryRowContext(ctx, `SELECT status FROM messages WHERE id = ?`, msgID).Scan(&status)
	if err != nil {
		t.Fatalf("query message: %v", err)
	}
	if status != StatusProcessing {
		t.Errorf("status = %q, want %q (should not have been failed)", status, StatusProcessing)
	}
}

// TestStalemateWorker_ProcessingTimeout_RaceGuard verifies that the
// auto-fail UPDATE re-checks claimed_at < cutoff and won't stomp a row that
// was legitimately re-claimed (claimed_at refreshed) between the worker's
// SELECT scan and its row-by-row UPDATE. This guards the TOCTOU window
// the stale-worker race depends on.
func TestStalemateWorker_ProcessingTimeout_RaceGuard(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Insert a message that *was* stale at SELECT time.
	oldClaimedAt := time.Now().Add(-25 * time.Hour)
	msgID := insertStaleMessage(t, db, "sender", "receiver", "racing task",
		StatusProcessing, time.Now().Add(-26*time.Hour), &oldClaimedAt, "receiver")

	config := DefaultStalemateConfig()
	config.ProcessingTimeout = 24 * time.Hour
	lookup := &stubChannelLookup{channelID: 0, err: fmt.Errorf("no channel")}
	worker := NewStalemateWorker(db, svc, lookup, config)

	// Simulate the race: between the worker's SELECT (which would have picked
	// this row) and its UPDATE, the legitimate claimer refreshes claimed_at to
	// "now". With the cutoff predicate in place the UPDATE no-ops instead of
	// silently failing live work.
	freshClaimedAt := time.Now()
	if _, err := db.ExecContext(ctx,
		`UPDATE messages SET claimed_at = ? WHERE id = ?`,
		freshClaimedAt, msgID,
	); err != nil {
		t.Fatalf("refresh claimed_at: %v", err)
	}

	worker.checkStaleMessages(ctx)

	var status string
	if err := db.QueryRowContext(ctx,
		`SELECT status FROM messages WHERE id = ?`, msgID,
	).Scan(&status); err != nil {
		t.Fatalf("query message: %v", err)
	}
	if status != StatusProcessing {
		t.Errorf("status = %q, want %q — re-claimed message must not be auto-failed", status, StatusProcessing)
	}
}

func TestStalemateWorker_PendingReminder(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Insert a pending DM that is 5 hours old
	insertStaleMessage(t, db, "sender", "receiver", "please review this", StatusPending, time.Now().Add(-5*time.Hour), nil, "")

	config := DefaultStalemateConfig()
	config.ReminderAfter = 4 * time.Hour
	config.EscalateAfter = 48 * time.Hour // won't trigger

	lookup := &stubChannelLookup{channelID: 0, err: fmt.Errorf("no channel")}
	worker := NewStalemateWorker(db, svc, lookup, config)

	worker.checkStaleMessages(ctx)

	// Verify a system reminder was sent to receiver
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE from_agent = 'system' AND to_agent = 'receiver' AND body LIKE '%Reminder%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query reminder: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 reminder, got %d", count)
	}
}

func TestStalemateWorker_SystemMessageSkip(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Insert a pending DM FROM system (should be skipped)
	insertStaleMessage(t, db, "system", "receiver", "system notification", StatusPending, time.Now().Add(-5*time.Hour), nil, "")

	config := DefaultStalemateConfig()
	config.ReminderAfter = 4 * time.Hour

	lookup := &stubChannelLookup{channelID: 0, err: fmt.Errorf("no channel")}
	worker := NewStalemateWorker(db, svc, lookup, config)

	worker.checkStaleMessages(ctx)

	// Verify NO reminder was sent (only the original system message should exist)
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE from_agent = 'system' AND body LIKE '%Reminder%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query reminder: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 reminders for system message, got %d", count)
	}
}

func TestStalemateWorker_DuplicateReminderPrevention(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Insert a pending DM that is old enough for a reminder
	insertStaleMessage(t, db, "sender", "receiver", "need your attention", StatusPending, time.Now().Add(-5*time.Hour), nil, "")

	config := DefaultStalemateConfig()
	config.ReminderAfter = 4 * time.Hour
	config.EscalateAfter = 48 * time.Hour

	lookup := &stubChannelLookup{channelID: 0, err: fmt.Errorf("no channel")}
	worker := NewStalemateWorker(db, svc, lookup, config)

	// Run check twice
	worker.checkStaleMessages(ctx)
	worker.checkStaleMessages(ctx)

	// Verify only ONE reminder was sent
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE from_agent = 'system' AND to_agent = 'receiver' AND body LIKE '%Reminder%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query reminders: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 reminder (no duplicates), got %d", count)
	}
}

func TestStalemateWorker_Escalation(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Create #approvals channel
	_, err := db.Exec(
		`INSERT INTO channels (id, name, description, topic, type, is_private, is_system, created_by, created_at, updated_at)
		 VALUES (1, 'approvals', 'Approval queue', '', 'standard', 0, 0, 'system', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("create approvals channel: %v", err)
	}
	// Add system as member
	_, err = db.Exec(
		`INSERT INTO channel_members (channel_id, agent_name, role, joined_at)
		 VALUES (1, 'system', 'owner', CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("add system to channel: %v", err)
	}

	// Insert a pending DM that is 49 hours old (beyond escalation threshold)
	insertStaleMessage(t, db, "sender", "receiver", "urgent task ignored", StatusPending, time.Now().Add(-49*time.Hour), nil, "")

	config := DefaultStalemateConfig()
	config.ReminderAfter = 4 * time.Hour
	config.EscalateAfter = 48 * time.Hour

	lookup := &stubChannelLookup{channelID: 1}
	worker := NewStalemateWorker(db, svc, lookup, config)

	worker.checkStaleMessages(ctx)

	// Verify an escalation was sent to #approvals channel
	var count int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE from_agent = 'system' AND channel_id = 1 AND body LIKE '%ESCALATION%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query escalations: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 escalation, got %d", count)
	}
}

func TestStalemateWorker_DuplicateEscalationPrevention(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Create #approvals channel
	db.Exec(
		`INSERT INTO channels (id, name, description, topic, type, is_private, is_system, created_by, created_at, updated_at)
		 VALUES (1, 'approvals', 'Approval queue', '', 'standard', 0, 0, 'system', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	db.Exec(
		`INSERT INTO channel_members (channel_id, agent_name, role, joined_at)
		 VALUES (1, 'system', 'owner', CURRENT_TIMESTAMP)`)

	// Insert a pending DM that is 49 hours old
	insertStaleMessage(t, db, "sender", "receiver", "urgent task", StatusPending, time.Now().Add(-49*time.Hour), nil, "")

	config := DefaultStalemateConfig()
	config.ReminderAfter = 4 * time.Hour
	config.EscalateAfter = 48 * time.Hour

	lookup := &stubChannelLookup{channelID: 1}
	worker := NewStalemateWorker(db, svc, lookup, config)

	// Run check twice
	worker.checkStaleMessages(ctx)
	worker.checkStaleMessages(ctx)

	// Verify only ONE escalation was sent
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE from_agent = 'system' AND channel_id = 1 AND body LIKE '%ESCALATION%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query escalations: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 escalation (no duplicates), got %d", count)
	}
}

func TestParseStalemateConfig(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected StalemateConfig
	}{
		{
			name:     "defaults when no env vars",
			envVars:  map[string]string{},
			expected: DefaultStalemateConfig(),
		},
		{
			name: "custom values with day format",
			envVars: map[string]string{
				"SYNAPBUS_STALEMATE_PROCESSING_TIMEOUT": "7d",
				"SYNAPBUS_STALEMATE_REMINDER_AFTER":     "8h",
				"SYNAPBUS_STALEMATE_ESCALATE_AFTER":     "3d",
				"SYNAPBUS_STALEMATE_INTERVAL":           "30m",
			},
			expected: StalemateConfig{
				ProcessingTimeout: 7 * 24 * time.Hour,
				ReminderAfter:     8 * time.Hour,
				EscalateAfter:     3 * 24 * time.Hour,
				Interval:          30 * time.Minute,
			},
		},
		{
			name: "standard Go duration format",
			envVars: map[string]string{
				"SYNAPBUS_STALEMATE_PROCESSING_TIMEOUT": "48h",
				"SYNAPBUS_STALEMATE_REMINDER_AFTER":     "2h30m",
				"SYNAPBUS_STALEMATE_ESCALATE_AFTER":     "72h",
				"SYNAPBUS_STALEMATE_INTERVAL":           "5m",
			},
			expected: StalemateConfig{
				ProcessingTimeout: 48 * time.Hour,
				ReminderAfter:     2*time.Hour + 30*time.Minute,
				EscalateAfter:     72 * time.Hour,
				Interval:          5 * time.Minute,
			},
		},
		{
			name: "invalid values fall back to defaults",
			envVars: map[string]string{
				"SYNAPBUS_STALEMATE_PROCESSING_TIMEOUT": "invalid",
				"SYNAPBUS_STALEMATE_REMINDER_AFTER":     "bad",
				"SYNAPBUS_STALEMATE_ESCALATE_AFTER":     "",
				"SYNAPBUS_STALEMATE_INTERVAL":           "-5m",
			},
			expected: DefaultStalemateConfig(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all env vars first
			envKeys := []string{
				"SYNAPBUS_STALEMATE_PROCESSING_TIMEOUT",
				"SYNAPBUS_STALEMATE_REMINDER_AFTER",
				"SYNAPBUS_STALEMATE_ESCALATE_AFTER",
				"SYNAPBUS_STALEMATE_INTERVAL",
			}
			for _, k := range envKeys {
				os.Unsetenv(k)
			}

			// Set test env vars
			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}
			defer func() {
				for _, k := range envKeys {
					os.Unsetenv(k)
				}
			}()

			cfg := ParseStalemateConfig()

			if cfg.ProcessingTimeout != tt.expected.ProcessingTimeout {
				t.Errorf("ProcessingTimeout = %v, want %v", cfg.ProcessingTimeout, tt.expected.ProcessingTimeout)
			}
			if cfg.ReminderAfter != tt.expected.ReminderAfter {
				t.Errorf("ReminderAfter = %v, want %v", cfg.ReminderAfter, tt.expected.ReminderAfter)
			}
			if cfg.EscalateAfter != tt.expected.EscalateAfter {
				t.Errorf("EscalateAfter = %v, want %v", cfg.EscalateAfter, tt.expected.EscalateAfter)
			}
			if cfg.Interval != tt.expected.Interval {
				t.Errorf("Interval = %v, want %v", cfg.Interval, tt.expected.Interval)
			}
		})
	}
}

func TestParseDurationWithDays(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     time.Duration
		wantErr  bool
	}{
		{"7 days", "7d", 7 * 24 * time.Hour, false},
		{"1 day", "1d", 24 * time.Hour, false},
		{"30 days", "30d", 30 * 24 * time.Hour, false},
		{"standard hours", "48h", 48 * time.Hour, false},
		{"standard minutes", "15m", 15 * time.Minute, false},
		{"mixed duration", "2h30m", 2*time.Hour + 30*time.Minute, false},
		{"empty string", "", 0, true},
		{"invalid", "xyz", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDurationWithDays(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDurationWithDays(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseDurationWithDays(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncated", "hello world, this is a long message", 10, "hello worl..."},
		{"empty", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestFormatAge(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"minutes", 30 * time.Minute, "30m"},
		{"hours", 5 * time.Hour, "5h"},
		{"1 day", 24 * time.Hour, "1 day"},
		{"2 days", 48 * time.Hour, "2 days"},
		{"1 day with hours", 25 * time.Hour, "1 day 1h"},
		{"2 days with hours", 50 * time.Hour, "2 days 2h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAge(tt.d)
			if got != tt.want {
				t.Errorf("formatAge(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestComputeWorkflowStateFromReactions(t *testing.T) {
	tests := []struct {
		name      string
		reactions []reactionRow
		want      string
	}{
		{"no reactions = proposed", nil, "proposed"},
		{"approve only", []reactionRow{{Reaction: "approve"}}, "approved"},
		{"in_progress only", []reactionRow{{Reaction: "in_progress"}}, "in_progress"},
		{"reject only", []reactionRow{{Reaction: "reject"}}, "rejected"},
		{"done only", []reactionRow{{Reaction: "done"}}, "done"},
		{"published only", []reactionRow{{Reaction: "published"}}, "published"},
		{"approve + in_progress = in_progress (higher priority)", []reactionRow{
			{Reaction: "approve"},
			{Reaction: "in_progress"},
		}, "in_progress"},
		{"approve + done = done", []reactionRow{
			{Reaction: "approve"},
			{Reaction: "done"},
		}, "done"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeWorkflowStateFromReactions(tt.reactions)
			if got != tt.want {
				t.Errorf("computeWorkflowStateFromReactions() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsTerminalWorkflowState(t *testing.T) {
	tests := []struct {
		state    string
		terminal bool
	}{
		{"proposed", false},
		{"approved", false},
		{"in_progress", false},
		{"rejected", true},
		{"done", true},
		{"published", true},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			got := isTerminalWorkflowState(tt.state)
			if got != tt.terminal {
				t.Errorf("isTerminalWorkflowState(%q) = %v, want %v", tt.state, got, tt.terminal)
			}
		})
	}
}

func TestStalemateWorker_WorkflowReminder(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Create a workflow-enabled channel with short timeouts
	_, err := db.Exec(
		`INSERT INTO channels (id, name, description, topic, type, is_private, is_system, created_by, workflow_enabled, stalemate_remind_after, stalemate_escalate_after, created_at, updated_at)
		 VALUES (10, 'news-test', 'Test news channel', '', 'standard', 0, 0, 'system', 1, '1s', '72h', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("create workflow channel: %v", err)
	}

	// Add system and sender as members
	db.Exec(`INSERT INTO channel_members (channel_id, agent_name, role, joined_at) VALUES (10, 'system', 'owner', CURRENT_TIMESTAMP)`)
	db.Exec(`INSERT INTO channel_members (channel_id, agent_name, role, joined_at) VALUES (10, 'sender', 'member', CURRENT_TIMESTAMP)`)
	db.Exec(`INSERT INTO channel_members (channel_id, agent_name, role, joined_at) VALUES (10, 'receiver', 'member', CURRENT_TIMESTAMP)`)

	// Insert a channel message with old created_at (will be in "proposed" state since no reactions)
	oldTime := time.Now().Add(-2 * time.Second)
	convResult, err := db.Exec(
		`INSERT INTO conversations (subject, created_by, created_at, updated_at) VALUES ('wf-test', 'sender', ?, ?)`,
		oldTime, oldTime,
	)
	if err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
	convID, _ := convResult.LastInsertId()

	channelID := int64(10)
	_, err = db.Exec(
		`INSERT INTO messages (conversation_id, from_agent, to_agent, body, priority, status, metadata, channel_id, created_at, updated_at)
		 VALUES (?, 'sender', '', 'Draft blog post about MCP', 5, 'pending', '{}', ?, ?, ?)`,
		convID, channelID, oldTime, oldTime,
	)
	if err != nil {
		t.Fatalf("insert channel message: %v", err)
	}

	// Wait for the timeout to elapse
	time.Sleep(10 * time.Millisecond)

	config := DefaultStalemateConfig()
	lookup := &stubChannelLookup{channelID: 0, err: fmt.Errorf("no approvals channel")}
	worker := NewStalemateWorker(db, svc, lookup, config)

	worker.checkStaleMessages(ctx)

	// Verify workflow stalemate reminders were sent to channel members
	var count int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE from_agent = 'system' AND body LIKE '%STALE%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query workflow reminders: %v", err)
	}
	// Should have sent reminders to all 3 members (system, sender, receiver)
	if count < 1 {
		t.Errorf("expected at least 1 workflow reminder, got %d", count)
	}
}

func TestStalemateWorker_WorkflowEscalation(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Create a workflow-enabled channel with short escalation timeout
	_, err := db.Exec(
		`INSERT INTO channels (id, name, description, topic, type, is_private, is_system, created_by, workflow_enabled, stalemate_remind_after, stalemate_escalate_after, created_at, updated_at)
		 VALUES (10, 'news-test', 'Test news channel', '', 'standard', 0, 0, 'system', 1, '1s', '1s', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("create workflow channel: %v", err)
	}

	// Create #approvals channel
	db.Exec(
		`INSERT INTO channels (id, name, description, topic, type, is_private, is_system, created_by, created_at, updated_at)
		 VALUES (20, 'approvals', 'Approval queue', '', 'standard', 0, 0, 'system', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	db.Exec(`INSERT INTO channel_members (channel_id, agent_name, role, joined_at) VALUES (20, 'system', 'owner', CURRENT_TIMESTAMP)`)

	// Add members to workflow channel
	db.Exec(`INSERT INTO channel_members (channel_id, agent_name, role, joined_at) VALUES (10, 'sender', 'member', CURRENT_TIMESTAMP)`)

	// Insert a channel message old enough to trigger escalation
	oldTime := time.Now().Add(-2 * time.Second)
	convResult, _ := db.Exec(
		`INSERT INTO conversations (subject, created_by, created_at, updated_at) VALUES ('wf-esc', 'sender', ?, ?)`,
		oldTime, oldTime,
	)
	convID, _ := convResult.LastInsertId()

	channelID := int64(10)
	_, err = db.Exec(
		`INSERT INTO messages (conversation_id, from_agent, to_agent, body, priority, status, metadata, channel_id, created_at, updated_at)
		 VALUES (?, 'sender', '', 'Stale proposal needing attention', 5, 'pending', '{}', ?, ?, ?)`,
		convID, channelID, oldTime, oldTime,
	)
	if err != nil {
		t.Fatalf("insert channel message: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	config := DefaultStalemateConfig()
	lookup := &stubChannelLookup{channelID: 20}
	worker := NewStalemateWorker(db, svc, lookup, config)

	worker.checkStaleMessages(ctx)

	// Verify escalation was sent to #approvals
	var count int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE from_agent = 'system' AND channel_id = 20 AND body LIKE '%STALE%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query workflow escalation: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 workflow escalation, got %d", count)
	}
}

func TestStalemateWorker_WorkflowTerminalStateSkip(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Create a workflow-enabled channel with short timeouts
	_, err := db.Exec(
		`INSERT INTO channels (id, name, description, topic, type, is_private, is_system, created_by, workflow_enabled, stalemate_remind_after, stalemate_escalate_after, created_at, updated_at)
		 VALUES (10, 'news-test', 'Test news channel', '', 'standard', 0, 0, 'system', 1, '1s', '1s', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("create workflow channel: %v", err)
	}
	db.Exec(`INSERT INTO channel_members (channel_id, agent_name, role, joined_at) VALUES (10, 'sender', 'member', CURRENT_TIMESTAMP)`)

	// Insert a channel message
	oldTime := time.Now().Add(-2 * time.Second)
	convResult, _ := db.Exec(
		`INSERT INTO conversations (subject, created_by, created_at, updated_at) VALUES ('wf-done', 'sender', ?, ?)`,
		oldTime, oldTime,
	)
	convID, _ := convResult.LastInsertId()

	channelID := int64(10)
	msgResult, err := db.Exec(
		`INSERT INTO messages (conversation_id, from_agent, to_agent, body, priority, status, metadata, channel_id, created_at, updated_at)
		 VALUES (?, 'sender', '', 'Completed task', 5, 'pending', '{}', ?, ?, ?)`,
		convID, channelID, oldTime, oldTime,
	)
	if err != nil {
		t.Fatalf("insert channel message: %v", err)
	}
	msgID, _ := msgResult.LastInsertId()

	// Add a "done" reaction — puts it in terminal state
	_, err = db.Exec(
		`INSERT INTO message_reactions (message_id, agent_name, reaction, metadata, created_at)
		 VALUES (?, 'sender', 'done', '{}', ?)`,
		msgID, oldTime,
	)
	if err != nil {
		t.Fatalf("insert reaction: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	config := DefaultStalemateConfig()
	lookup := &stubChannelLookup{channelID: 0, err: fmt.Errorf("no approvals")}
	worker := NewStalemateWorker(db, svc, lookup, config)

	worker.checkStaleMessages(ctx)

	// Verify NO reminders were sent (message is in terminal "done" state)
	var count int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE from_agent = 'system' AND body LIKE '%STALE%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query reminders: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 reminders for terminal state message, got %d", count)
	}
}

func TestStalemateWorker_WorkflowDuplicateReminderPrevention(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Create a workflow-enabled channel with short timeout
	_, err := db.Exec(
		`INSERT INTO channels (id, name, description, topic, type, is_private, is_system, created_by, workflow_enabled, stalemate_remind_after, stalemate_escalate_after, created_at, updated_at)
		 VALUES (10, 'news-test', 'Test', '', 'standard', 0, 0, 'system', 1, '1s', '72h', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("create workflow channel: %v", err)
	}
	db.Exec(`INSERT INTO channel_members (channel_id, agent_name, role, joined_at) VALUES (10, 'receiver', 'member', CURRENT_TIMESTAMP)`)

	// Insert a channel message
	oldTime := time.Now().Add(-2 * time.Second)
	convResult, _ := db.Exec(
		`INSERT INTO conversations (subject, created_by, created_at, updated_at) VALUES ('wf-dup', 'sender', ?, ?)`,
		oldTime, oldTime,
	)
	convID, _ := convResult.LastInsertId()

	channelID := int64(10)
	_, err = db.Exec(
		`INSERT INTO messages (conversation_id, from_agent, to_agent, body, priority, status, metadata, channel_id, created_at, updated_at)
		 VALUES (?, 'sender', '', 'Needs review', 5, 'pending', '{}', ?, ?, ?)`,
		convID, channelID, oldTime, oldTime,
	)
	if err != nil {
		t.Fatalf("insert channel message: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	config := DefaultStalemateConfig()
	lookup := &stubChannelLookup{channelID: 0, err: fmt.Errorf("no approvals")}
	worker := NewStalemateWorker(db, svc, lookup, config)

	// Run twice
	worker.checkStaleMessages(ctx)
	worker.checkStaleMessages(ctx)

	// Verify only one set of reminders was sent (no duplicates)
	var count int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE from_agent = 'system' AND to_agent = 'receiver' AND body LIKE '%STALE%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query reminders: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 reminder (no duplicates), got %d", count)
	}
}

func TestStalemateWorker_WorkflowNonWorkflowChannelSkip(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	// Create a channel with workflow DISABLED
	_, err := db.Exec(
		`INSERT INTO channels (id, name, description, topic, type, is_private, is_system, created_by, workflow_enabled, stalemate_remind_after, stalemate_escalate_after, created_at, updated_at)
		 VALUES (10, 'general', 'General', '', 'standard', 0, 0, 'system', 0, '1s', '1s', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	db.Exec(`INSERT INTO channel_members (channel_id, agent_name, role, joined_at) VALUES (10, 'sender', 'member', CURRENT_TIMESTAMP)`)

	// Insert a channel message
	oldTime := time.Now().Add(-2 * time.Second)
	convResult, _ := db.Exec(
		`INSERT INTO conversations (subject, created_by, created_at, updated_at) VALUES ('no-wf', 'sender', ?, ?)`,
		oldTime, oldTime,
	)
	convID, _ := convResult.LastInsertId()

	channelID := int64(10)
	db.Exec(
		`INSERT INTO messages (conversation_id, from_agent, to_agent, body, priority, status, metadata, channel_id, created_at, updated_at)
		 VALUES (?, 'sender', '', 'No workflow here', 5, 'pending', '{}', ?, ?, ?)`,
		convID, channelID, oldTime, oldTime,
	)

	time.Sleep(10 * time.Millisecond)

	config := DefaultStalemateConfig()
	lookup := &stubChannelLookup{channelID: 0, err: fmt.Errorf("no approvals")}
	worker := NewStalemateWorker(db, svc, lookup, config)

	worker.checkStaleMessages(ctx)

	// Verify NO reminders — channel is not workflow-enabled
	var count int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE from_agent = 'system' AND body LIKE '%STALE%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query reminders: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 reminders for non-workflow channel, got %d", count)
	}
}
