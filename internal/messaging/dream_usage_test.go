package messaging

import (
	"context"
	"testing"
	"time"
)

func TestDreamUsageStore_RecordAndQuery(t *testing.T) {
	db := newTestDB(t)
	store := NewDreamUsageStore(db)

	ctx := context.Background()
	owner := "42"

	// Start two jobs, complete one succeeded with tokens, one failed.
	if err := store.RecordStart(ctx, owner); err != nil {
		t.Fatalf("RecordStart: %v", err)
	}
	if err := store.RecordStart(ctx, owner); err != nil {
		t.Fatalf("RecordStart: %v", err)
	}
	if err := store.RecordCompletion(ctx, owner, 1500, 700, JobStatusSucceeded); err != nil {
		t.Fatalf("RecordCompletion succeeded: %v", err)
	}
	if err := store.RecordCompletion(ctx, owner, 0, 0, JobStatusFailed); err != nil {
		t.Fatalf("RecordCompletion failed: %v", err)
	}

	u, err := store.Today(ctx, owner)
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if u.JobsStarted != 2 || u.JobsSucceeded != 1 || u.JobsFailed != 1 {
		t.Errorf("counters mismatch: %+v", u)
	}
	if u.TokensIn != 1500 || u.TokensOut != 700 {
		t.Errorf("tokens mismatch: in=%d out=%d", u.TokensIn, u.TokensOut)
	}
}

func TestDreamUsageStore_CircuitBrokenAccounting(t *testing.T) {
	db := newTestDB(t)
	store := NewDreamUsageStore(db)
	ctx := context.Background()
	owner := "99"

	if err := store.RecordCompletion(ctx, owner, 0, 0, JobStatusCircuitBroken); err != nil {
		t.Fatalf("RecordCompletion: %v", err)
	}
	u, _ := store.Today(ctx, owner)
	if u.JobsCircuitBroken != 1 {
		t.Errorf("expected jobs_circuit_broken=1, got %d", u.JobsCircuitBroken)
	}
}

func TestUsageGate_AllowDeny(t *testing.T) {
	db := newTestDB(t)
	store := NewDreamUsageStore(db)
	ctx := context.Background()
	owner := "7"

	cfg := DefaultMemoryConfig()
	cfg.DreamDailyTokenLimitIn = 1000
	cfg.DreamDailyTokenLimitOut = 500
	cfg.DreamDailyJobLimit = 3
	gate := NewUsageGate(cfg, store)

	// Initially allowed.
	allowed, reason, err := gate.Allow(ctx, owner)
	if err != nil || !allowed || reason != "" {
		t.Fatalf("expected initial allow, got allowed=%v reason=%q err=%v", allowed, reason, err)
	}

	// Push tokens_in just below the limit — still allowed.
	if err := store.RecordCompletion(ctx, owner, 999, 0, JobStatusSucceeded); err != nil {
		t.Fatalf("RecordCompletion: %v", err)
	}
	allowed, reason, _ = gate.Allow(ctx, owner)
	if !allowed {
		t.Errorf("expected allow at 999/1000 in, got %q", reason)
	}

	// Cross the input threshold — denied.
	if err := store.RecordCompletion(ctx, owner, 2, 0, JobStatusSucceeded); err != nil {
		t.Fatalf("RecordCompletion: %v", err)
	}
	allowed, reason, _ = gate.Allow(ctx, owner)
	if allowed || reason != "tokens_in_exceeded" {
		t.Errorf("expected tokens_in_exceeded, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestUsageGate_JobsExceeded(t *testing.T) {
	db := newTestDB(t)
	store := NewDreamUsageStore(db)
	ctx := context.Background()
	owner := "5"

	cfg := DefaultMemoryConfig()
	cfg.DreamDailyTokenLimitIn = 0 // disable token gates
	cfg.DreamDailyTokenLimitOut = 0
	cfg.DreamDailyJobLimit = 2
	gate := NewUsageGate(cfg, store)

	for i := 0; i < 2; i++ {
		_ = store.RecordStart(ctx, owner)
	}
	allowed, reason, _ := gate.Allow(ctx, owner)
	if allowed || reason != "jobs_exceeded" {
		t.Errorf("expected jobs_exceeded, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestUsageGate_NilStoreAllows(t *testing.T) {
	gate := NewUsageGate(DefaultMemoryConfig(), nil)
	allowed, reason, err := gate.Allow(context.Background(), "1")
	if !allowed || reason != "" || err != nil {
		t.Errorf("nil store: expected allow, got %v %q %v", allowed, reason, err)
	}
}

func TestDreamUsageStore_Cleanup(t *testing.T) {
	db := newTestDB(t)
	store := NewDreamUsageStore(db)
	ctx := context.Background()

	// Insert an "old" row by overriding the clock.
	store.now = func() time.Time { return time.Now().AddDate(0, 0, -40).UTC() }
	if err := store.RecordStart(ctx, "1"); err != nil {
		t.Fatalf("RecordStart: %v", err)
	}
	// Restore now.
	store.now = time.Now
	if err := store.RecordStart(ctx, "1"); err != nil {
		t.Fatalf("RecordStart: %v", err)
	}

	n, err := store.Cleanup(ctx, 30)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row deleted, got %d", n)
	}
}
