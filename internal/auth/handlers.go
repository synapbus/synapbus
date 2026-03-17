package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ory/fosite"
)

// AgentLister provides a way to list agents by owner for the authorize page.
// This avoids a direct dependency on the agents package.
type AgentLister interface {
	ListAgentsByOwner(ctx context.Context, ownerID int64) ([]AgentInfo, error)
}

// AgentInfo is a minimal agent representation used by the authorize page.
type AgentInfo struct {
	Name        string
	DisplayName string
	Type        string
}

// Handlers holds the HTTP handlers for the auth subsystem.
type Handlers struct {
	userStore    UserStore
	sessionStore SessionStore
	clientStore  ClientStore
	provider     fosite.OAuth2Provider
	config       Config
	agentLister  AgentLister
	logger       *slog.Logger
}

// NewHandlers creates a new set of auth HTTP handlers.
func NewHandlers(
	userStore UserStore,
	sessionStore SessionStore,
	clientStore ClientStore,
	provider fosite.OAuth2Provider,
	config Config,
) *Handlers {
	return &Handlers{
		userStore:    userStore,
		sessionStore: sessionStore,
		clientStore:  clientStore,
		provider:     provider,
		config:       config,
		logger:       slog.Default().With("component", "auth"),
	}
}

// SetAgentLister sets the agent lister for the OAuth authorize page.
func (h *Handlers) SetAgentLister(lister AgentLister) {
	h.agentLister = lister
}

// HandleRegister handles POST /auth/register.
func (h *Handlers) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}

	user, err := h.userStore.CreateUser(r.Context(), req.Username, req.Password, req.DisplayName)
	if err != nil {
		switch err {
		case ErrDuplicateUsername:
			writeError(w, http.StatusConflict, "duplicate_username", "Username already exists")
		case ErrInvalidUsername:
			writeError(w, http.StatusBadRequest, "invalid_username", err.Error())
		case ErrPasswordTooShort:
			writeError(w, http.StatusBadRequest, "invalid_password", err.Error())
		case ErrPasswordTooLong:
			writeError(w, http.StatusBadRequest, "invalid_password", err.Error())
		default:
			h.logger.Error("create user failed", "error", err)
			writeError(w, http.StatusInternalServerError, "server_error", "Failed to create user")
		}
		return
	}

	LogAuthEvent(r.Context(), h.logger, AuthEvent{
		Type:     EventUserCreated,
		UserID:   user.ID,
		Username: user.Username,
		RemoteIP: remoteIP(r),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

// HandleLogin handles POST /auth/login.
func (h *Handlers) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}

	user, err := h.userStore.VerifyPassword(r.Context(), req.Username, req.Password)
	if err != nil {
		LogAuthEvent(r.Context(), h.logger, AuthEvent{
			Type:     EventLoginFailure,
			Username: req.Username,
			RemoteIP: remoteIP(r),
		})
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid username or password")
		return
	}

	session, err := h.sessionStore.CreateSession(r.Context(), user.ID, h.config.SessionLifetime)
	if err != nil {
		h.logger.Error("create session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "server_error", "Failed to create session")
		return
	}

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    session.SessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   !h.config.DevMode,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.config.SessionLifetime.Seconds()),
	})

	LogAuthEvent(r.Context(), h.logger, AuthEvent{
		Type:     EventLoginSuccess,
		UserID:   user.ID,
		Username: user.Username,
		RemoteIP: remoteIP(r),
	})

	LogAuthEvent(r.Context(), h.logger, AuthEvent{
		Type:     EventSessionCreated,
		UserID:   user.ID,
		Username: user.Username,
		RemoteIP: remoteIP(r),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

// HandleLogout handles POST /auth/logout.
func (h *Handlers) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	cookie, err := r.Cookie(SessionCookieName)
	if err == nil && cookie.Value != "" {
		h.sessionStore.DeleteSession(r.Context(), cookie.Value)

		user, ok := UserFromContext(r.Context())
		if ok {
			LogAuthEvent(r.Context(), h.logger, AuthEvent{
				Type:     EventSessionDestroyed,
				UserID:   user.ID,
				Username: user.Username,
				RemoteIP: remoteIP(r),
			})
		}
	}

	// Clear cookie
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   !h.config.DevMode,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"logged_out"}`))
}

// HandleMe handles GET /auth/me.
func (h *Handlers) HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Not authenticated")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

// HandleUpdateProfile handles PUT /api/auth/profile.
func (h *Handlers) HandleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "PUT required")
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Not authenticated")
		return
	}

	var req struct {
		DisplayName string `json:"display_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}

	if strings.TrimSpace(req.DisplayName) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Display name cannot be empty")
		return
	}

	if err := h.userStore.UpdateDisplayName(r.Context(), user.ID, req.DisplayName); err != nil {
		h.logger.Error("update display name failed", "error", err)
		writeError(w, http.StatusInternalServerError, "server_error", "Failed to update display name")
		return
	}

	// Fetch updated user
	updated, err := h.userStore.GetUserByID(r.Context(), user.ID)
	if err != nil {
		h.logger.Error("fetch updated user failed", "error", err)
		writeError(w, http.StatusInternalServerError, "server_error", "Profile updated but failed to fetch result")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"message": "Profile updated",
		"user": map[string]any{
			"id":           updated.ID,
			"username":     updated.Username,
			"display_name": updated.DisplayName,
			"role":         updated.Role,
		},
	})
}

