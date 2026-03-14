package k8s

import (
	"context"
	"fmt"
	"testing"
)

// fakeRunner implements JobRunner for testing, allowing control of IsAvailable
// and namespace without requiring a real K8s cluster.
type fakeRunner struct {
	available bool
	namespace string
}

func (f *fakeRunner) IsAvailable() bool { return f.available }
func (f *fakeRunner) GetNamespace() string { return f.namespace }
func (f *fakeRunner) CreateJob(ctx context.Context, handler *K8sHandler, msg *JobMessage) (string, error) {
	return fmt.Sprintf("synapbus-%s-%d", handler.AgentName, msg.MessageID), nil
}
func (f *fakeRunner) GetJobLogs(ctx context.Context, namespace, jobName string) (string, error) {
	return "fake logs", nil
}

func TestRegisterHandler_Success(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	runner := &fakeRunner{available: true, namespace: "default"}
	svc := NewK8sService(store, runner)
	ctx := context.Background()
	seedAgent(t, db, "svc-k8s-1")

	req := RegisterHandlerRequest{
		Image:  "myapp:latest",
		Events: []string{"message.received"},
		Env:    map[string]string{"FOO": "bar"},
	}

	handler, err := svc.RegisterHandler(ctx, "svc-k8s-1", req)
	if err != nil {
		t.Fatalf("RegisterHandler() error = %v", err)
	}
	if handler.ID <= 0 {
		t.Errorf("expected positive ID, got %d", handler.ID)
	}
	if handler.AgentName != "svc-k8s-1" {
		t.Errorf("AgentName = %q, want %q", handler.AgentName, "svc-k8s-1")
	}
	if handler.Image != "myapp:latest" {
		t.Errorf("Image = %q, want %q", handler.Image, "myapp:latest")
	}
	if handler.Status != HandlerStatusActive {
		t.Errorf("Status = %q, want %q", handler.Status, HandlerStatusActive)
	}
	if handler.Namespace != "default" {
		t.Errorf("Namespace = %q, want %q (should default to runner namespace)", handler.Namespace, "default")
	}
	if handler.TimeoutSeconds != 300 {
		t.Errorf("TimeoutSeconds = %d, want 300 (default)", handler.TimeoutSeconds)
	}
}

func TestRegisterHandler_CustomNamespaceAndTimeout(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	runner := &fakeRunner{available: true, namespace: "default"}
	svc := NewK8sService(store, runner)
	ctx := context.Background()
	seedAgent(t, db, "svc-k8s-2")

	req := RegisterHandlerRequest{
		Image:          "myapp:latest",
		Events:         []string{"message.received"},
		Namespace:      "custom-ns",
		TimeoutSeconds: 600,
	}

	handler, err := svc.RegisterHandler(ctx, "svc-k8s-2", req)
	if err != nil {
		t.Fatalf("RegisterHandler() error = %v", err)
	}
	if handler.Namespace != "custom-ns" {
		t.Errorf("Namespace = %q, want %q", handler.Namespace, "custom-ns")
	}
	if handler.TimeoutSeconds != 600 {
		t.Errorf("TimeoutSeconds = %d, want 600", handler.TimeoutSeconds)
	}
}

func TestRegisterHandler_InvalidEvent(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	runner := &fakeRunner{available: true, namespace: "default"}
	svc := NewK8sService(store, runner)
	ctx := context.Background()
	seedAgent(t, db, "svc-k8s-3")

	tests := []struct {
		name   string
		events []string
	}{
		{name: "invalid event", events: []string{"bogus.event"}},
		{name: "empty events", events: []string{}},
		{name: "mix valid and invalid", events: []string{"message.received", "nope"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := RegisterHandlerRequest{
				Image:  "myapp:latest",
				Events: tt.events,
			}
			_, err := svc.RegisterHandler(ctx, "svc-k8s-3", req)
			if err == nil {
				t.Error("expected error for invalid events, got nil")
			}
		})
	}
}

func TestRegisterHandler_MissingImage(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	runner := &fakeRunner{available: true, namespace: "default"}
	svc := NewK8sService(store, runner)
	ctx := context.Background()
	seedAgent(t, db, "svc-k8s-4")

	req := RegisterHandlerRequest{
		Image:  "",
		Events: []string{"message.received"},
	}
	_, err := svc.RegisterHandler(ctx, "svc-k8s-4", req)
	if err == nil {
		t.Error("expected error for missing image, got nil")
	}
}

