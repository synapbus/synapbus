package goaltasks

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/storage"
)

// testDB spins up an in-memory SQLite with all migrations applied and a
// minimal user/channel/agent/goal/goal_tasks set suitable for service tests.
func testDB(t *testing.T) (*sql.DB, int64, int64) {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_foreign_keys=on&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO users (username, password_hash) VALUES ('algis', 'x')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var userID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username='algis'`).Scan(&userID); err != nil {
		t.Fatalf("get user: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO channels (name, description, type, is_private, is_system, created_by)
		VALUES ('goal-test', 'Test goal channel', 'blackboard', 1, 0, 'algis')`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	var channelID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM channels WHERE name='goal-test'`).Scan(&channelID); err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO goals (slug, title, description, owner_user_id, channel_id, status, max_spawn_depth)
		VALUES ('test', 'Test', 'Desc', ?, ?, 'active', 3)`, userID, channelID); err != nil {
		t.Fatalf("insert goal: %v", err)
	}
	var goalID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM goals WHERE slug='test'`).Scan(&goalID); err != nil {
		t.Fatalf("get goal: %v", err)
	}
	return db, userID, goalID
}

func insertTestAgent(t *testing.T, db *sql.DB, name string, ownerID int64) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `
		INSERT INTO agents (name, type, capabilities, owner_id, api_key_hash, status)
		VALUES (?, 'ai', '[]', ?, 'hash', 'active')`, name, ownerID)
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestCreateTree_AncestryAndDepth(t *testing.T) {
	db, userID, goalID := testDB(t)
	svc := NewService(NewStore(db), slog.Default())

	root := TreeNode{
		Title:       "root",
		Description: "root desc",
		Children: []TreeNode{
			{
				Title:       "child-1",
				Description: "c1 desc",
				Children: []TreeNode{
					{Title: "grandchild", Description: "gc desc"},
				},
			},
			{Title: "child-2", Description: "c2 desc"},
		},
	}

	rootID, allIDs, err := svc.CreateTree(context.Background(), CreateTreeInput{
		GoalID:        goalID,
		CreatedByUser: &userID,
		Root:          root,
	})
	if err != nil {
		t.Fatalf("CreateTree: %v", err)
	}
	if len(allIDs) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(allIDs))
	}

	tasks, err := svc.ListByGoal(context.Background(), goalID)
	if err != nil {
		t.Fatalf("ListByGoal: %v", err)
	}
	byID := map[int64]*Task{}
	for _, task := range tasks {
		byID[task.ID] = task
	}

	if r := byID[rootID]; r == nil || r.Depth != 0 || len(r.Ancestry) != 0 {
		t.Errorf("root depth/ancestry wrong: %+v", r)
	}
	// grandchild should have two ancestors
	var gc *Task
	for _, task := range tasks {
		if task.Title == "grandchild" {
			gc = task
		}
	}
	if gc == nil || gc.Depth != 2 || len(gc.Ancestry) != 2 {
		t.Fatalf("grandchild depth/ancestry wrong: %+v", gc)
	}
	if gc.Ancestry[0].Title != "root" || gc.Ancestry[1].Title != "child-1" {
		t.Errorf("ancestry chain wrong: %+v", gc.Ancestry)
	}
}

func TestCreateTree_AncestryOverflow(t *testing.T) {
	db, userID, goalID := testDB(t)
	svc := NewService(NewStore(db), slog.Default())
	// Huge title on an intermediate node — the grandchild's ancestry snapshot
	// will contain this title and must exceed the 16 KB cap.
	huge := strings.Repeat("x", 20000)
	root := TreeNode{
		Title:       "root",
		Description: "d",
		Children: []TreeNode{
			{
				Title:       huge,
				Description: "d",
				Children: []TreeNode{
					{Title: "victim", Description: "d"},
				},
			},
		},
	}
	_, _, err := svc.CreateTree(context.Background(), CreateTreeInput{
		GoalID:        goalID,
		CreatedByUser: &userID,
		Root:          root,
	})
	if err == nil {
		t.Fatal("expected ancestry overflow error, got nil")
	}
}

func TestClaimAtomic_Race(t *testing.T) {
	db, userID, goalID := testDB(t)
	svc := NewService(NewStore(db), slog.Default())

	// Create one task in approved state.
	_, allIDs, err := svc.CreateTree(context.Background(), CreateTreeInput{
		GoalID:        goalID,
		CreatedByUser: &userID,
		Root:          TreeNode{Title: "solo", Description: "d"},
		InitialStatus: StatusApproved,
	})
	if err != nil {
		t.Fatalf("CreateTree: %v", err)
	}
	taskID := allIDs[0]

	// Two racing agents.
	agent1 := insertTestAgent(t, db, "racer1", userID)
	agent2 := insertTestAgent(t, db, "racer2", userID)

	const rounds = 50
	var oneWinsCount, alreadyClaimedCount int32
	for i := 0; i < rounds; i++ {
		// Reset the task to approved + unassigned each round.
		if _, err := db.ExecContext(context.Background(),
			`UPDATE goal_tasks SET status='approved', assignee_agent_id=NULL, claimed_at=NULL WHERE id=?`, taskID); err != nil {
			t.Fatalf("reset: %v", err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		for _, a := range []int64{agent1, agent2} {
			agentID := a
			go func() {
				defer wg.Done()
				err := svc.Claim(context.Background(), taskID, agentID, nil)
				switch err {
				case nil:
					atomic.AddInt32(&oneWinsCount, 1)
				case ErrAlreadyClaimed:
					atomic.AddInt32(&alreadyClaimedCount, 1)
				default:
					t.Errorf("unexpected claim error: %v", err)
				}
			}()
		}
		wg.Wait()
	}
	if oneWinsCount != rounds {
		t.Errorf("expected %d wins, got %d", rounds, oneWinsCount)
	}
	if alreadyClaimedCount != rounds {
		t.Errorf("expected %d ErrAlreadyClaimed, got %d", rounds, alreadyClaimedCount)
	}
}

func TestRollupCosts(t *testing.T) {
	db, userID, goalID := testDB(t)
	svc := NewService(NewStore(db), slog.Default())

	// Build: root → a, b; a → a1
	_, allIDs, err := svc.CreateTree(context.Background(), CreateTreeInput{
		GoalID:        goalID,
		CreatedByUser: &userID,
		Root: TreeNode{
			Title: "root", Description: "d",
			Children: []TreeNode{
				{Title: "a", Description: "d", Children: []TreeNode{
					{Title: "a1", Description: "d"},
				}},
				{Title: "b", Description: "d"},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateTree: %v", err)
	}
	if len(allIDs) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(allIDs))
	}
	rootID := allIDs[0]

	// Spend on a1 and b (the leaves).
	a1ID := allIDs[2]
	bID := allIDs[3]
	if err := svc.AddSpend(context.Background(), a1ID, 100, 50); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddSpend(context.Background(), bID, 200, 75); err != nil {
		t.Fatal(err)
	}

	tokens, dollars, count, err := svc.RollupCosts(context.Background(), rootID)
	if err != nil {
		t.Fatalf("RollupCosts: %v", err)
	}
	if tokens != 300 || dollars != 125 || count != 4 {
		t.Errorf("rollup wrong: tokens=%d dollars=%d count=%d", tokens, dollars, count)
	}
}

func TestTransition_StateMachine(t *testing.T) {
	db, userID, goalID := testDB(t)
	svc := NewService(NewStore(db), slog.Default())

	_, allIDs, err := svc.CreateTree(context.Background(), CreateTreeInput{
		GoalID:        goalID,
		CreatedByUser: &userID,
		Root:          TreeNode{Title: "solo", Description: "d"},
		InitialStatus: StatusApproved,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := allIDs[0]

	// Legal: approved → claimed → in_progress → awaiting_verification → done
	steps := []string{StatusClaimed, StatusInProgress, StatusAwaitingVerification, StatusDone}
	for _, step := range steps {
		if err := svc.Transition(context.Background(), taskID, step, Extras{}); err != nil {
			t.Fatalf("transition to %s: %v", step, err)
		}
	}

	// Illegal: done → approved
	if err := svc.Transition(context.Background(), taskID, StatusApproved, Extras{}); err == nil {
		t.Error("expected illegal transition from done → approved")
	}
}