// HandleChangePassword handles PUT /auth/password.
func (h *Handlers) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "PUT required")
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Not authenticated")
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}

	// Verify current password
	if _, err := h.userStore.VerifyPassword(r.Context(), user.Username, req.CurrentPassword); err != nil {
		writeError(w, http.StatusForbidden, "invalid_password", "Current password is incorrect")
		return
	}

	// Update password
	if err := h.userStore.UpdatePassword(r.Context(), user.ID, req.NewPassword); err != nil {
		switch err {
		case ErrPasswordTooShort:
			writeError(w, http.StatusBadRequest, "invalid_password", err.Error())
		case ErrPasswordTooLong:
			writeError(w, http.StatusBadRequest, "invalid_password", err.Error())
		default:
			h.logger.Error("update password failed", "error", err)
			writeError(w, http.StatusInternalServerError, "server_error", "Failed to update password")
		}
		return
	}

	// Invalidate all sessions except current
	if sessionID, ok := SessionIDFromContext(r.Context()); ok {
		h.sessionStore.DeleteSessionsByUserExcept(r.Context(), user.ID, sessionID)
	}

	LogAuthEvent(r.Context(), h.logger, AuthEvent{
		Type:     EventPasswordChanged,
		UserID:   user.ID,
		Username: user.Username,
		RemoteIP: remoteIP(r),
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"password_changed"}`))
}

// HandleOAuthMetadata handles GET /.well-known/oauth-authorization-server.
// Returns OAuth 2.1 server metadata as per RFC 8414.
func (h *Handlers) HandleOAuthMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}

	baseURL := h.config.IssuerURL
	if baseURL == "" || baseURL == "auto" {
		// Derive from request headers (supports reverse proxies and tunnels)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		// Cloudflare, nginx, and other proxies set these headers
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = proto
		}
		host := r.Host
		if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
			host = fwdHost
		}
		baseURL = fmt.Sprintf("%s://%s", scheme, host)
	}

	metadata := map[string]any{
		"issuer":                                baseURL,
		"authorization_endpoint":                baseURL + "/oauth/authorize",
		"token_endpoint":                        baseURL + "/oauth/token",
		"registration_endpoint":                 baseURL + "/oauth/register",
		"token_endpoint_auth_methods_supported": []string{"none"},
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      []string{"mcp"},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metadata)
}

// HandleDynamicRegistration handles POST /oauth/register (RFC 7591).
// MCP clients use this to register themselves as public OAuth clients.
func (h *Handlers) HandleDynamicRegistration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	var req struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		GrantTypes   []string `json:"grant_types"`
		Scope        string   `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_client_metadata", "Invalid JSON body")
		return
	}

	if req.ClientName == "" {
		req.ClientName = "dynamic-client"
	}

	// Default redirect URIs for MCP clients
	if len(req.RedirectURIs) == 0 {
		req.RedirectURIs = []string{"http://127.0.0.1"}
	}

	// Normalize localhost to 127.0.0.1 for fosite's loopback matching (RFC 8252 Section 7.3).
	// Fosite allows dynamic ports for loopback IPs but not for "localhost" hostname.
	normalizedURIs := make([]string, 0, len(req.RedirectURIs))
	for _, uri := range req.RedirectURIs {
		parsed, err := url.Parse(uri)
		if err == nil && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1") {
			// Store without port — fosite ignores port for loopback addresses
			normalized := fmt.Sprintf("http://127.0.0.1%s", parsed.Path)
			normalizedURIs = append(normalizedURIs, normalized)
		} else {
			normalizedURIs = append(normalizedURIs, uri)
		}
	}
	req.RedirectURIs = normalizedURIs

	// Only allow authorization_code + refresh_token for public clients
	grantTypes := []string{"authorization_code", "refresh_token"}
	scopes := []string{"mcp"}

	client, err := h.clientStore.CreatePublicClient(r.Context(), req.ClientName, req.RedirectURIs, grantTypes, scopes)
	if err != nil {
		h.logger.Error("dynamic client registration failed", "error", err)
		writeError(w, http.StatusInternalServerError, "server_error", "Failed to register client")
		return
	}

	h.logger.Info("dynamic client registered",
		"client_id", client.ID,
		"client_name", client.Name,
		"redirect_uris", client.RedirectURIs,
	)

	// RFC 7591 response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"client_id":                client.ID,
		"client_name":             client.Name,
		"redirect_uris":           client.RedirectURIs,
		"grant_types":             client.GrantTypes,
		"scope":                   "mcp",
		"token_endpoint_auth_method": "none",
	})
}

