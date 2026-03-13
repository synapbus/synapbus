package auth

import "time"

// Config holds configuration for the auth subsystem.
type Config struct {
	// BcryptCost is the bcrypt hashing cost. Minimum 10, default 12.
	BcryptCost int

	// AccessTokenTTL is the lifetime of access tokens. Default 1 hour.
	AccessTokenTTL time.Duration

	// RefreshTokenLifetime is the absolute lifetime of refresh tokens. Default 30 days.
	RefreshTokenLifetime time.Duration

	// SessionLifetime is the lifetime of Web UI sessions. Default 24 hours.
	SessionLifetime time.Duration

	// IssuerURL is the OAuth issuer URL (e.g., http://localhost:8080).
	IssuerURL string

	// DevMode allows HTTP without TLS. When true, a warning is logged.
	DevMode bool

	// Secret is the system secret used for HMAC signing of tokens.
	// Must be at least 32 bytes.
	Secret []byte
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		BcryptCost:           12,
		AccessTokenTTL:       1 * time.Hour,
		RefreshTokenLifetime: 30 * 24 * time.Hour,
		SessionLifetime:      24 * time.Hour,
		DevMode:              true,
	}
}

// Validate checks that the config values are within acceptable ranges.
func (c Config) Validate() error {
	if c.BcryptCost < 10 {
		return ErrBcryptCostTooLow
	}
	if c.BcryptCost > 31 {
		return ErrBcryptCostTooHigh
	}
	if len(c.Secret) < 32 {
		return ErrSecretTooShort
	}
	return nil
}
