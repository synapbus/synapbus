package auth

import "time"

// User represents a human account in SynapBus.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	DisplayName  string    `json:"display_name"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// User role constants.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// Session represents a Web UI session linking a browser cookie to a user.
type Session struct {
	SessionID    string    `json:"session_id"`
	UserID       int64     `json:"user_id"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	LastActiveAt time.Time `json:"last_active_at"`
}

// OAuthClient represents a registered OAuth 2.1 client.
type OAuthClient struct {
	ID           string   `json:"id"`
	SecretHash   string   `json:"-"`
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirect_uris"`
	GrantTypes   []string `json:"grant_types"`
	Scopes       []string `json:"scopes"`
	OwnerID      int64    `json:"owner_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// AuthorizationCode represents an OAuth authorization code.
type AuthorizationCode struct {
	Code                string    `json:"code"`
	ClientID            string    `json:"client_id"`
	UserID              int64     `json:"user_id"`
	RedirectURI         string    `json:"redirect_uri"`
	Scopes              string    `json:"scopes"`
	CodeChallenge       string    `json:"code_challenge"`
	CodeChallengeMethod string    `json:"code_challenge_method"`
	SessionData         string    `json:"session_data"`
	ExpiresAt           time.Time `json:"expires_at"`
	CreatedAt           time.Time `json:"created_at"`
	Used                bool      `json:"used"`
}

// OAuthToken represents an issued access or refresh token.
type OAuthToken struct {
	ID              int64     `json:"id"`
	Signature       string    `json:"signature"`
	ClientID        string    `json:"client_id"`
	UserID          int64     `json:"user_id"`
	Scopes          string    `json:"scopes"`
	TokenType       string    `json:"token_type"` // "access" or "refresh"
	SessionData     string    `json:"session_data"`
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
	Consumed        bool      `json:"consumed"`
	ParentSignature string    `json:"parent_signature"`
}

// Token type constants.
const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

// TokenResponse is the JSON response for OAuth token endpoint.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// IntrospectionResponse is the JSON response for token introspection (RFC 7662).
type IntrospectionResponse struct {
	Active    bool   `json:"active"`
	Scope     string `json:"scope,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	Username  string `json:"username,omitempty"`
	TokenType string `json:"token_type,omitempty"`
	ExpiresAt int64  `json:"exp,omitempty"`
	IssuedAt  int64  `json:"iat,omitempty"`
	Subject   string `json:"sub,omitempty"`
}

// ErrorResponse is a JSON error response.
type ErrorResponse struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}
