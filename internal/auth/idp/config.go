package idp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"
)

// LoadConfig reads environment variables and returns a slice of enabled identity providers.
// Providers are only included if both client ID and client secret are set.
func LoadConfig(baseURL string) []Provider {
	var providers []Provider

	// GitHub
	if clientID, clientSecret := os.Getenv("SYNAPBUS_IDP_GITHUB_CLIENT_ID"), os.Getenv("SYNAPBUS_IDP_GITHUB_CLIENT_SECRET"); clientID != "" && clientSecret != "" {
		redirectURL := baseURL + "/auth/callback/github"
		providers = append(providers, NewGitHubProvider(clientID, clientSecret, redirectURL))
		slog.Info("IdP enabled", "provider", "github")
	}

	// Google (OIDC)
	if clientID, clientSecret := os.Getenv("SYNAPBUS_IDP_GOOGLE_CLIENT_ID"), os.Getenv("SYNAPBUS_IDP_GOOGLE_CLIENT_SECRET"); clientID != "" && clientSecret != "" {
		var allowedDomains []string
		if domains := os.Getenv("SYNAPBUS_IDP_GOOGLE_ALLOWED_DOMAINS"); domains != "" {
			for _, d := range strings.Split(domains, ",") {
				d = strings.TrimSpace(d)
				if d != "" {
					allowedDomains = append(allowedDomains, d)
				}
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		p, err := NewOIDCProvider(ctx, OIDCConfig{
			ID:             "google",
			DisplayName:    "Google",
			IssuerURL:      "https://accounts.google.com",
			ClientID:       clientID,
			ClientSecret:   clientSecret,
			RedirectURL:    baseURL + "/auth/callback/google",
			AllowedDomains: allowedDomains,
		})
		if err != nil {
			slog.Error("failed to initialize Google OIDC provider", "error", err)
		} else {
			providers = append(providers, p)
			slog.Info("IdP enabled", "provider", "google", "allowed_domains", allowedDomains)
		}
	}

	// Azure AD (OIDC)
	if clientID, clientSecret := os.Getenv("SYNAPBUS_IDP_AZUREAD_CLIENT_ID"), os.Getenv("SYNAPBUS_IDP_AZUREAD_CLIENT_SECRET"); clientID != "" && clientSecret != "" {
		tenantID := os.Getenv("SYNAPBUS_IDP_AZUREAD_TENANT_ID")
		if tenantID == "" {
			tenantID = "common" // multi-tenant by default
		}

		var groupMapping map[string]string
		if gm := os.Getenv("SYNAPBUS_IDP_AZUREAD_GROUP_MAPPING"); gm != "" {
			if err := json.Unmarshal([]byte(gm), &groupMapping); err != nil {
				slog.Error("failed to parse Azure AD group mapping", "error", err)
			}
		}

		issuerURL := "https://login.microsoftonline.com/" + tenantID + "/v2.0"

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		p, err := NewOIDCProvider(ctx, OIDCConfig{
			ID:           "azuread",
			DisplayName:  "Microsoft",
			IssuerURL:    issuerURL,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  baseURL + "/auth/callback/azuread",
			GroupMapping: groupMapping,
		})
		if err != nil {
			slog.Error("failed to initialize Azure AD OIDC provider", "error", err)
		} else {
			providers = append(providers, p)
			slog.Info("IdP enabled", "provider", "azuread", "tenant_id", tenantID)
		}
	}

	return providers
}
