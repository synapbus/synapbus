// Package idp provides enterprise identity provider integrations for SynapBus.
// Supported providers: GitHub (OAuth), Google (OIDC), Azure AD (OIDC).
package idp

import "context"

// Provider is the interface that all identity providers must implement.
type Provider interface {
	// ID returns the unique identifier for this provider (e.g., "github", "google", "azuread").
	ID() string
	// Type returns the provider type ("oauth" or "oidc").
	Type() string
	// DisplayName returns the human-readable name (e.g., "GitHub", "Google").
	DisplayName() string
	// AuthCodeURL generates the authorization URL with the given state parameter.
	AuthCodeURL(state string) string
	// Exchange trades an authorization code for user information.
	Exchange(ctx context.Context, code string) (*ExternalUser, error)
}

// ExternalUser represents user information obtained from an external identity provider.
type ExternalUser struct {
	ProviderID string
	ExternalID string
	Email      string
	Name       string
	Username   string
	Groups     []string
	RawClaims  map[string]any
}

// ProviderInfo is a minimal representation of a provider for API responses.
type ProviderInfo struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
}
