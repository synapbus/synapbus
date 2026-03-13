package channels

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func newTestTaskStore(t *testing.T) (*SQLiteTaskStore, *SQLiteChannelStore) {
	t.Helper()
	db := newTestDB(t)
	seedAgent(t, db, "poster-agent")
	seedAgent(t, db, "bidder-agent")
	seedAgent(t, db, "bidder-agent-2")

	channelStore := NewSQLiteChannelStore(db)
	taskStore := NewSQLiteTaskStore(db)
	return taskStore, channelStore
}

func createAuctionChannel(t *testing.T, channelStore *SQLiteChannelStore) *Channel {
	t.Helper()
	ctx := context.Background()
	ch := &Channel{
		Name:      "auction-ch",
		Type:      TypeAuction,
		CreatedBy: "poster-agent",
	}
	if err := channelStore.CreateChannel(ctx, ch); err != nil {
		t.Fatalf("create auction channel: %v", err)
	}
	channelStore.AddMember(ctx, &Membership{ChannelID: ch.ID, AgentName: "poster-agent", Role: RoleOwner})
	channelStore.AddMember(ctx, &Membership{ChannelID: ch.ID, AgentName: "bidder-agent", Role: RoleMember})
	channelStore.AddMember(ctx, &Membership{ChannelID: ch.ID, AgentName: "bidder-agent-2", Role: RoleMember})
	return ch
}

func TestSQLiteTaskStore_CreateTask(t *testing.T) {
	taskStore, channelStore := newTestTaskStore(t)
	ch := createAuctionChannel(t, channelStore)
	ctx := context.Background()

	deadline := time.Now().Add(1 * time.Hour)
	task := &Task{
		ChannelID:    ch.ID,
		PostedBy:     "poster-agent",
		Title:        "Test Task",
		Description:  "A test task",
		Requirements: json.RawMessage(`{"skill":"go"}`),
		Deadline:     &deadline,
		Status:       TaskStatusOpen,
	}

	if err := taskStore.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.ID == 0 {
		t.Error("task ID should not be 0")
	}
}

func TestSQLiteTaskStore_GetTask(t *testing.T) {
	taskStore, channelStore := newTestTaskStore(t)
	ch := createAuctionChannel(t, channelStore)
	ctx := context.Background()

	task := &Task{
		ChannelID:    ch.ID,
		PostedBy:     "poster-agent",
		Title:        "Get Test",
		Description:  "Description",
		Requirements: json.RawMessage(`{}`),
		Status:       TaskStatusOpen,
	}
	taskStore.CreateTask(ctx, task)

	got, err := taskStore.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != "Get Test" {
		t.Errorf("title = %s, want Get Test", got.Title)
	}
	if got.Status != TaskStatusOpen {
		t.Errorf("status = %s, want open", got.Status)
	}
}

func TestSQLiteTaskStore_GetTask_NotFound(t *testing.T) {
	taskStore, _ := newTestTaskStore(t)
	ctx := context.Background()

	_, err := taskStore.GetTask(ctx, 99999)
	if err == nil {
		t.Error("expected error for non-existent task")
	}
}

