package attachments

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/storage"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	ctx := context.Background()
	if err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Seed test user and agent.
	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testowner', 'hash', 'Test Owner')`)

	return db
}

func newTestStore(t *testing.T) (*SQLiteStore, *sql.DB) {
	t.Helper()
	db := newTestDB(t)
	store := NewSQLiteStore(db, slog.Default())
	return store, db
}

// seedMessage inserts a test message so we can reference it via foreign key.
func seedMessage(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	// Ensure user exists.
	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testowner', 'hash', 'Test Owner')`)
	// Ensure agents exist.
	db.Exec(`INSERT OR IGNORE INTO agents (id, name, display_name, owner_id, api_key_hash) VALUES (1, 'agent-a', 'Agent A', 1, 'hash')`)
	db.Exec(`INSERT OR IGNORE INTO agents (id, name, display_name, owner_id, api_key_hash) VALUES (2, 'agent-b', 'Agent B', 1, 'hash')`)
	// Ensure conversation exists.
	db.Exec(`INSERT OR IGNORE INTO conversations (id, subject, created_by) VALUES (1, 'test', 'agent-a')`)
	// Insert message.
	result, err := db.Exec(`INSERT INTO messages (conversation_id, from_agent, to_agent, body) VALUES (1, 'agent-a', 'agent-b', 'hello')`)
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestSQLiteStore_InsertMetadata(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	a := &Attachment{
		Hash:             "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		OriginalFilename: "test.png",
		Size:             1024,
		MIMEType:         "image/png",
		UploadedBy:       "agent-a",
	}

	if err := store.InsertMetadata(ctx, a); err != nil {
		t.Fatalf("InsertMetadata: %v", err)
	}

	if a.ID == 0 {
		t.Error("ID not populated after insert")
	}
	if a.CreatedAt.IsZero() {
		t.Error("CreatedAt not populated after insert")
	}
}

func TestSQLiteStore_GetByHash(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	hash := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	// Insert two rows with the same hash.
	a1 := &Attachment{Hash: hash, OriginalFilename: "file1.png", Size: 100, MIMEType: "image/png", UploadedBy: "agent-a"}
	a2 := &Attachment{Hash: hash, OriginalFilename: "file2.png", Size: 100, MIMEType: "image/png", UploadedBy: "agent-b"}

	if err := store.InsertMetadata(ctx, a1); err != nil {
		t.Fatalf("InsertMetadata a1: %v", err)
	}
	if err := store.InsertMetadata(ctx, a2); err != nil {
		t.Fatalf("InsertMetadata a2: %v", err)
	}

	results, err := store.GetByHash(ctx, hash)
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestSQLiteStore_GetByMessageID(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	msgID := seedMessage(t, db)

	a := &Attachment{
		Hash:             "1111111111111111111111111111111111111111111111111111111111111111",
		OriginalFilename: "report.pdf",
		Size:             2048,
		MIMEType:         "application/pdf",
		MessageID:        &msgID,
		UploadedBy:       "agent-a",
	}
	if err := store.InsertMetadata(ctx, a); err != nil {
		t.Fatalf("InsertMetadata: %v", err)
	}

	results, err := store.GetByMessageID(ctx, msgID)
	if err != nil {
		t.Fatalf("GetByMessageID: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].OriginalFilename != "report.pdf" {
		t.Errorf("filename = %s, want report.pdf", results[0].OriginalFilename)
	}
}

func TestSQLiteStore_DeleteByHash(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	hash := "2222222222222222222222222222222222222222222222222222222222222222"
	a := &Attachment{Hash: hash, OriginalFilename: "del.txt", Size: 10, MIMEType: "text/plain", UploadedBy: "agent-a"}
	if err := store.InsertMetadata(ctx, a); err != nil {
		t.Fatalf("InsertMetadata: %v", err)
	}

	if err := store.DeleteByHash(ctx, hash); err != nil {
		t.Fatalf("DeleteByHash: %v", err)
	}

	results, err := store.GetByHash(ctx, hash)
	if err != nil {
		t.Fatalf("GetByHash after delete: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after delete, got %d", len(results))
	}
}

func TestSQLiteStore_CountReferences(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	hash := "3333333333333333333333333333333333333333333333333333333333333333"

	count, err := store.CountReferences(ctx, hash)
	if err != nil {
		t.Fatalf("CountReferences: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 references, got %d", count)
	}

	a := &Attachment{Hash: hash, OriginalFilename: "ref.txt", Size: 5, MIMEType: "text/plain", UploadedBy: "agent-a"}
	if err := store.InsertMetadata(ctx, a); err != nil {
		t.Fatalf("InsertMetadata: %v", err)
	}

	count, err = store.CountReferences(ctx, hash)
	if err != nil {
		t.Fatalf("CountReferences: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 reference, got %d", count)
	}
}

func TestSQLiteStore_FindOrphanHashes(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	// Create an orphan attachment (no message_id).
	orphanHash := "4444444444444444444444444444444444444444444444444444444444444444"
	a := &Attachment{Hash: orphanHash, OriginalFilename: "orphan.txt", Size: 5, MIMEType: "text/plain", UploadedBy: "agent-a"}
	if err := store.InsertMetadata(ctx, a); err != nil {
		t.Fatalf("InsertMetadata orphan: %v", err)
	}

	// Create a linked attachment.
	msgID := seedMessage(t, db)
	linkedHash := "5555555555555555555555555555555555555555555555555555555555555555"
	aLinked := &Attachment{Hash: linkedHash, OriginalFilename: "linked.txt", Size: 5, MIMEType: "text/plain", MessageID: &msgID, UploadedBy: "agent-a"}
	if err := store.InsertMetadata(ctx, aLinked); err != nil {
		t.Fatalf("InsertMetadata linked: %v", err)
	}

	orphans, err := store.FindOrphanHashes(ctx)
	if err != nil {
		t.Fatalf("FindOrphanHashes: %v", err)
	}

	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0] != orphanHash {
		t.Errorf("orphan hash = %s, want %s", orphans[0], orphanHash)
	}
}
