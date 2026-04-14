package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/synapbus/synapbus/internal/trust"
)

func renderReport(_ *cobra.Command, _ []string) error {
	db, err := openDB(flagDBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	goalID := flagGoalID
	if goalID == 0 {
		// Try .last_goal_id marker first, then fall back to most recent goal.
		if data, err := os.ReadFile(".last_goal_id"); err == nil {
			fmt.Sscanf(string(data), "%d", &goalID)
		}
	}
	if goalID == 0 {
		if err := db.QueryRowContext(ctx, `SELECT id FROM goals ORDER BY id DESC LIMIT 1`).Scan(&goalID); err != nil {
			return fmt.Errorf("no goals found — did you run ./run_task.sh?")
		}
	}

	snap, err := buildSnapshot(ctx, db, goalID)
	if err != nil {
		return err
	}

	tmpl := template.Must(template.New("report").Funcs(template.FuncMap{
		"dollars":    func(cents int64) string { return fmt.Sprintf("$%.2f", float64(cents)/100) },
		"cents":      func(cents int64) string { return fmt.Sprintf("¢%d", cents) },
		"shortHash":  func(s string) string { if len(s) > 12 { return s[:12] }; return s },
		"pct":        func(x float64) string { return fmt.Sprintf("%.1f", x*100) },
		"nonZero":    func(n int64) bool { return n != 0 },
		"formatTime": func(t time.Time) string { return t.Format("15:04:05") },
		"mul":        func(a, b int) int { return a * b },
	}).Parse(reportTemplate))

	f, err := os.Create(flagOutputPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := tmpl.Execute(f, snap); err != nil {
		return fmt.Errorf("render template: %w", err)
	}

	fmt.Printf("✓ Report written to %s\n", flagOutputPath)
	return nil
}

// --- snapshot types ---------------------------------------------------

type reportSnapshot struct {
	Goal            goalView
	Tree            []taskView
	Agents          []agentView
	BillingBreakdown []billingRow
	TotalTokens     int64
	TotalDollarsC   int64
	BudgetTokens    int64
	BudgetDollarsC  int64
	SpendPctDollar  float64
	Timeline        []timelineEvent
	Artifacts       []artifactView
	GeneratedAt     time.Time
}

type goalView struct {
	ID          int64
	Slug        string
	Title       string
	Description string
	Status      string
	Owner       string
	ChannelName string
	CreatedAt   time.Time
	CompletedAt *time.Time
}

type taskView struct {
	ID                int64
	ParentID          *int64
	Depth             int
	Title             string
	Description       string
	Status            string
	Assignee          string
	BillingCode       string
	SpentTokens       int64
	SpentDollarsC     int64
	CreatedAt         time.Time
	CompletedAt       *time.Time
	VerifierKind      string
	Children          []taskView
}

type agentView struct {
	ID                 int64
	Name               string
	DisplayName        string
	ParentAgentName    string
	SpawnDepth         int
	ConfigHash         string
	AutonomyTier       string
	ToolScope          []string
	RollingRep         float64
	EvidenceCount      int
	SystemPromptFirst  string
}

type billingRow struct {
	Code         string
	Tokens       int64
	DollarsCents int64
	TaskCount    int
}

type timelineEvent struct {
	When     time.Time
	Kind     string
	Actor    string
	Message  string
	Priority int
}

type artifactView struct {
	From  string
	Body  string
	When  time.Time
	Kind  string
}

// --- snapshot builder -------------------------------------------------

func buildSnapshot(ctx context.Context, db *sql.DB, goalID int64) (*reportSnapshot, error) {
	snap := &reportSnapshot{GeneratedAt: time.Now().UTC()}

	// Goal row.
	var g goalView
	var ownerID, channelID int64
	var budgetTokens, budgetDollars sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT id, slug, title, description, status, owner_user_id, channel_id, created_at, completed_at, budget_tokens, budget_dollars_cents
		  FROM goals WHERE id=?`, goalID).Scan(
		&g.ID, &g.Slug, &g.Title, &g.Description, &g.Status, &ownerID, &channelID, &g.CreatedAt, &g.CompletedAt, &budgetTokens, &budgetDollars)
	if err != nil {
		return nil, fmt.Errorf("goal %d: %w", goalID, err)
	}
	_ = db.QueryRowContext(ctx, `SELECT username FROM users WHERE id=?`, ownerID).Scan(&g.Owner)
	_ = db.QueryRowContext(ctx, `SELECT name FROM channels WHERE id=?`, channelID).Scan(&g.ChannelName)
	snap.Goal = g
	if budgetTokens.Valid {
		snap.BudgetTokens = budgetTokens.Int64
	}
	if budgetDollars.Valid {
		snap.BudgetDollarsC = budgetDollars.Int64
	}

	// Tasks — load all rows into memory first, then resolve the
	// assignee agent names with separate queries. With MaxOpenConns=1
	// we cannot issue nested queries while the outer rows iterator is
	// still open.
	type rawTask struct {
		view     *taskView
		assignee sql.NullInt64
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, parent_task_id, depth, title, description, status, assignee_agent_id,
		       COALESCE(billing_code, ''), spent_tokens, spent_dollars_cents,
		       created_at, completed_at, verifier_config_json
		  FROM goal_tasks WHERE goal_id=? ORDER BY id`, goalID)
	if err != nil {
		return nil, err
	}
	var raws []rawTask
	for rows.Next() {
		t := &taskView{}
		var parentID sql.NullInt64
		var verifierJSON sql.NullString
		var assignee sql.NullInt64
		if err := rows.Scan(&t.ID, &parentID, &t.Depth, &t.Title, &t.Description, &t.Status, &assignee,
			&t.BillingCode, &t.SpentTokens, &t.SpentDollarsC, &t.CreatedAt, &t.CompletedAt, &verifierJSON); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if parentID.Valid {
			p := parentID.Int64
			t.ParentID = &p
		}
		if verifierJSON.Valid && verifierJSON.String != "" {
			var v struct {
				Kind string `json:"kind"`
			}
			_ = json.Unmarshal([]byte(verifierJSON.String), &v)
			t.VerifierKind = v.Kind
		}
		raws = append(raws, rawTask{view: t, assignee: assignee})
	}
	_ = rows.Close()

	flatByID := map[int64]*taskView{}
	var rootID int64
	for _, raw := range raws {
		t := raw.view
		if t.ParentID == nil {
			rootID = t.ID
		}
		if raw.assignee.Valid {
			var name string
			_ = db.QueryRowContext(ctx, `SELECT name FROM agents WHERE id=?`, raw.assignee.Int64).Scan(&name)
			t.Assignee = name
		}
		snap.TotalTokens += t.SpentTokens
		snap.TotalDollarsC += t.SpentDollarsC
		flatByID[t.ID] = t
	}
	// Build recursive tree.
	for _, t := range flatByID {
		if t.ParentID != nil {
			if parent, ok := flatByID[*t.ParentID]; ok {
				parent.Children = append(parent.Children, *t)
			}
		}
	}
	if root, ok := flatByID[rootID]; ok {
		snap.Tree = []taskView{*root}
		// Re-resolve children so the root's children have their own children populated (one pass isn't enough in map iteration order).
		var resolve func(tv *taskView)
		resolve = func(tv *taskView) {
			tv.Children = nil
			for _, t := range flatByID {
				if t.ParentID != nil && *t.ParentID == tv.ID {
					child := *t
					resolve(&child)
					tv.Children = append(tv.Children, child)
				}
			}
		}
		resolve(&snap.Tree[0])
	}

	// Budget percentage.
	if snap.BudgetDollarsC > 0 {
		snap.SpendPctDollar = float64(snap.TotalDollarsC) / float64(snap.BudgetDollarsC)
	}

	// Billing breakdown.
	brows, err := db.QueryContext(ctx, `
		SELECT COALESCE(billing_code, ''), SUM(spent_tokens), SUM(spent_dollars_cents), COUNT(*)
		  FROM goal_tasks WHERE goal_id=? GROUP BY billing_code ORDER BY billing_code`, goalID)
	if err != nil {
		return nil, err
	}
	for brows.Next() {
		var b billingRow
		if err := brows.Scan(&b.Code, &b.Tokens, &b.DollarsCents, &b.TaskCount); err != nil {
			_ = brows.Close()
			return nil, err
		}
		snap.BillingBreakdown = append(snap.BillingBreakdown, b)
	}
	_ = brows.Close()

	// Agents: everyone who appears in goal_tasks.assignee_agent_id plus the coordinator.
	var coordinatorID sql.NullInt64
	_ = db.QueryRowContext(ctx, `SELECT coordinator_agent_id FROM goals WHERE id=?`, goalID).Scan(&coordinatorID)
	agentIDSet := map[int64]bool{}
	if coordinatorID.Valid {
		agentIDSet[coordinatorID.Int64] = true
	}
	aRows, err := db.QueryContext(ctx, `
		SELECT DISTINCT assignee_agent_id FROM goal_tasks
		 WHERE goal_id=? AND assignee_agent_id IS NOT NULL`, goalID)
	if err != nil {
		return nil, err
	}
	var aIDs []int64
	for aRows.Next() {
		var id int64
		if err := aRows.Scan(&id); err != nil {
			_ = aRows.Close()
			return nil, err
		}
		aIDs = append(aIDs, id)
	}
	_ = aRows.Close()
	for _, id := range aIDs {
		agentIDSet[id] = true
	}

	ledger := trust.NewLedger(db)
	for id := range agentIDSet {
		var av agentView
		var parentID sql.NullInt64
		var toolScopeJSON string
		if err := db.QueryRowContext(ctx, `
			SELECT id, name, display_name, config_hash, parent_agent_id, spawn_depth, autonomy_tier,
			       tool_scope_json, system_prompt
			FROM agents WHERE id=?`, id).Scan(
			&av.ID, &av.Name, &av.DisplayName, &av.ConfigHash, &parentID, &av.SpawnDepth, &av.AutonomyTier,
			&toolScopeJSON, &av.SystemPromptFirst); err != nil {
			continue
		}
		if parentID.Valid {
			_ = db.QueryRowContext(ctx, `SELECT name FROM agents WHERE id=?`, parentID.Int64).Scan(&av.ParentAgentName)
		}
		if toolScopeJSON != "" {
			_ = json.Unmarshal([]byte(toolScopeJSON), &av.ToolScope)
		}
		if len(av.SystemPromptFirst) > 160 {
			av.SystemPromptFirst = av.SystemPromptFirst[:160] + "…"
		}
		av.RollingRep, av.EvidenceCount, _ = ledger.RollingScore(ctx, av.ConfigHash, "default", 30)
		snap.Agents = append(snap.Agents, av)
	}

	// Timeline: every message posted to the goal's backing channel, broken
	// into "system" vs "artifact" by the metadata.kind field we set at write.
	mRows, err := db.QueryContext(ctx, `
		SELECT from_agent, metadata, body, priority, created_at
		  FROM messages
		 WHERE channel_id=?
		 ORDER BY created_at, id`, channelID)
	if err != nil {
		return nil, err
	}
	for mRows.Next() {
		var e timelineEvent
		var metaStr string
		if err := mRows.Scan(&e.Actor, &metaStr, &e.Message, &e.Priority, &e.When); err != nil {
			_ = mRows.Close()
			return nil, err
		}
		var meta struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal([]byte(metaStr), &meta)
		e.Kind = meta.Kind
		if e.Kind == "" {
			e.Kind = "message"
		}
		snap.Timeline = append(snap.Timeline, e)
		if e.Kind == "artifact" {
			snap.Artifacts = append(snap.Artifacts, artifactView{
				From: e.Actor,
				Body: e.Message,
				When: e.When,
				Kind: e.Kind,
			})
		}
	}
	_ = mRows.Close()

	return snap, nil
}
