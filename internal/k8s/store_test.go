package k8s

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/synapbus/synapbus/internal/storage"
	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.RunMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

// seedAgent inserts prerequisite user and agent rows so that foreign key
// constraints on k8s_handlers (agent_name -> agents.name) are satisfied.
func seedAgent(t *testing.T, db *sql.DB, agentName string) {
	t.Helper()
	ctx := context.Background()
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO users (username, password_hash, display_name) VALUES (?, 'hash', 'Test User')`,
		"owner-"+agentName,
	)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var ownerID int64
	err = db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, "owner-"+agentName).Scan(&ownerID)
	if err != nil {
		t.Fatalf("get owner id: %v", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES (?, ?, 'ai', '{}', ?, 'hash', 'active')`,
		agentName, agentName, ownerID,
	)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
}

func makeTestHandler(agentName, image, namespace string, events []string) *K8sHandler {
	return &K8sHandler{
		AgentName:       agentName,
		Image:           image,
		Events:          events,
		Namespace:       namespace,
		ResourcesMemory: "256Mi",
		ResourcesCPU:    "100m",
		Env:             map[string]string{"KEY": "value"},
		TimeoutSeconds:  300,
		Status:          HandlerStatusActive,
	}
}

func TestInsertHandler(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	ctx := context.Background()
	seedAgent(t, db, "k8s-agent-a")

	tests := []struct {
		name    string
		handler *K8sHandler
		wantErr bool
	}{
		{
			name:    "valid handler",
			handler: makeTestHandler("k8s-agent-a", "myimage:latest", "default", []string{"message.received"}),
			wantErr: false,
		},
		{
			name:    "different image same agent",
			handler: makeTestHandler("k8s-agent-a", "other:v2", "default", []string{"message.mentioned"}),
			wantErr: false,
		},
		{
			name:    "duplicate agent+image+namespace fails",
			handler: makeTestHandler("k8s-agent-a", "myimage:latest", "default", []string{"message.received"}),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := store.InsertHandler(ctx, tt.handler)
			if (err != nil) != tt.wantErr {
				t.Fatalf("InsertHandler() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && id <= 0 {
				t.Errorf("expected positive ID, got %d", id)
			}
		})
	}
}

func TestGetHandlerByID(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	ctx := context.Background()
	seedAgent(t, db, "k8s-agent-b")

	h := makeTestHandler("k8s-agent-b", "myimage:latest", "test-ns", []string{"message.received", "channel.message"})
	id, err := store.InsertHandler(ctx, h)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		id      int64
		wantErr bool
	}{
		{name: "existing handler", id: id, wantErr: false},
		{name: "non-existent handler", id: 9999, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.GetHandlerByID(ctx, tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetHandlerByID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if got.ID != id {
					t.Errorf("ID = %d, want %d", got.ID, id)
				}
				if got.AgentName != "k8s-agent-b" {
					t.Errorf("AgentName = %q, want %q", got.AgentName, "k8s-agent-b")
				}
				if got.Image != "myimage:latest" {
					t.Errorf("Image = %q, want %q", got.Image, "myimage:latest")
				}
				if got.Namespace != "test-ns" {
					t.Errorf("Namespace = %q, want %q", got.Namespace, "test-ns")
				}
				if len(got.Events) != 2 {
					t.Errorf("Events length = %d, want 2", len(got.Events))
				}
				if got.Env["KEY"] != "value" {
					t.Errorf("Env[KEY] = %q, want %q", got.Env["KEY"], "value")
				}
				if got.TimeoutSeconds != 300 {
					t.Errorf("TimeoutSeconds = %d, want 300", got.TimeoutSeconds)
				}
				if got.Status != HandlerStatusActive {
					t.Errorf("Status = %q, want %q", got.Status, HandlerStatusActive)
				}
			}
		})
	}
}

