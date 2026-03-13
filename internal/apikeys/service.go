package apikeys

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Service provides business logic for API key management.
type Service struct {
	store  Store
	logger *slog.Logger
}

// NewService creates a new API key service.
func NewService(store Store) *Service {
	return &Service{
		store:  store,
		logger: slog.Default().With("component", "apikeys"),
	}
}

// CreateKey creates a new API key with the given parameters.
// Returns the APIKey and the raw key (shown once).
func (s *Service) CreateKey(ctx context.Context, req CreateKeyRequest) (*APIKey, string, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, "", fmt.Errorf("key name is required")
	}

	key, rawKey, err := s.store.CreateKey(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("create API key: %w", err)
	}

	s.logger.Info("API key created",
		"key_id", key.ID,
		"name", key.Name,
		"user_id", key.UserID,
		"agent_id", key.AgentID,
		"read_only", key.ReadOnly,
	)

	return key, rawKey, nil
}

// ListKeys returns all non-revoked API keys for a user.
func (s *Service) ListKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	keys, err := s.store.ListKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list API keys: %w", err)
	}
	return keys, nil
}

// GetByID returns an API key by ID.
func (s *Service) GetByID(ctx context.Context, id int64) (*APIKey, error) {
	key, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get API key: %w", err)
	}
	return key, nil
}

// Authenticate verifies a raw API key and returns the associated APIKey.
// Also updates the last_used_at timestamp.
func (s *Service) Authenticate(ctx context.Context, rawKey string) (*APIKey, error) {
	if !strings.HasPrefix(rawKey, "sb_") {
		return nil, fmt.Errorf("invalid API key format")
	}

	key, err := s.store.Authenticate(ctx, rawKey)
	if err != nil {
		return nil, err
	}

	// Update last used asynchronously (best effort)
	go func() {
		if updateErr := s.store.UpdateLastUsed(context.Background(), key.ID); updateErr != nil {
			s.logger.Warn("failed to update last_used_at", "key_id", key.ID, "error", updateErr)
		}
	}()

	return key, nil
}

// RevokeKey soft-deletes an API key.
func (s *Service) RevokeKey(ctx context.Context, id int64) error {
	if err := s.store.RevokeKey(ctx, id); err != nil {
		return fmt.Errorf("revoke API key: %w", err)
	}

	s.logger.Info("API key revoked", "key_id", id)
	return nil
}

// DeleteKey permanently removes an API key.
func (s *Service) DeleteKey(ctx context.Context, id int64) error {
	if err := s.store.DeleteKey(ctx, id); err != nil {
		return fmt.Errorf("delete API key: %w", err)
	}

	s.logger.Info("API key deleted", "key_id", id)
	return nil
}
