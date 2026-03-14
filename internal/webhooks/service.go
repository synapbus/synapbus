package webhooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
)

const (
	// MaxWebhooksPerAgent is the maximum number of webhooks per agent.
	MaxWebhooksPerAgent = 3
	// MaxDepth is the maximum webhook chain depth for loop prevention.
	MaxDepth = 5
	// AutoDisableThreshold is the consecutive failure count that triggers auto-disable.
	AutoDisableThreshold = 50
)

// WebhookService provides business logic for webhook operations.
type WebhookService struct {
	store        WebhookStore
	allowHTTP    bool
	allowPrivate bool
	logger       *slog.Logger
}

// NewWebhookService creates a new webhook service.
func NewWebhookService(store WebhookStore, allowHTTP, allowPrivate bool) *WebhookService {
	return &WebhookService{
		store:        store,
		allowHTTP:    allowHTTP,
		allowPrivate: allowPrivate,
		logger:       slog.Default().With("component", "webhooks"),
	}
}

// RegisterWebhook registers a new webhook for an agent.
func (s *WebhookService) RegisterWebhook(ctx context.Context, agentName, url string, events []string, secret string) (*Webhook, error) {
	// Validate events
	for _, e := range events {
		if !IsValidEvent(e) {
			return nil, fmt.Errorf("invalid event type: %q (valid: %v)", e, ValidEvents)
		}
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("at least one event type is required")
	}

	// Validate URL
	if err := ValidateWebhookURL(url, s.allowHTTP, s.allowPrivate); err != nil {
		return nil, fmt.Errorf("invalid webhook URL: %w", err)
	}

	// Check max webhooks
	count, err := s.store.CountWebhooksByAgent(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("count webhooks: %w", err)
	}
	if count >= MaxWebhooksPerAgent {
		return nil, fmt.Errorf("maximum webhooks reached (%d/%d); delete one before registering another", count, MaxWebhooksPerAgent)
	}

	// Hash the secret for storage
	secretHash := hashSecret(secret)

	wh := &Webhook{
		AgentName:  agentName,
		URL:        url,
		Events:     events,
		SecretHash: secretHash,
		Status:     WebhookStatusActive,
	}

	id, err := s.store.InsertWebhook(ctx, wh)
	if err != nil {
		return nil, fmt.Errorf("insert webhook: %w", err)
	}

	s.logger.Info("webhook registered",
		"id", id,
		"agent", agentName,
		"url", url,
		"events", events,
	)

	return wh, nil
}

// ListWebhooks returns all webhooks for an agent.
func (s *WebhookService) ListWebhooks(ctx context.Context, agentName string) ([]*Webhook, error) {
	return s.store.GetWebhooksByAgent(ctx, agentName)
}

// DeleteWebhook deletes a webhook owned by the agent.
func (s *WebhookService) DeleteWebhook(ctx context.Context, agentName string, webhookID int64) error {
	if err := s.store.DeleteWebhook(ctx, webhookID, agentName); err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}
	s.logger.Info("webhook deleted",
		"id", webhookID,
		"agent", agentName,
	)
	return nil
}

// GetActiveWebhooksForEvent returns active webhooks for an agent subscribed to a specific event.
func (s *WebhookService) GetActiveWebhooksForEvent(ctx context.Context, agentName, event string) ([]*Webhook, error) {
	return s.store.GetActiveWebhooksByEvent(ctx, agentName, event)
}

// RecordSuccess records a successful delivery: resets consecutive failures.
func (s *WebhookService) RecordSuccess(ctx context.Context, webhookID int64) error {
	return s.store.ResetConsecutiveFailures(ctx, webhookID)
}

// RecordFailure increments the failure counter and auto-disables if threshold reached.
func (s *WebhookService) RecordFailure(ctx context.Context, webhookID int64) error {
	count, err := s.store.IncrementConsecutiveFailures(ctx, webhookID)
	if err != nil {
		return err
	}
	if count >= AutoDisableThreshold {
		s.logger.Warn("auto-disabling webhook after consecutive failures",
			"webhook_id", webhookID,
			"failures", count,
		)
		return s.store.UpdateWebhookStatus(ctx, webhookID, WebhookStatusDisabled)
	}
	return nil
}

// Store returns the underlying webhook store (for delivery engine access).
func (s *WebhookService) Store() WebhookStore {
	return s.store
}

// hashSecret computes SHA-256 hex digest of the secret for storage.
func hashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}
