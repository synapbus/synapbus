package idp

import (
	"context"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCProvider implements OpenID Connect authentication.
// Works with any OIDC-compliant provider (Google, Azure AD, etc.).
type OIDCProvider struct {
	id             string
	displayName    string
	issuerURL      string
	config         *oauth2.Config
	verifier       *oidc.IDTokenVerifier
	allowedDomains []string // Empty means allow all domains
	groupMapping   map[string]string
}

// OIDCConfig holds configuration for an OIDC provider.
type OIDCConfig struct {
	ID             string
	DisplayName    string
	IssuerURL      string
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	Scopes         []string
	AllowedDomains []string
	GroupMapping   map[string]string
}

// NewOIDCProvider creates a new OIDC provider using discovery.
func NewOIDCProvider(ctx context.Context, cfg OIDCConfig) (*OIDCProvider, error) {
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover %s: %w", cfg.IssuerURL, err)
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	}

	oauthConfig := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID: cfg.ClientID,
	})

	return &OIDCProvider{
		id:             cfg.ID,
		displayName:    cfg.DisplayName,
		issuerURL:      cfg.IssuerURL,
		config:         oauthConfig,
		verifier:       verifier,
		allowedDomains: cfg.AllowedDomains,
		groupMapping:   cfg.GroupMapping,
	}, nil
}

func (p *OIDCProvider) ID() string          { return p.id }
func (p *OIDCProvider) Type() string        { return "oidc" }
func (p *OIDCProvider) DisplayName() string { return p.displayName }

func (p *OIDCProvider) AuthCodeURL(state string) string {
	return p.config.AuthCodeURL(state)
}

func (p *OIDCProvider) Exchange(ctx context.Context, code string) (*ExternalUser, error) {
	token, err := p.config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("oidc: exchange code: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("oidc: no id_token in response")
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("oidc: verify id_token: %w", err)
	}

	// Extract claims
	var claims struct {
		Sub      string   `json:"sub"`
		Email    string   `json:"email"`
		Name     string   `json:"name"`
		Username string   `json:"preferred_username"`
		HD       string   `json:"hd"` // Google hosted domain
		Groups   []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oidc: parse claims: %w", err)
	}

	// Extract raw claims for storage
	var rawClaims map[string]any
	if err := idToken.Claims(&rawClaims); err != nil {
		rawClaims = map[string]any{"sub": claims.Sub}
	}

	// Validate domain restriction
	if len(p.allowedDomains) > 0 {
		if err := p.validateDomain(claims.Email, claims.HD); err != nil {
			return nil, err
		}
	}

	// Map groups through group mapping
	var mappedGroups []string
	if len(p.groupMapping) > 0 {
		for _, group := range claims.Groups {
			if mapped, ok := p.groupMapping[group]; ok {
				mappedGroups = append(mappedGroups, mapped)
			}
		}
	} else {
		mappedGroups = claims.Groups
	}

	// Derive username from email if preferred_username is empty
	username := claims.Username
	if username == "" && claims.Email != "" {
		parts := strings.SplitN(claims.Email, "@", 2)
		username = parts[0]
	}

	return &ExternalUser{
		ProviderID: p.id,
		ExternalID: claims.Sub,
		Email:      claims.Email,
		Name:       claims.Name,
		Username:   username,
		Groups:     mappedGroups,
		RawClaims:  rawClaims,
	}, nil
}

// validateDomain checks that the user's email domain is in the allowed list.
func (p *OIDCProvider) validateDomain(email, hostedDomain string) error {
	// Prefer hd (hosted domain) claim when available (Google Workspace)
	domain := hostedDomain
	if domain == "" && email != "" {
		parts := strings.SplitN(email, "@", 2)
		if len(parts) == 2 {
			domain = parts[1]
		}
	}

	if domain == "" {
		return fmt.Errorf("oidc: no domain found in claims, required domains: %v", p.allowedDomains)
	}

	for _, allowed := range p.allowedDomains {
		if strings.EqualFold(domain, allowed) {
			return nil
		}
	}

	return fmt.Errorf("oidc: domain %q not in allowed list %v", domain, p.allowedDomains)
}
