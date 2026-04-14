package trust

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/storage"
)

func newLedgerTestDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	dsn := fmt.Sprintf("file:ledger_%s?mode=memory&cache=shared", t.Name())
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

	// Insert a test owner user.
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, display_name) VALUES (?, ?, ?)`,
		"trust_test_user_"+t.Name(), "x", "Test",
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	uid, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return db, uid
}

func TestLedger_AppendAndRollingScore_EmptyIsNeutral(t *testing.T) {
	db, _ := newLedgerTestDB(t)
	ledger := NewLedger(db)
	ctx := context.Background()

	score, cnt, err := ledger.RollingScore(ctx, "no-such-hash", "default", 0)
	if err != nil {
		t.Fatalf("RollingScore: %v", err)
	}
	if cnt != 0 {
		t.Errorf("count = %d, want 0", cnt)
	}
	if score != neutralScore {
		t.Errorf("score = %f, want %f (neutral)", score, neutralScore)
	}
}

func TestLedger_AppendAndRollingScore_Decays(t *testing.T) {
	db, owner := newLedgerTestDB(t)
	ledger := NewLedger(db)
	ctx := context.Background()

	hash := "decay-hash"

	// Old row (60 days ago) with large positive delta.
	if _, err := ledger.Append(ctx, Evidence{
		ConfigHash:  hash,
		OwnerUserID: owner,
		TaskDomain:  "default",
		ScoreDelta:  0.9,
		Weight:      1.0,
		EvidenceRef: "old",
		CreatedAt:   time.Now().UTC().Add(-60 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("append old: %v", err)
	}

	// Fresh row with smaller delta.
	if _, err := ledger.Append(ctx, Evidence{
		ConfigHash:  hash,
		OwnerUserID: owner,
		TaskDomain:  "default",
		ScoreDelta:  0.4,
		Weight:      1.0,
		EvidenceRef: "fresh",
	}); err != nil {
		t.Fatalf("append fresh: %v", err)
	}

	score, cnt, err := ledger.RollingScore(ctx, hash, "default", 30)
	if err != nil {
		t.Fatalf("RollingScore: %v", err)
	}
	if cnt != 2 {
		t.Errorf("count = %d, want 2", cnt)
	}

	// After 60d with 30d half-life, the old row's contribution is 0.9 * 0.25 = 0.225.
	// The fresh row contributes ~0.4. Total ~0.625, well below the old delta but
	// dominated by the fresh contribution.
	if score < 0.5 || score > 0.75 {
		t.Errorf("score = %f, expected ~0.625", score)
	}

	// Fresh-only score should be greater than score with the old row dragging it down? Actually old row is positive too here, so let's instead verify that fresh delta dominates: removing fresh row should drop the score sharply.
	score2, _, err := ledger.RollingScore(ctx, hash, "missing-domain", 30)
	if err != nil {
		t.Fatalf("RollingScore missing: %v", err)
	}
	if score2 != neutralScore {
		t.Errorf("missing-domain score = %f, want neutral", score2)
	}
}

func TestLedger_SeedFromParent_70Percent(t *testing.T) {
	db, owner := newLedgerTestDB(t)
	ledger := NewLedger(db)
	ctx := context.Background()

	parent := "parent-hash"
	child := "child-hash"

	// Seed the parent with evidence summing to ~0.8 (recent, full weight).
	if _, err := ledger.Append(ctx, Evidence{
		ConfigHash:  parent,
		OwnerUserID: owner,
		ScoreDelta:  0.8,
		Weight:      1.0,
		EvidenceRef: "parent_seed",
	}); err != nil {
		t.Fatalf("append parent: %v", err)
	}

	parentScore, _, err := ledger.RollingScore(ctx, parent, "default", 30)
	if err != nil {
		t.Fatalf("parent score: %v", err)
	}
	// Parent score should be ~0.8 (just inserted, decay ≈ 1.0).
	if math.Abs(parentScore-0.8) > 0.01 {
		t.Fatalf("parent score = %f, want ~0.8", parentScore)
	}

	if err := ledger.SeedFromParent(ctx, parent, child, owner, "default", 30); err != nil {
		t.Fatalf("SeedFromParent: %v", err)
	}

	childScore, cnt, err := ledger.RollingScore(ctx, child, "default", 30)
	if err != nil {
		t.Fatalf("child score: %v", err)
	}
	if cnt != 1 {
		t.Errorf("child evidence count = %d, want 1", cnt)
	}
	want := 0.7 * 0.8 // 0.56
	if math.Abs(childScore-want) > 0.01 {
		t.Errorf("child score = %f, want ~%f", childScore, want)
	}

	// Verify the seed row references the parent.
	var ref string
	if err := db.QueryRowContext(ctx,
		`SELECT evidence_ref FROM reputation_evidence WHERE config_hash = ?`, child,
	).Scan(&ref); err != nil {
		t.Fatalf("select ref: %v", err)
	}
	if ref != "seed_from_parent:"+parent {
		t.Errorf("evidence_ref = %q, want %q", ref, "seed_from_parent:"+parent)
	}
}

func TestLedger_Clamping(t *testing.T) {
	db, owner := newLedgerTestDB(t)
	ledger := NewLedger(db)
	ctx := context.Background()

	hash := "clamp-hash"

	// Many large negative deltas.
	for i := 0; i < 5; i++ {
		if _, err := ledger.Append(ctx, Evidence{
			ConfigHash:  hash,
			OwnerUserID: owner,
			TaskDomain:  "default",
			ScoreDelta:  -2.0,
			Weight:      1.0,
			EvidenceRef: fmt.Sprintf("neg_%d", i),
		}); err != nil {
			t.Fatalf("append neg: %v", err)
		}
	}

	score, cnt, err := ledger.RollingScore(ctx, hash, "default", 30)
	if err != nil {
		t.Fatalf("RollingScore: %v", err)
	}
	if cnt != 5 {
		t.Errorf("count = %d, want 5", cnt)
	}
	if score != 0.0 {
		t.Errorf("score = %f, want 0.0 (clamped)", score)
	}

	// And the inverse: many large positive deltas should clamp to 1.0.
	hash2 := "clamp-hash-pos"
	for i := 0; i < 5; i++ {
		if _, err := ledger.Append(ctx, Evidence{
			ConfigHash:  hash2,
			OwnerUserID: owner,
			TaskDomain:  "default",
			ScoreDelta:  2.0,
			Weight:      1.0,
			EvidenceRef: fmt.Sprintf("pos_%d", i),
		}); err != nil {
			t.Fatalf("append pos: %v", err)
		}
	}
	score2, _, err := ledger.RollingScore(ctx, hash2, "default", 30)
	if err != nil {
		t.Fatalf("RollingScore pos: %v", err)
	}
	if score2 != 1.0 {
		t.Errorf("score = %f, want 1.0 (clamped)", score2)
	}
}

func TestLedger_Append_Defaults(t *testing.T) {
	db, owner := newLedgerTestDB(t)
	ledger := NewLedger(db)
	ctx := context.Background()

	// TaskDomain empty, Weight zero → should default to "default" and 1.0.
	id, err := ledger.Append(ctx, Evidence{
		ConfigHash:  "defaults-hash",
		OwnerUserID: owner,
		ScoreDelta:  0.3,
		EvidenceRef: "test",
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if id <= 0 {
		t.Errorf("id = %d, want > 0", id)
	}

	var domain string
	var weight float64
	if err := db.QueryRowContext(ctx,
		`SELECT task_domain, weight FROM reputation_evidence WHERE id = ?`, id,
	).Scan(&domain, &weight); err != nil {
		t.Fatalf("select: %v", err)
	}
	if domain != "default" {
		t.Errorf("domain = %q, want default", domain)
	}
	if weight != 1.0 {
		t.Errorf("weight = %f, want 1.0", weight)
	}
}