// HandleAuthorizeGet handles GET /oauth/authorize.
// Renders the login form or agent selector depending on session state.
func (h *Handlers) HandleAuthorizeGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}

	// Collect OAuth params from query string
	oauthParams := authorizeParams{
		ResponseType:        r.URL.Query().Get("response_type"),
		ClientID:            r.URL.Query().Get("client_id"),
		RedirectURI:         r.URL.Query().Get("redirect_uri"),
		State:               r.URL.Query().Get("state"),
		Scope:               r.URL.Query().Get("scope"),
		CodeChallenge:       r.URL.Query().Get("code_challenge"),
		CodeChallengeMethod: r.URL.Query().Get("code_challenge_method"),
	}

	// Check if user has a valid session
	user, loggedIn := h.trySessionAuth(r)

	data := authorizePageData{
		Params:   oauthParams,
		LoggedIn: loggedIn,
	}

	if loggedIn {
		data.Username = user.Username
		// Fetch user's agents
		agentsList, err := h.listUserAgents(r.Context(), user.ID)
		if err != nil {
			h.logger.Error("list user agents failed", "error", err)
		}
		data.Agents = agentsList
		if len(agentsList) == 0 {
			data.NoAgents = true
		}
	}

	h.renderAuthorizePage(w, data)
}

// HandleAuthorizePost handles POST /oauth/authorize.
// Processes login or authorization approval.
func (h *Handlers) HandleAuthorizePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid form data")
		return
	}

	// Collect OAuth params from form (passed as hidden fields)
	oauthParams := authorizeParams{
		ResponseType:        r.FormValue("response_type"),
		ClientID:            r.FormValue("client_id"),
		RedirectURI:         r.FormValue("redirect_uri"),
		State:               r.FormValue("state"),
		Scope:               r.FormValue("scope"),
		CodeChallenge:       r.FormValue("code_challenge"),
		CodeChallengeMethod: r.FormValue("code_challenge_method"),
	}

	action := r.FormValue("action")

	switch action {
	case "login":
		h.handleAuthorizeLogin(w, r, oauthParams)
	case "authorize":
		h.handleAuthorizeApprove(w, r, oauthParams)
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "Unknown action")
	}
}

