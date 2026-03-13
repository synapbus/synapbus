package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ClientStore defines the storage interface for OAuth client operations.
type ClientStore interface {
	CreateClient(ctx context.Context, name string, redirectURIs, grantTypes, scopes []string, ownerID int64) (*OAuthClient, string, error)
	GetClient(ctx context.Context, clientID string) (*OAuthClient, error)
	ListClientsByOwner(ctx context.Context, ownerID int64) ([]*OAuthClient, error)
	VerifyClientSecret(ctx context.Context, clientID, secret string) (*OAuthClient, error)
}

// SQLiteClientStore implements ClientStore using SQLite.
type SQLiteClientStore struct {
	db         *sql.DB
	bcryptCost int
}

// NewSQLiteClientStore creates a new SQLite-backed client store.
func NewSQLiteClientStore(db *sql.DB, bcryptCost int) *SQLiteClientStore {
	if bcryptCost < 10 {
		bcryptCost = 12
	}
	return &SQLiteClientStore{db: db, bcryptCost: bcryptCost}
}

// CreateClient registers a new OAuth client, generating client_id and client_secret.
// Returns the client and the raw secret (shown once).
func (s *SQLiteClientStore) CreateClient(ctx context.Context, name string, redirectURIs, grantTypes, scopes []string, ownerID int64) (*OAuthClient, string, error) {
	clientID, err := generateClientID()
	if err != nil {
		return nil, "", fmt.Errorf("generate client_id: %w", err)
	}

	secret, err := generateClientSecret()
	if err != nil {
		return nil, "", fmt.Errorf("generate client_secret: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(secret), s.bcryptCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash secret: %w", err)
	}

	if redirectURIs == nil {
		redirectURIs = []string{}
	}
	if grantTypes == nil {
		grantTypes = []string{"client_credentials"}
	}
	if scopes == nil {
		scopes = []string{}
	}

	redirectURIsJSON, _ := json.Marshal(redirectURIs)
	grantTypesJSON, _ := json.Marshal(grantTypes)
	scopesJSON, _ := json.Marshal(scopes)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO oauth_clients (id, secret_hash, name, redirect_uris, grant_types, scopes, owner_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		clientID, string(hash), name, string(redirectURIsJSON), string(grantTypesJSON), string(scopesJSON), ownerID,
	)
	if err != nil {
		return nil, "", fmt.Errorf("insert client: %w", err)
	}

	client := &OAuthClient{
		ID:           clientID,
		SecretHash:   string(hash),
		Name:         name,
		RedirectURIs: redirectURIs,
		GrantTypes:   grantTypes,
		Scopes:       scopes,
		OwnerID:      ownerID,
		CreatedAt:    time.Now(),
	}

	return client, secret, nil
}

// GetClient retrieves an OAuth client by its ID.
func (s *SQLiteClientStore) GetClient(ctx context.Context, clientID string) (*OAuthClient, error) {
	var redirectURIsJSON, grantTypesJSON, scopesJSON string
	var ownerID sql.NullInt64
	client := &OAuthClient{}

	err := s.db.QueryRowContext(ctx,
		`SELECT id, secret_hash, name, redirect_uris, grant_types, scopes, owner_id, created_at
		 FROM oauth_clients WHERE id = ?`, clientID,
	).Scan(&client.ID, &client.SecretHash, &client.Name,
		&redirectURIsJSON, &grantTypesJSON, &scopesJSON, &ownerID, &client.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrClientNotFound
		}
		return nil, fmt.Errorf("query client: %w", err)
	}

	json.Unmarshal([]byte(redirectURIsJSON), &client.RedirectURIs)
	json.Unmarshal([]byte(grantTypesJSON), &client.GrantTypes)
	json.Unmarshal([]byte(scopesJSON), &client.Scopes)
	if ownerID.Valid {
		client.OwnerID = ownerID.Int64
	}

	if client.RedirectURIs == nil {
		client.RedirectURIs = []string{}
	}
	if client.GrantTypes == nil {
		client.GrantTypes = []string{}
	}
	if client.Scopes == nil {
		client.Scopes = []string{}
	}

	return client, nil
}

// ListClientsByOwner returns all OAuth clients owned by the given user.
func (s *SQLiteClientStore) ListClientsByOwner(ctx context.Context, ownerID int64) ([]*OAuthClient, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, secret_hash, name, redirect_uris, grant_types, scopes, owner_id, created_at
		 FROM oauth_clients WHERE owner_id = ? ORDER BY name`, ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("query clients: %w", err)
	}
	defer rows.Close()

	var clients []*OAuthClient
	for rows.Next() {
		var redirectURIsJSON, grantTypesJSON, scopesJSON string
		var oid sql.NullInt64
		c := &OAuthClient{}
		if err := rows.Scan(&c.ID, &c.SecretHash, &c.Name,
			&redirectURIsJSON, &grantTypesJSON, &scopesJSON, &oid, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan client: %w", err)
		}
		json.Unmarshal([]byte(redirectURIsJSON), &c.RedirectURIs)
		json.Unmarshal([]byte(grantTypesJSON), &c.GrantTypes)
		json.Unmarshal([]byte(scopesJSON), &c.Scopes)
		if oid.Valid {
			c.OwnerID = oid.Int64
		}
		if c.RedirectURIs == nil {
			c.RedirectURIs = []string{}
		}
		if c.GrantTypes == nil {
			c.GrantTypes = []string{}
		}
		if c.Scopes == nil {
			c.Scopes = []string{}
		}
		clients = append(clients, c)
	}
	if clients == nil {
		clients = []*OAuthClient{}
	}
	return clients, rows.Err()
}

// VerifyClientSecret checks a client_id/secret combination.
func (s *SQLiteClientStore) VerifyClientSecret(ctx context.Context, clientID, secret string) (*OAuthClient, error) {
	client, err := s.GetClient(ctx, clientID)
	if err != nil {
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(client.SecretHash), []byte(secret)); err != nil {
		return nil, ErrInvalidPassword
	}

	return client, nil
}

// generateClientID creates a random client identifier.
func generateClientID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "synapbus_" + hex.EncodeToString(b), nil
}

// generateClientSecret creates a random client secret.
func generateClientSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sbs_" + hex.EncodeToString(b), nil
}

// HasGrantType checks if the client supports the given grant type.
func (c *OAuthClient) HasGrantType(grantType string) bool {
	for _, gt := range c.GrantTypes {
		if strings.EqualFold(gt, grantType) {
			return true
		}
	}
	return false
}
