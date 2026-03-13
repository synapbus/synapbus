package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func setupOAuth(t *testing.T) (*Handlers, *SQLiteUserStore, *SQLiteClientStore, *SQLiteSessionStore) {
	t.Helper()
	db := newTestDB(t)

	cfg := DefaultConfig()
	cfg.BcryptCost = 10
	cfg.DevMode = true
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

	return handlers, userStore, clientStore, sessionStore
}

func TestOAuth_ClientCredentials(t *testing.T) {
	h, userStore, clientStore, _ := setupOAuth(t)
	ctx := context.Background()

	// Create owner user and client
	user, _ := userStore.CreateUser(ctx, "oauthowner", "password123", "")
	client, secret, err := clientStore.CreateClient(ctx, "test-client",
		[]string{}, []string{"client_credentials"}, []string{"read", "write"}, user.ID)
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}

	t.Run("valid client credentials", func(t *testing.T) {
		form := url.Values{}
		form.Set("grant_type", "client_credentials")
		form.Set("scope", "read write")

		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(client.ID, secret)
		rr := httptest.NewRecorder()

		h.HandleToken(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d. Body: %s", rr.Code, http.StatusOK, rr.Body.String())
			return
		}

		var resp map[string]interface{}
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}

		if resp["access_token"] == nil || resp["access_token"] == "" {
			t.Error("expected access_token in response")
		}
		if resp["token_type"] != "bearer" {
			t.Errorf("token_type = %v, want bearer", resp["token_type"])
		}
		if resp["expires_in"] == nil {
			t.Error("expected expires_in in response")
		}
	})

	t.Run("invalid client secret", func(t *testing.T) {
		form := url.Values{}
		form.Set("grant_type", "client_credentials")

		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(client.ID, "wrong-secret")
		rr := httptest.NewRecorder()

		h.HandleToken(rr, req)

		if rr.Code == http.StatusOK {
			t.Error("expected non-200 for invalid secret")
		}
	})

	t.Run("unknown client", func(t *testing.T) {
		form := url.Values{}
		form.Set("grant_type", "client_credentials")

		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth("nonexistent-client", "secret")
		rr := httptest.NewRecorder()

		h.HandleToken(rr, req)

		if rr.Code == http.StatusOK {
			t.Error("expected non-200 for unknown client")
		}
	})
}

func TestOAuth_TokenIntrospection(t *testing.T) {
	h, userStore, clientStore, _ := setupOAuth(t)
	ctx := context.Background()

	user, _ := userStore.CreateUser(ctx, "introowner", "password123", "")
	client, secret, _ := clientStore.CreateClient(ctx, "intro-client",
		[]string{}, []string{"client_credentials"}, []string{"read"}, user.ID)

	// First, get a token
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("scope", "read")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(client.ID, secret)
	rr := httptest.NewRecorder()

	h.HandleToken(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("token request failed: %d %s", rr.Code, rr.Body.String())
	}

	var tokenResp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&tokenResp)
	accessToken := tokenResp["access_token"].(string)

	t.Run("valid token introspection", func(t *testing.T) {
		form := url.Values{}
		form.Set("token", accessToken)

		req := httptest.NewRequest(http.MethodPost, "/oauth/introspect", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(client.ID, secret)
		rr := httptest.NewRecorder()

		h.HandleIntrospect(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d. Body: %s", rr.Code, http.StatusOK, rr.Body.String())
			return
		}

		var resp IntrospectionResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if !resp.Active {
			t.Error("token should be active")
		}
		if resp.ClientID != client.ID {
			t.Errorf("client_id = %q, want %q", resp.ClientID, client.ID)
		}
	})

	t.Run("invalid token introspection", func(t *testing.T) {
		form := url.Values{}
		form.Set("token", "invalid-token-value")

		req := httptest.NewRequest(http.MethodPost, "/oauth/introspect", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(client.ID, secret)
		rr := httptest.NewRecorder()

		h.HandleIntrospect(rr, req)

		var resp IntrospectionResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Active {
			t.Error("invalid token should not be active")
		}
	})
}

func TestOAuth_ClientStore(t *testing.T) {
	db := newTestDB(t)
	userStore := NewSQLiteUserStore(db, 10)
	clientStore := NewSQLiteClientStore(db, 10)
	ctx := context.Background()

	user, _ := userStore.CreateUser(ctx, "clientowner", "password123", "")

	t.Run("create and get client", func(t *testing.T) {
		client, secret, err := clientStore.CreateClient(ctx, "test-app",
			[]string{"http://localhost:3000/callback"},
			[]string{"authorization_code", "client_credentials"},
			[]string{"read", "write"},
			user.ID,
		)
		if err != nil {
			t.Fatalf("CreateClient: %v", err)
		}
		if client.ID == "" {
			t.Error("client ID should not be empty")
		}
		if secret == "" {
			t.Error("client secret should not be empty")
		}
		if client.Name != "test-app" {
			t.Errorf("Name = %q, want %q", client.Name, "test-app")
		}

		// Get client
		got, err := clientStore.GetClient(ctx, client.ID)
		if err != nil {
			t.Fatalf("GetClient: %v", err)
		}
		if got.Name != "test-app" {
			t.Errorf("Name = %q, want %q", got.Name, "test-app")
		}
		if len(got.RedirectURIs) != 1 {
			t.Errorf("RedirectURIs length = %d, want 1", len(got.RedirectURIs))
		}
	})

	t.Run("verify client secret", func(t *testing.T) {
		client, secret, _ := clientStore.CreateClient(ctx, "verify-app",
			nil, nil, nil, user.ID)

		_, err := clientStore.VerifyClientSecret(ctx, client.ID, secret)
		if err != nil {
			t.Errorf("VerifyClientSecret should succeed: %v", err)
		}

		_, err = clientStore.VerifyClientSecret(ctx, client.ID, "wrong-secret")
		if err == nil {
			t.Error("VerifyClientSecret should fail with wrong secret")
		}
	})

	t.Run("list clients by owner", func(t *testing.T) {
		clients, err := clientStore.ListClientsByOwner(ctx, user.ID)
		if err != nil {
			t.Fatalf("ListClientsByOwner: %v", err)
		}
		if len(clients) < 1 {
			t.Error("should have at least 1 client")
		}
	})

	t.Run("non-existent client", func(t *testing.T) {
		_, err := clientStore.GetClient(ctx, "nonexistent-id")
		if err != ErrClientNotFound {
			t.Errorf("expected ErrClientNotFound, got %v", err)
		}
	})
}