// handleAuthorizeLogin processes the login form submission within the authorize flow.
func (h *Handlers) handleAuthorizeLogin(w http.ResponseWriter, r *http.Request, params authorizeParams) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := h.userStore.VerifyPassword(r.Context(), username, password)
	if err != nil {
		data := authorizePageData{
			Params:     params,
			LoggedIn:   false,
			LoginError: "Invalid username or password",
		}
		h.renderAuthorizePage(w, data)
		return
	}

	// Create session
	session, err := h.sessionStore.CreateSession(r.Context(), user.ID, h.config.SessionLifetime)
	if err != nil {
		h.logger.Error("create session failed during authorize", "error", err)
		data := authorizePageData{
			Params:     params,
			LoggedIn:   false,
			LoginError: "Server error, please try again",
		}
		h.renderAuthorizePage(w, data)
		return
	}

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    session.SessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   !h.config.DevMode,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.config.SessionLifetime.Seconds()),
	})

	LogAuthEvent(r.Context(), h.logger, AuthEvent{
		Type:     EventLoginSuccess,
		UserID:   user.ID,
		Username: user.Username,
		RemoteIP: remoteIP(r),
	})

	// Now show the agent selector
	agentsList, err := h.listUserAgents(r.Context(), user.ID)
	if err != nil {
		h.logger.Error("list user agents failed", "error", err)
	}

	data := authorizePageData{
		Params:   params,
		LoggedIn: true,
		Username: user.Username,
		Agents:   agentsList,
		NoAgents: len(agentsList) == 0,
	}

	h.renderAuthorizePage(w, data)
}

// handleAuthorizeApprove processes the "Authorize" button click.
func (h *Handlers) handleAuthorizeApprove(w http.ResponseWriter, r *http.Request, params authorizeParams) {
	// Verify session
	user, ok := h.trySessionAuth(r)
	if !ok {
		data := authorizePageData{
			Params:     params,
			LoggedIn:   false,
			LoginError: "Session expired, please log in again",
		}
		h.renderAuthorizePage(w, data)
		return
	}

	agentName := r.FormValue("agent_name")
	if agentName == "" {
		agentsList, _ := h.listUserAgents(r.Context(), user.ID)
		data := authorizePageData{
			Params:     params,
			LoggedIn:   true,
			Username:   user.Username,
			Agents:     agentsList,
			LoginError: "Please select an agent",
		}
		h.renderAuthorizePage(w, data)
		return
	}

	// Normalize localhost to 127.0.0.1 for fosite's loopback matching (RFC 8252)
	redirectURI := params.RedirectURI
	if parsed, err := url.Parse(redirectURI); err == nil && parsed.Hostname() == "localhost" {
		parsed.Host = "127.0.0.1:" + parsed.Port()
		if parsed.Port() == "" {
			parsed.Host = "127.0.0.1"
		}
		redirectURI = parsed.String()
	}

	// Build a synthetic GET request with the OAuth params for fosite
	q := url.Values{
		"response_type":         {params.ResponseType},
		"client_id":             {params.ClientID},
		"redirect_uri":          {redirectURI},
		"state":                 {params.State},
		"scope":                 {params.Scope},
		"code_challenge":        {params.CodeChallenge},
		"code_challenge_method": {params.CodeChallengeMethod},
	}
	syntheticReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "/oauth/authorize?"+q.Encode(), nil)

	ar, err := h.provider.NewAuthorizeRequest(r.Context(), syntheticReq)
	if err != nil {
		h.logger.Debug("authorize request failed", "error", err)
		h.provider.WriteAuthorizeError(r.Context(), w, ar, err)
		return
	}

	// Grant requested scopes
	for _, scope := range ar.GetRequestedScopes() {
		ar.GrantScope(scope)
	}

	// Create fosite session with agent_name
	sess := NewSessionWithAgent(user, agentName)
	response, err := h.provider.NewAuthorizeResponse(r.Context(), ar, sess)
	if err != nil {
		h.logger.Debug("authorize response failed", "error", err)
		h.provider.WriteAuthorizeError(r.Context(), w, ar, err)
		return
	}

	h.provider.WriteAuthorizeResponse(r.Context(), w, ar, response)
}

// trySessionAuth checks if the request has a valid session cookie.
func (h *Handlers) trySessionAuth(r *http.Request) (*User, bool) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, false
	}

	session, err := h.sessionStore.GetSession(r.Context(), cookie.Value)
	if err != nil {
		return nil, false
	}

	user, err := h.userStore.GetUserByID(r.Context(), session.UserID)
	if err != nil {
		return nil, false
	}

	return user, true
}

// listUserAgents returns a list of agents owned by the user.
func (h *Handlers) listUserAgents(ctx context.Context, userID int64) ([]AgentInfo, error) {
	if h.agentLister == nil {
		return nil, nil
	}
	return h.agentLister.ListAgentsByOwner(ctx, userID)
}

// authorizeParams holds the OAuth query/form parameters.
type authorizeParams struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	State               string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
}

