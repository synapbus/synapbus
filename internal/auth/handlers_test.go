package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func setupHandlers(t *testing.T) (*Handlers, *sql.DB) {
	t.Helper()
	db := newTestDB(t)

	cfg := DefaultConfig()
	cfg.BcryptCost = 10 // faster for tests
	cfg.Secret = make([]byte, 32)
	for i := range cfg.Secret {
		cfg.Secret[i] = byte(i)
	}

	userStore := NewSQLiteUserStore(db, cfg.BcryptCost)
	sessionStore := NewSQLiteSessionStore(db)
	clientStore := NewSQLiteClientStore(db, cfg.BcryptCost)
	fositeStore := NewFositeStore(db, cfg.BcryptCost)
	provider := NewOAuthProvider(cfg, fositeStore)
	handlers := NewHandlers(userStore, sessionStore, clientStore, provider, cfg)

	return handlers, db
}

func TestHandlers_Register(t *testing.T) {
	h, _ := setupHandlers(t)

	t.Run("successful registration", func(t *testing.T) {
		body := `{"username":"newuser","password":"password123","display_name":"New User"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleRegister(rr, req)

		if rr.Code != http.StatusCreated {
			t.Errorf("status = %d, want %d. Body: %s", rr.Code, http.StatusCreated, rr.Body.String())
		}

		var user User
		if err := json.NewDecoder(rr.Body).Decode(&user); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if user.Username != "newuser" {
			t.Errorf("Username = %q, want %q", user.Username, "newuser")
		}
		if user.DisplayName != "New User" {
			t.Errorf("DisplayName = %q, want %q", user.DisplayName, "New User")
		}
	})

	t.Run("duplicate username returns 409", func(t *testing.T) {
		body := `{"username":"newuser","password":"password123"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleRegister(rr, req)

		if rr.Code != http.StatusConflict {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusConflict)
		}
	})

	t.Run("short password returns 400", func(t *testing.T) {
		body := `{"username":"shortpw","password":"short"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleRegister(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	})

	t.Run("invalid username returns 400", func(t *testing.T) {
		body := `{"username":"ab","password":"password123"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleRegister(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString("{invalid"))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleRegister(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	})
}

func TestHandlers_Login(t *testing.T) {
	h, db := setupHandlers(t)
	ctx := context.Background()

	// Create a test user
	userStore := NewSQLiteUserStore(db, 10)
	userStore.CreateUser(ctx, "loginuser", "password123", "Login User")

	t.Run("successful login", func(t *testing.T) {
		body := `{"username":"loginuser","password":"password123"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleLogin(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d. Body: %s", rr.Code, http.StatusOK, rr.Body.String())
		}

		// Check session cookie
		cookies := rr.Result().Cookies()
		var sessionCookie *http.Cookie
		for _, c := range cookies {
			if c.Name == SessionCookieName {
				sessionCookie = c
				break
			}
		}
		if sessionCookie == nil {
			t.Fatal("session cookie not set")
		}
		if !sessionCookie.HttpOnly {
			t.Error("session cookie should be HttpOnly")
		}
		if sessionCookie.SameSite != http.SameSiteLaxMode {
			t.Error("session cookie should be SameSite=Lax")
		}
	})

	t.Run("wrong password returns 401", func(t *testing.T) {
		body := `{"username":"loginuser","password":"wrongpassword"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleLogin(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})

	t.Run("non-existent user returns 401", func(t *testing.T) {
		body := `{"username":"nobody","password":"password123"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.HandleLogin(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})
}

func TestHandlers_Logout(t *testing.T) {
	h, db := setupHandlers(t)
	ctx := context.Background()

	userStore := NewSQLiteUserStore(db, 10)
	sessStore := NewSQLiteSessionStore(db)

	user, _ := userStore.CreateUser(ctx, "logoutuser", "password123", "")
	session, _ := sessStore.CreateSession(ctx, user.ID, 24*time.Hour)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.SessionID})
	// Add user to context (normally done by middleware)
	req = req.WithContext(ContextWithUser(req.Context(), user))
	rr := httptest.NewRecorder()

	h.HandleLogout(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Cookie should be cleared
	cookies := rr.Result().Cookies()
	for _, c := range cookies {
		if c.Name == SessionCookieName && c.MaxAge != -1 {
			t.Error("session cookie should be cleared (MaxAge = -1)")
		}
	}

	// Session should be deleted
	_, err := sessStore.GetSession(ctx, session.SessionID)
	if err != ErrSessionNotFound {
		t.Errorf("session should be deleted after logout")
	}
}

func TestHandlers_Me(t *testing.T) {
	h, db := setupHandlers(t)
	ctx := context.Background()

	userStore := NewSQLiteUserStore(db, 10)
	user, _ := userStore.CreateUser(ctx, "meuser", "password123", "Me User")

	t.Run("authenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
		req = req.WithContext(ContextWithUser(req.Context(), user))
		rr := httptest.NewRecorder()

		h.HandleMe(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
		}

		var resp User
		json.NewDecoder(rr.Body).Decode(&resp)
		if resp.Username != "meuser" {
			t.Errorf("Username = %q, want %q", resp.Username, "meuser")
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
		rr := httptest.NewRecorder()

		h.HandleMe(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})
}

func TestHandlers_ChangePassword(t *testing.T) {
	h, db := setupHandlers(t)
	ctx := context.Background()

	userStore := NewSQLiteUserStore(db, 10)
	sessStore := NewSQLiteSessionStore(db)

	user, _ := userStore.CreateUser(ctx, "chpwuser", "oldpassword1", "")
	session, _ := sessStore.CreateSession(ctx, user.ID, 24*time.Hour)
	// Create another session that should be invalidated
	sess2, _ := sessStore.CreateSession(ctx, user.ID, 24*time.Hour)

	t.Run("successful password change", func(t *testing.T) {
		body := `{"current_password":"oldpassword1","new_password":"newpassword1"}`
		req := httptest.NewRequest(http.MethodPut, "/auth/password", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(ContextWithUser(req.Context(), user))
		req = req.WithContext(ContextWithSessionID(req.Context(), session.SessionID))
		rr := httptest.NewRecorder()

		h.HandleChangePassword(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d. Body: %s", rr.Code, http.StatusOK, rr.Body.String())
		}

		// Verify old password no longer works
		_, err := userStore.VerifyPassword(ctx, "chpwuser", "oldpassword1")
		if err != ErrInvalidPassword {
			t.Error("old password should not work")
		}

		// Verify new password works
		_, err = userStore.VerifyPassword(ctx, "chpwuser", "newpassword1")
		if err != nil {
			t.Errorf("new password should work: %v", err)
		}

		// Other sessions should be invalidated
		_, err = sessStore.GetSession(ctx, sess2.SessionID)
		if err != ErrSessionNotFound {
			t.Error("other sessions should be invalidated after password change")
		}

		// Current session should still exist
		_, err = sessStore.GetSession(ctx, session.SessionID)
		if err != nil {
			t.Errorf("current session should still exist: %v", err)
		}
	})

	t.Run("wrong current password returns 403", func(t *testing.T) {
		body := `{"current_password":"wrongpassword","new_password":"newpassword2"}`
		req := httptest.NewRequest(http.MethodPut, "/auth/password", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(ContextWithUser(req.Context(), user))
		rr := httptest.NewRecorder()

		h.HandleChangePassword(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
		}
	})

	t.Run("short new password returns 400", func(t *testing.T) {
		body := `{"current_password":"newpassword1","new_password":"short"}`
		req := httptest.NewRequest(http.MethodPut, "/auth/password", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(ContextWithUser(req.Context(), user))
		rr := httptest.NewRecorder()

		h.HandleChangePassword(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	})
}
