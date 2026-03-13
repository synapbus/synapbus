// Package attachments provides content-addressable file storage for SynapBus.
//
// Files are stored on the local filesystem using a two-level sharded directory
// structure based on the SHA-256 hash of the content:
//
//	{data_dir}/attachments/{hash[0:2]}/{hash[2:4]}/{hash}
//
// Deduplication is automatic: identical content produces the same hash and is
// stored only once on disk, while each upload creates its own metadata row in
// SQLite. Garbage collection removes orphaned files that are no longer
// referenced by any message.
package attachments
