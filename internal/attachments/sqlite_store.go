package attachments

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// SQLiteStore implements Store backed by modernc.org/sqlite.
type SQLiteStore struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewSQLiteStore creates a new SQLite-backed attachment metadata store.
func NewSQLiteStore(db *sql.DB, logger *slog.Logger) *SQLiteStore {
	return &SQLiteStore{
		db:     db,
		logger: logger.With("component", "attachment-store"),
	}
}

// InsertMetadata inserts a new attachment metadata row.
func (s *SQLiteStore) InsertMetadata(ctx context.Context, a *Attachment) error {
	const query = `INSERT INTO attachments (hash, original_filename, size, mime_type, message_id, uploaded_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, query,
		a.Hash,
		a.OriginalFilename,
		a.Size,
		a.MIMEType,
		a.MessageID,
		a.UploadedBy,
		now,
	)
	if err != nil {
		return fmt.Errorf("insert attachment metadata: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}

	a.ID = id
	a.CreatedAt = now

	s.logger.Debug("attachment metadata inserted",
		"id", id,
		"hash", a.Hash,
		"filename", a.OriginalFilename,
	)
	return nil
}

// GetByHash returns all attachment metadata rows matching the given hash.
func (s *SQLiteStore) GetByHash(ctx context.Context, hash string) ([]*Attachment, error) {
	const query = `SELECT id, hash, original_filename, size, mime_type, message_id, uploaded_by, created_at
		FROM attachments WHERE hash = ? ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, hash)
	if err != nil {
		return nil, fmt.Errorf("query attachments by hash: %w", err)
	}
	defer rows.Close()

	return scanAttachments(rows)
}

// GetByMessageID returns all attachment metadata rows for a given message.
func (s *SQLiteStore) GetByMessageID(ctx context.Context, messageID int64) ([]*Attachment, error) {
	const query = `SELECT id, hash, original_filename, size, mime_type, message_id, uploaded_by, created_at
		FROM attachments WHERE message_id = ? ORDER BY created_at ASC`

	rows, err := s.db.QueryContext(ctx, query, messageID)
	if err != nil {
		return nil, fmt.Errorf("query attachments by message_id: %w", err)
	}
	defer rows.Close()

	return scanAttachments(rows)
}

// DeleteByHash removes all metadata rows matching the given hash.
func (s *SQLiteStore) DeleteByHash(ctx context.Context, hash string) error {
	const query = `DELETE FROM attachments WHERE hash = ?`

	result, err := s.db.ExecContext(ctx, query, hash)
	if err != nil {
		return fmt.Errorf("delete attachment metadata: %w", err)
	}

	n, _ := result.RowsAffected()
	s.logger.Debug("attachment metadata deleted", "hash", hash, "rows", n)
	return nil
}

// FindOrphanHashes returns hashes that have no valid message reference.
// A hash is orphaned if all its metadata rows have message_id IS NULL or
// the referenced message no longer exists.
func (s *SQLiteStore) FindOrphanHashes(ctx context.Context) ([]string, error) {
	const query = `SELECT DISTINCT a.hash FROM attachments a
		WHERE a.message_id IS NULL
		OR a.message_id NOT IN (SELECT id FROM messages)
		GROUP BY a.hash
		HAVING COUNT(CASE WHEN a.message_id IN (SELECT id FROM messages) THEN 1 END) = 0`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("find orphan hashes: %w", err)
	}
	defer rows.Close()

	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("scan orphan hash: %w", err)
		}
		hashes = append(hashes, h)
	}
	return hashes, rows.Err()
}

// CountReferences returns the number of metadata rows referencing the hash.
func (s *SQLiteStore) CountReferences(ctx context.Context, hash string) (int64, error) {
	const query = `SELECT COUNT(*) FROM attachments WHERE hash = ?`

	var count int64
	if err := s.db.QueryRowContext(ctx, query, hash).Scan(&count); err != nil {
		return 0, fmt.Errorf("count references: %w", err)
	}
	return count, nil
}

// scanAttachments scans rows into a slice of Attachment pointers.
func scanAttachments(rows *sql.Rows) ([]*Attachment, error) {
	var attachments []*Attachment
	for rows.Next() {
		a := &Attachment{}
		var createdAtStr string
		if err := rows.Scan(
			&a.ID,
			&a.Hash,
			&a.OriginalFilename,
			&a.Size,
			&a.MIMEType,
			&a.MessageID,
			&a.UploadedBy,
			&createdAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		// Parse the created_at timestamp.
		if t, err := time.Parse("2006-01-02 15:04:05", createdAtStr); err == nil {
			a.CreatedAt = t
		} else if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
			a.CreatedAt = t
		}
		attachments = append(attachments, a)
	}
	return attachments, rows.Err()
}
