package messaging

import (
	"context"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMemoryInjections_RecordAndList(t *testing.T) {
	db := newTestDB(t)
	store := NewMemoryInjections(db)
	ctx := context.Background()

	rec := InjectionRecord{
		OwnerID:          "1",
		AgentName:        "research-mcpproxy",
		ToolName:         "my_status",
		PacketSizeChars:  412,
		PacketItemsCount: 3,
		MessageIDs:       []int64{1, 2, 3},
		CoreBlobIncluded: true,
	}
	if err := store.Record(ctx, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := store.ListRecent(ctx, "1", 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListRecent returned %d rows, want 1", len(got))
	}
	if got[0].PacketSizeChars != 412 || got[0].PacketItemsCount != 3 {
		t.Errorf("packet counters mismatch: %+v", got[0])
	}
	if len(got[0].MessageIDs) != 3 || got[0].MessageIDs[0] != 1 {
		t.Errorf("MessageIDs round-trip failed: %v", got[0].MessageIDs)
	}
	if !got[0].CoreBlobIncluded {
		t.Error("CoreBlobIncluded round-trip failed")
	}
}

func TestMemoryInjections_CleanupOnlyOldRows(t *testing.T) {
	db := newTestDB(t)
	store := NewMemoryInjections(db)
	ctx := context.Background()

	now := time.Now().UTC()
	tests := []struct {
		name     string
		offset   time.Duration
		wantKept bool
	}{
		{"3 days old", -72 * time.Hour, false},
		{"36 hours old", -36 * time.Hour, false},
		{"23 hours old", -23 * time.Hour, true},
		{"30 minutes old", -30 * time.Minute, true},
		{"current", 0, true},
	}

	// Seed 50 rows: 10 per bucket so we exercise the DELETE plan.
	for _, tc := range tests {
		for i := 0; i < 10; i++ {
			rec := InjectionRecord{
				OwnerID:          "1",
				AgentName:        "a",
				ToolName:         "my_status",
				PacketSizeChars:  100,
				PacketItemsCount: 1,
				MessageIDs:       []int64{int64(i)},
				CreatedAt:        now.Add(tc.offset),
			}
			if err := store.Record(ctx, rec); err != nil {
				t.Fatalf("Record %s: %v", tc.name, err)
			}
		}
	}

	deleted, err := store.Cleanup(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// 2 buckets older than 24h × 10 rows = 20 expected deletions.
	if deleted != 20 {
		t.Errorf("Cleanup removed %d rows, want 20", deleted)
	}

	remaining, err := store.ListRecent(ctx, "1", 100)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(remaining) != 30 {
		t.Errorf("after cleanup got %d rows, want 30", len(remaining))
	}
}

func TestMemoryInjections_ListRecent_OwnerScoped(t *testing.T) {
	db := newTestDB(t)
	store := NewMemoryInjections(db)
	ctx := context.Background()

	for _, owner := range []string{"1", "2", "3"} {
		for i := 0; i < 5; i++ {
			if err := store.Record(ctx, InjectionRecord{
				OwnerID:          owner,
				AgentName:        "a",
				ToolName:         "my_status",
				PacketSizeChars:  100,
				PacketItemsCount: 1,
				MessageIDs:       []int64{int64(i)},
			}); err != nil {
				t.Fatalf("Record owner=%s i=%d: %v", owner, i, err)
			}
		}
	}

	got, err := store.ListRecent(ctx, "2", 100)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("owner=2 got %d rows, want 5", len(got))
	}
	for _, r := range got {
		if r.OwnerID != "2" {
			t.Errorf("found leaked row OwnerID=%q in owner=2 listing", r.OwnerID)
		}
	}
}

func TestMemoryInjections_ListRecent_LimitOrdering(t *testing.T) {
	db := newTestDB(t)
	store := NewMemoryInjections(db)
	ctx := context.Background()

	base := time.Now().UTC().Add(-1 * time.Hour)
	for i := 0; i < 10; i++ {
		if err := store.Record(ctx, InjectionRecord{
			OwnerID:          "1",
			AgentName:        "a",
			ToolName:         "my_status",
			PacketSizeChars:  100,
			PacketItemsCount: 1,
			MessageIDs:       []int64{int64(i)},
			CreatedAt:        base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	got, err := store.ListRecent(ctx, "1", 3)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListRecent limit=3 returned %d", len(got))
	}
	// Newest first: i=9, 8, 7. Validate MessageIDs[0] descends.
	if got[0].MessageIDs[0] != 9 || got[1].MessageIDs[0] != 8 || got[2].MessageIDs[0] != 7 {
		t.Errorf("ordering wrong: %d, %d, %d", got[0].MessageIDs[0], got[1].MessageIDs[0], got[2].MessageIDs[0])
	}
}
