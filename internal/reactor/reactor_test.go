package reactor

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fmt"
	"log/slog"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/dispatcher"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/harness/stub"
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
	// modernc.org/sqlite gives every pool connection its own fresh
	// :memory: database, which breaks tests that spawn goroutines
	// (harness runs) alongside the main test goroutine. Pin to one
	// connection so every query sees the same schema and rows.
	db.SetMaxOpenConns(1)

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

// insertSubprocessAgent creates a reactive agent backed by a local
// command instead of a K8s image.
func insertSubprocessAgent(t *testing.T, db *sql.DB, name, localCommand string, cooldown, budget, maxDepth int) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO agents (name, display_name, type, owner_id, trigger_mode, cooldown_seconds, daily_trigger_budget, max_trigger_depth, k8s_resource_preset, local_command)
		 VALUES (?, ?, 'ai', 1, 'reactive', ?, ?, ?, 'default', ?)`,
		name, name, cooldown, budget, maxDepth, localCommand,
	)
	if err != nil {
		t.Fatalf("insert subprocess agent: %v", err)
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
	if !strings.Contains(runs[0].ErrorLog, "no backend configured") {
		t.Errorf("expected 'no backend configured' error, got: %s", runs[0].ErrorLog)
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

// --- subprocess/harness path tests ---------------------------------------

// newHarnessReactor wires a reactor with a registry that dispatches to
// the provided stub harness under the "subprocess" name. The NoopRunner
// ensures the K8s path is skipped for agents without a k8s_image.
func newHarnessReactor(t *testing.T, db *sql.DB, stubHarness *stub.Harness) *Reactor {
	t.Helper()
	store := NewStore(db)
	agentStore := agents.NewSQLiteAgentStore(db)
	reactor := New(store, agentStore, k8spkg.NewNoopRunner(), slog.Default())
	reg := harness.NewRegistry()
	reg.Register(stubHarness)
	reactor.SetHarnessRegistry(reg)
	return reactor
}

// waitForRun polls the store until a run in the given status exists or
// the deadline expires. Subprocess dispatches run in a goroutine so
// tests need to wait a bit for the terminal status.
func waitForRun(t *testing.T, store *Store, agentName, status string) *ReactiveRun {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runs, _, err := store.ListRuns(context.Background(), agentName, status, 5, 0)
		if err == nil && len(runs) > 0 {
			return runs[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for run with status=%s for agent=%s", status, agentName)
	return nil
}

func TestReactorSubprocess_SuccessFromMention(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	insertSubprocessAgent(t, db, "local-agent", `["sh","-c","true"]`, 0, 8, 5)

	s := stub.New()
	s.NameStr = "subprocess"
	s.Result = &harness.ExecResult{ExitCode: 0, Logs: "ok"}
	reactor := newHarnessReactor(t, db, s)

	event := dispatcher.MessageEvent{
		EventType:       "message.mentioned",
		MessageID:       42,
		FromAgent:       "algis",
		Body:            "@local-agent please help",
		MentionedAgents: []string{"local-agent"},
	}
	if err := reactor.Dispatch(context.Background(), event); err != nil {
		t.Fatalf("Dispatch err = %v", err)
	}

	run := waitForRun(t, reactor.store, "local-agent", StatusSucceeded)
	if run.TriggerEvent != "message.mentioned" {
		t.Errorf("trigger_event = %q", run.TriggerEvent)
	}
	if run.TriggerFrom != "algis" {
		t.Errorf("trigger_from = %q", run.TriggerFrom)
	}
	if len(s.Calls) != 1 {
		t.Fatalf("stub got %d calls, want 1", len(s.Calls))
	}
	call := s.Calls[0]
	if call.Agent == nil || call.Agent.Name != "local-agent" {
		t.Errorf("stub call agent = %+v", call.Agent)
	}
	if call.Env["SYNAPBUS_TRIGGER_DEPTH"] != "0" {
		t.Errorf("trigger depth env = %q", call.Env["SYNAPBUS_TRIGGER_DEPTH"])
	}
	if call.Env["SYNAPBUS_FROM_AGENT"] != "algis" {
		t.Errorf("from_agent env = %q", call.Env["SYNAPBUS_FROM_AGENT"])
	}
	if call.Message == nil || call.Message.ID != 42 {
		t.Errorf("message = %+v", call.Message)
	}
}

func TestReactorSubprocess_FailureRecordedAndDMSent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	insertSubprocessAgent(t, db, "crash-agent", `["sh","-c","false"]`, 0, 8, 5)
	// Insert a human owner so notifyFailure can resolve the recipient.
	_, _ = db.Exec(`INSERT INTO agents (name, display_name, type, owner_id, trigger_mode, k8s_resource_preset)
		VALUES ('algis', 'algis', 'human', 1, 'passive', 'default')`)

	s := stub.New()
	s.NameStr = "subprocess"
	s.Result = &harness.ExecResult{ExitCode: 7, Logs: "bad"}
	reactor := newHarnessReactor(t, db, s)

	notifier := &fakeNotifier{}
	reactor.SetFailureNotifier(notifier)

	event := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "algis",
		ToAgent:   "crash-agent",
		Body:      "work",
	}
	_ = reactor.Dispatch(context.Background(), event)

	run := waitForRun(t, reactor.store, "crash-agent", StatusFailed)
	if run.ErrorLog == "" {
		t.Error("expected error log on failed run")
	}
	if notifier.calls != 1 {
		t.Errorf("notifier calls = %d, want 1", notifier.calls)
	}
}

func TestReactorSubprocess_DepthExceededSkipped(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	insertSubprocessAgent(t, db, "deep-local", `["sh","-c","true"]`, 0, 8, 3)

	s := stub.New()
	s.NameStr = "subprocess"
	reactor := newHarnessReactor(t, db, s)

	event := dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "algis",
		ToAgent:   "deep-local",
		Depth:     3, // at limit
	}
	_ = reactor.Dispatch(context.Background(), event)

	runs, _, _ := reactor.store.ListRuns(context.Background(), "deep-local", StatusDepthExceeded, 5, 0)
	if len(runs) != 1 {
		t.Fatalf("expected depth_exceeded run, got %d", len(runs))
	}
	if len(s.Calls) != 0 {
		t.Errorf("harness should not be called when depth exceeded (calls=%d)", len(s.Calls))
	}
}

func TestReactorSubprocess_BudgetExhaustedSkipped(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	insertSubprocessAgent(t, db, "budget-local", `["sh","-c","true"]`, 0, 1, 5)

	// Seed one completed run today to exhaust the daily budget (=1).
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO reactive_runs (agent_name, trigger_event, trigger_depth, trigger_from, status, created_at)
		VALUES ('budget-local', 'message.received', 0, 'algis', 'succeeded', ?)`, now)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}

	s := stub.New()
	s.NameStr = "subprocess"
	reactor := newHarnessReactor(t, db, s)

	_ = reactor.Dispatch(context.Background(), dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "algis",
		ToAgent:   "budget-local",
	})

	runs, _, _ := reactor.store.ListRuns(context.Background(), "budget-local", StatusBudgetExhausted, 5, 0)
	if len(runs) != 1 {
		t.Fatalf("expected budget_exhausted run, got %d", len(runs))
	}
	if len(s.Calls) != 0 {
		t.Errorf("harness called despite budget exhaustion")
	}
}

