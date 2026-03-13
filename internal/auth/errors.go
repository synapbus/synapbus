package auth

import "errors"

// Sentinel errors for the auth package.
var (
	// User errors
	ErrUserNotFound      = errors.New("user not found")
	ErrDuplicateUsername = errors.New("username already exists")
	ErrInvalidUsername   = errors.New("username must be 3-64 characters, alphanumeric and underscore only")
	ErrPasswordTooShort  = errors.New("password must be at least 8 characters")
	ErrPasswordTooLong   = errors.New("password must be at most 72 bytes (bcrypt limit)")
	ErrInvalidPassword   = errors.New("invalid password")

	// Session errors
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session expired")

	// OAuth client errors
	ErrClientNotFound = errors.New("client not found")

	// Token errors
	ErrTokenNotFound  = errors.New("token not found")
	ErrTokenExpired   = errors.New("token expired")
	ErrTokenConsumed  = errors.New("token already consumed")
	ErrTokenInvalid   = errors.New("invalid token")
	ErrCodeNotFound   = errors.New("authorization code not found")
	ErrCodeExpired    = errors.New("authorization code expired")
	ErrCodeUsed       = errors.New("authorization code already used")
	ErrPKCERequired   = errors.New("PKCE code_challenge is required")
	ErrPKCEPlainNotAllowed = errors.New("code_challenge_method 'plain' is not allowed, use S256")
	ErrPKCEVerifierMismatch = errors.New("code_verifier does not match code_challenge")

	// Config errors
	ErrBcryptCostTooLow  = errors.New("bcrypt cost must be at least 10")
	ErrBcryptCostTooHigh = errors.New("bcrypt cost must be at most 31")
	ErrSecretTooShort    = errors.New("system secret must be at least 32 bytes")
)
