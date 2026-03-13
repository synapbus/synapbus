package attachments

import "context"

// Store is the metadata persistence interface for attachments.
type Store interface {
	// InsertMetadata inserts a new attachment metadata row.
	InsertMetadata(ctx context.Context, a *Attachment) error

	// GetByHash returns all attachment metadata rows matching the given hash.
	GetByHash(ctx context.Context, hash string) ([]*Attachment, error)

	// GetByMessageID returns all attachment metadata rows for a given message.
	GetByMessageID(ctx context.Context, messageID int64) ([]*Attachment, error)

	// DeleteByHash removes all metadata rows matching the given hash.
	DeleteByHash(ctx context.Context, hash string) error

	// FindOrphanHashes returns hashes that have no message_id reference
	// (message_id IS NULL or the referenced message no longer exists).
	FindOrphanHashes(ctx context.Context) ([]string, error)

	// CountReferences returns the number of metadata rows referencing the hash.
	CountReferences(ctx context.Context, hash string) (int64, error)
}
