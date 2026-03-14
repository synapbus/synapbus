package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Webhook status constants.
const (
	WebhookStatusActive   = "active"
	WebhookStatusDisabled = "disabled"
)

// Delivery status constants.
const (
	DeliveryStatusPending      = "pending"
	DeliveryStatusDelivered    = "delivered"
	DeliveryStatusRetrying     = "retrying"
	DeliveryStatusDeadLettered = "dead_lettered"
)

// Webhook represents a registered webhook endpoint for an agent.
type Webhook struct {
	ID                    int64     `json:"id"`
	AgentName             string    `json:"agent_name"`
	URL                   string    `json:"url"`
	Events                []string  `json:"events"`
	SecretHash            string    `json:"-"`
	Status                string    `json:"status"`
	ConsecutiveFailures   int       `json:"consecutive_failures"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// WebhookDelivery represents a single delivery attempt for a webhook.
type WebhookDelivery struct {
	ID          int64      `json:"id"`
	WebhookID   int64      `json:"webhook_id"`
	AgentName   string     `json:"agent_name"`
	Event       string     `json:"event"`
	MessageID   int64      `json:"message_id"`
	Payload     string     `json:"payload"`
	Status      string     `json:"status"`
	HTTPStatus  int        `json:"http_status,omitempty"`
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"`
	LastError   string     `json:"last_error,omitempty"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
	Depth       int        `json:"depth"`
	CreatedAt   time.Time  `json:"created_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}

// WebhookStore defines the storage interface for webhook operations.
type WebhookStore interface {
	InsertWebhook(ctx context.Context, webhook *Webhook) (int64, error)
	GetWebhookByID(ctx context.Context, id int64) (*Webhook, error)
	GetWebhooksByAgent(ctx context.Context, agentName string) ([]*Webhook, error)
	GetActiveWebhooksByEvent(ctx context.Context, agentName string, event string) ([]*Webhook, error)
	UpdateWebhookStatus(ctx context.Context, id int64, status string) error
	IncrementConsecutiveFailures(ctx context.Context, id int64) (int, error)
	ResetConsecutiveFailures(ctx context.Context, id int64) error
	DeleteWebhook(ctx context.Context, id int64, agentName string) error
	CountWebhooksByAgent(ctx context.Context, agentName string) (int, error)
	InsertDelivery(ctx context.Context, delivery *WebhookDelivery) (int64, error)
	UpdateDeliveryStatus(ctx context.Context, id int64, status string, httpStatus int, lastError string, nextRetryAt *time.Time, deliveredAt *time.Time) error
	UpdateDeliveryAttempts(ctx context.Context, id int64, attempts int) error
	GetPendingDeliveries(ctx context.Context, limit int) ([]*WebhookDelivery, error)
	GetRetryableDeliveries(ctx context.Context, now time.Time, limit int) ([]*WebhookDelivery, error)
	GetDeliveriesByAgent(ctx context.Context, agentName string, status string, limit int) ([]*WebhookDelivery, error)
	GetDeliveryByID(ctx context.Context, id int64) (*WebhookDelivery, error)
	PurgeOldDeadLetters(ctx context.Context, olderThan time.Time) (int64, error)
}

// SQLiteWebhookStore implements WebhookStore using SQLite.
type SQLiteWebhookStore struct {
	db *sql.DB
}

// NewSQLiteWebhookStore creates a new SQLite-backed webhook store.
func NewSQLiteWebhookStore(db *sql.DB) *SQLiteWebhookStore {
	return &SQLiteWebhookStore{db: db}
}

// InsertWebhook inserts a new webhook and returns its ID.
func (s *SQLiteWebhookStore) InsertWebhook(ctx context.Context, webhook *Webhook) (int64, error) {
	eventsJSON, err := json.Marshal(webhook.Events)
	if err != nil {
		return 0, fmt.Errorf("marshal webhook events: %w", err)
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO webhooks (agent_name, url, events, secret_hash, status, consecutive_failures, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		webhook.AgentName, webhook.URL, string(eventsJSON), webhook.SecretHash, webhook.Status,
	)
	if err != nil {
		return 0, fmt.Errorf("insert webhook: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get webhook id: %w", err)
	}
	webhook.ID = id
	return id, nil
}

// GetWebhookByID retrieves a webhook by its ID.
func (s *SQLiteWebhookStore) GetWebhookByID(ctx context.Context, id int64) (*Webhook, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_name, url, events, secret_hash, status, consecutive_failures, created_at, updated_at
		 FROM webhooks WHERE id = ?`, id,
	)
	return scanWebhook(row)
}

// GetWebhooksByAgent retrieves all webhooks for an agent.
func (s *SQLiteWebhookStore) GetWebhooksByAgent(ctx context.Context, agentName string) ([]*Webhook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_name, url, events, secret_hash, status, consecutive_failures, created_at, updated_at
		 FROM webhooks WHERE agent_name = ?
		 ORDER BY created_at ASC`, agentName,
	)
	if err != nil {
		return nil, fmt.Errorf("get webhooks by agent: %w", err)
	}
	defer rows.Close()

	return scanWebhooks(rows)
}

// GetActiveWebhooksByEvent retrieves active webhooks for an agent that are subscribed to a given event.
// Since events is a JSON array stored as TEXT and there are few webhooks per agent,
// we fetch all active webhooks for the agent and filter in Go.
func (s *SQLiteWebhookStore) GetActiveWebhooksByEvent(ctx context.Context, agentName string, event string) ([]*Webhook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_name, url, events, secret_hash, status, consecutive_failures, created_at, updated_at
		 FROM webhooks WHERE agent_name = ? AND status = 'active'
		 ORDER BY created_at ASC`, agentName,
	)
	if err != nil {
		return nil, fmt.Errorf("get active webhooks by event: %w", err)
	}
	defer rows.Close()

	all, err := scanWebhooks(rows)
	if err != nil {
		return nil, err
	}

	var matched []*Webhook
	for _, wh := range all {
		for _, e := range wh.Events {
			if e == event {
				matched = append(matched, wh)
				break
			}
		}
	}
	if matched == nil {
		matched = []*Webhook{}
	}
	return matched, nil
}

// UpdateWebhookStatus updates the status of a webhook.
func (s *SQLiteWebhookStore) UpdateWebhookStatus(ctx context.Context, id int64, status string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE webhooks SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("update webhook status: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("webhook not found")
	}
	return nil
}

// IncrementConsecutiveFailures atomically increments the consecutive failure count
// and returns the new value.
func (s *SQLiteWebhookStore) IncrementConsecutiveFailures(ctx context.Context, id int64) (int, error) {
	_, err := s.db.ExecContext(ctx,
		`UPDATE webhooks SET consecutive_failures = consecutive_failures + 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	if err != nil {
		return 0, fmt.Errorf("increment consecutive failures: %w", err)
	}

	var count int
	err = s.db.QueryRowContext(ctx,
		`SELECT consecutive_failures FROM webhooks WHERE id = ?`, id,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("get consecutive failures: %w", err)
	}
	return count, nil
}

// ResetConsecutiveFailures resets the consecutive failure count to zero.
func (s *SQLiteWebhookStore) ResetConsecutiveFailures(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE webhooks SET consecutive_failures = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	if err != nil {
		return fmt.Errorf("reset consecutive failures: %w", err)
	}
	return nil
}

// DeleteWebhook deletes a webhook only if it is owned by the specified agent.
func (s *SQLiteWebhookStore) DeleteWebhook(ctx context.Context, id int64, agentName string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM webhooks WHERE id = ? AND agent_name = ?`,
		id, agentName,
	)
	if err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("webhook not found or not owned by agent")
	}
	return nil
}

// CountWebhooksByAgent returns the number of webhooks for an agent.
func (s *SQLiteWebhookStore) CountWebhooksByAgent(ctx context.Context, agentName string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM webhooks WHERE agent_name = ?`, agentName,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count webhooks by agent: %w", err)
	}
	return count, nil
}