func TestRegisterHandler_NotAvailable(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	runner := NewNoopRunner() // not available
	svc := NewK8sService(store, runner)
	ctx := context.Background()
	seedAgent(t, db, "svc-k8s-5")

	req := RegisterHandlerRequest{
		Image:  "myapp:latest",
		Events: []string{"message.received"},
	}

	_, err := svc.RegisterHandler(ctx, "svc-k8s-5", req)
	if err == nil {
		t.Error("expected error when K8s runner is not available, got nil")
	}
}

func TestRegisterHandler_MaxHandlers(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	runner := &fakeRunner{available: true, namespace: "default"}
	svc := NewK8sService(store, runner)
	ctx := context.Background()
	seedAgent(t, db, "svc-k8s-6")

	// Register MaxHandlersPerAgent handlers
	for i := 0; i < MaxHandlersPerAgent; i++ {
		req := RegisterHandlerRequest{
			Image:  fmt.Sprintf("img-%d:v1", i),
			Events: []string{"message.received"},
		}
		_, err := svc.RegisterHandler(ctx, "svc-k8s-6", req)
		if err != nil {
			t.Fatalf("register handler %d: %v", i, err)
		}
	}

	// Next one should fail
	req := RegisterHandlerRequest{
		Image:  "one-too-many:v1",
		Events: []string{"message.received"},
	}
	_, err := svc.RegisterHandler(ctx, "svc-k8s-6", req)
	if err == nil {
		t.Error("expected error when exceeding max handlers, got nil")
	}
}

func TestDeleteHandler_Service(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	runner := &fakeRunner{available: true, namespace: "default"}
	svc := NewK8sService(store, runner)
	ctx := context.Background()
	seedAgent(t, db, "svc-k8s-7")
	seedAgent(t, db, "svc-k8s-8")

	req := RegisterHandlerRequest{
		Image:  "del-img:v1",
		Events: []string{"message.received"},
	}
	handler, err := svc.RegisterHandler(ctx, "svc-k8s-7", req)
	if err != nil {
		t.Fatal(err)
	}

	// Wrong owner
	err = svc.DeleteHandler(ctx, "svc-k8s-8", handler.ID)
	if err == nil {
		t.Error("expected error when deleting with wrong owner")
	}

	// Correct owner
	err = svc.DeleteHandler(ctx, "svc-k8s-7", handler.ID)
	if err != nil {
		t.Fatalf("DeleteHandler() error = %v", err)
	}

	// Verify deleted
	handlers, err := svc.ListHandlers(ctx, "svc-k8s-7")
	if err != nil {
		t.Fatal(err)
	}
	if len(handlers) != 0 {
		t.Errorf("expected 0 handlers after delete, got %d", len(handlers))
	}
}

func TestListHandlers(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)
	runner := &fakeRunner{available: true, namespace: "default"}
	svc := NewK8sService(store, runner)
	ctx := context.Background()
	seedAgent(t, db, "svc-k8s-9")

	// Empty initially
	handlers, err := svc.ListHandlers(ctx, "svc-k8s-9")
	if err != nil {
		t.Fatal(err)
	}
	if len(handlers) != 0 {
		t.Errorf("expected 0 handlers initially, got %d", len(handlers))
	}

	// Register two
	for i := 0; i < 2; i++ {
		req := RegisterHandlerRequest{
			Image:  fmt.Sprintf("list-img-%d:v1", i),
			Events: []string{"message.received"},
		}
		if _, err := svc.RegisterHandler(ctx, "svc-k8s-9", req); err != nil {
			t.Fatal(err)
		}
	}

	handlers, err = svc.ListHandlers(ctx, "svc-k8s-9")
	if err != nil {
		t.Fatal(err)
	}
	if len(handlers) != 2 {
		t.Errorf("expected 2 handlers, got %d", len(handlers))
	}
}

func TestIsAvailable(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteK8sStore(db)

	t.Run("available runner", func(t *testing.T) {
		svc := NewK8sService(store, &fakeRunner{available: true})
		if !svc.IsAvailable() {
			t.Error("expected IsAvailable() = true")
		}
	})

	t.Run("unavailable runner", func(t *testing.T) {
		svc := NewK8sService(store, NewNoopRunner())
		if svc.IsAvailable() {
			t.Error("expected IsAvailable() = false")
		}
	})
}