func TestReactorSubprocess_CooldownSkipped(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	insertSubprocessAgent(t, db, "cool-local", `["sh","-c","true"]`, 3600, 8, 5)

	// Seed a recent completed run so cooldown applies.
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.Exec(`INSERT INTO reactive_runs (agent_name, trigger_event, trigger_depth, trigger_from, status, created_at)
		VALUES ('cool-local', 'message.received', 0, 'algis', 'succeeded', ?)`, now)

	s := stub.New()
	s.NameStr = "subprocess"
	reactor := newHarnessReactor(t, db, s)

	_ = reactor.Dispatch(context.Background(), dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "algis",
		ToAgent:   "cool-local",
	})

	runs, _, _ := reactor.store.ListRuns(context.Background(), "cool-local", StatusCooldownSkipped, 5, 0)
	if len(runs) != 1 {
		t.Fatalf("expected cooldown_skipped run, got %d", len(runs))
	}
	if len(s.Calls) != 0 {
		t.Errorf("harness called despite cooldown")
	}
}

func TestReactorSubprocess_AlreadyRunningQueued(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	insertSubprocessAgent(t, db, "busy-local", `["sh","-c","true"]`, 0, 8, 5)

	// Seed a running run so the already-running check fires.
	_, _ = db.Exec(`INSERT INTO reactive_runs (agent_name, trigger_event, trigger_depth, trigger_from, status, started_at, created_at)
		VALUES ('busy-local', 'message.received', 0, 'algis', 'running', datetime('now'), datetime('now'))`)

	s := stub.New()
	s.NameStr = "subprocess"
	reactor := newHarnessReactor(t, db, s)

	_ = reactor.Dispatch(context.Background(), dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "algis",
		ToAgent:   "busy-local",
	})

	runs, _, _ := reactor.store.ListRuns(context.Background(), "busy-local", StatusQueued, 5, 0)
	if len(runs) != 1 {
		t.Fatalf("expected queued run, got %d", len(runs))
	}
	if len(s.Calls) != 0 {
		t.Errorf("harness called despite already-running")
	}
}

func TestReactorSubprocess_NoBackendFailsCleanly(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	// Reactive agent with no k8s_image, no local_command, no harness config.
	_, _ = db.Exec(`INSERT INTO agents (name, display_name, type, owner_id, trigger_mode,
		cooldown_seconds, daily_trigger_budget, max_trigger_depth, k8s_resource_preset)
		VALUES ('orphan', 'orphan', 'ai', 1, 'reactive', 0, 8, 5, 'default')`)

	reactor := newHarnessReactor(t, db, stub.New())
	_ = reactor.Dispatch(context.Background(), dispatcher.MessageEvent{
		EventType: "message.received",
		MessageID: 1,
		FromAgent: "algis",
		ToAgent:   "orphan",
	})

	runs, _, _ := reactor.store.ListRuns(context.Background(), "orphan", StatusFailed, 5, 0)
	if len(runs) != 1 {
		t.Fatalf("expected failed run for orphan agent, got %d", len(runs))
	}
	if !strings.Contains(runs[0].ErrorLog, "no backend configured") {
		t.Errorf("error_log = %q", runs[0].ErrorLog)
	}
}

// fakeNotifier captures NotifyFailure calls.
type fakeNotifier struct {
	calls int
	last  string
}

func (f *fakeNotifier) NotifyFailure(_ context.Context, _, _, _, _ string, _ int64, errorSummary string) error {
	f.calls++
	f.last = errorSummary
	return nil
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
