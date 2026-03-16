package idp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/synapbus/synapbus/internal/auth"
)

// AgentProvisioner creates a human agent and personal channel after IdP login.
// This is a narrow interface to avoid importing the agents/channels packages.
type AgentProvisioner interface {
	ProvisionHumanAgent(ctx context.Context, username, displayName string, ownerID int64) error
}

// Handlers holds HTTP handlers for external identity provider authentication.
type Handlers struct {
	providers    map[string]Provider
	providerList []Provider // preserve order for listing
	idStore      *UserIdentityStore
	userStore    auth.UserStore
	sessionStore auth.SessionStore
	agentProvisioner AgentProvisioner
	logger       *slog.Logger
}

// NewHandlers creates a new set of IdP HTTP handlers.
func NewHandlers(
	providers []Provider,
	idStore *UserIdentityStore,
	userStore auth.UserStore,
	sessionStore auth.SessionStore,
	agentProvisioner AgentProvisioner,
) *Handlers {
	providerMap := make(map[string]Provider, len(providers))
	for _, p := range providers {
		providerMap[p.ID()] = p
	}
	return &Handlers{
		providers:    providerMap,
		providerList: providers,
		idStore:      idStore,
		userStore:    userStore,
		sessionStore: sessionStore,
		agentProvisioner: agentProvisioner,
		logger:       slog.Default().With("component", "idp"),
	}
}