func TestGetHandlersByAgent(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	ctx := context.Background()
	seedAgent(t, db, "k8s-agent-c")
	seedAgent(t, db, "k8s-agent-d")

	// Two handlers for agent-c
	h1 := makeTestHandler("k8s-agent-c", "img1:v1", "ns1", []string{"message.received"})
	if _, err := store.InsertHandler(ctx, h1); err != nil {
		t.Fatal(err)
	}
	h2 := makeTestHandler("k8s-agent-c", "img2:v1", "ns1", []string{"message.mentioned"})
	if _, err := store.InsertHandler(ctx, h2); err != nil {
		t.Fatal(err)
	}

	// One handler for agent-d
	h3 := makeTestHandler("k8s-agent-d", "img1:v1", "ns1", []string{"message.received"})
	if _, err := store.InsertHandler(ctx, h3); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		agentName string
		wantCount int
	}{
		{name: "agent with two handlers", agentName: "k8s-agent-c", wantCount: 2},
		{name: "agent with one handler", agentName: "k8s-agent-d", wantCount: 1},
		{name: "non-existent agent", agentName: "nope", wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlers, err := store.GetHandlersByAgent(ctx, tt.agentName)
			if err != nil {
				t.Fatalf("GetHandlersByAgent() error = %v", err)
			}
			if len(handlers) != tt.wantCount {
				t.Errorf("got %d handlers, want %d", len(handlers), tt.wantCount)
			}
		})
	}
}

func TestGetActiveHandlersByEvent(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	ctx := context.Background()
	seedAgent(t, db, "k8s-agent-e")

	// Active handler for message.received
	h1 := makeTestHandler("k8s-agent-e", "img-recv:v1", "ns", []string{"message.received"})
	if _, err := store.InsertHandler(ctx, h1); err != nil {
		t.Fatal(err)
	}

	// Active handler for both events
	h2 := makeTestHandler("k8s-agent-e", "img-both:v1", "ns", []string{"message.received", "message.mentioned"})
	if _, err := store.InsertHandler(ctx, h2); err != nil {
		t.Fatal(err)
	}

	// Disabled handler for message.received (should not appear)
	h3 := makeTestHandler("k8s-agent-e", "img-dis:v1", "ns", []string{"message.received"})
	h3.Status = HandlerStatusDisabled
	if _, err := store.InsertHandler(ctx, h3); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		agentName string
		event     string
		wantCount int
	}{
		{name: "message.received matches two active", agentName: "k8s-agent-e", event: "message.received", wantCount: 2},
		{name: "message.mentioned matches one active", agentName: "k8s-agent-e", event: "message.mentioned", wantCount: 1},
		{name: "channel.message matches none", agentName: "k8s-agent-e", event: "channel.message", wantCount: 0},
		{name: "non-existent agent", agentName: "nope", event: "message.received", wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlers, err := store.GetActiveHandlersByEvent(ctx, tt.agentName, tt.event)
			if err != nil {
				t.Fatalf("GetActiveHandlersByEvent() error = %v", err)
			}
			if len(handlers) != tt.wantCount {
				t.Errorf("got %d handlers, want %d", len(handlers), tt.wantCount)
			}
		})
	}
}

