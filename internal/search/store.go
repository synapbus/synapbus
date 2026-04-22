package search

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// EmbeddingRecord tracks which messages have been embedded.
type EmbeddingRecord struct {
	MessageID  int64
	Provider   string
	Model      string
	Dimensions int
	EmbeddedAt time.Time
}

// QueueItem represents a message in the embedding queue.
type QueueItem struct {
	ID         int64
	MessageID  int64
	Status     string
	Attempts   int
	LastError  string
	CreatedAt  time.Time
}

// EmbeddingStore manages the embeddings and embedding_queue tables.
type EmbeddingStore struct {
	db *sql.DB
}

// NewEmbeddingStore creates a new embedding store.
func NewEmbeddingStore(db *sql.DB) *EmbeddingStore {
	return &EmbeddingStore{db: db}
}

// SaveEmbedding records that a message has been embedded.
func (s *EmbeddingStore) SaveEmbedding(ctx context.Context, messageID int64, provider, model string, dimensions int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO embeddings (message_id, provider, model, dimensions, embedded_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		messageID, provider, model, dimensions,
	)
	if err != nil {
		return fmt.Errorf("save embedding: %w", err)
	}
	return nil
}

// DeleteEmbedding removes an embedding record.
func (s *EmbeddingStore) DeleteEmbedding(ctx context.Context, messageID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM embeddings WHERE message_id = ?`, messageID,
	)
	return err
}

// DeleteAllEmbeddings removes all embedding records (for provider switch).
func (s *EmbeddingStore) DeleteAllEmbeddings(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM embeddings`)
	return err
}

// GetEmbeddingProvider returns the provider of the most recent embedding, if any.
func (s *EmbeddingStore) GetEmbeddingProvider(ctx context.Context) (string, error) {
	var provider string
	err := s.db.QueryRowContext(ctx,
		`SELECT provider FROM embeddings ORDER BY embedded_at DESC LIMIT 1`,
	).Scan(&provider)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return provider, err
}

// EmbeddingCount returns the number of embedded messages.
func (s *EmbeddingStore) EmbeddingCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM embeddings`).Scan(&count)
	return count, err
}

// --- Queue operations ---

// Enqueue adds a message to the embedding queue.
func (s *EmbeddingStore) Enqueue(ctx context.Context, messageID int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO embedding_queue (message_id, status, created_at)
		 VALUES (?, 'pending', CURRENT_TIMESTAMP)`,
		messageID,
	)
	if err != nil {
		return fmt.Errorf("enqueue message %d: %w", messageID, err)
	}
	return nil
}

// Dequeue atomically fetches and claims a batch of pending items.
func (s *EmbeddingStore) Dequeue(ctx context.Context, batchSize int) ([]QueueItem, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT id, message_id, attempts FROM embedding_queue
		 WHERE status = 'pending'
		 ORDER BY created_at ASC
		 LIMIT ?`,
		batchSize,
	)
	if err != nil {
		return nil, fmt.Errorf("query pending: %w", err)
	}

	var items []QueueItem
	var ids []int64
	for rows.Next() {
		var item QueueItem
		if err := rows.Scan(&item.ID, &item.MessageID, &item.Attempts); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan queue item: %w", err)
		}
		item.Status = "processing"
		items = append(items, item)
		ids = append(ids, item.ID)
	}
	rows.Close()

	if len(ids) == 0 {
		return nil, nil
	}

	// Mark as processing
	query := fmt.Sprintf(
		`UPDATE embedding_queue SET status = 'processing', attempts = attempts + 1 WHERE id IN (%s)`,
		makePlaceholders(len(ids)),
	)
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	_, err = tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("mark processing: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit dequeue: %w", err)
	}

	return items, nil
}

// MarkCompleted marks a queue item as completed.
func (s *EmbeddingStore) MarkCompleted(ctx context.Context, messageID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE embedding_queue SET status = 'completed', completed_at = CURRENT_TIMESTAMP
		 WHERE message_id = ?`,
		messageID,
	)
	return err
}