// InsertDelivery inserts a new webhook delivery record and returns its ID.
func (s *SQLiteWebhookStore) InsertDelivery(ctx context.Context, delivery *WebhookDelivery) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO webhook_deliveries (webhook_id, agent_name, event, message_id, payload, status, http_status, attempts, max_attempts, last_error, next_retry_at, depth, created_at, delivered_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?)`,
		delivery.WebhookID, delivery.AgentName, delivery.Event, delivery.MessageID,
		delivery.Payload, delivery.Status, nullIntIfZero(delivery.HTTPStatus),
		delivery.Attempts, delivery.MaxAttempts, nullStringIfEmpty(delivery.LastError),
		nullTimePtr(delivery.NextRetryAt), delivery.Depth, nullTimePtr(delivery.DeliveredAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert delivery: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get delivery id: %w", err)
	}
	delivery.ID = id
	return id, nil
}

// UpdateDeliveryStatus updates the status and related fields of a delivery.
func (s *SQLiteWebhookStore) UpdateDeliveryStatus(ctx context.Context, id int64, status string, httpStatus int, lastError string, nextRetryAt *time.Time, deliveredAt *time.Time) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE webhook_deliveries
		 SET status = ?, http_status = ?, last_error = ?, next_retry_at = ?, delivered_at = ?
		 WHERE id = ?`,
		status, nullIntIfZero(httpStatus), nullStringIfEmpty(lastError),
		nullTimePtr(nextRetryAt), nullTimePtr(deliveredAt), id,
	)
	if err != nil {
		return fmt.Errorf("update delivery status: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("delivery not found")
	}
	return nil
}