// HandleListProviders returns the list of enabled identity providers.
// GET /auth/providers
func (h *Handlers) HandleListProviders(w http.ResponseWriter, r *http.Request) {
	providers := make([]ProviderInfo, 0, len(h.providerList))
	for _, p := range h.providerList {
		providers = append(providers, ProviderInfo{
			ID:          p.ID(),
			Type:        p.Type(),
			DisplayName: p.DisplayName(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"providers": providers,
	})
}

// HandleLogin initiates the OAuth/OIDC flow by redirecting to the IdP.
// GET /auth/login/{provider}
func (h *Handlers) HandleLogin(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "provider")
	provider, ok := h.providers[providerID]
	if !ok {
		http.Error(w, fmt.Sprintf(`{"error":"unknown_provider","message":"Provider %q not found"}`, providerID), http.StatusNotFound)
		return
	}

	state, err := generateState()
	if err != nil {
		h.logger.Error("failed to generate state", "error", err)
		http.Error(w, `{"error":"server_error","message":"Failed to generate state"}`, http.StatusInternalServerError)
		return
	}

	// Store state in a short-lived cookie for CSRF protection
	http.SetCookie(w, &http.Cookie{
		Name:     "idp_state",
		Value:    state,
		Path:     "/auth/callback/" + providerID,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600, // 10 minutes
	})

	authURL := provider.AuthCodeURL(state)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// HandleCallback processes the OAuth/OIDC callback from the IdP.
// GET /auth/callback/{provider}
func (h *Handlers) HandleCallback(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "provider")
	provider, ok := h.providers[providerID]
	if !ok {
		http.Error(w, "Unknown provider", http.StatusNotFound)
		return
	}

	// Verify state parameter
	stateCookie, err := r.Cookie("idp_state")
	if err != nil || stateCookie.Value == "" {
		http.Error(w, "Missing state cookie", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("state") != stateCookie.Value {
		http.Error(w, "State mismatch — possible CSRF attack", http.StatusBadRequest)
		return
	}

	// Clear state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "idp_state",
		Value:    "",
		Path:     "/auth/callback/" + providerID,
		HttpOnly: true,
		MaxAge:   -1,
	})

	// Check for error from IdP
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		h.logger.Warn("IdP returned error", "provider", providerID, "error", errParam, "description", desc)
		http.Redirect(w, r, "/login?error="+errParam, http.StatusTemporaryRedirect)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	// Exchange code for user info
	extUser, err := provider.Exchange(r.Context(), code)
	if err != nil {
		h.logger.Error("IdP exchange failed", "provider", providerID, "error", err)
		http.Redirect(w, r, "/login?error=exchange_failed", http.StatusTemporaryRedirect)
		return
	}

	// Find or create local user
	user, err := h.findOrCreateUser(r.Context(), extUser)
	if err != nil {
		h.logger.Error("failed to provision user from IdP", "provider", providerID, "error", err)
		http.Redirect(w, r, "/login?error=provisioning_failed", http.StatusTemporaryRedirect)
		return
	}

	// Create session
	session, err := h.sessionStore.CreateSession(r.Context(), user.ID, 24*time.Hour)
	if err != nil {
		h.logger.Error("failed to create session after IdP login", "error", err)
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    session.SessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400, // 24 hours
	})

	auth.LogAuthEvent(r.Context(), h.logger, auth.AuthEvent{
		Type:     auth.EventLoginSuccess,
		UserID:   user.ID,
		Username: user.Username,
		RemoteIP: r.RemoteAddr,
		Details: map[string]any{
			"provider":    providerID,
			"external_id": extUser.ExternalID,
		},
	})

	// Ensure human agent and channel in background
	if h.agentProvisioner != nil {
		go func() {
			ctx := context.Background()
			if err := h.agentProvisioner.ProvisionHumanAgent(ctx, user.Username, user.DisplayName, user.ID); err != nil {
				h.logger.Warn("failed to ensure human agent after IdP login", "username", user.Username, "error", err)
			}
		}()
	}

	// Redirect to home
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

// findOrCreateUser looks up or provisions a local user from external identity.
// 1. Check user_identities for (provider, external_id) -> if found, load user
// 2. If not found, check users by email -> if found, link identity
// 3. If no user at all, create new user with random password
func (h *Handlers) findOrCreateUser(ctx context.Context, ext *ExternalUser) (*auth.User, error) {
	// Step 1: Check existing identity link
	userID, err := h.idStore.FindByProvider(ctx, ext.ProviderID, ext.ExternalID)
	if err == nil {
		// Found existing link
		user, err := h.userStore.GetUserByID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("load linked user %d: %w", userID, err)
		}
		return user, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("find identity: %w", err)
	}

	// Step 2: Try to find user by email
	if ext.Email != "" {
		user, err := h.userStore.GetUserByEmail(ctx, ext.Email)
		if err == nil {
			// Found user with matching email — link this identity
			if linkErr := h.idStore.Create(ctx, user.ID, ext.ProviderID, ext.ExternalID, ext.Email, ext.Name, ext.RawClaims); linkErr != nil {
				h.logger.Warn("failed to link identity to existing user", "user_id", user.ID, "error", linkErr)
			}
			return user, nil
		}
		// If error is not "not found", it's a real error
		if err != auth.ErrUserNotFound {
			return nil, fmt.Errorf("find user by email: %w", err)
		}
	}

	// Step 3: Create new user
	username := sanitizeUsername(ext.Username, ext.ProviderID, ext.ExternalID)
	displayName := ext.Name
	if displayName == "" {
		displayName = username
	}

	// Generate random password (user will login via IdP)
	randomPW, err := generateRandomPassword()
	if err != nil {
		return nil, fmt.Errorf("generate password: %w", err)
	}

	user, err := h.userStore.CreateUser(ctx, username, randomPW, displayName)
	if err != nil {
		// If username conflicts, try with a suffix
		if strings.Contains(err.Error(), "already exists") {
			suffix, _ := generateShortID()
			username = username + "_" + suffix
			user, err = h.userStore.CreateUser(ctx, username, randomPW, displayName)
		}
		if err != nil {
			return nil, fmt.Errorf("create user: %w", err)
		}
	}

	// Set email on the user if available
	if ext.Email != "" {
		if emailStore, ok := h.userStore.(*auth.SQLiteUserStore); ok {
			emailStore.SetEmail(ctx, user.ID, ext.Email)
		}
	}

	// Link identity
	if linkErr := h.idStore.Create(ctx, user.ID, ext.ProviderID, ext.ExternalID, ext.Email, ext.Name, ext.RawClaims); linkErr != nil {
		h.logger.Warn("failed to link identity to new user", "user_id", user.ID, "error", linkErr)
	}

	h.logger.Info("provisioned new user from IdP",
		"username", username,
		"provider", ext.ProviderID,
		"external_id", ext.ExternalID,
		"email", ext.Email,
	)

	return user, nil
}

// sanitizeUsername normalizes a username from an IdP to match SynapBus requirements.
// Usernames must be 3-64 chars, alphanumeric and underscore only.
func sanitizeUsername(username, provider, externalID string) string {
	if username == "" {
		username = provider + "_" + externalID
	}

	// Replace invalid characters with underscore
	var result strings.Builder
	for _, r := range username {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			result.WriteRune(r)
		} else {
			result.WriteRune('_')
		}
	}
	username = result.String()

	// Trim underscores from edges and ensure minimum length
	username = strings.Trim(username, "_")
	if len(username) < 3 {
		username = username + "_user"
	}
	if len(username) > 64 {
		username = username[:64]
	}

	return username
}

// generateState creates a cryptographically random state parameter for OAuth.
func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateRandomPassword creates a random password for IdP-provisioned users.
func generateRandomPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateShortID creates a short random string for username disambiguation.
func generateShortID() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
