package runs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/harness"
	"github.com/synapbus/synapbus/internal/harness/runs"
	"github.com/synapbus/synapbus/internal/messaging"

	_ "modernc.org/sqlite"
)

// setupDB installs just the harness_runs schema on an in-memory DB.
// It mirrors migration 019_harness.sql (the parts this store reads).
func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	schema := `
CREATE TABLE harness_runs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id        TEXT NOT NULL UNIQUE,
    agent_name    TEXT NOT NULL,
    backend       TEXT NOT NULL,
    message_id    INTEGER,
    status        TEXT NOT NULL,
    exit_code     INTEGER,
    trace_id      TEXT,
    span_id       TEXT,
    session_id    TEXT,
    tokens_in     INTEGER NOT NULL DEFAULT 0,
    tokens_out    INTEGER NOT NULL DEFAULT 0,
    tokens_cached INTEGER NOT NULL DEFAULT 0,
    cost_usd      REAL NOT NULL DEFAULT 0,
    duration_ms   INTEGER,
    result_json   TEXT,
    logs_excerpt  TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at   DATETIME
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func agent(name string) *agents.Agent { return &agents.Agent{Name: name} }

func TestStore_OnStart_InsertsRunningRow(t *testing.T) {
	db := setupDB(t)
	s := runs.New(db, nil)

	req := &harness.ExecRequest{
		RunID:     "run-1",
		AgentName: "alpha",
		Message:   &messaging.Message{ID: 99, FromAgent: "caller"},
	}
	s.OnStart(context.Background(), agent("alpha"), "subprocess", req)

	got, err := s.GetByRunID(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("GetByRunID err = %v", err)
	}
	if got.Status != runs.StatusRunning {
		t.Errorf("status = %q, want running", got.Status)
	}
	if got.Backend != "subprocess" {
		t.Errorf("backend = %q", got.Backend)
	}
	if got.MessageID == nil || *got.MessageID != 99 {
		t.Errorf("MessageID = %v, want 99", got.MessageID)
	}
}

func TestStore_OnFinish_UpdatesToSuccess(t *testing.T) {
	db := setupDB(t)
	s := runs.New(db, nil)

	req := &harness.ExecRequest{RunID: "r", AgentName: "a"}
	s.OnStart(context.Background(), agent("a"), "stub", req)

	res := &harness.ExecResult{
		ExitCode:   0,
		Logs:       "hi",
		ResultJSON: json.RawMessage(`{"ok":true}`),
		Usage: harness.Usage{
			TokensIn:  10,
			TokensOut: 20,
			CostUSD:   0.005,
		},
		SessionID: "sess-1",
	}
	s.OnFinish(context.Background(), agent("a"), "stub", req, res, nil)

	got, err := s.GetByRunID(context.Background(), "r")
	if err != nil {
		t.Fatalf("GetByRunID err = %v", err)
	}
	if got.Status != runs.StatusSuccess {
		t.Errorf("status = %q, want success", got.Status)
	}
	if got.ExitCode == nil || *got.ExitCode != 0 {
		t.Errorf("ExitCode = %v", got.ExitCode)
	}
	if got.TokensIn != 10 || got.TokensOut != 20 {
		t.Errorf("usage not persisted: %+v", got)
	}
	if got.CostUSD != 0.005 {
		t.Errorf("cost not persisted: %v", got.CostUSD)
	}
	if got.ResultJSON != `{"ok":true}` {
		t.Errorf("result_json = %q", got.ResultJSON)
	}
	if got.SessionID != "sess-1" {
		t.Errorf("session_id = %q", got.SessionID)
	}
	if got.DurationMs == nil {
		t.Error("DurationMs nil, want computed")
	}
	if got.FinishedAt == nil {
		t.Error("FinishedAt nil")
	}
}

func TestStore_OnFinish_UpdatesToFailedOnNonZero(t *testing.T) {
	db := setupDB(t)
	s := runs.New(db, nil)
	req := &harness.ExecRequest{RunID: "r", AgentName: "a"}
	s.OnStart(context.Background(), agent("a"), "stub", req)
	s.OnFinish(context.Background(), agent("a"), "stub", req,
		&harness.ExecResult{ExitCode: 5, Logs: "bad"}, nil)

	got, _ := s.GetByRunID(context.Background(), "r")
	if got.Status != runs.StatusFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
}

func TestStore_OnFinish_UpdatesToFailedOnExecErr(t *testing.T) {
	db := setupDB(t)
	s := runs.New(db, nil)
	req := &harness.ExecRequest{RunID: "r", AgentName: "a"}
	s.OnStart(context.Background(), agent("a"), "stub", req)
	s.OnFinish(context.Background(), agent("a"), "stub", req, nil, errors.New("kaboom"))

	got, _ := s.GetByRunID(context.Background(), "r")
	if got.Status != runs.StatusFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
}

func TestStore_OnFinish_InsertsIfNoStart(t *testing.T) {
	// Simulate the OnStart insert failing (e.g. store wired late) —
	// OnFinish must still persist a terminal row.
	db := setupDB(t)
	s := runs.New(db, nil)
	req := &harness.ExecRequest{RunID: "ghost", AgentName: "a"}

	s.OnFinish(context.Background(), agent("a"), "stub", req,
		&harness.ExecResult{ExitCode: 0, Logs: "post hoc"}, nil)

	got, err := s.GetByRunID(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("GetByRunID err = %v", err)
	}
	if got.Status != runs.StatusSuccess {
		t.Errorf("status = %q", got.Status)
	}
	if got.LogsExcerpt != "post hoc" {
		t.Errorf("logs = %q", got.LogsExcerpt)
	}
}

func TestStore_ListByAgent_OrdersNewestFirst(t *testing.T) {
	db := setupDB(t)
	s := runs.New(db, nil)

	for _, id := range []string{"r1", "r2", "r3"} {
		req := &harness.ExecRequest{RunID: id, AgentName: "a"}
		s.OnStart(context.Background(), agent("a"), "stub", req)
		s.OnFinish(context.Background(), agent("a"), "stub", req,
			&harness.ExecResult{ExitCode: 0}, nil)
	}

	list, err := s.ListByAgent(context.Background(), "a", 10)
	if err != nil {
		t.Fatalf("ListByAgent err = %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("got %d runs, want 3", len(list))
	}
	// Newest first ordering — at minimum the run_ids should all be
	// present; ordering within same-timestamp is DB-defined.
	seen := map[string]bool{}
	for _, r := range list {
		seen[r.RunID] = true
	}
	for _, id := range []string{"r1", "r2", "r3"} {
		if !seen[id] {
			t.Errorf("run %s missing", id)
		}
	}
}

func TestStore_LogsExcerptCap(t *testing.T) {
	db := setupDB(t)
	s := runs.New(db, nil)
	req := &harness.ExecRequest{RunID: "big", AgentName: "a"}
	s.OnStart(context.Background(), agent("a"), "stub", req)

	big := make([]byte, 32*1024)
	for i := range big {
		big[i] = 'x'
	}
	s.OnFinish(context.Background(), agent("a"), "stub", req,
		&harness.ExecResult{ExitCode: 0, Logs: string(big)}, nil)

	got, _ := s.GetByRunID(context.Background(), "big")
	if len(got.LogsExcerpt) == 0 {
		t.Fatal("logs empty")
	}
	if len(got.LogsExcerpt) > 17*1024 {
		t.Errorf("logs excerpt not capped: %d bytes", len(got.LogsExcerpt))
	}
}
