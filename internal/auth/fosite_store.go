package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/ory/fosite"
)

// FositeStore implements fosite's storage interfaces backed by SQLite.
// It implements:
//   - fosite.Storage (ClientManager + ClientAssertionJWT)
//   - oauth2.CoreStorage (AuthorizeCodeStorage + AccessTokenStorage + RefreshTokenStorage)
//   - oauth2.TokenRevocationStorage
//   - pkce.PKCERequestStorage
type FositeStore struct {
	db         *sql.DB
	bcryptCost int
	logger     *slog.Logger
}

// NewFositeStore creates a new fosite storage adapter.
func NewFositeStore(db *sql.DB, bcryptCost int) *FositeStore {
	return &FositeStore{
		db:         db,
		bcryptCost: bcryptCost,
		logger:     slog.Default().With("component", "fosite-store"),
	}
}

// fositeSession implements fosite.Session for token storage.
type fositeSession struct {
	UserID      int64             `json:"user_id"`
	Username    string            `json:"username"`
	Subject     string            `json:"subject"`
	ExpiresAtMap map[fosite.TokenType]time.Time `json:"expires_at_map"`
}

// SetExpiresAt implements fosite.Session.
func (s *fositeSession) SetExpiresAt(key fosite.TokenType, exp time.Time) {
	if s.ExpiresAtMap == nil {
		s.ExpiresAtMap = make(map[fosite.TokenType]time.Time)
	}
	s.ExpiresAtMap[key] = exp
}

// GetExpiresAt implements fosite.Session.
func (s *fositeSession) GetExpiresAt(key fosite.TokenType) time.Time {
	if s.ExpiresAtMap == nil {
		return time.Time{}
	}
	return s.ExpiresAtMap[key]
}

// GetUsername implements fosite.Session.
func (s *fositeSession) GetUsername() string {
	return s.Username
}

// GetSubject implements fosite.Session.
func (s *fositeSession) GetSubject() string {
	return s.Subject
}

// Clone implements fosite.Session.
func (s *fositeSession) Clone() fosite.Session {
	expiresAtMap := make(map[fosite.TokenType]time.Time)
	for k, v := range s.ExpiresAtMap {
		expiresAtMap[k] = v
	}
	return &fositeSession{
		UserID:       s.UserID,
		Username:     s.Username,
		Subject:      s.Subject,
		ExpiresAtMap: expiresAtMap,
	}
}

// --- fosite.ClientManager ---

// GetClient implements fosite.Storage (ClientManager).
func (s *FositeStore) GetClient(ctx context.Context, id string) (fosite.Client, error) {
	var secretHash, name, redirectURIsJSON, grantTypesJSON, scopesJSON string
	var ownerID sql.NullInt64

	err := s.db.QueryRowContext(ctx,
		`SELECT id, secret_hash, name, redirect_uris, grant_types, scopes, owner_id
		 FROM oauth_clients WHERE id = ?`, id,
	).Scan(&id, &secretHash, &name, &redirectURIsJSON, &grantTypesJSON, &scopesJSON, &ownerID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fosite.ErrNotFound
		}
		s.logger.Error("get client failed", "error", err)
		return nil, fosite.ErrServerError
	}

	var redirectURIs, grantTypes, scopes []string
	json.Unmarshal([]byte(redirectURIsJSON), &redirectURIs)
	json.Unmarshal([]byte(grantTypesJSON), &grantTypes)
	json.Unmarshal([]byte(scopesJSON), &scopes)

	return &fositeClient{
		id:           id,
		secretHash:   []byte(secretHash),
		name:         name,
		redirectURIs: redirectURIs,
		grantTypes:   grantTypes,
		scopes:       scopes,
	}, nil
}

// ClientAssertionJWTValid implements fosite.Storage.
func (s *FositeStore) ClientAssertionJWTValid(ctx context.Context, jti string) error {
	return fosite.ErrNotFound
}

// SetClientAssertionJWT implements fosite.Storage.
func (s *FositeStore) SetClientAssertionJWT(ctx context.Context, jti string, exp time.Time) error {
	return nil
}

// --- oauth2.AuthorizeCodeStorage ---

