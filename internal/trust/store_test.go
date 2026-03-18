package trust

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/storage"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	ctx := context.Background()
	if err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	return db
}

func TestSQLiteStore_UpsertAndGet(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	ts, err := store.UpsertScore(ctx, "agent-a", ActionResearch, 0.5)
	if err != nil {
		t.Fatalf("UpsertScore: %v", err)
	}

	if ts.Score != 0.5 {
		t.Errorf("Score = %f, want 0.5", ts.Score)
	}
	if ts.AgentName != "agent-a" {
		t.Errorf("AgentName = %q, want %q", ts.AgentName, "agent-a")
	}
	if ts.ActionType != ActionResearch {
		t.Errorf("ActionType = %q, want %q", ts.ActionType, ActionResearch)
	}
	if ts.AdjustmentsCount != 1 {
		t.Errorf("AdjustmentsCount = %d, want 1", ts.AdjustmentsCount)
	}

	// Verify it's retrievable via GetScore
	got, err := store.GetScore(ctx, "agent-a", ActionResearch)
	if err != nil {
		t.Fatalf("GetScore: %v", err)
	}
	if got.Score != 0.5 {
		t.Errorf("GetScore Score = %f, want 0.5", got.Score)
	}
	if got.AgentName != "agent-a" {
		t.Errorf("GetScore AgentName = %q, want %q", got.AgentName, "agent-a")
	}
	if got.ActionType != ActionResearch {
		t.Errorf("GetScore ActionType = %q, want %q", got.ActionType, ActionResearch)
	}
}

func TestSQLiteStore_UpsertIncrement(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	// First upsert: initial score
	_, err := store.UpsertScore(ctx, "agent-a", ActionPublish, 0.3)
	if err != nil {
		t.Fatalf("UpsertScore first: %v", err)
	}

	// Second upsert: should increment
	ts, err := store.UpsertScore(ctx, "agent-a", ActionPublish, 0.2)
	if err != nil {
		t.Fatalf("UpsertScore second: %v", err)
	}

	want := 0.5
	if ts.Score != want {
		t.Errorf("Score = %f, want %f", ts.Score, want)
	}
	if ts.AdjustmentsCount != 2 {
		t.Errorf("AdjustmentsCount = %d, want 2", ts.AdjustmentsCount)
	}
}

func TestSQLiteStore_ClampMax(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Insert a high score
	_, err := store.UpsertScore(ctx, "agent-a", ActionComment, 0.9)
	if err != nil {
		t.Fatalf("UpsertScore first: %v", err)
	}

	// Push past 1.0
	ts, err := store.UpsertScore(ctx, "agent-a", ActionComment, 0.5)
	if err != nil {
		t.Fatalf("UpsertScore second: %v", err)
	}

	if ts.Score != MaxScore {
		t.Errorf("Score = %f, want %f (clamped to max)", ts.Score, MaxScore)
	}
}

func TestSQLiteStore_ClampMin(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Insert a low score
	_, err := store.UpsertScore(ctx, "agent-a", ActionOperate, 0.1)
	if err != nil {
		t.Fatalf("UpsertScore first: %v", err)
	}

	// Push past 0.0 with a large negative delta
	ts, err := store.UpsertScore(ctx, "agent-a", ActionOperate, -0.5)
	if err != nil {
		t.Fatalf("UpsertScore second: %v", err)
	}

	if ts.Score != MinScore {
		t.Errorf("Score = %f, want %f (clamped to min)", ts.Score, MinScore)
	}
}

func TestSQLiteStore_GetAllScores(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Insert multiple action types for the same agent
	actions := []struct {
		actionType string
		delta      float64
	}{
		{ActionResearch, 0.3},
		{ActionPublish, 0.5},
		{ActionComment, 0.7},
	}

	for _, a := range actions {
		if _, err := store.UpsertScore(ctx, "agent-a", a.actionType, a.delta); err != nil {
			t.Fatalf("UpsertScore %s: %v", a.actionType, err)
		}
	}

	scores, err := store.GetAllScores(ctx, "agent-a")
	if err != nil {
		t.Fatalf("GetAllScores: %v", err)
	}

	if len(scores) != 3 {
		t.Fatalf("got %d scores, want 3", len(scores))
	}

	// Scores are ordered by action_type alphabetically
	scoreMap := make(map[string]float64)
	for _, s := range scores {
		scoreMap[s.ActionType] = s.Score
	}

	for _, a := range actions {
		got, ok := scoreMap[a.actionType]
		if !ok {
			t.Errorf("missing score for action %q", a.actionType)
			continue
		}
		if got != a.delta {
			t.Errorf("score for %q = %f, want %f", a.actionType, got, a.delta)
		}
	}

	// Different agent should return empty
	other, err := store.GetAllScores(ctx, "agent-nonexistent")
	if err != nil {
		t.Fatalf("GetAllScores (other): %v", err)
	}
	if len(other) != 0 {
		t.Errorf("got %d scores for nonexistent agent, want 0", len(other))
	}
}

func TestSQLiteStore_GetScoreNotFound(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Get score for non-existent agent should return 0.0 (not an error)
	ts, err := store.GetScore(ctx, "nonexistent-agent", ActionResearch)
	if err != nil {
		t.Fatalf("GetScore: %v", err)
	}

	if ts.Score != 0.0 {
		t.Errorf("Score = %f, want 0.0 for non-existent agent", ts.Score)
	}
	if ts.AgentName != "nonexistent-agent" {
		t.Errorf("AgentName = %q, want %q", ts.AgentName, "nonexistent-agent")
	}
	if ts.ActionType != ActionResearch {
		t.Errorf("ActionType = %q, want %q", ts.ActionType, ActionResearch)
	}
	if ts.AdjustmentsCount != 0 {
		t.Errorf("AdjustmentsCount = %d, want 0", ts.AdjustmentsCount)
	}
}
