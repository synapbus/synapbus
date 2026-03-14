package auth

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/storage"
	"golang.org/x/crypto/bcrypt"
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

func TestUserStore_CreateUser(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteUserStore(db, 10) // use cost 10 for test speed
	ctx := context.Background()

	t.Run("valid user", func(t *testing.T) {
		user, err := store.CreateUser(ctx, "testuser", "password123", "Test User")
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		if user.ID == 0 {
			t.Error("expected non-zero user ID")
		}
		if user.Username != "testuser" {
			t.Errorf("Username = %q, want %q", user.Username, "testuser")
		}
		if user.DisplayName != "Test User" {
			t.Errorf("DisplayName = %q, want %q", user.DisplayName, "Test User")
		}
		// First user should be admin
		if user.Role != RoleAdmin {
			t.Errorf("Role = %q, want %q (first user should be admin)", user.Role, RoleAdmin)
		}
	})

	t.Run("second user is regular", func(t *testing.T) {
		user, err := store.CreateUser(ctx, "testuser2", "password123", "Test User 2")
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		if user.Role != RoleUser {
			t.Errorf("Role = %q, want %q (second user should be regular)", user.Role, RoleUser)
		}
	})

	t.Run("bcrypt cost >= 10", func(t *testing.T) {
		user, err := store.GetUserByUsername(ctx, "testuser")
		if err != nil {
			t.Fatalf("GetUserByUsername: %v", err)
		}
		cost, err := bcrypt.Cost([]byte(user.PasswordHash))
		if err != nil {
			t.Fatalf("bcrypt.Cost: %v", err)
		}
		if cost < 10 {
			t.Errorf("bcrypt cost = %d, want >= 10", cost)
		}
	})
}

func TestUserStore_DuplicateUsername(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteUserStore(db, 10)
	ctx := context.Background()

	if _, err := store.CreateUser(ctx, "dupuser", "password123", ""); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	_, err := store.CreateUser(ctx, "dupuser", "password456", "")
	if err != ErrDuplicateUsername {
		t.Errorf("expected ErrDuplicateUsername, got %v", err)
	}
}

func TestUserStore_InvalidUsername(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteUserStore(db, 10)
	ctx := context.Background()

	tests := []struct {
		name     string
		username string
	}{
		{"too short", "ab"},
		{"has spaces", "hello world"},
		{"has special chars", "user@name"},
		{"has dash", "user-name"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.CreateUser(ctx, tt.username, "password123", "")
			if err != ErrInvalidUsername {
				t.Errorf("expected ErrInvalidUsername for %q, got %v", tt.username, err)
			}
		})
	}
}

func TestUserStore_PasswordValidation(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteUserStore(db, 10)
	ctx := context.Background()

	t.Run("too short", func(t *testing.T) {
		_, err := store.CreateUser(ctx, "shortpw", "short", "")
		if err != ErrPasswordTooShort {
			t.Errorf("expected ErrPasswordTooShort, got %v", err)
		}
	})

	t.Run("too long", func(t *testing.T) {
		longPW := make([]byte, 73)
		for i := range longPW {
			longPW[i] = 'a'
		}
		_, err := store.CreateUser(ctx, "longpw", string(longPW), "")
		if err != ErrPasswordTooLong {
			t.Errorf("expected ErrPasswordTooLong, got %v", err)
		}
	})
}

func TestUserStore_GetByID(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteUserStore(db, 10)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, "getbyid", "password123", "Get By ID")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := store.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Username != "getbyid" {
		t.Errorf("Username = %q, want %q", got.Username, "getbyid")
	}

	// Non-existent user
	_, err = store.GetUserByID(ctx, 99999)
	if err != ErrUserNotFound {
		t.Errorf("expected ErrUserNotFound, got %v", err)
	}
}

func TestUserStore_VerifyPassword(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteUserStore(db, 10)
	ctx := context.Background()

	store.CreateUser(ctx, "verifypw", "correctpassword", "")

	t.Run("correct password", func(t *testing.T) {
		user, err := store.VerifyPassword(ctx, "verifypw", "correctpassword")
		if err != nil {
			t.Fatalf("VerifyPassword: %v", err)
		}
		if user.Username != "verifypw" {
			t.Errorf("Username = %q, want %q", user.Username, "verifypw")
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		_, err := store.VerifyPassword(ctx, "verifypw", "wrongpassword")
		if err != ErrInvalidPassword {
			t.Errorf("expected ErrInvalidPassword, got %v", err)
		}
	})

	t.Run("non-existent user", func(t *testing.T) {
		_, err := store.VerifyPassword(ctx, "nonexistent", "password")
		if err != ErrInvalidPassword {
			t.Errorf("expected ErrInvalidPassword, got %v", err)
		}
	})
}

func TestUserStore_UpdatePassword(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteUserStore(db, 10)
	ctx := context.Background()

	user, _ := store.CreateUser(ctx, "updatepw", "oldpassword1", "")

	t.Run("successful update", func(t *testing.T) {
		err := store.UpdatePassword(ctx, user.ID, "newpassword1")
		if err != nil {
			t.Fatalf("UpdatePassword: %v", err)
		}

		// Old password should fail
		_, err = store.VerifyPassword(ctx, "updatepw", "oldpassword1")
		if err != ErrInvalidPassword {
			t.Error("old password should not work after update")
		}

		// New password should work
		_, err = store.VerifyPassword(ctx, "updatepw", "newpassword1")
		if err != nil {
			t.Errorf("new password should work: %v", err)
		}
	})

	t.Run("too short new password", func(t *testing.T) {
		err := store.UpdatePassword(ctx, user.ID, "short")
		if err != ErrPasswordTooShort {
			t.Errorf("expected ErrPasswordTooShort, got %v", err)
		}
	})

	t.Run("non-existent user", func(t *testing.T) {
		err := store.UpdatePassword(ctx, 99999, "password123")
		if err != ErrUserNotFound {
			t.Errorf("expected ErrUserNotFound, got %v", err)
		}
	})
}

func TestUserStore_ListUsers(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteUserStore(db, 10)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		store.CreateUser(ctx, fmt.Sprintf("listuser%d", i), "password123", "")
	}

	users, err := store.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 3 {
		t.Errorf("got %d users, want 3", len(users))
	}
}

func TestUserStore_CountUsers(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteUserStore(db, 10)
	ctx := context.Background()

	count, err := store.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 0 {
		t.Errorf("initial count = %d, want 0", count)
	}

	store.CreateUser(ctx, "countuser", "password123", "")

	count, err = store.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 1 {
		t.Errorf("count after create = %d, want 1", count)
	}
}