func TestSQLiteTaskStore_ListTasks(t *testing.T) {
	taskStore, channelStore := newTestTaskStore(t)
	ch := createAuctionChannel(t, channelStore)
	ctx := context.Background()

	// Create multiple tasks
	taskStore.CreateTask(ctx, &Task{ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Task 1", Status: TaskStatusOpen, Requirements: json.RawMessage(`{}`)})
	taskStore.CreateTask(ctx, &Task{ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Task 2", Status: TaskStatusOpen, Requirements: json.RawMessage(`{}`)})

	t.Run("list all tasks", func(t *testing.T) {
		tasks, err := taskStore.ListTasks(ctx, ch.ID, "")
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 2 {
			t.Errorf("got %d tasks, want 2", len(tasks))
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		tasks, err := taskStore.ListTasks(ctx, ch.ID, TaskStatusOpen)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 2 {
			t.Errorf("got %d tasks, want 2", len(tasks))
		}
	})

	t.Run("filter by non-matching status", func(t *testing.T) {
		tasks, err := taskStore.ListTasks(ctx, ch.ID, TaskStatusCompleted)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 0 {
			t.Errorf("got %d tasks, want 0", len(tasks))
		}
	})
}

func TestSQLiteTaskStore_UpdateTaskStatus(t *testing.T) {
	taskStore, channelStore := newTestTaskStore(t)
	ch := createAuctionChannel(t, channelStore)
	ctx := context.Background()

	task := &Task{ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Update Test", Status: TaskStatusOpen, Requirements: json.RawMessage(`{}`)}
	taskStore.CreateTask(ctx, task)

	t.Run("update to assigned with assigned_to", func(t *testing.T) {
		err := taskStore.UpdateTaskStatus(ctx, task.ID, TaskStatusAssigned, "bidder-agent")
		if err != nil {
			t.Fatalf("UpdateTaskStatus: %v", err)
		}

		got, _ := taskStore.GetTask(ctx, task.ID)
		if got.Status != TaskStatusAssigned {
			t.Errorf("status = %s, want assigned", got.Status)
		}
		if got.AssignedTo != "bidder-agent" {
			t.Errorf("assigned_to = %s, want bidder-agent", got.AssignedTo)
		}
	})

	t.Run("update non-existent task", func(t *testing.T) {
		err := taskStore.UpdateTaskStatus(ctx, 99999, TaskStatusCompleted, "")
		if err == nil {
			t.Error("expected error for non-existent task")
		}
	})
}

func TestSQLiteTaskStore_CreateBid(t *testing.T) {
	taskStore, channelStore := newTestTaskStore(t)
	ch := createAuctionChannel(t, channelStore)
	ctx := context.Background()

	task := &Task{ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Bid Test", Status: TaskStatusOpen, Requirements: json.RawMessage(`{}`)}
	taskStore.CreateTask(ctx, task)

	bid := &Bid{
		TaskID:       task.ID,
		AgentName:    "bidder-agent",
		Capabilities: json.RawMessage(`{"lang":"go"}`),
		TimeEstimate: "30m",
		Message:      "I can do this",
	}

	if err := taskStore.CreateBid(ctx, bid); err != nil {
		t.Fatalf("CreateBid: %v", err)
	}
	if bid.ID == 0 {
		t.Error("bid ID should not be 0")
	}
	if bid.Status != BidStatusPending {
		t.Errorf("status = %s, want pending", bid.Status)
	}
}

func TestSQLiteTaskStore_CreateBid_DuplicateRejected(t *testing.T) {
	taskStore, channelStore := newTestTaskStore(t)
	ch := createAuctionChannel(t, channelStore)
	ctx := context.Background()

	task := &Task{ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Dup Bid Test", Status: TaskStatusOpen, Requirements: json.RawMessage(`{}`)}
	taskStore.CreateTask(ctx, task)

	bid1 := &Bid{TaskID: task.ID, AgentName: "bidder-agent", Capabilities: json.RawMessage(`{}`)}
	taskStore.CreateBid(ctx, bid1)

	bid2 := &Bid{TaskID: task.ID, AgentName: "bidder-agent", Capabilities: json.RawMessage(`{}`)}
	err := taskStore.CreateBid(ctx, bid2)
	if err == nil {
		t.Error("expected error for duplicate bid from same agent")
	}
}

func TestSQLiteTaskStore_GetBids(t *testing.T) {
	taskStore, channelStore := newTestTaskStore(t)
	ch := createAuctionChannel(t, channelStore)
	ctx := context.Background()

	task := &Task{ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Bids Test", Status: TaskStatusOpen, Requirements: json.RawMessage(`{}`)}
	taskStore.CreateTask(ctx, task)

	taskStore.CreateBid(ctx, &Bid{TaskID: task.ID, AgentName: "bidder-agent", Capabilities: json.RawMessage(`{}`), Message: "bid 1"})
	taskStore.CreateBid(ctx, &Bid{TaskID: task.ID, AgentName: "bidder-agent-2", Capabilities: json.RawMessage(`{}`), Message: "bid 2"})

	bids, err := taskStore.GetBids(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetBids: %v", err)
	}
	if len(bids) != 2 {
		t.Errorf("got %d bids, want 2", len(bids))
	}
}

func TestSQLiteTaskStore_GetBid(t *testing.T) {
	taskStore, channelStore := newTestTaskStore(t)
	ch := createAuctionChannel(t, channelStore)
	ctx := context.Background()

	task := &Task{ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Get Bid Test", Status: TaskStatusOpen, Requirements: json.RawMessage(`{}`)}
	taskStore.CreateTask(ctx, task)

	bid := &Bid{TaskID: task.ID, AgentName: "bidder-agent", Capabilities: json.RawMessage(`{"x":1}`), Message: "my bid"}
	taskStore.CreateBid(ctx, bid)

	got, err := taskStore.GetBid(ctx, bid.ID)
	if err != nil {
		t.Fatalf("GetBid: %v", err)
	}
	if got.AgentName != "bidder-agent" {
		t.Errorf("agent_name = %s, want bidder-agent", got.AgentName)
	}
	if got.Message != "my bid" {
		t.Errorf("message = %s, want my bid", got.Message)
	}
}

func TestSQLiteTaskStore_UpdateBidStatus(t *testing.T) {
	taskStore, channelStore := newTestTaskStore(t)
	ch := createAuctionChannel(t, channelStore)
	ctx := context.Background()

	task := &Task{ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Bid Status Test", Status: TaskStatusOpen, Requirements: json.RawMessage(`{}`)}
	taskStore.CreateTask(ctx, task)

	bid := &Bid{TaskID: task.ID, AgentName: "bidder-agent", Capabilities: json.RawMessage(`{}`)}
	taskStore.CreateBid(ctx, bid)

	if err := taskStore.UpdateBidStatus(ctx, bid.ID, BidStatusAccepted); err != nil {
		t.Fatalf("UpdateBidStatus: %v", err)
	}

	got, _ := taskStore.GetBid(ctx, bid.ID)
	if got.Status != BidStatusAccepted {
		t.Errorf("status = %s, want accepted", got.Status)
	}
}

func TestSQLiteTaskStore_ExpireTasks(t *testing.T) {
	taskStore, channelStore := newTestTaskStore(t)
	ch := createAuctionChannel(t, channelStore)
	ctx := context.Background()

	// Create a task with deadline in the past
	pastDeadline := time.Now().Add(-1 * time.Hour)
	taskStore.CreateTask(ctx, &Task{
		ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Expired Task",
		Status: TaskStatusOpen, Deadline: &pastDeadline, Requirements: json.RawMessage(`{}`),
	})

	// Create a task with deadline in the future
	futureDeadline := time.Now().Add(1 * time.Hour)
	taskStore.CreateTask(ctx, &Task{
		ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Future Task",
		Status: TaskStatusOpen, Deadline: &futureDeadline, Requirements: json.RawMessage(`{}`),
	})

	// Create a task without deadline
	taskStore.CreateTask(ctx, &Task{
		ChannelID: ch.ID, PostedBy: "poster-agent", Title: "No Deadline Task",
		Status: TaskStatusOpen, Requirements: json.RawMessage(`{}`),
	})

	count, err := taskStore.ExpireTasks(ctx)
	if err != nil {
		t.Fatalf("ExpireTasks: %v", err)
	}
	if count != 1 {
		t.Errorf("expired count = %d, want 1", count)
	}

	// Verify the expired task is cancelled
	tasks, _ := taskStore.ListTasks(ctx, ch.ID, TaskStatusCancelled)
	if len(tasks) != 1 {
		t.Errorf("cancelled tasks = %d, want 1", len(tasks))
	}
	if tasks[0].Title != "Expired Task" {
		t.Errorf("cancelled task title = %s, want Expired Task", tasks[0].Title)
	}

	// Verify the other tasks are still open
	openTasks, _ := taskStore.ListTasks(ctx, ch.ID, TaskStatusOpen)
	if len(openTasks) != 2 {
		t.Errorf("open tasks = %d, want 2", len(openTasks))
	}
}

func TestSQLiteTaskStore_CancelTasksByChannel(t *testing.T) {
	taskStore, channelStore := newTestTaskStore(t)
	ch := createAuctionChannel(t, channelStore)
	ctx := context.Background()

	taskStore.CreateTask(ctx, &Task{ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Task A", Status: TaskStatusOpen, Requirements: json.RawMessage(`{}`)})
	taskStore.CreateTask(ctx, &Task{ChannelID: ch.ID, PostedBy: "poster-agent", Title: "Task B", Status: TaskStatusOpen, Requirements: json.RawMessage(`{}`)})

	count, err := taskStore.CancelTasksByChannel(ctx, ch.ID)
	if err != nil {
		t.Fatalf("CancelTasksByChannel: %v", err)
	}
	if count != 2 {
		t.Errorf("cancelled count = %d, want 2", count)
	}

	tasks, _ := taskStore.ListTasks(ctx, ch.ID, TaskStatusOpen)
	if len(tasks) != 0 {
		t.Errorf("open tasks = %d, want 0", len(tasks))
	}
}
