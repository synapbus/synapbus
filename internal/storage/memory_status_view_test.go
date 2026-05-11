package storage

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// TestMemoryStatusView verifies the view's status / superseded_by /
// soft_deleted_at derivations against crafted memory_consolidation_jobs
// rows. See data-model.md §`memory_status` view.
func TestMemoryStatusView(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	finishedAt := time.Date(2026, 5, 11, 3, 0, 0, 0, time.UTC).Format("2006-01-02 15:04:05")

	// Job 1: completed mark_duplicate where keep_id=100, target=101 (loser).
	// → message 101 must be soft_deleted; 100 stays active.
	actionDup := `[{
	  "tool":"memory_mark_duplicate",
	  "target_message_id":101,
	  "args":{"a_id":100,"b_id":101,"keep_id":100,"reason":"shorter paraphrase"},
	  "at":"2026-05-11T03:00:00Z"
	}]`
	if _, err := db.Exec(
		`INSERT INTO memory_consolidation_jobs
		   (owner_id, job_type, status, trigger_reason, actions, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"1", "dedup_contradiction", "succeeded", "manual:test", actionDup, finishedAt,
	); err != nil {
		t.Fatalf("insert dup job: %v", err)
	}

	// Job 2: memory_supersede target=200, by=201. → 200 superseded.
	actionSup := `[{
	  "tool":"memory_supersede",
	  "target_message_id":200,
	  "args":{"a_id":200,"b_id":201,"reason":"newer fact"},
	  "at":"2026-05-11T03:00:00Z"
	}]`
	if _, err := db.Exec(
		`INSERT INTO memory_consolidation_jobs
		   (owner_id, job_type, status, trigger_reason, actions, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"1", "dedup_contradiction", "succeeded", "manual:test", actionSup, finishedAt,
	); err != nil {
		t.Fatalf("insert supersede job: %v", err)
	}

	// Failed job — must NOT appear in the view.
	if _, err := db.Exec(
		`INSERT INTO memory_consolidation_jobs
		   (owner_id, job_type, status, trigger_reason, actions, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"1", "reflection", "failed", "manual:test",
		`[{"tool":"memory_supersede","target_message_id":300,"args":{"b_id":301}}]`,
		finishedAt,
	); err != nil {
		t.Fatalf("insert failed job: %v", err)
	}

	statuses, supersededBy, deletedAt := readStatuses(t, db, []int64{100, 101, 200, 201, 300})

	if got := statuses[100]; got != "" {
		// 100 has no action row → not in view → empty string from the
		// helper's zero-value default.
		t.Errorf("100: want active/missing, got %q", got)
	}
	if got := statuses[101]; got != "soft_deleted" {
		t.Errorf("101: want soft_deleted, got %q", got)
	}
	if got := deletedAt[101]; got == "" {
		t.Errorf("101: expected non-empty soft_deleted_at")
	}
	if got := statuses[200]; got != "superseded" {
		t.Errorf("200: want superseded, got %q", got)
	}
	if got := supersededBy[200]; got != 201 {
		t.Errorf("200.superseded_by: want 201, got %d", got)
	}
	// 300 was on a failed job → should not appear in view.
	if got := statuses[300]; got != "" {
		t.Errorf("300: want missing (failed job), got %q", got)
	}
}

func readStatuses(t *testing.T, db *sql.DB, ids []int64) (map[int64]string, map[int64]int64, map[int64]string) {
	t.Helper()
	statuses := map[int64]string{}
	supersededBy := map[int64]int64{}
	deletedAt := map[int64]string{}

	rows, err := db.Query(
		`SELECT message_id, status, COALESCE(superseded_by, 0), COALESCE(soft_deleted_at, '')
		   FROM memory_status`,
	)
	if err != nil {
		t.Fatalf("query view: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id        int64
			status    string
			sup       int64
			deletedTs string
		)
		if err := rows.Scan(&id, &status, &sup, &deletedTs); err != nil {
			t.Fatalf("scan view: %v", err)
		}
		statuses[id] = status
		supersededBy[id] = sup
		deletedAt[id] = deletedTs
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	_ = ids
	return statuses, supersededBy, deletedAt
}
