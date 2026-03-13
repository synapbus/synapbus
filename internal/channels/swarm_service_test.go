package channels

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

func newTestSwarmService(t *testing.T) (*SwarmService, *SQLiteChannelStore) {
	t.Helper()
	db := newTestDB(t)
	seedAgent(t, db, "poster-agent")
	seedAgent(t, db, "bidder-agent")
	seedAgent(t, db, "bidder-agent-2")
	seedAgent(t, db, "outsider-agent")

	channelStore := NewSQLiteChannelStore(db)
	taskStore := NewSQLiteTaskStore(db)
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	svc := NewSwarmService(taskStore, channelStore, tracer)
	return svc, channelStore
}

func createTestAuctionChannel(t *testing.T, channelStore *SQLiteChannelStore) *Channel {
	t.Helper()
	ctx := context.Background()
	ch := &Channel{Name: "test-auction", Type: TypeAuction, CreatedBy: "poster-agent"}
	if err := channelStore.CreateChannel(ctx, ch); err != nil {
		t.Fatalf("create auction channel: %v", err)
	}
	channelStore.AddMember(ctx, &Membership{ChannelID: ch.ID, AgentName: "poster-agent", Role: RoleOwner})
	channelStore.AddMember(ctx, &Membership{ChannelID: ch.ID, AgentName: "bidder-agent", Role: RoleMember})
	channelStore.AddMember(ctx, &Membership{ChannelID: ch.ID, AgentName: "bidder-agent-2", Role: RoleMember})
	return ch
}

// --- PostTask tests ---

func TestSwarmService_PostTask(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	deadline := time.Now().Add(1 * time.Hour)
	task, err := svc.PostTask(ctx, ch.ID, "poster-agent", "Test Task", "A test task",
		json.RawMessage(`{"skill":"go"}`), &deadline)
	if err != nil {
		t.Fatalf("PostTask: %v", err)
	}
	if task.ID == 0 {
		t.Error("task ID should not be 0")
	}
	if task.Status != TaskStatusOpen {
		t.Errorf("status = %s, want open", task.Status)
	}
	if task.PostedBy != "poster-agent" {
		t.Errorf("posted_by = %s, want poster-agent", task.PostedBy)
	}
}

func TestSwarmService_PostTask_RequiresAuctionChannel(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ctx := context.Background()

	// Create a standard channel
	stdCh := &Channel{Name: "standard-ch", Type: TypeStandard, CreatedBy: "poster-agent"}
	channelStore.CreateChannel(ctx, stdCh)
	channelStore.AddMember(ctx, &Membership{ChannelID: stdCh.ID, AgentName: "poster-agent", Role: RoleOwner})

	_, err := svc.PostTask(ctx, stdCh.ID, "poster-agent", "Task", "", nil, nil)
	if err == nil {
		t.Fatal("expected error for non-auction channel")
	}
}

func TestSwarmService_PostTask_RequiresMembership(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	_, err := svc.PostTask(ctx, ch.ID, "outsider-agent", "Task", "", nil, nil)
	if err == nil {
		t.Fatal("expected error for non-member")
	}
}

func TestSwarmService_PostTask_RejectsPastDeadline(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	past := time.Now().Add(-1 * time.Hour)
	_, err := svc.PostTask(ctx, ch.ID, "poster-agent", "Past Deadline", "", nil, &past)
	if err == nil {
		t.Fatal("expected error for past deadline")
	}
}

// --- BidOnTask tests ---

func TestSwarmService_BidOnTask(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "Bid Test", "", nil, nil)

	bid, err := svc.BidOnTask(ctx, task.ID, "bidder-agent",
		json.RawMessage(`{"lang":"go"}`), "30m", "I can do this")
	if err != nil {
		t.Fatalf("BidOnTask: %v", err)
	}
	if bid.ID == 0 {
		t.Error("bid ID should not be 0")
	}
	if bid.Status != BidStatusPending {
		t.Errorf("status = %s, want pending", bid.Status)
	}
}

func TestSwarmService_BidOnTask_CannotBidOnOwnTask(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "Own Bid Test", "", nil, nil)

	_, err := svc.BidOnTask(ctx, task.ID, "poster-agent", nil, "", "")
	if err == nil {
		t.Fatal("expected error when bidding on own task")
	}
}

func TestSwarmService_BidOnTask_RequiresOpenTask(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "Status Test", "", nil, nil)

	// Bid and accept to move to assigned
	bid, _ := svc.BidOnTask(ctx, task.ID, "bidder-agent", nil, "", "")
	svc.AcceptBid(ctx, task.ID, bid.ID, "poster-agent")

	// Try to bid on assigned task
	_, err := svc.BidOnTask(ctx, task.ID, "bidder-agent-2", nil, "", "")
	if err == nil {
		t.Fatal("expected error when bidding on assigned task")
	}
}

