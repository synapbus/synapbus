package reactor

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"fmt"
	"log/slog"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/dispatcher"
	k8spkg "github.com/synapbus/synapbus/internal/k8s"

	_ "modernc.org/sqlite"
)

// setupTestDB creates an in-memory SQLite database with schema for testing.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	// Create minimal schema
	schema := `
		CREATE TABLE agents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT 'ai',
			capabilities TEXT NOT NULL DEFAULT '{}',
			owner_id INTEGER NOT NULL DEFAULT 1,
			api_key_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			trigger_mode TEXT NOT NULL DEFAULT 'passive',
			cooldown_seconds INTEGER NOT NULL DEFAULT 600,
			daily_trigger_budget INTEGER NOT NULL DEFAULT 8,
			max_trigger_depth INTEGER NOT NULL DEFAULT 5,
			k8s_image TEXT,
			k8s_env_json TEXT,
			k8s_resource_preset TEXT NOT NULL DEFAULT 'default',
			pending_work INTEGER NOT NULL DEFAULT 0,
			harness_name TEXT,
			local_command TEXT,
			harness_config_json TEXT
		);
		CREATE TABLE reactive_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_name TEXT NOT NULL,
			trigger_message_id INTEGER,
			trigger_event TEXT NOT NULL,
			trigger_depth INTEGER NOT NULL DEFAULT 0,
			trigger_from TEXT,
			status TEXT NOT NULL DEFAULT 'queued',
			k8s_job_name TEXT,
			k8s_namespace TEXT,
			started_at DATETIME,
			completed_at DATETIME,
			duration_ms INTEGER,
			error_log TEXT,
			token_cost_json TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	return db
}

func insertTestAgent(t *testing.T, db *sql.DB, name, triggerMode, image string, cooldown, budget, maxDepth int) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO agents (name, display_name, type, owner_id, trigger_mode, cooldown_seconds, daily_trigger_budget, max_trigger_depth, k8s_image, k8s_resource_preset)
		 VALUES (?, ?, 'ai', 1, ?, ?, ?, ?, ?, 'default')`,
		name, name, triggerMode, cooldown, budget, maxDepth, image,
	)
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}
}

func TestReactorPassiveAgentSkipped(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestAgent(t, db, "passive-agent", "passive", "image:latest", 600, 8, 5)

	store := NewStore(db)
	agentStore := agents.NewSQLiteAgentStore(db)
	runner := k8spkg.NewNoopRunner()
	logger := slog.Default()

	reactor := New(store, agentStore, runner, logger)

	event := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "algis",
		ToAgent:   "passive-agent",
		Body:      "hello",
	}

	err := reactor.Dispatch(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No runs should be created for passive agents
	runs, total, err := store.ListRuns(context.Background(), "passive-agent", "", 10, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if total != 0 || len(runs) != 0 {
		t.Errorf("expected 0 runs for passive agent, got %d", total)
	}
}

func TestReactorNoK8sImage(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestAgent(t, db, "no-image-agent", "reactive", "", 600, 8, 5)

	store := NewStore(db)
	agentStore := agents.NewSQLiteAgentStore(db)
	runner := k8spkg.NewNoopRunner()
	logger := slog.Default()

	reactor := New(store, agentStore, runner, logger)

	event := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "algis",
		ToAgent:   "no-image-agent",
		Body:      "hello",
	}

	_ = reactor.Dispatch(context.Background(), event)

	runs, _, _ := store.ListRuns(context.Background(), "no-image-agent", StatusFailed, 10, 0)
	if len(runs) != 1 {
		t.Fatalf("expected 1 failed run for agent with no image, got %d", len(runs))
	}
	if runs[0].ErrorLog != "no k8s_image configured" {
		t.Errorf("expected 'no k8s_image configured' error, got: %s", runs[0].ErrorLog)
	}
}

func TestReactorDepthExceeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestAgent(t, db, "deep-agent", "reactive", "image:latest", 600, 8, 3)

	store := NewStore(db)
	agentStore := agents.NewSQLiteAgentStore(db)
	runner := &fakeRunner{available: true}
	logger := slog.Default()

	reactor := New(store, agentStore, runner, logger)

	event := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "other-agent",
		ToAgent:   "deep-agent",
		Body:      "hello from depth 3",
		Depth:     3, // equals max depth
	}

	_ = reactor.Dispatch(context.Background(), event)

	runs, _, _ := store.ListRuns(context.Background(), "deep-agent", StatusDepthExceeded, 10, 0)
	if len(runs) != 1 {
		t.Fatalf("expected 1 depth_exceeded run, got %d", len(runs))
	}
}

func TestReactorBudgetExhausted(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestAgent(t, db, "budget-agent", "reactive", "image:latest", 0, 2, 5)

	store := NewStore(db)
	agentStore := agents.NewSQLiteAgentStore(db)
	runner := &fakeRunner{available: true}
	logger := slog.Default()

	reactor := New(store, agentStore, runner, logger)

	// Record 2 existing runs today
	for i := 0; i < 2; i++ {
		_, _ = store.InsertRun(context.Background(), &ReactiveRun{
			AgentName:    "budget-agent",
			TriggerEvent: "message.received",
			Status:       StatusSucceeded,
		})
	}

	event := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 10,
		FromAgent: "algis",
		ToAgent:   "budget-agent",
		Body:      "one more",
	}

	_ = reactor.Dispatch(context.Background(), event)

	runs, _, _ := store.ListRuns(context.Background(), "budget-agent", StatusBudgetExhausted, 10, 0)
	if len(runs) != 1 {
		t.Fatalf("expected 1 budget_exhausted run, got %d", len(runs))
	}
}

func TestReactorCooldownSkipped(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestAgent(t, db, "cool-agent", "reactive", "image:latest", 600, 8, 5)

	store := NewStore(db)
	agentStore := agents.NewSQLiteAgentStore(db)
	runner := &fakeRunner{available: true}
	logger := slog.Default()

	reactor := New(store, agentStore, runner, logger)

	// Record a recent run
	now := time.Now().UTC()
	_, _ = store.InsertRun(context.Background(), &ReactiveRun{
		AgentName:    "cool-agent",
		TriggerEvent: "message.received",
		Status:       StatusSucceeded,
	})
	// Hack: the above uses CURRENT_TIMESTAMP which is "now", so cooldown should be active

	event := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 10,
		FromAgent: "algis",
		ToAgent:   "cool-agent",
		Body:      "too soon",
	}

	_ = reactor.Dispatch(context.Background(), event)
	_ = now // avoid unused

	runs, _, _ := store.ListRuns(context.Background(), "cool-agent", StatusCooldownSkipped, 10, 0)
	if len(runs) != 1 {
		t.Fatalf("expected 1 cooldown_skipped run, got %d", len(runs))
	}

	// Check pending_work was set
	agent, _ := agentStore.GetAgentByName(context.Background(), "cool-agent")
	if !agent.PendingWork {
		t.Error("expected pending_work to be set after cooldown skip")
	}
}

func TestReactorSequentialExecution(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestAgent(t, db, "busy-agent", "reactive", "image:latest", 0, 8, 5)

	store := NewStore(db)
	agentStore := agents.NewSQLiteAgentStore(db)
	runner := &fakeRunner{available: true}
	logger := slog.Default()

	reactor := New(store, agentStore, runner, logger)

	// First trigger — should succeed
	event1 := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "algis",
		ToAgent:   "busy-agent",
		Body:      "first",
	}
	_ = reactor.Dispatch(context.Background(), event1)

	// Second trigger — agent is running, should queue
	event2 := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 2,
		FromAgent: "algis",
		ToAgent:   "busy-agent",
		Body:      "second",
	}
	_ = reactor.Dispatch(context.Background(), event2)

	// Check: one running, one queued
	running, _, _ := store.ListRuns(context.Background(), "busy-agent", StatusRunning, 10, 0)
	queued, _, _ := store.ListRuns(context.Background(), "busy-agent", StatusQueued, 10, 0)

	if len(running) != 1 {
		t.Errorf("expected 1 running, got %d", len(running))
	}
	if len(queued) != 1 {
		t.Errorf("expected 1 queued, got %d", len(queued))
	}

	// Check pending_work is set
	agent, _ := agentStore.GetAgentByName(context.Background(), "busy-agent")
	if !agent.PendingWork {
		t.Error("expected pending_work to be set")
	}
}

