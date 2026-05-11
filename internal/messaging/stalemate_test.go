package messaging

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/trace"
)

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

	oldClaimedAt := time.Now().Add(-25 * time.Hour)
	msgID := insertStaleMessage(t, db, "sender", "receiver", "stale processing task", StatusProcessing, time.Now().Add(-26*time.Hour), &oldClaimedAt, "receiver")

	config := DefaultStalemateConfig()
	config.ProcessingTimeout = 24 * time.Hour
	worker := NewStalemateWorker(db, svc, config)

	worker.checkStaleMessages(ctx)

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

	recentClaimedAt := time.Now().Add(-1 * time.Hour)
	msgID := insertStaleMessage(t, db, "sender", "receiver", "recent processing task", StatusProcessing, time.Now().Add(-2*time.Hour), &recentClaimedAt, "receiver")

	config := DefaultStalemateConfig()
	config.ProcessingTimeout = 24 * time.Hour
	worker := NewStalemateWorker(db, svc, config)

	worker.checkStaleMessages(ctx)

	var status string
	err := db.QueryRowContext(ctx, `SELECT status FROM messages WHERE id = ?`, msgID).Scan(&status)
	if err != nil {
		t.Fatalf("query message: %v", err)
	}
	if status != StatusProcessing {
		t.Errorf("status = %q, want %q (should not have been failed)", status, StatusProcessing)
	}
}

// TestStalemateWorker_ProcessingTimeout_RaceGuard verifies that the auto-fail
// UPDATE re-checks claimed_at < cutoff so a row re-claimed between SELECT and
// UPDATE is not stomped. Guards the TOCTOU window the stale-worker race
// depends on.
func TestStalemateWorker_ProcessingTimeout_RaceGuard(t *testing.T) {
	svc, db := newStalemateTestService(t)
	ctx := context.Background()

	oldClaimedAt := time.Now().Add(-25 * time.Hour)
	msgID := insertStaleMessage(t, db, "sender", "receiver", "racing task",
		StatusProcessing, time.Now().Add(-26*time.Hour), &oldClaimedAt, "receiver")

	config := DefaultStalemateConfig()
	config.ProcessingTimeout = 24 * time.Hour
	worker := NewStalemateWorker(db, svc, config)

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
				"SYNAPBUS_STALEMATE_INTERVAL":           "30m",
			},
			expected: StalemateConfig{
				ProcessingTimeout: 7 * 24 * time.Hour,
				Interval:          30 * time.Minute,
			},
		},
		{
			name: "standard Go duration format",
			envVars: map[string]string{
				"SYNAPBUS_STALEMATE_PROCESSING_TIMEOUT": "48h",
				"SYNAPBUS_STALEMATE_INTERVAL":           "5m",
			},
			expected: StalemateConfig{
				ProcessingTimeout: 48 * time.Hour,
				Interval:          5 * time.Minute,
			},
		},
		{
			name: "invalid values fall back to defaults",
			envVars: map[string]string{
				"SYNAPBUS_STALEMATE_PROCESSING_TIMEOUT": "invalid",
				"SYNAPBUS_STALEMATE_INTERVAL":           "-5m",
			},
			expected: DefaultStalemateConfig(),
		},
	}

	envKeys := []string{
		"SYNAPBUS_STALEMATE_PROCESSING_TIMEOUT",
		"SYNAPBUS_STALEMATE_INTERVAL",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range envKeys {
				os.Unsetenv(k)
			}
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
			if cfg.Interval != tt.expected.Interval {
				t.Errorf("Interval = %v, want %v", cfg.Interval, tt.expected.Interval)
			}
		})
	}
}

func TestParseDurationWithDays(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
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