// CreateAuthorizeCodeSession stores an authorization code.
func (s *FositeStore) CreateAuthorizeCodeSession(ctx context.Context, code string, request fosite.Requester) error {
	sess := s.extractSession(request)
	sessJSON, _ := json.Marshal(sess)
	scopes := strings.Join(request.GetRequestedScopes(), " ")
	client := request.GetClient()

	form := request.GetRequestForm()
	codeChallenge := form.Get("code_challenge")
	codeChallengeMethod := form.Get("code_challenge_method")

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_authorization_codes (code, client_id, user_id, redirect_uri, scopes,
		 code_challenge, code_challenge_method, session_data, expires_at, created_at, used)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, 0)`,
		code, client.GetID(), sess.UserID,
		form.Get("redirect_uri"),
		scopes, codeChallenge, codeChallengeMethod,
		string(sessJSON),
		request.GetSession().GetExpiresAt(fosite.AuthorizeCode),
	)
	if err != nil {
		s.logger.Error("create auth code session failed", "error", err)
		return fosite.ErrServerError
	}
	return nil
}

// GetAuthorizeCodeSession retrieves an authorization code session.
func (s *FositeStore) GetAuthorizeCodeSession(ctx context.Context, code string, session fosite.Session) (fosite.Requester, error) {
	var clientID string
	var userID int64
	var redirectURI, scopes, sessData, codeChallenge, codeChallengeMethod string
	var expiresAt time.Time
	var used int

	err := s.db.QueryRowContext(ctx,
		`SELECT client_id, user_id, redirect_uri, scopes, code_challenge,
		 code_challenge_method, session_data, expires_at, used
		 FROM oauth_authorization_codes WHERE code = ?`, code,
	).Scan(&clientID, &userID, &redirectURI, &scopes, &codeChallenge,
		&codeChallengeMethod, &sessData, &expiresAt, &used)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fosite.ErrNotFound
		}
		s.logger.Error("get auth code session failed", "error", err)
		return nil, fosite.ErrServerError
	}

	if used != 0 {
		return nil, fosite.ErrInvalidatedAuthorizeCode
	}

	var sess fositeSession
	json.Unmarshal([]byte(sessData), &sess)

	client, err := s.GetClient(ctx, clientID)
	if err != nil {
		return nil, err
	}

	scopesList := splitScopes(scopes)
	form := url.Values{}
	form.Set("redirect_uri", redirectURI)
	form.Set("code_challenge", codeChallenge)
	form.Set("code_challenge_method", codeChallengeMethod)

	req := &fosite.Request{
		ID:                uuid(),
		Client:            client,
		RequestedScope:    scopesList,
		GrantedScope:      scopesList,
		Session:           &sess,
		Form:              form,
		RequestedAt:       time.Now(),
		RequestedAudience: []string{},
		GrantedAudience:   []string{},
	}

	return req, nil
}

// InvalidateAuthorizeCodeSession marks an authorization code as used.
func (s *FositeStore) InvalidateAuthorizeCodeSession(ctx context.Context, code string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE oauth_authorization_codes SET used = 1 WHERE code = ?", code,
	)
	if err != nil {
		s.logger.Error("invalidate auth code failed", "error", err)
		return fosite.ErrServerError
	}
	return nil
}

// --- oauth2.AccessTokenStorage ---

// CreateAccessTokenSession stores an access token.
func (s *FositeStore) CreateAccessTokenSession(ctx context.Context, signature string, request fosite.Requester) error {
	return s.createTokenSession(ctx, signature, TokenTypeAccess, request)
}

// GetAccessTokenSession retrieves an access token session.
func (s *FositeStore) GetAccessTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	return s.getTokenSession(ctx, signature, TokenTypeAccess)
}

// DeleteAccessTokenSession removes an access token.
func (s *FositeStore) DeleteAccessTokenSession(ctx context.Context, signature string) error {
	return s.deleteTokenSession(ctx, signature)
}

// --- oauth2.RefreshTokenStorage ---

// CreateRefreshTokenSession stores a refresh token.
// accessSignature links this refresh token to the corresponding access token.
func (s *FositeStore) CreateRefreshTokenSession(ctx context.Context, signature string, accessSignature string, request fosite.Requester) error {
	sess := s.extractSession(request)
	sessJSON, _ := json.Marshal(sess)
	scopes := strings.Join(request.GetGrantedScopes(), " ")
	client := request.GetClient()

	var userID interface{}
	if sess.UserID > 0 {
		userID = sess.UserID
	} else {
		userID = nil
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_tokens (signature, client_id, user_id, access_token_hash, refresh_token_hash,
		 scope, token_type, session_data, expires_at, created_at, consumed, parent_signature)
		 VALUES (?, ?, ?, ?, '', ?, ?, ?, ?, CURRENT_TIMESTAMP, 0, ?)`,
		signature, client.GetID(), userID,
		hashSignature(signature), scopes, TokenTypeRefresh, string(sessJSON),
		request.GetSession().GetExpiresAt(fosite.RefreshToken),
		accessSignature,
	)
	if err != nil {
		s.logger.Error("create refresh token session failed", "error", err)
		return fosite.ErrServerError
	}
	return nil
}

