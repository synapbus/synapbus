package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ory/fosite"
)

// Handlers holds the HTTP handlers for the auth subsystem.
type Handlers struct {
	userStore    UserStore
	sessionStore SessionStore
	clientStore  ClientStore
	provider     fosite.OAuth2Provider
	config       Config
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

// HandleAuthorize handles GET /oauth/authorize.
func (h *Handlers) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ar, err := h.provider.NewAuthorizeRequest(ctx, r)
	if err != nil {
		h.logger.Debug("authorize request failed", "error", err)
		h.provider.WriteAuthorizeError(ctx, w, ar, err)
		return
	}

	// Check if user is authenticated via session
	user, ok := UserFromContext(ctx)
	if !ok {
		// User needs to log in first
		// In a real implementation, this would redirect to a login page
		writeError(w, http.StatusUnauthorized, "login_required", "Please log in first")
		return
	}

	// Create session for fosite
	sess := NewSession(user)
	response, err := h.provider.NewAuthorizeResponse(ctx, ar, sess)
	if err != nil {
		h.logger.Debug("authorize response failed", "error", err)
		h.provider.WriteAuthorizeError(ctx, w, ar, err)
		return
	}

	h.provider.WriteAuthorizeResponse(ctx, w, ar, response)
}

// HandleToken handles POST /oauth/token.
func (h *Handlers) HandleToken(w http.ResponseWriter, r *http.Request) {
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