func TestDeleteHandler(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	ctx := context.Background()
	seedAgent(t, db, "k8s-agent-f")
	seedAgent(t, db, "k8s-agent-g")

	h := makeTestHandler("k8s-agent-f", "del-img:v1", "ns", []string{"message.received"})
	id, err := store.InsertHandler(ctx, h)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		id        int64
		agentName string
		wantErr   bool
	}{
		{name: "wrong owner fails", id: id, agentName: "k8s-agent-g", wantErr: true},
		{name: "correct owner succeeds", id: id, agentName: "k8s-agent-f", wantErr: false},
		{name: "already deleted fails", id: id, agentName: "k8s-agent-f", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.DeleteHandler(ctx, tt.id, tt.agentName)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DeleteHandler() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInsertJobRun(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	ctx := context.Background()
	seedAgent(t, db, "k8s-agent-h")

	h := makeTestHandler("k8s-agent-h", "job-img:v1", "ns", []string{"message.received"})
	hID, err := store.InsertHandler(ctx, h)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	run := &K8sJobRun{
		HandlerID: hID,
		AgentName: "k8s-agent-h",
		MessageID: 100,
		JobName:   "synapbus-k8s-agent-h-100",
		Namespace: "ns",
		Status:    "pending",
		StartedAt: &now,
	}

	id, err := store.InsertJobRun(ctx, run)
	if err != nil {
		t.Fatalf("InsertJobRun() error = %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive ID, got %d", id)
	}
	if run.ID != id {
		t.Errorf("run.ID = %d, want %d", run.ID, id)
	}

	// Verify round trip
	got, err := store.GetJobRunByJobName(ctx, "synapbus-k8s-agent-h-100")
	if err != nil {
		t.Fatal(err)
	}
	if got.HandlerID != hID {
		t.Errorf("HandlerID = %d, want %d", got.HandlerID, hID)
	}
	if got.Status != "pending" {
		t.Errorf("Status = %q, want %q", got.Status, "pending")
	}
	if got.StartedAt == nil {
		t.Error("StartedAt should not be nil")
	}
}

func TestUpdateJobRunStatus(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	ctx := context.Background()
	seedAgent(t, db, "k8s-agent-i")

	h := makeTestHandler("k8s-agent-i", "upd-img:v1", "ns", []string{"message.received"})
	hID, err := store.InsertHandler(ctx, h)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	run := &K8sJobRun{
		HandlerID: hID,
		AgentName: "k8s-agent-i",
		MessageID: 200,
		JobName:   "synapbus-k8s-agent-i-200",
		Namespace: "ns",
		Status:    "pending",
		StartedAt: &now,
	}
	id, err := store.InsertJobRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}

	// Update to succeeded
	completed := time.Now()
	err = store.UpdateJobRunStatus(ctx, id, "succeeded", "", &now, &completed)
	if err != nil {
		t.Fatalf("UpdateJobRunStatus(succeeded) error = %v", err)
	}

	got, err := store.GetJobRunByJobName(ctx, "synapbus-k8s-agent-i-200")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "succeeded" {
		t.Errorf("Status = %q, want %q", got.Status, "succeeded")
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt should not be nil")
	}

	// Update to failed
	err = store.UpdateJobRunStatus(ctx, id, "failed", "OOMKilled", &now, &completed)
	if err != nil {
		t.Fatalf("UpdateJobRunStatus(failed) error = %v", err)
	}

	got, err = store.GetJobRunByJobName(ctx, "synapbus-k8s-agent-i-200")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" {
		t.Errorf("Status = %q, want %q", got.Status, "failed")
	}
	if got.FailureReason != "OOMKilled" {
		t.Errorf("FailureReason = %q, want %q", got.FailureReason, "OOMKilled")
	}

	// Non-existent run
	err = store.UpdateJobRunStatus(ctx, 9999, "failed", "test", nil, nil)
	if err == nil {
		t.Error("expected error for non-existent job run")
	}
}

func TestGetJobRunsByAgent(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	ctx := context.Background()
	seedAgent(t, db, "k8s-agent-j")
	seedAgent(t, db, "k8s-agent-k")

	hJ := makeTestHandler("k8s-agent-j", "runs-img:v1", "ns", []string{"message.received"})
	hJID, err := store.InsertHandler(ctx, hJ)
	if err != nil {
		t.Fatal(err)
	}

	hK := makeTestHandler("k8s-agent-k", "runs-img:v1", "ns", []string{"message.received"})
	hKID, err := store.InsertHandler(ctx, hK)
	if err != nil {
		t.Fatal(err)
	}

	// Runs for agent-j
	for i, status := range []string{"pending", "succeeded", "failed"} {
		run := &K8sJobRun{
			HandlerID: hJID,
			AgentName: "k8s-agent-j",
			MessageID: int64(300 + i),
			JobName:   "j-run-" + status,
			Namespace: "ns",
			Status:    status,
		}
		if _, err := store.InsertJobRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}

	// Run for agent-k
	runK := &K8sJobRun{
		HandlerID: hKID,
		AgentName: "k8s-agent-k",
		MessageID: 400,
		JobName:   "k-run-1",
		Namespace: "ns",
		Status:    "pending",
	}
	if _, err := store.InsertJobRun(ctx, runK); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		agentName string
		status    string
		wantCount int
	}{
		{name: "all runs for agent-j", agentName: "k8s-agent-j", status: "", wantCount: 3},
		{name: "pending for agent-j", agentName: "k8s-agent-j", status: "pending", wantCount: 1},
		{name: "succeeded for agent-j", agentName: "k8s-agent-j", status: "succeeded", wantCount: 1},
		{name: "failed for agent-j", agentName: "k8s-agent-j", status: "failed", wantCount: 1},
		{name: "all for agent-k", agentName: "k8s-agent-k", status: "", wantCount: 1},
		{name: "non-existent agent", agentName: "nope", status: "", wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs, err := store.GetJobRunsByAgent(ctx, tt.agentName, tt.status, 50)
			if err != nil {
				t.Fatalf("GetJobRunsByAgent() error = %v", err)
			}
			if len(runs) != tt.wantCount {
				t.Errorf("got %d runs, want %d", len(runs), tt.wantCount)
			}
		})
	}
}
