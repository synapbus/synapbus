package messaging

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// insertJob seeds a memory_consolidation_jobs row and returns its id.
// Dispatch tokens FK-reference this row.
func insertJob(t *testing.T, db *sql.DB, ownerID, jobType string) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO memory_consolidation_jobs (owner_id, job_type, trigger_reason)
		 VALUES (?, ?, ?)`,
		ownerID, jobType, "manual:test",
	)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestDispatchTokenStore_IssueAndValidate(t *testing.T) {
	db := newTestDB(t)
	store := NewDispatchTokenStore(db)
	ctx := context.Background()

	jobID := insertJob(t, db, "1", "reflection")

	token, expiresAt, err := store.Issue(ctx, "1", jobID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" {
		t.Fatal("Issue returned empty token")
	}
	if !expiresAt.After(time.Now()) {
		t.Errorf("expiresAt = %v, want > now()", expiresAt)
	}

	ok, err := store.Validate(ctx, token, "1", jobID)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !ok {
		t.Fatal("Validate returned false for a freshly-issued token")
	}

	// Second Validate must also succeed (token is bound to one job;
	// multiple tool calls within the job are expected per R7).
	ok, err = store.Validate(ctx, token, "1", jobID)
	if err != nil {
		t.Fatalf("second Validate: %v", err)
	}
	if !ok {
		t.Error("second Validate returned false (token should remain valid within its job)")
	}
}

func TestDispatchTokenStore_RejectCases(t *testing.T) {
	db := newTestDB(t)
	store := NewDispatchTokenStore(db)
	ctx := context.Background()

	jobID := insertJob(t, db, "1", "reflection")
	otherJobID := insertJob(t, db, "1", "core_rewrite")

	mintFresh := func(t *testing.T) string {
		t.Helper()
		tok, _, err := store.Issue(ctx, "1", jobID)
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		return tok
	}

	tests := []struct {
		name      string
		setup     func(t *testing.T) (token, ownerID string, job int64)
		wantValid bool
	}{
		{
			name: "happy path",
			setup: func(t *testing.T) (string, string, int64) {
				return mintFresh(t), "1", jobID
			},
			wantValid: true,
		},
		{
			name: "owner mismatch",
			setup: func(t *testing.T) (string, string, int64) {
				return mintFresh(t), "2", jobID
			},
			wantValid: false,
		},
		{
			name: "wrong job",
			setup: func(t *testing.T) (string, string, int64) {
				return mintFresh(t), "1", otherJobID
			},
			wantValid: false,
		},
		{
			name: "expired",
			setup: func(t *testing.T) (string, string, int64) {
				tok := mintFresh(t)
				// Forcibly expire the token by direct UPDATE.
				if _, err := db.Exec(
					`UPDATE memory_dispatch_tokens SET expires_at = ? WHERE token = ?`,
					time.Now().Add(-1*time.Minute).UTC(), tok,
				); err != nil {
					t.Fatalf("expire token: %v", err)
				}
				return tok, "1", jobID
			},
			wantValid: false,
		},
		{
			name: "revoked",
			setup: func(t *testing.T) (string, string, int64) {
				tok := mintFresh(t)
				if err := store.Revoke(ctx, tok); err != nil {
					t.Fatalf("Revoke: %v", err)
				}
				return tok, "1", jobID
			},
			wantValid: false,
		},
		{
			name: "unknown token",
			setup: func(t *testing.T) (string, string, int64) {
				return "definitely-not-a-real-token", "1", jobID
			},
			wantValid: false,
		},
		{
			name: "empty token",
			setup: func(t *testing.T) (string, string, int64) {
				return "", "1", jobID
			},
			wantValid: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tok, owner, job := tc.setup(t)
			got, err := store.Validate(ctx, tok, owner, job)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if got != tc.wantValid {
				t.Errorf("Validate = %v, want %v", got, tc.wantValid)
			}
		})
	}
}

func TestDispatchTokenStore_RevokeIdempotent(t *testing.T) {
	db := newTestDB(t)
	store := NewDispatchTokenStore(db)
	ctx := context.Background()

	jobID := insertJob(t, db, "1", "reflection")
	tok, _, err := store.Issue(ctx, "1", jobID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := store.Revoke(ctx, tok); err != nil {
		t.Fatalf("first Revoke: %v", err)
	}
	if err := store.Revoke(ctx, tok); err != nil {
		t.Fatalf("second Revoke: %v", err)
	}
	if err := store.Revoke(ctx, "unknown"); err != nil {
		t.Fatalf("Revoke unknown: %v", err)
	}
}

func TestDispatchTokenStore_IssueRequiresArgs(t *testing.T) {
	db := newTestDB(t)
	store := NewDispatchTokenStore(db)
	ctx := context.Background()

	if _, _, err := store.Issue(ctx, "", 1); err == nil {
		t.Error("Issue with empty owner should error")
	}
	if _, _, err := store.Issue(ctx, "1", 0); err == nil {
		t.Error("Issue with zero job id should error")
	}
}
