package auth

import (
	"crypto/rand"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	"github.com/ory/fosite/token/hmac"
)

// NewOAuthProvider creates a configured fosite OAuth 2.1 provider.
// It supports authorization code with PKCE (S256 only), client credentials, and refresh token rotation.
func NewOAuthProvider(cfg Config, store *FositeStore) fosite.OAuth2Provider {
	secret := cfg.Secret
	if len(secret) < 32 {
		// Generate a random secret if not configured
		secret = make([]byte, 32)
		rand.Read(secret)
	}

	config := &fosite.Config{
		AccessTokenLifespan:        cfg.AccessTokenTTL,
		RefreshTokenLifespan:       cfg.RefreshTokenLifetime,
		AuthorizeCodeLifespan:      10 * time.Minute,
		GlobalSecret:               secret,
		SendDebugMessagesToClients:  cfg.DevMode,
		EnforcePKCE:                true,
		EnforcePKCEForPublicClients: true,
		EnablePKCEPlainChallengeMethod: false,
		TokenURL:                   cfg.IssuerURL + "/oauth/token",
		HashCost:                   cfg.BcryptCost,
	}

	// HMACSHAStrategy for token generation
	hmacStrategy := &hmac.HMACStrategy{
		Config: config,
	}

	_ = hmacStrategy

	return compose.Compose(
		config,
		store,
		&compose.CommonStrategy{
			CoreStrategy: compose.NewOAuth2HMACStrategy(config),
		},
		compose.OAuth2AuthorizeExplicitFactory,
		compose.OAuth2ClientCredentialsGrantFactory,
		compose.OAuth2RefreshTokenGrantFactory,
		compose.OAuth2PKCEFactory,
		compose.OAuth2TokenIntrospectionFactory,
	)
}

// NewSession creates a new fosite session for a user.
func NewSession(user *User) fosite.Session {
	return &fositeSession{
		UserID:   user.ID,
		Username: user.Username,
		Subject:  user.Username,
	}
}
