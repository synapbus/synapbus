package auth

import (
	"context"
	"testing"
	"time"
)

func TestSessionStore_CreateAndGet(t *testing.T) {
	db := newTestDB(t)
	userStore := NewSQLiteUserStore(db, 10)
	sessStore := NewSQLiteSessionStore(db)
	ctx := context.Background()

	user, err := userStore.CreateUser(ctx, "sessuser", "password123", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	session, err := sessStore.CreateSession(ctx, user.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if session.SessionID == "" {
		t.Error("SessionID should not be empty")
	}
	if session.UserID != user.ID {
		t.Errorf("UserID = %d, want %d", session.UserID, user.ID)
	}

	// Get session
	got, err := sessStore.GetSession(ctx, session.SessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.UserID != user.ID {
		t.Errorf("UserID = %d, want %d", got.UserID, user.ID)
	}
}

func TestSessionStore_NotFound(t *testing.T) {
	db := newTestDB(t)
	sessStore := NewSQLiteSessionStore(db)
	ctx := context.Background()

	_, err := sessStore.GetSession(ctx, "nonexistent-session-id")
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestSessionStore_Expired(t *testing.T) {
	db := newTestDB(t)
	userStore := NewSQLiteUserStore(db, 10)
	sessStore := NewSQLiteSessionStore(db)
	ctx := context.Background()

	user, _ := userStore.CreateUser(ctx, "expuser", "password123", "")

	// Create a session with very short lifetime
	session, err := sessStore.CreateSession(ctx, user.ID, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Wait for expiry
	time.Sleep(10 * time.Millisecond)

	_, err = sessStore.GetSession(ctx, session.SessionID)
	if err != ErrSessionExpired {
		t.Errorf("expected ErrSessionExpired, got %v", err)
	}
}

func TestSessionStore_Delete(t *testing.T) {
	db := newTestDB(t)
	userStore := NewSQLiteUserStore(db, 10)
	sessStore := NewSQLiteSessionStore(db)
	ctx := context.Background()

	user, _ := userStore.CreateUser(ctx, "deluser", "password123", "")

	session, _ := sessStore.CreateSession(ctx, user.ID, 24*time.Hour)

	if err := sessStore.DeleteSession(ctx, session.SessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	_, err := sessStore.GetSession(ctx, session.SessionID)
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound after delete, got %v", err)
	}
}

func TestSessionStore_DeleteByUser(t *testing.T) {
	db := newTestDB(t)
	userStore := NewSQLiteUserStore(db, 10)
	sessStore := NewSQLiteSessionStore(db)
	ctx := context.Background()

	user, _ := userStore.CreateUser(ctx, "delbyuser", "password123", "")

	sess1, _ := sessStore.CreateSession(ctx, user.ID, 24*time.Hour)
	sess2, _ := sessStore.CreateSession(ctx, user.ID, 24*time.Hour)

	if err := sessStore.DeleteSessionsByUser(ctx, user.ID); err != nil {
		t.Fatalf("DeleteSessionsByUser: %v", err)
	}

	_, err := sessStore.GetSession(ctx, sess1.SessionID)
	if err != ErrSessionNotFound {
		t.Errorf("session 1 should be deleted")
	}
	_, err = sessStore.GetSession(ctx, sess2.SessionID)
	if err != ErrSessionNotFound {
		t.Errorf("session 2 should be deleted")
	}
}

func TestSessionStore_DeleteByUserExcept(t *testing.T) {
	db := newTestDB(t)
	userStore := NewSQLiteUserStore(db, 10)
	sessStore := NewSQLiteSessionStore(db)
	ctx := context.Background()

	user, _ := userStore.CreateUser(ctx, "exceptuser", "password123", "")

	sess1, _ := sessStore.CreateSession(ctx, user.ID, 24*time.Hour)
	sess2, _ := sessStore.CreateSession(ctx, user.ID, 24*time.Hour)

	if err := sessStore.DeleteSessionsByUserExcept(ctx, user.ID, sess1.SessionID); err != nil {
		t.Fatalf("DeleteSessionsByUserExcept: %v", err)
	}

	// Session 1 should still exist
	_, err := sessStore.GetSession(ctx, sess1.SessionID)
	if err != nil {
		t.Errorf("session 1 should still exist: %v", err)
	}

	// Session 2 should be deleted
	_, err = sessStore.GetSession(ctx, sess2.SessionID)
	if err != ErrSessionNotFound {
		t.Errorf("session 2 should be deleted")
	}
}

func TestSessionStore_CleanupExpired(t *testing.T) {
	db := newTestDB(t)
	userStore := NewSQLiteUserStore(db, 10)
	sessStore := NewSQLiteSessionStore(db)
	ctx := context.Background()

	user, _ := userStore.CreateUser(ctx, "cleanuser", "password123", "")

	// Create an expired session
	sessStore.CreateSession(ctx, user.ID, 1*time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	// Create a valid session
	sessStore.CreateSession(ctx, user.ID, 24*time.Hour)

	removed, err := sessStore.CleanupExpired(ctx)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
}