func TestSwarmService_BidOnTask_RequiresMembership(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "Member Test", "", nil, nil)

	_, err := svc.BidOnTask(ctx, task.ID, "outsider-agent", nil, "", "")
	if err == nil {
		t.Fatal("expected error for non-member bidder")
	}
}

// --- AcceptBid tests ---

func TestSwarmService_AcceptBid(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "Accept Test", "", nil, nil)
	bid1, _ := svc.BidOnTask(ctx, task.ID, "bidder-agent", nil, "", "bid 1")
	bid2, _ := svc.BidOnTask(ctx, task.ID, "bidder-agent-2", nil, "", "bid 2")

	err := svc.AcceptBid(ctx, task.ID, bid1.ID, "poster-agent")
	if err != nil {
		t.Fatalf("AcceptBid: %v", err)
	}

	// Verify task is assigned
	updatedTask, _, _ := svc.GetTaskWithBids(ctx, task.ID)
	if updatedTask.Status != TaskStatusAssigned {
		t.Errorf("task status = %s, want assigned", updatedTask.Status)
	}
	if updatedTask.AssignedTo != "bidder-agent" {
		t.Errorf("assigned_to = %s, want bidder-agent", updatedTask.AssignedTo)
	}

	// Verify bid statuses
	_, bids, _ := svc.GetTaskWithBids(ctx, task.ID)
	for _, b := range bids {
		if b.ID == bid1.ID && b.Status != BidStatusAccepted {
			t.Errorf("winning bid status = %s, want accepted", b.Status)
		}
		if b.ID == bid2.ID && b.Status != BidStatusRejected {
			t.Errorf("losing bid status = %s, want rejected", b.Status)
		}
	}
}

func TestSwarmService_AcceptBid_OnlyPosterCanAccept(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "Auth Test", "", nil, nil)
	bid, _ := svc.BidOnTask(ctx, task.ID, "bidder-agent", nil, "", "")

	err := svc.AcceptBid(ctx, task.ID, bid.ID, "bidder-agent")
	if err == nil {
		t.Fatal("expected error when non-poster accepts bid")
	}
}

func TestSwarmService_AcceptBid_CannotAcceptOnNonOpenTask(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "Double Accept Test", "", nil, nil)
	bid, _ := svc.BidOnTask(ctx, task.ID, "bidder-agent", nil, "", "")

	// Accept once
	svc.AcceptBid(ctx, task.ID, bid.ID, "poster-agent")

	// Try to accept again
	err := svc.AcceptBid(ctx, task.ID, bid.ID, "poster-agent")
	if err == nil {
		t.Fatal("expected error when accepting bid on non-open task")
	}
}

// --- CompleteTask tests ---

func TestSwarmService_CompleteTask(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "Complete Test", "", nil, nil)
	bid, _ := svc.BidOnTask(ctx, task.ID, "bidder-agent", nil, "", "")
	svc.AcceptBid(ctx, task.ID, bid.ID, "poster-agent")

	err := svc.CompleteTask(ctx, task.ID, "bidder-agent")
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	updatedTask, _, _ := svc.GetTaskWithBids(ctx, task.ID)
	if updatedTask.Status != TaskStatusCompleted {
		t.Errorf("status = %s, want completed", updatedTask.Status)
	}
}

func TestSwarmService_CompleteTask_OnlyAssignedAgentCanComplete(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "Auth Complete Test", "", nil, nil)
	bid, _ := svc.BidOnTask(ctx, task.ID, "bidder-agent", nil, "", "")
	svc.AcceptBid(ctx, task.ID, bid.ID, "poster-agent")

	// Non-assigned agent tries to complete
	err := svc.CompleteTask(ctx, task.ID, "poster-agent")
	if err == nil {
		t.Fatal("expected error when non-assigned agent completes task")
	}

	err = svc.CompleteTask(ctx, task.ID, "bidder-agent-2")
	if err == nil {
		t.Fatal("expected error when non-assigned agent completes task")
	}
}

func TestSwarmService_CompleteTask_IdempotentCompletion(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "Idempotent Test", "", nil, nil)
	bid, _ := svc.BidOnTask(ctx, task.ID, "bidder-agent", nil, "", "")
	svc.AcceptBid(ctx, task.ID, bid.ID, "poster-agent")
	svc.CompleteTask(ctx, task.ID, "bidder-agent")

	// Complete again should be idempotent
	err := svc.CompleteTask(ctx, task.ID, "bidder-agent")
	if err != nil {
		t.Fatalf("expected idempotent completion, got: %v", err)
	}
}

func TestSwarmService_CompleteTask_CannotCompleteOpenTask(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "Open Complete Test", "", nil, nil)

	err := svc.CompleteTask(ctx, task.ID, "poster-agent")
	if err == nil {
		t.Fatal("expected error when completing open task")
	}
}

// --- Full auction lifecycle test ---

