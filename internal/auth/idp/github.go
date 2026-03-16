package idp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

// GitHubProvider implements GitHub OAuth authentication.
// GitHub does not support OIDC discovery, so this uses plain OAuth 2.0
// with GitHub's user API endpoints.
type GitHubProvider struct {
	config *oauth2.Config
}

// NewGitHubProvider creates a new GitHub OAuth provider.
func NewGitHubProvider(clientID, clientSecret, redirectURL string) *GitHubProvider {
	return &GitHubProvider{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"read:user", "user:email"},
			Endpoint:     github.Endpoint,
		},
	}
}

func (p *GitHubProvider) ID() string          { return "github" }
func (p *GitHubProvider) Type() string        { return "oauth" }
func (p *GitHubProvider) DisplayName() string { return "GitHub" }

func (p *GitHubProvider) AuthCodeURL(state string) string {
	return p.config.AuthCodeURL(state)
}

func (p *GitHubProvider) Exchange(ctx context.Context, code string) (*ExternalUser, error) {
	token, err := p.config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("github: exchange code: %w", err)
	}

	client := p.config.Client(ctx, token)

	// Fetch user profile
	userInfo, err := fetchGitHubUser(client)
	if err != nil {
		return nil, err
	}

	// Fetch primary verified email
	email, err := fetchGitHubPrimaryEmail(client)
	if err != nil {
		// Non-fatal: email might not be available
		email = ""
	}

	// If user profile has an email and we didn't get one from the emails endpoint, use it
	if email == "" {
		if e, ok := userInfo["email"].(string); ok && e != "" {
			email = e
		}
	}

	idNum, _ := userInfo["id"].(float64)
	login, _ := userInfo["login"].(string)
	name, _ := userInfo["name"].(string)

	return &ExternalUser{
		ProviderID: "github",
		ExternalID: strconv.FormatInt(int64(idNum), 10),
		Email:      email,
		Name:       name,
		Username:   login,
		RawClaims:  userInfo,
	}, nil
}

func fetchGitHubUser(client *http.Client) (map[string]any, error) {
	resp, err := client.Get("https://api.github.com/user")
	if err != nil {
		return nil, fmt.Errorf("github: fetch user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("github: user API returned %d: %s", resp.StatusCode, body)
	}

	var user map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("github: decode user: %w", err)
	}
	return user, nil
}

// githubEmail represents an email entry from the GitHub emails API.
type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func fetchGitHubPrimaryEmail(client *http.Client) (string, error) {
	resp, err := client.Get("https://api.github.com/user/emails")
	if err != nil {
		return "", fmt.Errorf("github: fetch emails: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: emails API returned %d", resp.StatusCode)
	}

	var emails []githubEmail
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", fmt.Errorf("github: decode emails: %w", err)
	}

	// Find primary verified email
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	// Fallback: first verified email
	for _, e := range emails {
		if e.Verified {
			return e.Email, nil
		}
	}
	return "", fmt.Errorf("github: no verified email found")
}
