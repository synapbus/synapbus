package messaging

import (
	"context"
	"testing"
)

func TestPinStore_PinUnpin(t *testing.T) {
	db := newTestDB(t)
	s := NewPinStore(db)
	ctx := context.Background()

	if err := s.Pin(ctx, "1", 42, "human:algis", "always relevant"); err != nil {
		t.Fatalf("Pin: %v", err)
	}

	ids, err := s.ListForOwner(ctx, "1")
	if err != nil {
		t.Fatalf("ListForOwner: %v", err)
	}
	if len(ids) != 1 || ids[0] != 42 {
		t.Errorf("ListForOwner: got %v want [42]", ids)
	}

	// Re-pin updates note in place; no duplicate row.
	if err := s.Pin(ctx, "1", 42, "human:algis", "updated note"); err != nil {
		t.Fatalf("re-Pin: %v", err)
	}
	pins, err := s.ListPinsForOwner(ctx, "1")
	if err != nil {
		t.Fatalf("ListPinsForOwner: %v", err)
	}
	if len(pins) != 1 || pins[0].Note != "updated note" {
		t.Errorf("Re-pin: unexpected pins %v", pins)
	}

	if err := s.Unpin(ctx, "1", 42); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	ids, _ = s.ListForOwner(ctx, "1")
	if len(ids) != 0 {
		t.Errorf("after Unpin: want empty, got %v", ids)
	}

	// Unpin on missing row is a no-op.
	if err := s.Unpin(ctx, "1", 42); err != nil {
		t.Errorf("Unpin on missing: %v", err)
	}
}

func TestPinStore_OwnerScoping(t *testing.T) {
	db := newTestDB(t)
	s := NewPinStore(db)
	ctx := context.Background()

	if err := s.Pin(ctx, "1", 10, "human:1", ""); err != nil {
		t.Fatalf("Pin 1: %v", err)
	}
	if err := s.Pin(ctx, "1", 20, "human:1", ""); err != nil {
		t.Fatalf("Pin 1: %v", err)
	}
	if err := s.Pin(ctx, "2", 30, "human:2", ""); err != nil {
		t.Fatalf("Pin 2: %v", err)
	}

	one, err := s.ListForOwner(ctx, "1")
	if err != nil {
		t.Fatalf("ListForOwner 1: %v", err)
	}
	if len(one) != 2 {
		t.Errorf("owner 1: want 2 pins, got %d", len(one))
	}

	two, err := s.ListForOwner(ctx, "2")
	if err != nil {
		t.Fatalf("ListForOwner 2: %v", err)
	}
	if len(two) != 1 || two[0] != 30 {
		t.Errorf("owner 2: got %v want [30]", two)
	}
}
