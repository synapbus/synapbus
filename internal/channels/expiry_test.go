package channels

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

func TestExpiryWorker_ExpiresOverdueTasks(t *testing.T) {
	db := newTestDB(t)
	seedAgent(t, db, "poster-agent")

	channelStore := NewSQLiteChannelStore(db)
	taskStore := NewSQLiteTaskStore(db)
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	svc := NewSwarmService(taskStore, channelStore, tracer)
	ctx := context.Background()

	// Create auction channel
	ch := &Channel{Name: "expiry-test", Type: TypeAuction, CreatedBy: "poster-agent"}
	channelStore.CreateChannel(ctx, ch)
	channelStore.AddMember(ctx, &Membership{ChannelID: ch.ID, AgentName: "poster-agent", Role: RoleOwner})

	// Create a task with deadline in the past (bypass PostTask validation by using store directly)
	pastDeadline := time.Now().Add(-1 * time.Hour)
	taskStore.CreateTask(ctx, &Task{
		ChannelID:    ch.ID,
		PostedBy:     "poster-agent",
		Title:        "Expired Task",
		Status:       TaskStatusOpen,
		Deadline:     &pastDeadline,
		Requirements: json.RawMessage(`{}`),
	})

	// Start worker with very short interval
	worker := NewExpiryWorker(svc, 50*time.Millisecond)
	worker.Start()

	// Wait for at least one tick
	time.Sleep(200 * time.Millisecond)

	// Stop worker
	worker.Stop()

	// Verify the task was cancelled
	tasks, err := taskStore.ListTasks(ctx, ch.ID, TaskStatusCancelled)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("cancelled tasks = %d, want 1", len(tasks))
	}
}

func TestExpiryWorker_DoesNotExpireFutureTasks(t *testing.T) {
	db := newTestDB(t)
	seedAgent(t, db, "poster-agent")

	channelStore := NewSQLiteChannelStore(db)
	taskStore := NewSQLiteTaskStore(db)
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	svc := NewSwarmService(taskStore, channelStore, tracer)
	ctx := context.Background()

	ch := &Channel{Name: "no-expire-test", Type: TypeAuction, CreatedBy: "poster-agent"}
	channelStore.CreateChannel(ctx, ch)
	channelStore.AddMember(ctx, &Membership{ChannelID: ch.ID, AgentName: "poster-agent", Role: RoleOwner})

	futureDeadline := time.Now().Add(1 * time.Hour)
	taskStore.CreateTask(ctx, &Task{
		ChannelID:    ch.ID,
		PostedBy:     "poster-agent",
		Title:        "Future Task",
		Status:       TaskStatusOpen,
		Deadline:     &futureDeadline,
		Requirements: json.RawMessage(`{}`),
	})

	worker := NewExpiryWorker(svc, 50*time.Millisecond)
	worker.Start()
	time.Sleep(200 * time.Millisecond)
	worker.Stop()

	// Task should still be open
	tasks, _ := taskStore.ListTasks(ctx, ch.ID, TaskStatusOpen)
	if len(tasks) != 1 {
		t.Errorf("open tasks = %d, want 1", len(tasks))
	}
}

func TestExpiryWorker_StartStop(t *testing.T) {
	db := newTestDB(t)
	channelStore := NewSQLiteChannelStore(db)
	taskStore := NewSQLiteTaskStore(db)
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	svc := NewSwarmService(taskStore, channelStore, tracer)

	worker := NewExpiryWorker(svc, 100*time.Millisecond)
	worker.Start()
	// Stop should not block/panic
	worker.Stop()
}

func TestExpiryWorker_DefaultInterval(t *testing.T) {
	db := newTestDB(t)
	channelStore := NewSQLiteChannelStore(db)
	taskStore := NewSQLiteTaskStore(db)
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	svc := NewSwarmService(taskStore, channelStore, tracer)

	worker := NewExpiryWorker(svc, 0)
	if worker.interval != 1*time.Minute {
		t.Errorf("default interval = %v, want 1m", worker.interval)
	}
}