// UpdateDeliveryAttempts updates the attempt count for a delivery.
func (s *SQLiteWebhookStore) UpdateDeliveryAttempts(ctx context.Context, id int64, attempts int) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE webhook_deliveries SET attempts = ? WHERE id = ?`,
		attempts, id,
	)
	if err != nil {
		return fmt.Errorf("update delivery attempts: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("delivery not found")
	}
	return nil
}

// GetPendingDeliveries retrieves pending deliveries up to the specified limit.
func (s *SQLiteWebhookStore) GetPendingDeliveries(ctx context.Context, limit int) ([]*WebhookDelivery, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, webhook_id, agent_name, event, message_id, payload, status, http_status, attempts, max_attempts, last_error, next_retry_at, depth, created_at, delivered_at
		 FROM webhook_deliveries WHERE status = 'pending'
		 ORDER BY created_at ASC
		 LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get pending deliveries: %w", err)
	}
	defer rows.Close()

	return scanDeliveries(rows)
}

// GetRetryableDeliveries retrieves deliveries that are due for retry.
func (s *SQLiteWebhookStore) GetRetryableDeliveries(ctx context.Context, now time.Time, limit int) ([]*WebhookDelivery, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, webhook_id, agent_name, event, message_id, payload, status, http_status, attempts, max_attempts, last_error, next_retry_at, depth, created_at, delivered_at
		 FROM webhook_deliveries WHERE status = 'retrying' AND next_retry_at <= ?
		 ORDER BY next_retry_at ASC
		 LIMIT ?`, now, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get retryable deliveries: %w", err)
	}
	defer rows.Close()

	return scanDeliveries(rows)
}

// GetDeliveriesByAgent retrieves deliveries for an agent filtered by status.
func (s *SQLiteWebhookStore) GetDeliveriesByAgent(ctx context.Context, agentName string, status string, limit int) ([]*WebhookDelivery, error) {
	if limit <= 0 {
		limit = 50
	}

	var rows *sql.Rows
	var err error

	if status != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, webhook_id, agent_name, event, message_id, payload, status, http_status, attempts, max_attempts, last_error, next_retry_at, depth, created_at, delivered_at
			 FROM webhook_deliveries WHERE agent_name = ? AND status = ?
			 ORDER BY created_at DESC
			 LIMIT ?`, agentName, status, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, webhook_id, agent_name, event, message_id, payload, status, http_status, attempts, max_attempts, last_error, next_retry_at, depth, created_at, delivered_at
			 FROM webhook_deliveries WHERE agent_name = ?
			 ORDER BY created_at DESC
			 LIMIT ?`, agentName, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("get deliveries by agent: %w", err)
	}
	defer rows.Close()

	return scanDeliveries(rows)
}

// GetDeliveryByID retrieves a single delivery by its ID.
func (s *SQLiteWebhookStore) GetDeliveryByID(ctx context.Context, id int64) (*WebhookDelivery, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, webhook_id, agent_name, event, message_id, payload, status, http_status, attempts, max_attempts, last_error, next_retry_at, depth, created_at, delivered_at
		 FROM webhook_deliveries WHERE id = ?`, id,
	)
	return scanDelivery(row)
}

// PurgeOldDeadLetters deletes dead-lettered deliveries older than the given time
// and returns the number of rows deleted.
func (s *SQLiteWebhookStore) PurgeOldDeadLetters(ctx context.Context, olderThan time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM webhook_deliveries WHERE status = 'dead_lettered' AND created_at < ?`,
		olderThan,
	)
	if err != nil {
		return 0, fmt.Errorf("purge old dead letters: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("get rows affected: %w", err)
	}
	return count, nil
}

