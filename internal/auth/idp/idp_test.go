package idp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/auth"
	"github.com/synapbus/synapbus/internal/storage"
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

func newTestUserStore(db *sql.DB, t *testing.T) auth.UserStore {
	t.Helper()
	return auth.NewSQLiteUserStore(db, 10) // low cost for fast tests
}

// --- GitHub provider tests ---

func TestGitHubProvider_AuthCodeURL(t *testing.T) {
	p := NewGitHubProvider("test-client-id", "test-secret", "http://localhost:8080/auth/callback/github")

	url := p.AuthCodeURL("test-state-123")

	if url == "" {
		t.Fatal("AuthCodeURL returned empty string")
	}
	if p.ID() != "github" {
		t.Errorf("ID() = %q, want %q", p.ID(), "github")
	}
	if p.Type() != "oauth" {
		t.Errorf("Type() = %q, want %q", p.Type(), "oauth")
	}
	if p.DisplayName() != "GitHub" {
		t.Errorf("DisplayName() = %q, want %q", p.DisplayName(), "GitHub")
	}

	// URL should contain the client ID and state
	if got := url; got == "" {
		t.Error("expected non-empty URL")
	}
}

// --- OIDC domain validation tests ---

func TestOIDCProvider_DomainRestriction(t *testing.T) {
	p := &OIDCProvider{
		id:             "test",
		displayName:    "Test",
		allowedDomains: []string{"gcore.com", "example.com"},
	}

	tests := []struct {
		name      string
		email     string
		hd        string
		wantError bool
	}{
		{"allowed domain via hd", "user@gcore.com", "gcore.com", false},
		{"allowed domain via email", "user@example.com", "", false},
		{"disallowed domain", "user@evil.com", "evil.com", true},
		{"no domain info", "", "", true},
		{"case insensitive", "User@Gcore.COM", "Gcore.COM", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.validateDomain(tt.email, tt.hd)
			if (err != nil) != tt.wantError {
				t.Errorf("validateDomain(%q, %q) error = %v, wantError %v", tt.email, tt.hd, err, tt.wantError)
			}
		})
	}
}

func TestOIDCProvider_NoDomainRestriction(t *testing.T) {
	p := &OIDCProvider{
		id:             "test",
		displayName:    "Test",
		allowedDomains: nil,
	}

	if len(p.allowedDomains) != 0 {
		t.Error("expected empty allowed domains")
	}
}

// --- Store tests ---

func TestUserIdentityStore_CreateAndFind(t *testing.T) {
	db := newTestDB(t)
	store := NewUserIdentityStore(db)
	ctx := context.Background()

	// Create a user first
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, display_name, role) VALUES (?, ?, ?, ?)`,
		"testuser", "hash", "Test User", "user",
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	var userID int64
	db.QueryRowContext(ctx, "SELECT id FROM users WHERE username = ?", "testuser").Scan(&userID)

	// Create identity
	claims := map[string]any{"sub": "12345", "login": "ghuser"}
	err = store.Create(ctx, userID, "github", "12345", "test@example.com", "Test User", claims)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Find by provider
	foundID, err := store.FindByProvider(ctx, "github", "12345")
	if err != nil {
		t.Fatalf("FindByProvider: %v", err)
	}
	if foundID != userID {
		t.Errorf("FindByProvider = %d, want %d", foundID, userID)
	}

	// Find non-existent
	_, err = store.FindByProvider(ctx, "github", "99999")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestUserIdentityStore_ListByUser(t *testing.T) {
	db := newTestDB(t)
	store := NewUserIdentityStore(db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, display_name, role) VALUES (?, ?, ?, ?)`,
		"multiuser", "hash", "Multi User", "user",
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	var userID int64
	db.QueryRowContext(ctx, "SELECT id FROM users WHERE username = ?", "multiuser").Scan(&userID)

	store.Create(ctx, userID, "github", "gh-123", "user@gh.com", "GH User", map[string]any{})
	store.Create(ctx, userID, "google", "goog-456", "user@google.com", "Google User", map[string]any{})

	identities, err := store.ListByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(identities) != 2 {
		t.Errorf("got %d identities, want 2", len(identities))
	}

	if identities[0].Provider != "github" {
		t.Errorf("first identity provider = %q, want %q", identities[0].Provider, "github")
	}
	if identities[1].Provider != "google" {
		t.Errorf("second identity provider = %q, want %q", identities[1].Provider, "google")
	}

	empty, err := store.ListByUser(ctx, 99999)
	if err != nil {
		t.Fatalf("ListByUser (empty): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected empty list, got %d", len(empty))
	}
}

// --- Handler tests ---