func TestSwarmService_FullAuctionLifecycle(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	// 1. Post task
	deadline := time.Now().Add(30 * time.Minute)
	task, err := svc.PostTask(ctx, ch.ID, "poster-agent",
		"Translate Document",
		"Translate document X into French",
		json.RawMessage(`{"language":"french","domain":"legal"}`),
		&deadline,
	)
	if err != nil {
		t.Fatalf("PostTask: %v", err)
	}

	// 2. Two agents bid
	bid1, err := svc.BidOnTask(ctx, task.ID, "bidder-agent",
		json.RawMessage(`{"languages":["french"]}`),
		"10m", "Fast translation")
	if err != nil {
		t.Fatalf("BidOnTask 1: %v", err)
	}

	bid2, err := svc.BidOnTask(ctx, task.ID, "bidder-agent-2",
		json.RawMessage(`{"languages":["french","german"]}`),
		"20m", "Thorough translation")
	if err != nil {
		t.Fatalf("BidOnTask 2: %v", err)
	}

	// 3. Poster accepts bid1
	if err := svc.AcceptBid(ctx, task.ID, bid1.ID, "poster-agent"); err != nil {
		t.Fatalf("AcceptBid: %v", err)
	}

	// Verify state
	task, bids, err := svc.GetTaskWithBids(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskWithBids: %v", err)
	}
	if task.Status != TaskStatusAssigned {
		t.Errorf("task status = %s, want assigned", task.Status)
	}
	if task.AssignedTo != "bidder-agent" {
		t.Errorf("assigned_to = %s, want bidder-agent", task.AssignedTo)
	}

	for _, b := range bids {
		switch b.ID {
		case bid1.ID:
			if b.Status != BidStatusAccepted {
				t.Errorf("bid1 status = %s, want accepted", b.Status)
			}
		case bid2.ID:
			if b.Status != BidStatusRejected {
				t.Errorf("bid2 status = %s, want rejected", b.Status)
			}
		}
	}

	// 4. Assigned agent completes
	if err := svc.CompleteTask(ctx, task.ID, "bidder-agent"); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	task, _, _ = svc.GetTaskWithBids(ctx, task.ID)
	if task.Status != TaskStatusCompleted {
		t.Errorf("final task status = %s, want completed", task.Status)
	}
}

// --- ListTasks and GetTaskWithBids tests ---

func TestSwarmService_ListTasks(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	svc.PostTask(ctx, ch.ID, "poster-agent", "Task 1", "", nil, nil)
	svc.PostTask(ctx, ch.ID, "poster-agent", "Task 2", "", nil, nil)

	tasks, err := svc.ListTasks(ctx, ch.ID, "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d tasks, want 2", len(tasks))
	}
}

func TestSwarmService_GetTaskWithBids(t *testing.T) {
	svc, channelStore := newTestSwarmService(t)
	ch := createTestAuctionChannel(t, channelStore)
	ctx := context.Background()

	task, _ := svc.PostTask(ctx, ch.ID, "poster-agent", "With Bids", "", nil, nil)
	svc.BidOnTask(ctx, task.ID, "bidder-agent", nil, "", "bid 1")
	svc.BidOnTask(ctx, task.ID, "bidder-agent-2", nil, "", "bid 2")

	gotTask, gotBids, err := svc.GetTaskWithBids(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskWithBids: %v", err)
	}
	if gotTask.Title != "With Bids" {
		t.Errorf("title = %s, want With Bids", gotTask.Title)
	}
	if len(gotBids) != 2 {
		t.Errorf("got %d bids, want 2", len(gotBids))
	}
}

// --- ExpireTasks test ---

func TestSwarmService_ExpireTasks(t *testing.T) {
	db := newTestDB(t)
	seedAgent(t, db, "poster-agent")
	seedAgent(t, db, "bidder-agent")
	seedAgent(t, db, "bidder-agent-2")
	seedAgent(t, db, "outsider-agent")

	channelStore := NewSQLiteChannelStore(db)
	taskStore := NewSQLiteTaskStore(db)
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	svc := NewSwarmService(taskStore, channelStore, tracer)
	ctx := context.Background()

	ch := &Channel{Name: "expire-svc-test", Type: TypeAuction, CreatedBy: "poster-agent"}
	channelStore.CreateChannel(ctx, ch)
	channelStore.AddMember(ctx, &Membership{ChannelID: ch.ID, AgentName: "poster-agent", Role: RoleOwner})

	// Create task with past deadline directly via store (bypassing PostTask validation)
	pastDeadline := time.Now().Add(-1 * time.Hour)
	taskStore.CreateTask(ctx, &Task{
		ChannelID:    ch.ID,
		PostedBy:     "poster-agent",
		Title:        "Expired",
		Status:       TaskStatusOpen,
		Deadline:     &pastDeadline,
		Requirements: json.RawMessage(`{}`),
	})

	count, err := svc.ExpireTasks(ctx)
	if err != nil {
		t.Fatalf("ExpireTasks: %v", err)
	}
	if count != 1 {
		t.Errorf("expired count = %d, want 1", count)
	}
}