// GetRefreshTokenSession retrieves a refresh token session.
func (s *FositeStore) GetRefreshTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	return s.getTokenSession(ctx, signature, TokenTypeRefresh)
}

// DeleteRefreshTokenSession removes a refresh token.
func (s *FositeStore) DeleteRefreshTokenSession(ctx context.Context, signature string) error {
	return s.deleteTokenSession(ctx, signature)
}

// RotateRefreshToken rotates a refresh token by consuming the old one and linking to the new one.
func (s *FositeStore) RotateRefreshToken(ctx context.Context, requestID string, refreshTokenSignature string) error {
	// Mark the old refresh token as consumed
	_, err := s.db.ExecContext(ctx,
		"UPDATE oauth_tokens SET consumed = 1 WHERE signature = ? AND token_type = 'refresh'",
		refreshTokenSignature,
	)
	if err != nil {
		s.logger.Error("rotate refresh token failed", "error", err)
		return fosite.ErrServerError
	}
	return nil
}

// --- oauth2.TokenRevocationStorage ---
// Note: TokenRevocationStorage embeds RefreshTokenStorage and AccessTokenStorage,
// both of which are already implemented above.

// RevokeRefreshToken revokes a refresh token by request ID.
func (s *FositeStore) RevokeRefreshToken(ctx context.Context, requestID string) error {
	// Revoke all refresh tokens for this request
	_, err := s.db.ExecContext(ctx,
		"UPDATE oauth_tokens SET consumed = 1 WHERE token_type = 'refresh' AND signature = ?",
		requestID,
	)
	return err
}

// RevokeAccessToken revokes an access token by request ID.
func (s *FositeStore) RevokeAccessToken(ctx context.Context, requestID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM oauth_tokens WHERE signature = ? AND token_type = 'access'", requestID,
	)
	return err
}

// --- pkce.PKCERequestStorage ---

// CreatePKCERequestSession stores PKCE data for an authorization code.
func (s *FositeStore) CreatePKCERequestSession(ctx context.Context, signature string, request fosite.Requester) error {
	// PKCE data is stored as part of the authorization code session
	return nil
}

// GetPKCERequestSession retrieves PKCE data for an authorization code.
func (s *FositeStore) GetPKCERequestSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	return s.GetAuthorizeCodeSession(ctx, signature, session)
}

// DeletePKCERequestSession removes PKCE data for an authorization code.
func (s *FositeStore) DeletePKCERequestSession(ctx context.Context, signature string) error {
	return nil
}

// --- Internal helpers ---

