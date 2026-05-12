package messaging

import (
	"context"
	"errors"
	"testing"
)

func TestLinkStore_AddValidTypesPerActor(t *testing.T) {
	db := newTestDB(t)
	s := NewLinkStore(db)
	ctx := context.Background()

	// Agent may write semantic types.
	semantic := []string{"refines", "contradicts", "examples", "related"}
	src := int64(1)
	for i, rt := range semantic {
		dst := int64(100 + i)
		id, err := s.Add(ctx, src, dst, rt, "1", "agent:dream-algis:tok", nil)
		if err != nil {
			t.Fatalf("agent semantic %s: %v", rt, err)
		}
		if id == 0 {
			t.Fatalf("agent semantic %s: zero id", rt)
		}
	}

	// auto:<rule> may write auto types.
	autos := []string{"mention", "reply_to", "channel_cooccurrence"}
	for i, rt := range autos {
		dst := int64(200 + i)
		id, err := s.Add(ctx, src, dst, rt, "1", "auto:on-message-created", nil)
		if err != nil {
			t.Fatalf("auto %s: %v", rt, err)
		}
		if id == 0 {
			t.Fatalf("auto %s: zero id", rt)
		}
	}

	// human:<name> may write any type (no reserved-type guard).
	for i, rt := range []string{"refines", "duplicate_of", "mention"} {
		dst := int64(300 + i)
		if _, err := s.Add(ctx, src, dst, rt, "1", "human:algis", nil); err != nil {
			t.Errorf("human %s: unexpected err %v", rt, err)
		}
	}
}

func TestLinkStore_AddRejectsReservedFromAgent(t *testing.T) {
	db := newTestDB(t)
	s := NewLinkStore(db)
	ctx := context.Background()

	reserved := []string{"mention", "reply_to", "channel_cooccurrence", "duplicate_of", "superseded_by"}
	for i, rt := range reserved {
		dst := int64(400 + i)
		_, err := s.Add(ctx, 1, dst, rt, "1", "agent:dream:tok", nil)
		if err == nil {
			t.Errorf("agent %s: expected ErrLinkTypeReserved, got nil", rt)
			continue
		}
		if !errors.Is(err, ErrLinkTypeReserved) {
			t.Errorf("agent %s: expected ErrLinkTypeReserved, got %v", rt, err)
		}
	}
}

func TestLinkStore_AddRejectsNonAutoFromAutoActor(t *testing.T) {
	db := newTestDB(t)
	s := NewLinkStore(db)
	ctx := context.Background()

	for i, rt := range []string{"refines", "duplicate_of", "superseded_by", "related"} {
		dst := int64(500 + i)
		_, err := s.Add(ctx, 1, dst, rt, "1", "auto:something", nil)
		if !errors.Is(err, ErrLinkTypeReserved) {
			t.Errorf("auto actor %s: expected ErrLinkTypeReserved, got %v", rt, err)
		}
	}
}

func TestLinkStore_ListByMessageOwnerScoping(t *testing.T) {
	db := newTestDB(t)
	s := NewLinkStore(db)
	ctx := context.Background()

	if _, err := s.Add(ctx, 1, 2, "refines", "1", "agent:dream:tok", nil); err != nil {
		t.Fatalf("Add owner=1: %v", err)
	}
	if _, err := s.Add(ctx, 1, 3, "refines", "2", "agent:dream:tok", nil); err != nil {
		t.Fatalf("Add owner=2: %v", err)
	}

	// ListByMessage returns all links touching msg 1 regardless of owner —
	// owner-scoping happens in ListByOwner.
	ls, err := s.ListByMessage(ctx, 1)
	if err != nil {
		t.Fatalf("ListByMessage: %v", err)
	}
	if len(ls) != 2 {
		t.Errorf("ListByMessage: want 2, got %d", len(ls))
	}

	owner1, err := s.ListByOwner(ctx, "1", nil, 0)
	if err != nil {
		t.Fatalf("ListByOwner 1: %v", err)
	}
	if len(owner1) != 1 || owner1[0].DstMessageID != 2 {
		t.Errorf("ListByOwner 1: unexpected %v", owner1)
	}

	owner2, err := s.ListByOwner(ctx, "2", []string{"refines"}, 0)
	if err != nil {
		t.Fatalf("ListByOwner 2: %v", err)
	}
	if len(owner2) != 1 || owner2[0].DstMessageID != 3 {
		t.Errorf("ListByOwner 2: unexpected %v", owner2)
	}

	// Filtered ListByOwner with a non-matching type returns empty.
	none, err := s.ListByOwner(ctx, "1", []string{"contradicts"}, 0)
	if err != nil {
		t.Fatalf("ListByOwner filter: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("ListByOwner filter: want 0, got %d", len(none))
	}
}

func TestLinkStore_AddMetadata(t *testing.T) {
	db := newTestDB(t)
	s := NewLinkStore(db)
	ctx := context.Background()

	meta := map[string]any{"confidence": 0.84}
	id, err := s.Add(ctx, 10, 20, "refines", "1", "agent:dream:tok", meta)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	links, err := s.ListByMessage(ctx, 10)
	if err != nil {
		t.Fatalf("ListByMessage: %v", err)
	}
	if len(links) != 1 || links[0].ID != id {
		t.Fatalf("unexpected listing: %v", links)
	}
	if got, _ := links[0].Metadata["confidence"].(float64); got != 0.84 {
		t.Errorf("metadata round-trip: got %v", links[0].Metadata)
	}
}
