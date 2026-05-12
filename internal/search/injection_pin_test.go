package search

import (
	"context"
	"testing"
)

// stubPinProvider lets a test return a fixed pin set.
type stubPinProvider struct {
	ids []int64
}

func (s *stubPinProvider) ListForOwner(ctx context.Context, ownerID string) ([]int64, error) {
	return s.ids, nil
}

// stubMessageLookup loads pinned messages by id.
type stubMessageLookup struct {
	byID map[int64]MemoryItem
}

func (s *stubMessageLookup) LookupForInjection(ctx context.Context, ids []int64) ([]MemoryItem, error) {
	out := make([]MemoryItem, 0, len(ids))
	for _, id := range ids {
		if m, ok := s.byID[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// stubStatusProvider returns canned statuses.
type stubStatusProvider struct {
	byID map[int64]MemoryStatusInfo
}

func (s *stubStatusProvider) Statuses(ctx context.Context, msgIDs []int64) (map[int64]MemoryStatusInfo, error) {
	out := map[int64]MemoryStatusInfo{}
	for _, id := range msgIDs {
		if v, ok := s.byID[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

func TestBuildContextPacket_PinOverlaySurfacesBelowFloor(t *testing.T) {
	svc, _, db := newTestServices(t)
	ctx := context.Background()

	a := seedOwnedAgent(t, db, 1, "alice", "a1")
	// Seed a channel + message so search has something to find for the
	// owner; the pin will be a separate id we splice in via lookup.
	seedChannel(t, db, 1, "open-brain", "a1")
	regularID := seedChannelMessage(t, db, 1, "a1", "some unrelated body")

	pinnedID := regularID + 1000
	lookup := &stubMessageLookup{
		byID: map[int64]MemoryItem{
			pinnedID: {ID: pinnedID, FromAgent: "a1", Body: "pinned fact", Score: 0.0},
		},
	}

	pkt, err := BuildContextPacket(ctx, svc, a, "kuzu unrelated query", InjectionOpts{
		BudgetTokens:  500,
		MaxItems:      5,
		MinScore:      0.95, // very high floor → drops everything from search
		PinProvider:   &stubPinProvider{ids: []int64{pinnedID}},
		MessageLookup: lookup,
	})
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt == nil {
		t.Fatal("expected non-nil packet (pinned overlay)")
	}

	sawPinned := false
	for _, m := range pkt.Memories {
		if m.ID == pinnedID {
			sawPinned = true
			if !m.Pinned {
				t.Errorf("pinned item must be marked Pinned=true: %+v", m)
			}
			if m.Score < 0.99 {
				t.Errorf("pinned item must have Score=1.0, got %v", m.Score)
			}
		}
	}
	if !sawPinned {
		t.Errorf("pinned message id=%d not surfaced in packet: %#v", pinnedID, pkt.Memories)
	}
}

func TestBuildContextPacket_StatusFilterDropsSoftDeleted(t *testing.T) {
	svc, _, db := newTestServices(t)
	ctx := context.Background()
	a := seedOwnedAgent(t, db, 1, "alice", "a1")
	seedChannel(t, db, 1, "open-brain", "a1")

	keepID := seedChannelMessage(t, db, 1, "a1", "active fact about Kuzu")
	dropID := seedChannelMessage(t, db, 1, "a1", "duplicate Kuzu fact")

	statusProv := &stubStatusProvider{
		byID: map[int64]MemoryStatusInfo{
			dropID: {Status: "soft_deleted"},
		},
	}

	pkt, err := BuildContextPacket(ctx, svc, a, "Kuzu", InjectionOpts{
		BudgetTokens:   500,
		MaxItems:       5,
		MinScore:       0.0,
		StatusProvider: statusProv,
	})
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt == nil {
		t.Fatal("expected non-nil packet")
	}
	for _, m := range pkt.Memories {
		if m.ID == dropID {
			t.Errorf("soft-deleted message id=%d should have been dropped", dropID)
		}
	}
	// keep should still be present
	sawKeep := false
	for _, m := range pkt.Memories {
		if m.ID == keepID {
			sawKeep = true
		}
	}
	if !sawKeep {
		t.Errorf("active message id=%d should be present, packet=%#v", keepID, pkt.Memories)
	}
}