// authorizePageData holds template data for the authorize page.
type authorizePageData struct {
	Params     authorizeParams
	LoggedIn   bool
	Username   string
	Agents     []AgentInfo
	NoAgents   bool
	LoginError string
}

// renderAuthorizePage renders the authorize HTML template.
func (h *Handlers) renderAuthorizePage(w http.ResponseWriter, data authorizePageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := authorizeTemplate.Execute(w, data); err != nil {
		h.logger.Error("render authorize template failed", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// authorizeTemplate is the HTML template for the OAuth authorize page.
var authorizeTemplate = template.Must(template.New("authorize").Parse(authorizeTemplateHTML))

const authorizeTemplateHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>SynapBus — Authorize</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Instrument+Sans:wght@500;700&family=DM+Sans:wght@400;500&display=swap" rel="stylesheet">
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    font-family: 'DM Sans', -apple-system, BlinkMacSystemFont, sans-serif;
    background: #1a1d21;
    color: #e8e8e8;
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    background-image:
      radial-gradient(ellipse at 20% 50%, rgba(124,58,237,0.08) 0%, transparent 50%),
      radial-gradient(ellipse at 80% 50%, rgba(54,197,240,0.06) 0%, transparent 50%);
  }
  .card {
    background: #222529;
    border: 1px solid #383b40;
    border-radius: 16px;
    padding: 2.5rem 2rem 2rem;
    max-width: 420px;
    width: 100%;
    box-shadow: 0 8px 32px rgba(0,0,0,0.4), 0 0 0 1px rgba(255,255,255,0.03) inset;
  }
  .logo {
    text-align: center;
    margin-bottom: 2rem;
  }
  .logo svg {
    width: 48px;
    height: 48px;
    margin-bottom: 0.75rem;
    filter: drop-shadow(0 0 8px rgba(54,197,240,0.3));
  }
  .logo h1 {
    font-family: 'Instrument Sans', sans-serif;
    font-size: 1.6rem;
    font-weight: 700;
    background: linear-gradient(135deg, #36c5f0, #7c3aed);
    -webkit-background-clip: text;
    -webkit-text-fill-color: transparent;
    background-clip: text;
    letter-spacing: -0.02em;
  }
  .logo p {
    font-size: 0.82rem;
    color: #9b9da0;
    margin-top: 0.3rem;
    letter-spacing: 0.02em;
  }
  .error {
    background: rgba(224,30,90,0.12);
    color: #f87171;
    padding: 0.7rem 0.85rem;
    border-radius: 8px;
    margin-bottom: 1.25rem;
    font-size: 0.85rem;
    border: 1px solid rgba(224,30,90,0.2);
  }
  .info {
    background: rgba(54,197,240,0.08);
    color: #7dd3fc;
    padding: 0.7rem 0.85rem;
    border-radius: 8px;
    margin-bottom: 1.25rem;
    font-size: 0.85rem;
    border: 1px solid rgba(54,197,240,0.15);
  }
  label {
    display: block;
    margin-bottom: 0.35rem;
    font-size: 0.82rem;
    font-weight: 500;
    color: #9b9da0;
    letter-spacing: 0.01em;
  }
  input, select {
    width: 100%;
    padding: 0.65rem 0.85rem;
    background: #1a1d21;
    border: 1px solid #383b40;
    border-radius: 8px;
    color: #e8e8e8;
    font-family: 'DM Sans', sans-serif;
    font-size: 0.92rem;
    margin-bottom: 1.1rem;
    transition: border-color 0.15s;
  }
  input:focus, select:focus {
    outline: none;
    border-color: #36c5f0;
    box-shadow: 0 0 0 2px rgba(54,197,240,0.15);
  }
  select option { background: #1a1d21; }
  button {
    width: 100%;
    padding: 0.72rem;
    background: linear-gradient(135deg, #2eb67d, #1a9e6a);
    color: #fff;
    border: none;
    border-radius: 8px;
    font-family: 'DM Sans', sans-serif;
    font-size: 0.95rem;
    font-weight: 500;
    cursor: pointer;
    transition: opacity 0.15s, transform 0.1s;
    letter-spacing: 0.01em;
  }
  button:hover { opacity: 0.9; }
  button:active { transform: scale(0.99); }
  .meta {
    font-size: 0.72rem;
    color: #545760;
    margin-top: 1.25rem;
    text-align: center;
    font-family: 'JetBrains Mono', monospace;
    word-break: break-all;
  }
  .user-info {
    font-size: 0.88rem;
    color: #9b9da0;
    margin-bottom: 1.25rem;
    padding: 0.6rem 0.85rem;
    background: rgba(124,58,237,0.08);
    border-radius: 8px;
    border: 1px solid rgba(124,58,237,0.15);
  }
  .user-info strong { color: #e8e8e8; }
  .divider {
    height: 1px;
    background: #383b40;
    margin: 1.5rem 0;
  }
</style>
</head>
<body>
<div class="card">
  <div class="logo">
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
      <defs><linearGradient id="bg" x1="0" y1="0" x2="32" y2="32" gradientUnits="userSpaceOnUse">
        <stop stop-color="#7c3aed"/><stop offset="1" stop-color="#06b6d4"/>
      </linearGradient></defs>
      <rect width="32" height="32" rx="7" fill="url(#bg)"/>
      <g transform="translate(4,4)">
        <circle cx="12" cy="5" r="1.8" fill="#c4b5fd"/>
        <circle cx="6" cy="10" r="1.5" fill="#a78bfa"/>
        <circle cx="18" cy="9" r="1.5" fill="#c4b5fd"/>
        <circle cx="12" cy="13" r="2" fill="#67e8f9"/>
        <circle cx="5" cy="17" r="1.5" fill="#a78bfa"/>
        <circle cx="19" cy="17" r="1.3" fill="#a78bfa"/>
        <circle cx="11" cy="20" r="1.3" fill="#c4b5fd"/>
        <line x1="12" y1="5" x2="6" y2="10" stroke="white" stroke-width="0.5" opacity="0.5"/>
        <line x1="12" y1="5" x2="18" y2="9" stroke="white" stroke-width="0.5" opacity="0.5"/>
        <line x1="6" y1="10" x2="12" y2="13" stroke="white" stroke-width="0.5" opacity="0.5"/>
        <line x1="18" y1="9" x2="12" y2="13" stroke="white" stroke-width="0.5" opacity="0.5"/>
        <line x1="12" y1="13" x2="5" y2="17" stroke="white" stroke-width="0.5" opacity="0.5"/>
        <line x1="12" y1="13" x2="19" y2="17" stroke="white" stroke-width="0.5" opacity="0.5"/>
        <line x1="5" y1="17" x2="11" y2="20" stroke="white" stroke-width="0.5" opacity="0.3"/>
        <line x1="6" y1="10" x2="5" y2="17" stroke="white" stroke-width="0.5" opacity="0.3"/>
      </g>
    </svg>
    <h1>SynapBus</h1>
    <p>Agent Authorization</p>
  </div>

  {{if .LoginError}}<div class="error">{{.LoginError}}</div>{{end}}

  {{if not .LoggedIn}}
  <form method="POST" action="/oauth/authorize">
    <input type="hidden" name="action" value="login">
    <input type="hidden" name="response_type" value="{{.Params.ResponseType}}">
    <input type="hidden" name="client_id" value="{{.Params.ClientID}}">
    <input type="hidden" name="redirect_uri" value="{{.Params.RedirectURI}}">
    <input type="hidden" name="state" value="{{.Params.State}}">
    <input type="hidden" name="scope" value="{{.Params.Scope}}">
    <input type="hidden" name="code_challenge" value="{{.Params.CodeChallenge}}">
    <input type="hidden" name="code_challenge_method" value="{{.Params.CodeChallengeMethod}}">

    <label for="username">Username</label>
    <input type="text" id="username" name="username" required autofocus placeholder="Enter your username">

    <label for="password">Password</label>
    <input type="password" id="password" name="password" required placeholder="Enter your password">

    <button type="submit">Log In</button>
  </form>
  {{else}}
  <div class="user-info">Logged in as <strong>{{.Username}}</strong></div>

  {{if .NoAgents}}
  <div class="info">No agents registered yet. Create an agent in the SynapBus Web UI first, then return here to authorize.</div>
  {{else}}
  <form method="POST" action="/oauth/authorize">
    <input type="hidden" name="action" value="authorize">
    <input type="hidden" name="response_type" value="{{.Params.ResponseType}}">
    <input type="hidden" name="client_id" value="{{.Params.ClientID}}">
    <input type="hidden" name="redirect_uri" value="{{.Params.RedirectURI}}">
    <input type="hidden" name="state" value="{{.Params.State}}">
    <input type="hidden" name="scope" value="{{.Params.Scope}}">
    <input type="hidden" name="code_challenge" value="{{.Params.CodeChallenge}}">
    <input type="hidden" name="code_challenge_method" value="{{.Params.CodeChallengeMethod}}">

    <label for="agent_name">Select Agent</label>
    <select id="agent_name" name="agent_name" required>
      {{if eq (len .Agents) 1}}
      {{range .Agents}}<option value="{{.Name}}" selected>{{.DisplayName}} ({{.Name}})</option>{{end}}
      {{else}}
      <option value="">— choose an agent —</option>
      {{range .Agents}}
      <option value="{{.Name}}">{{.DisplayName}} ({{.Name}})</option>
      {{end}}
      {{end}}
    </select>

    <button type="submit">Authorize</button>
  </form>
  {{end}}
  {{end}}

  <div class="meta">{{.Params.ClientID}}</div>
</div>
</body>
</html>
`

// HandleToken handles POST /oauth/token.
func (h *Handlers) HandleToken(w http.ResponseWriter, r *http.Request) {
	// Normalize localhost redirect_uri to 127.0.0.1 to match what was stored during authorization (RFC 8252)
	if err := r.ParseForm(); err == nil {
		if redirectURI := r.Form.Get("redirect_uri"); redirectURI != "" {
			if parsed, err := url.Parse(redirectURI); err == nil && parsed.Hostname() == "localhost" {
				parsed.Host = "127.0.0.1:" + parsed.Port()
				if parsed.Port() == "" {
					parsed.Host = "127.0.0.1"
				}
				r.Form.Set("redirect_uri", parsed.String())
				r.PostForm.Set("redirect_uri", parsed.String())
			}
		}
	}

	ctx := r.Context()
	sess := &fositeSession{}

	ar, err := h.provider.NewAccessRequest(ctx, r, sess)
	if err != nil {
		h.logger.Debug("access request failed", "error", err)
		h.provider.WriteAccessError(ctx, w, ar, err)
		return
	}

	// Grant requested scopes
	for _, scope := range ar.GetRequestedScopes() {
		ar.GrantScope(scope)
	}

	response, err := h.provider.NewAccessResponse(ctx, ar)
	if err != nil {
		h.logger.Debug("access response failed", "error", err)
		h.provider.WriteAccessError(ctx, w, ar, err)
		return
	}

	grantType := r.FormValue("grant_type")
	LogAuthEvent(ctx, h.logger, AuthEvent{
		Type:     EventTokenIssued,
		ClientID: ar.GetClient().GetID(),
		RemoteIP: remoteIP(r),
		Details: map[string]any{
			"grant_type": grantType,
		},
	})

	if grantType == "refresh_token" {
		LogAuthEvent(ctx, h.logger, AuthEvent{
			Type:     EventTokenRefreshed,
			ClientID: ar.GetClient().GetID(),
			RemoteIP: remoteIP(r),
		})
	}

	h.provider.WriteAccessResponse(ctx, w, ar, response)
}

// HandleIntrospect handles POST /oauth/introspect.
func (h *Handlers) HandleIntrospect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	ctx := r.Context()
	sess := &fositeSession{}

	ir, err := h.provider.NewIntrospectionRequest(ctx, r, sess)
	if err != nil {
		h.logger.Debug("introspection request failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(IntrospectionResponse{Active: false})
		return
	}

	resp := IntrospectionResponse{
		Active:    ir.IsActive(),
		ClientID:  ir.GetAccessRequester().GetClient().GetID(),
		TokenType: "bearer",
	}

	if sess, ok := ir.GetAccessRequester().GetSession().(*fositeSession); ok {
		resp.Username = sess.Username
		resp.Subject = sess.Subject
		if exp := sess.GetExpiresAt(fosite.AccessToken); !exp.IsZero() {
			resp.ExpiresAt = exp.Unix()
		}
	}

	resp.Scope = strings.Join(ir.GetAccessRequester().GetGrantedScopes(), " ")
	resp.IssuedAt = time.Now().Unix() // approximate

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, errCode, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{
		Error:       errCode,
		Description: description,
	})
}