// scanWebhook scans a single webhook from sql.Row.
func scanWebhook(row *sql.Row) (*Webhook, error) {
	var wh Webhook
	var eventsJSON string

	err := row.Scan(
		&wh.ID, &wh.AgentName, &wh.URL, &eventsJSON, &wh.SecretHash,
		&wh.Status, &wh.ConsecutiveFailures, &wh.CreatedAt, &wh.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(eventsJSON), &wh.Events); err != nil {
		return nil, fmt.Errorf("unmarshal webhook events: %w", err)
	}
	if wh.Events == nil {
		wh.Events = []string{}
	}
	return &wh, nil
}

// scanWebhookFromRows scans a single webhook from sql.Rows.
func scanWebhookFromRows(rows *sql.Rows) (*Webhook, error) {
	var wh Webhook
	var eventsJSON string

	err := rows.Scan(
		&wh.ID, &wh.AgentName, &wh.URL, &eventsJSON, &wh.SecretHash,
		&wh.Status, &wh.ConsecutiveFailures, &wh.CreatedAt, &wh.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan webhook: %w", err)
	}

	if err := json.Unmarshal([]byte(eventsJSON), &wh.Events); err != nil {
		return nil, fmt.Errorf("unmarshal webhook events: %w", err)
	}
	if wh.Events == nil {
		wh.Events = []string{}
	}
	return &wh, nil
}

// scanWebhooks scans multiple webhook rows.
func scanWebhooks(rows *sql.Rows) ([]*Webhook, error) {
	var webhooks []*Webhook
	for rows.Next() {
		wh, err := scanWebhookFromRows(rows)
		if err != nil {
			return nil, err
		}
		webhooks = append(webhooks, wh)
	}
	if webhooks == nil {
		webhooks = []*Webhook{}
	}
	return webhooks, rows.Err()
}

// scanDelivery scans a single delivery from sql.Row.
func scanDelivery(row *sql.Row) (*WebhookDelivery, error) {
	var d WebhookDelivery
	var httpStatus sql.NullInt64
	var lastError sql.NullString
	var nextRetryAt sql.NullTime
	var deliveredAt sql.NullTime

	err := row.Scan(
		&d.ID, &d.WebhookID, &d.AgentName, &d.Event, &d.MessageID,
		&d.Payload, &d.Status, &httpStatus, &d.Attempts, &d.MaxAttempts,
		&lastError, &nextRetryAt, &d.Depth, &d.CreatedAt, &deliveredAt,
	)
	if err != nil {
		return nil, err
	}

	if httpStatus.Valid {
		d.HTTPStatus = int(httpStatus.Int64)
	}
	if lastError.Valid {
		d.LastError = lastError.String
	}
	if nextRetryAt.Valid {
		d.NextRetryAt = &nextRetryAt.Time
	}
	if deliveredAt.Valid {
		d.DeliveredAt = &deliveredAt.Time
	}
	return &d, nil
}

// scanDeliveryFromRows scans a single delivery from sql.Rows.
func scanDeliveryFromRows(rows *sql.Rows) (*WebhookDelivery, error) {
	var d WebhookDelivery
	var httpStatus sql.NullInt64
	var lastError sql.NullString
	var nextRetryAt sql.NullTime
	var deliveredAt sql.NullTime

	err := rows.Scan(
		&d.ID, &d.WebhookID, &d.AgentName, &d.Event, &d.MessageID,
		&d.Payload, &d.Status, &httpStatus, &d.Attempts, &d.MaxAttempts,
		&lastError, &nextRetryAt, &d.Depth, &d.CreatedAt, &deliveredAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan delivery: %w", err)
	}

	if httpStatus.Valid {
		d.HTTPStatus = int(httpStatus.Int64)
	}
	if lastError.Valid {
		d.LastError = lastError.String
	}
	if nextRetryAt.Valid {
		d.NextRetryAt = &nextRetryAt.Time
	}
	if deliveredAt.Valid {
		d.DeliveredAt = &deliveredAt.Time
	}
	return &d, nil
}

// scanDeliveries scans multiple delivery rows.
func scanDeliveries(rows *sql.Rows) ([]*WebhookDelivery, error) {
	var deliveries []*WebhookDelivery
	for rows.Next() {
		d, err := scanDeliveryFromRows(rows)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, d)
	}
	if deliveries == nil {
		deliveries = []*WebhookDelivery{}
	}
	return deliveries, rows.Err()
}

// nullIntIfZero returns sql.NullInt64 that is null when value is 0.
func nullIntIfZero(v int) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(v), Valid: true}
}

// nullStringIfEmpty returns sql.NullString that is null when value is empty.
func nullStringIfEmpty(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

// nullTimePtr returns sql.NullTime from a *time.Time, null when pointer is nil.
func nullTimePtr(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}