func (s *FositeStore) createTokenSession(ctx context.Context, signature, tokenType string, request fosite.Requester) error {
	sess := s.extractSession(request)
	sessJSON, _ := json.Marshal(sess)
	scopes := strings.Join(request.GetGrantedScopes(), " ")
	client := request.GetClient()

	expiresAt := request.GetSession().GetExpiresAt(fosite.AccessToken)
	if tokenType == TokenTypeRefresh {
		expiresAt = request.GetSession().GetExpiresAt(fosite.RefreshToken)
	}

	// user_id can be NULL for client_credentials grants (no user involved)
	var userID interface{}
	if sess.UserID > 0 {
		userID = sess.UserID
	} else {
		userID = nil
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_tokens (signature, client_id, user_id, access_token_hash, refresh_token_hash,
		 scope, token_type, session_data, expires_at, created_at, consumed, parent_signature)
		 VALUES (?, ?, ?, ?, '', ?, ?, ?, ?, CURRENT_TIMESTAMP, 0, '')`,
		signature, client.GetID(), userID,
		hashSignature(signature), scopes, tokenType, string(sessJSON),
		expiresAt,
	)
	if err != nil {
		s.logger.Error("create token session failed", "token_type", tokenType, "error", err)
		return fosite.ErrServerError
	}
	return nil
}

func (s *FositeStore) getTokenSession(ctx context.Context, signature, tokenType string) (fosite.Requester, error) {
	var clientID string
	var userID sql.NullInt64
	var scopes, sessData string
	var expiresAt time.Time
	var consumed int

	err := s.db.QueryRowContext(ctx,
		`SELECT client_id, user_id, scope, session_data, expires_at, consumed
		 FROM oauth_tokens WHERE signature = ? AND token_type = ?`,
		signature, tokenType,
	).Scan(&clientID, &userID, &scopes, &sessData, &expiresAt, &consumed)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fosite.ErrNotFound
		}
		s.logger.Error("get token session failed", "error", err)
		return nil, fosite.ErrServerError
	}

	if consumed != 0 {
		// Token was consumed (refresh token rotation)
		// Revoke entire token family
		s.revokeTokenFamily(ctx, signature)
		return nil, fosite.ErrInactiveToken
	}

	var sess fositeSession
	json.Unmarshal([]byte(sessData), &sess)

	client, err := s.GetClient(ctx, clientID)
	if err != nil {
		return nil, err
	}

	scopesList := splitScopes(scopes)
	req := &fosite.Request{
		ID:                uuid(),
		Client:            client,
		RequestedScope:    scopesList,
		GrantedScope:      scopesList,
		Session:           &sess,
		Form:              url.Values{},
		RequestedAt:       time.Now(),
		RequestedAudience: []string{},
		GrantedAudience:   []string{},
	}

	return req, nil
}

func (s *FositeStore) deleteTokenSession(ctx context.Context, signature string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM oauth_tokens WHERE signature = ?", signature,
	)
	return err
}

// revokeTokenFamily revokes all tokens in a refresh token chain.
func (s *FositeStore) revokeTokenFamily(ctx context.Context, signature string) {
	_, err := s.db.ExecContext(ctx,
		`UPDATE oauth_tokens SET consumed = 1 WHERE client_id IN (
		   SELECT client_id FROM oauth_tokens WHERE signature = ?
		 )`, signature,
	)
	if err != nil {
		s.logger.Error("revoke token family failed", "signature", signature, "error", err)
	}
}

func (s *FositeStore) extractSession(request fosite.Requester) *fositeSession {
	if sess, ok := request.GetSession().(*fositeSession); ok {
		return sess
	}
	return &fositeSession{}
}

func hashSignature(sig string) string {
	h := sha256.Sum256([]byte(sig))
	return fmt.Sprintf("%x", h)
}

func splitScopes(scopes string) fosite.Arguments {
	if scopes == "" {
		return fosite.Arguments{}
	}
	return fosite.Arguments(strings.Split(scopes, " "))
}

func uuid() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// --- fositeClient implements fosite.Client ---

type fositeClient struct {
	id           string
	secretHash   []byte
	name         string
	redirectURIs []string
	grantTypes   []string
	scopes       []string
}

func (c *fositeClient) GetID() string                     { return c.id }
func (c *fositeClient) GetHashedSecret() []byte           { return c.secretHash }
func (c *fositeClient) GetRedirectURIs() []string          { return c.redirectURIs }
func (c *fositeClient) GetGrantTypes() fosite.Arguments    { return fosite.Arguments(c.grantTypes) }
func (c *fositeClient) GetResponseTypes() fosite.Arguments { return fosite.Arguments{"code"} }
func (c *fositeClient) GetScopes() fosite.Arguments        { return fosite.Arguments(c.scopes) }
func (c *fositeClient) IsPublic() bool                     { return false }
func (c *fositeClient) GetAudience() fosite.Arguments      { return fosite.Arguments{} }

// GetResponseModes implements fosite.ResponseModeClient.
func (c *fositeClient) GetResponseModes() []fosite.ResponseModeType {
	return []fosite.ResponseModeType{fosite.ResponseModeQuery}
}
