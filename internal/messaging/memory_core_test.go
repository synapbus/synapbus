package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCoreMemoryStore_GetSetRoundTrip(t *testing.T) {
	db := newTestDB(t)
	store := NewCoreMemoryStore(db, 2048)
	ctx := context.Background()

	const owner = "1"
	const agent = "research-mcpproxy"
	const blob = "You are research-mcpproxy. Focus on benchmarking."

	if err := store.Set(ctx, owner, agent, blob, "human"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, updatedAt, ok, err := store.Get(ctx, owner, agent)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: expected ok=true, got false")
	}
	if got != blob {
		t.Errorf("Get blob mismatch: got %q want %q", got, blob)
	}
	if updatedAt.IsZero() {
		t.Error("Get: expected non-zero updated_at")
	}
}

func TestCoreMemoryStore_SetReplacesWholesale(t *testing.T) {
	db := newTestDB(t)
	store := NewCoreMemoryStore(db, 2048)
	ctx := context.Background()

	const owner = "1"
	const agent = "alpha"

	if err := store.Set(ctx, owner, agent, "first version", "human"); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := store.Set(ctx, owner, agent, "second version", "dream:1"); err != nil {
		t.Fatalf("second Set: %v", err)
	}

	got, _, ok, err := store.Get(ctx, owner, agent)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || got != "second version" {
		t.Errorf("expected wholesale replace, got ok=%v blob=%q", ok, got)
	}
	if strings.Contains(got, "first") {
		t.Errorf("expected merge to NOT happen; got %q", got)
	}
}

func TestCoreMemoryStore_SetRejectsOverSize(t *testing.T) {
	db := newTestDB(t)
	store := NewCoreMemoryStore(db, 16)
	ctx := context.Background()

	tooBig := strings.Repeat("x", 17)
	err := store.Set(ctx, "1", "alpha", tooBig, "human")
	if err == nil {
		t.Fatal("Set: expected ErrCoreMemoryTooLarge, got nil")
	}
	if !errors.Is(err, ErrCoreMemoryTooLarge) {
		t.Errorf("Set: expected ErrCoreMemoryTooLarge, got %v", err)
	}

	// Exactly at-cap is fine.
	atCap := strings.Repeat("x", 16)
	if err := store.Set(ctx, "1", "alpha", atCap, "human"); err != nil {
		t.Errorf("Set at cap: unexpected error %v", err)
	}
}

func TestCoreMemoryStore_Delete(t *testing.T) {
	db := newTestDB(t)
	store := NewCoreMemoryStore(db, 2048)
	ctx := context.Background()

	if err := store.Set(ctx, "1", "alpha", "blob", "human"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Delete(ctx, "1", "alpha"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, _, ok, err := store.Get(ctx, "1", "alpha")
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if ok {
		t.Error("Get after Delete: expected ok=false")
	}

	// Delete on missing row is idempotent.
	if err := store.Delete(ctx, "1", "alpha"); err != nil {
		t.Errorf("Delete on missing: unexpected error %v", err)
	}
}

func TestCoreMemoryStore_OwnerScoping(t *testing.T) {
	db := newTestDB(t)
	store := NewCoreMemoryStore(db, 2048)
	ctx := context.Background()

	if err := store.Set(ctx, "1", "shared-name", "H1 blob", "h1"); err != nil {
		t.Fatalf("Set H1: %v", err)
	}
	if err := store.Set(ctx, "2", "shared-name", "H2 blob", "h2"); err != nil {
		t.Fatalf("Set H2: %v", err)
	}

	got1, _, ok1, err := store.Get(ctx, "1", "shared-name")
	if err != nil || !ok1 || got1 != "H1 blob" {
		t.Errorf("H1 Get: ok=%v got=%q err=%v", ok1, got1, err)
	}
	got2, _, ok2, err := store.Get(ctx, "2", "shared-name")
	if err != nil || !ok2 || got2 != "H2 blob" {
		t.Errorf("H2 Get: ok=%v got=%q err=%v", ok2, got2, err)
	}

	// H1 cannot see H2's blob and vice versa — distinct PKs.
	if got1 == got2 {
		t.Error("owner scoping broken: H1 and H2 see the same blob")
	}

	// List is scoped to a single owner.
	listH1, err := store.List(ctx, "1")
	if err != nil {
		t.Fatalf("List H1: %v", err)
	}
	if len(listH1) != 1 || listH1[0].OwnerID != "1" {
		t.Errorf("List H1: expected 1 row owned by 1, got %#v", listH1)
	}
}

func TestCoreMemoryStore_GetForInjection(t *testing.T) {
	db := newTestDB(t)
	store := NewCoreMemoryStore(db, 2048)
	ctx := context.Background()

	// Missing row → "" + no error (NOT sql.ErrNoRows).
	blob, err := store.GetForInjection(ctx, "1", "missing-agent")
	if err != nil {
		t.Fatalf("GetForInjection on missing: unexpected error %v", err)
	}
	if blob != "" {
		t.Errorf("GetForInjection on missing: expected \"\" got %q", blob)
	}

	// Present row → blob.
	if err := store.Set(ctx, "1", "alpha", "core blob", "human"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	blob, err = store.GetForInjection(ctx, "1", "alpha")
	if err != nil {
		t.Fatalf("GetForInjection: %v", err)
	}
	if blob != "core blob" {
		t.Errorf("GetForInjection: got %q want %q", blob, "core blob")
	}

	// Adapter satisfies search.CoreMemoryProvider implicitly.
	provider := NewCoreProvider(store)
	got, err := provider.Get(ctx, "1", "alpha")
	if err != nil || got != "core blob" {
		t.Errorf("CoreProvider.Get: got %q err=%v", got, err)
	}
}