// MarkFailed marks a queue item as failed with an error message.
func (s *EmbeddingStore) MarkFailed(ctx context.Context, messageID int64, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE embedding_queue SET status = 'failed', last_error = ?
		 WHERE message_id = ?`,
		errMsg, messageID,
	)
	return err
}

// RequeueFailed re-queues failed items that haven't exceeded max attempts.
func (s *EmbeddingStore) RequeueFailed(ctx context.Context, maxAttempts int) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE embedding_queue SET status = 'pending'
		 WHERE status = 'failed' AND attempts < ?`,
		maxAttempts,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ResetStale resets items stuck in "processing" for too long.
func (s *EmbeddingStore) ResetStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	result, err := s.db.ExecContext(ctx,
		`UPDATE embedding_queue SET status = 'pending'
		 WHERE status = 'processing' AND created_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// PendingCount returns the number of pending items.
func (s *EmbeddingStore) PendingCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM embedding_queue WHERE status IN ('pending', 'processing')`,
	).Scan(&count)
	return count, err
}

// EnqueueAllMessages enqueues all messages that don't have embeddings yet.
func (s *EmbeddingStore) EnqueueAllMessages(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO embedding_queue (message_id, status, created_at)
		 SELECT m.id, 'pending', CURRENT_TIMESTAMP
		 FROM messages m
		 LEFT JOIN embeddings e ON e.message_id = m.id
		 LEFT JOIN embedding_queue q ON q.message_id = m.id
		 WHERE e.message_id IS NULL AND q.message_id IS NULL
		 AND TRIM(m.body) != ''`,
	)
	if err != nil {
		return 0, fmt.Errorf("enqueue all messages: %w", err)
	}
	return result.RowsAffected()
}

// GetMessageBody retrieves the body of a message by ID.
func (s *EmbeddingStore) GetMessageBody(ctx context.Context, messageID int64) (string, error) {
	var body string
	err := s.db.QueryRowContext(ctx,
		`SELECT body FROM messages WHERE id = ?`, messageID,
	).Scan(&body)
	if err != nil {
		return "", fmt.Errorf("get message body %d: %w", messageID, err)
	}
	return body, nil
}

// ClearQueue deletes all items from the embedding queue.
func (s *EmbeddingStore) ClearQueue(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM embedding_queue`)
	return err
}

// FailedCount returns the number of failed items in the queue.
func (s *EmbeddingStore) FailedCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM embedding_queue WHERE status = 'failed'`,
	).Scan(&count)
	return count, err
}

// EmbeddingStats returns aggregate statistics about the embedding subsystem.
type EmbeddingStatsResult struct {
	Provider      string `json:"provider"`
	TotalEmbedded int64  `json:"total_embedded"`
	PendingCount  int64  `json:"pending_count"`
	FailedCount   int64  `json:"failed_count"`
	Dimensions    int    `json:"dimensions"`
}

func makePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n*2-1)
	for i := 0; i < n; i++ {
		b[i*2] = '?'
		if i < n-1 {
			b[i*2+1] = ','
		}
	}
	return string(b)
}

// Stats returns aggregate embedding statistics.
func (s *EmbeddingStore) Stats(ctx context.Context) (*EmbeddingStatsResult, error) {
	stats := &EmbeddingStatsResult{}

	// Get provider and dimensions from most recent embedding
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(provider, ''), COALESCE(dimensions, 0) FROM embeddings ORDER BY embedded_at DESC LIMIT 1`,
	).Scan(&stats.Provider, &stats.Dimensions)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("get provider: %w", err)
	}

	stats.TotalEmbedded, _ = s.EmbeddingCount(ctx)
	stats.PendingCount, _ = s.PendingCount(ctx)
	stats.FailedCount, _ = s.FailedCount(ctx)

	return stats, nil
}