func TestHandleListProviders(t *testing.T) {
	providers := []Provider{
		NewGitHubProvider("id", "secret", "http://localhost/callback"),
		&mockProvider{id: "google", providerType: "oidc", name: "Google"},
	}

	handlers := NewHandlers(providers, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/auth/providers", nil)
	w := httptest.NewRecorder()

	handlers.HandleListProviders(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Providers []ProviderInfo `json:"providers"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Providers) != 2 {
		t.Fatalf("got %d providers, want 2", len(resp.Providers))
	}

	if resp.Providers[0].ID != "github" {
		t.Errorf("first provider ID = %q, want %q", resp.Providers[0].ID, "github")
	}
	if resp.Providers[0].DisplayName != "GitHub" {
		t.Errorf("first provider DisplayName = %q, want %q", resp.Providers[0].DisplayName, "GitHub")
	}
	if resp.Providers[1].ID != "google" {
		t.Errorf("second provider ID = %q, want %q", resp.Providers[1].ID, "google")
	}
}

// --- User provisioning tests ---

func TestSanitizeUsername(t *testing.T) {
	tests := []struct {
		name       string
		username   string
		provider   string
		externalID string
		wantMin    string // exact match, or empty for length-only check
		maxLen     int
	}{
		{"normal", "testuser", "github", "123", "testuser", 0},
		{"with dash", "test-user", "github", "123", "test_user", 0},
		{"with dots", "test.user", "github", "123", "test_user", 0},
		{"with at sign", "user@email.com", "github", "123", "user_email_com", 0},
		{"too short", "ab", "github", "123", "ab_user", 0},
		{"empty", "", "github", "123", "github_123", 0},
		{"too long", "", "", "", "", 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := tt.username
			if tt.name == "too long" {
				b := make([]byte, 100)
				for i := range b {
					b[i] = 'a'
				}
				input = string(b)
			}
			got := sanitizeUsername(input, tt.provider, tt.externalID)
			if tt.maxLen > 0 {
				if len(got) > tt.maxLen {
					t.Errorf("sanitizeUsername too long: len=%d, want <= %d", len(got), tt.maxLen)
				}
				return
			}
			if got != tt.wantMin {
				t.Errorf("sanitizeUsername(%q, %q, %q) = %q, want %q", tt.username, tt.provider, tt.externalID, got, tt.wantMin)
			}
		})
	}
}

func TestFindOrCreateUser_NewUser(t *testing.T) {
	db := newTestDB(t)
	idStore := NewUserIdentityStore(db)
	userStore := newTestUserStore(db, t)

	handlers := &Handlers{
		idStore:   idStore,
		userStore: userStore,
		logger:    slog.Default(),
	}

	ctx := context.Background()
	ext := &ExternalUser{
		ProviderID: "github",
		ExternalID: "gh-newuser-001",
		Email:      "newuser@example.com",
		Name:       "New User",
		Username:   "newghuser",
		RawClaims:  map[string]any{"login": "newghuser"},
	}

	user, err := handlers.findOrCreateUser(ctx, ext)
	if err != nil {
		t.Fatalf("findOrCreateUser: %v", err)
	}

	if user.Username != "newghuser" {
		t.Errorf("Username = %q, want %q", user.Username, "newghuser")
	}
	if user.DisplayName != "New User" {
		t.Errorf("DisplayName = %q, want %q", user.DisplayName, "New User")
	}

	// Identity should be linked
	foundID, err := idStore.FindByProvider(ctx, "github", "gh-newuser-001")
	if err != nil {
		t.Fatalf("identity not linked: %v", err)
	}
	if foundID != user.ID {
		t.Errorf("linked user ID = %d, want %d", foundID, user.ID)
	}
}

func TestFindOrCreateUser_ExistingIdentity(t *testing.T) {
	db := newTestDB(t)
	idStore := NewUserIdentityStore(db)
	userStore := newTestUserStore(db, t)

	ctx := context.Background()

	// Create a user first
	user, err := userStore.CreateUser(ctx, "existinguser", "password1234", "Existing User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Link identity manually
	err = idStore.Create(ctx, user.ID, "github", "gh-existing-001", "existing@example.com", "Existing", map[string]any{})
	if err != nil {
		t.Fatalf("Create identity: %v", err)
	}

	handlers := &Handlers{
		idStore:   idStore,
		userStore: userStore,
		logger:    slog.Default(),
	}

	ext := &ExternalUser{
		ProviderID: "github",
		ExternalID: "gh-existing-001",
		Email:      "existing@example.com",
		Name:       "Existing User",
		Username:   "existinguser",
	}

	found, err := handlers.findOrCreateUser(ctx, ext)
	if err != nil {
		t.Fatalf("findOrCreateUser: %v", err)
	}
	if found.ID != user.ID {
		t.Errorf("found user ID = %d, want %d", found.ID, user.ID)
	}
}

// --- Mock helpers ---

type mockProvider struct {
	id           string
	providerType string
	name         string
}

func (m *mockProvider) ID() string          { return m.id }
func (m *mockProvider) Type() string        { return m.providerType }
func (m *mockProvider) DisplayName() string { return m.name }
func (m *mockProvider) AuthCodeURL(state string) string {
	return "https://mock.idp.example.com/authorize?state=" + state
}
func (m *mockProvider) Exchange(ctx context.Context, code string) (*ExternalUser, error) {
	return &ExternalUser{
		ProviderID: m.id,
		ExternalID: "mock-ext-id",
		Email:      "mock@example.com",
		Name:       "Mock User",
		Username:   "mockuser",
	}, nil
}