func TestReactorSelfMentionIgnored(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertTestAgent(t, db, "self-agent", "reactive", "image:latest", 0, 8, 5)

	store := NewStore(db)
	agentStore := agents.NewSQLiteAgentStore(db)
	runner := &fakeRunner{available: true}
	logger := slog.Default()

	reactor := New(store, agentStore, runner, logger)

	// Agent mentions itself
	event := dispatcher.MessageEvent{
		EventType:       "message.mentioned",
		MessageID:       1,
		FromAgent:       "self-agent",
		Body:            "hey @self-agent",
		MentionedAgents: []string{"self-agent"},
	}

	_ = reactor.Dispatch(context.Background(), event)

	runs, total, _ := store.ListRuns(context.Background(), "self-agent", "", 10, 0)
	if total != 0 || len(runs) != 0 {
		t.Errorf("expected 0 runs for self-mention, got %d", total)
	}
}

func TestReactorSuccessfulTrigger(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	envJSON, _ := json.Marshal(map[string]string{
		"AGENT_GIT_REPO": "Dumbris/test-agent",
	})
	_, _ = db.Exec(
		`INSERT INTO agents (name, display_name, type, owner_id, trigger_mode, cooldown_seconds, daily_trigger_budget, max_trigger_depth, k8s_image, k8s_env_json, k8s_resource_preset)
		 VALUES (?, ?, 'ai', 1, 'reactive', 0, 8, 5, 'image:latest', ?, 'default')`,
		"test-agent", "Test Agent", string(envJSON),
	)

	store := NewStore(db)
	agentStore := agents.NewSQLiteAgentStore(db)
	runner := &fakeRunner{available: true}
	logger := slog.Default()

	reactor := New(store, agentStore, runner, logger)

	event := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 42,
		FromAgent: "algis",
		ToAgent:   "test-agent",
		Body:      "research this topic",
	}

	err := reactor.Dispatch(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify job was created
	if runner.lastJobName == "" {
		t.Fatal("expected K8s Job to be created")
	}

	// Verify run record
	runs, _, _ := store.ListRuns(context.Background(), "test-agent", StatusRunning, 10, 0)
	if len(runs) != 1 {
		t.Fatalf("expected 1 running run, got %d", len(runs))
	}
	run := runs[0]
	if run.TriggerFrom != "algis" {
		t.Errorf("expected trigger_from=algis, got %s", run.TriggerFrom)
	}
	if run.TriggerEvent != "message.received" {
		t.Errorf("expected trigger_event=message.received, got %s", run.TriggerEvent)
	}

	// Verify env vars passed to job
	if runner.lastEnv["SYNAPBUS_TRIGGER_DEPTH"] != "0" {
		t.Errorf("expected SYNAPBUS_TRIGGER_DEPTH=0, got %s", runner.lastEnv["SYNAPBUS_TRIGGER_DEPTH"])
	}
	if runner.lastEnv["AGENT_GIT_REPO"] != "Dumbris/test-agent" {
		t.Errorf("expected AGENT_GIT_REPO from k8s_env_json, got %s", runner.lastEnv["AGENT_GIT_REPO"])
	}
}

// fakeRunner is a test double for k8spkg.JobRunner.
type fakeRunner struct {
	available   bool
	lastJobName string
	lastEnv     map[string]string
	callCount   int
}

func (f *fakeRunner) IsAvailable() bool { return f.available }
func (f *fakeRunner) GetNamespace() string { return "test-ns" }
func (f *fakeRunner) GetJobLogs(_ context.Context, _, _ string) (string, error) {
	return "test logs", nil
}
func (f *fakeRunner) CreateJob(_ context.Context, handler *k8spkg.K8sHandler, msg *k8spkg.JobMessage) (string, error) {
	f.callCount++
	f.lastJobName = fmt.Sprintf("synapbus-%s-%d", handler.AgentName, msg.MessageID)
	f.lastEnv = make(map[string]string)
	for k, v := range handler.Env {
		f.lastEnv[k] = v
	}
	return f.lastJobName, nil
}
