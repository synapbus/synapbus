package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/goals"
	"github.com/synapbus/synapbus/internal/goaltasks"
)

// GoalsHandler serves the /api/goals endpoints used by the Web UI /goals
// page: list goals, show a single goal's full task tree with cost
// rollup, billing-code breakdown, and the spawned agents attached.
type GoalsHandler struct {
	goals *goals.Service
	tasks *goaltasks.Service
	db    *sql.DB
}

func NewGoalsHandler(g *goals.Service, t *goaltasks.Service, db *sql.DB) *GoalsHandler {
	return &GoalsHandler{goals: g, tasks: t, db: db}
}

// ListGoals returns recent goals with basic metadata + total spend.
func (h *GoalsHandler) ListGoals(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}

	gs, err := h.goals.ListGoals(r.Context(), nil, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody("internal_error", err.Error()))
		return
	}

	type goalSummary struct {
		ID                 int64   `json:"id"`
		Slug               string  `json:"slug"`
		Title              string  `json:"title"`
		Status             string  `json:"status"`
		ChannelID          int64   `json:"channel_id"`
		OwnerUsername      string  `json:"owner_username"`
		RootTaskID         *int64  `json:"root_task_id"`
		SpentTokens        int64   `json:"spent_tokens"`
		SpentDollarsCents  int64   `json:"spent_dollars_cents"`
		TaskCount          int     `json:"task_count"`
		BudgetTokens       *int64  `json:"budget_tokens"`
		BudgetDollarsCents *int64  `json:"budget_dollars_cents"`
		PercentBudget      float64 `json:"percent_budget"`
		CreatedAt          string  `json:"created_at"`
	}

	out := make([]goalSummary, 0, len(gs))
	for _, g := range gs {
		s := goalSummary{
			ID:                 g.ID,
			Slug:               g.Slug,
			Title:              g.Title,
			Status:             g.Status,
			ChannelID:          g.ChannelID,
			RootTaskID:         g.RootTaskID,
			BudgetTokens:       g.BudgetTokens,
			BudgetDollarsCents: g.BudgetDollarsCents,
			CreatedAt:          g.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT username FROM users WHERE id=?`, g.OwnerUserID).Scan(&s.OwnerUsername)
		if g.RootTaskID != nil {
			tokens, cents, count, err := h.tasks.RollupCosts(r.Context(), *g.RootTaskID)
			if err == nil {
				s.SpentTokens = tokens
				s.SpentDollarsCents = cents
				s.TaskCount = count
			}
		}
		if g.BudgetDollarsCents != nil && *g.BudgetDollarsCents > 0 {
			s.PercentBudget = float64(s.SpentDollarsCents) / float64(*g.BudgetDollarsCents) * 100.0
		}
		out = append(out, s)
	}

	writeJSON(w, http.StatusOK, map[string]any{"goals": out})
}

// GetGoal returns a single goal with its full task tree, cost rollup,
// billing-code breakdown, and spawned-agent snapshot.
func (h *GoalsHandler) GetGoal(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("bad_request", "invalid goal id"))
		return
	}

	g, err := h.goals.GetGoal(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", err.Error()))
		return
	}

	tasks, err := h.tasks.ListByGoal(r.Context(), g.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody("internal_error", err.Error()))
		return
	}

	var rollupTokens, rollupCents int64
	var rollupCount int
	if g.RootTaskID != nil {
		rollupTokens, rollupCents, rollupCount, _ = h.tasks.RollupCosts(r.Context(), *g.RootTaskID)
	}

	type taskOut struct {
		ID                 int64                      `json:"id"`
		ParentTaskID       *int64                     `json:"parent_task_id"`
		Title              string                     `json:"title"`
		Description        string                     `json:"description"`
		AcceptanceCriteria string                     `json:"acceptance_criteria"`
		Status             string                     `json:"status"`
		Depth              int                        `json:"depth"`
		BillingCode        string                     `json:"billing_code"`
		AssigneeAgentID    *int64                     `json:"assignee_agent_id"`
		AssigneeAgentName  string                     `json:"assignee_agent_name,omitempty"`
		SpentTokens        int64                      `json:"spent_tokens"`
		SpentDollarsCents  int64                      `json:"spent_dollars_cents"`
		VerifierConfig     *goaltasks.VerifierConfig  `json:"verifier_config,omitempty"`
		HeartbeatConfig    *goaltasks.HeartbeatConfig `json:"heartbeat_config,omitempty"`
		FailureReason      string                     `json:"failure_reason,omitempty"`
		CreatedAt          string                     `json:"created_at"`
		CompletedAt        *string                    `json:"completed_at,omitempty"`
	}

	agentNameByID := map[int64]string{}
	out := make([]taskOut, 0, len(tasks))
	for _, t := range tasks {
		tt := taskOut{
			ID:                 t.ID,
			ParentTaskID:       t.ParentTaskID,
			Title:              t.Title,
			Description:        t.Description,
			AcceptanceCriteria: t.AcceptanceCriteria,
			Status:             t.Status,
			Depth:              t.Depth,
			BillingCode:        t.BillingCode,
			AssigneeAgentID:    t.AssigneeAgentID,
			SpentTokens:        t.SpentTokens,
			SpentDollarsCents:  t.SpentDollarsCents,
			VerifierConfig:     t.VerifierConfig,
			HeartbeatConfig:    t.HeartbeatConfig,
			FailureReason:      t.FailureReason,
			CreatedAt:          t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if t.CompletedAt != nil {
			s := t.CompletedAt.UTC().Format("2006-01-02T15:04:05Z")
			tt.CompletedAt = &s
		}
		if t.AssigneeAgentID != nil {
			name, ok := agentNameByID[*t.AssigneeAgentID]
			if !ok {
				_ = h.db.QueryRowContext(r.Context(),
					`SELECT name FROM agents WHERE id=?`, *t.AssigneeAgentID).Scan(&name)
				agentNameByID[*t.AssigneeAgentID] = name
			}
			tt.AssigneeAgentName = name
		}
		out = append(out, tt)
	}

	// Spawned agents attached to this goal (any agent whose config_hash
	// appears as an assignee on one of the goal's tasks, plus the
	// coordinator itself).
	type spawnedAgent struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		DisplayName  string `json:"display_name"`
		ConfigHash   string `json:"config_hash"`
		SpawnDepth   int    `json:"spawn_depth"`
		AutonomyTier string `json:"autonomy_tier"`
		ParentAgent  string `json:"parent_agent_name,omitempty"`
	}
	agentSeen := map[int64]bool{}
	agentList := []spawnedAgent{}
	collectAgent := func(id int64) {
		if id == 0 || agentSeen[id] {
			return
		}
		agentSeen[id] = true
		var sa spawnedAgent
		var parentID sql.NullInt64
		err := h.db.QueryRowContext(r.Context(), `
			SELECT id, name, display_name,
			       COALESCE(config_hash,''), COALESCE(spawn_depth,0),
			       COALESCE(autonomy_tier,''), parent_agent_id
			  FROM agents WHERE id=?`, id).
			Scan(&sa.ID, &sa.Name, &sa.DisplayName, &sa.ConfigHash, &sa.SpawnDepth, &sa.AutonomyTier, &parentID)
		if err != nil {
			return
		}
		if parentID.Valid {
			var pname string
			_ = h.db.QueryRowContext(r.Context(),
				`SELECT name FROM agents WHERE id=?`, parentID.Int64).Scan(&pname)
			sa.ParentAgent = pname
		}
		agentList = append(agentList, sa)
	}
	if g.CoordinatorAgentID != nil {
		collectAgent(*g.CoordinatorAgentID)
	}
	for _, t := range tasks {
		if t.AssigneeAgentID != nil {
			collectAgent(*t.AssigneeAgentID)
		}
	}

	// Billing-code rollup via raw query (service wrapper not needed).
	billingBreakdown := map[string]map[string]int64{}
	if g.RootTaskID != nil {
		rows, err := h.db.QueryContext(r.Context(), `
			WITH RECURSIVE subtree(id) AS (
				SELECT id FROM goal_tasks WHERE id = ?
				UNION ALL
				SELECT t.id FROM goal_tasks t
				JOIN subtree s ON t.parent_task_id = s.id
			)
			SELECT COALESCE(billing_code,''), SUM(spent_tokens), SUM(spent_dollars_cents)
			  FROM goal_tasks WHERE id IN subtree
			 GROUP BY billing_code`, *g.RootTaskID)
		if err == nil {
			for rows.Next() {
				var code string
				var tokens, cents int64
				if err := rows.Scan(&code, &tokens, &cents); err == nil {
					billingBreakdown[code] = map[string]int64{
						"tokens": tokens,
						"cents":  cents,
					}
				}
			}
			rows.Close()
		}
	}

	var ownerUsername string
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT username FROM users WHERE id=?`, g.OwnerUserID).Scan(&ownerUsername)

	// Recent system/artifact messages on the goal channel for a small timeline.
	type timelineEvent struct {
		ID        int64  `json:"id"`
		From      string `json:"from"`
		Body      string `json:"body"`
		Kind      string `json:"kind"`
		CreatedAt string `json:"created_at"`
	}
	timeline := []timelineEvent{}
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, from_agent, body, COALESCE(metadata,''), created_at
		  FROM messages
		 WHERE channel_id = ?
		 ORDER BY id DESC
		 LIMIT 50`, g.ChannelID)
	if err == nil {
		for rows.Next() {
			var ev timelineEvent
			var meta string
			if err := rows.Scan(&ev.ID, &ev.From, &ev.Body, &meta, &ev.CreatedAt); err == nil {
				if meta != "" {
					var m map[string]any
					if json.Unmarshal([]byte(meta), &m) == nil {
						if k, ok := m["kind"].(string); ok {
							ev.Kind = k
						}
					}
				}
				timeline = append(timeline, ev)
			}
		}
		rows.Close()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"goal": map[string]any{
			"id":                   g.ID,
			"slug":                 g.Slug,
			"title":                g.Title,
			"description":          g.Description,
			"status":               g.Status,
			"channel_id":           g.ChannelID,
			"coordinator_agent_id": g.CoordinatorAgentID,
			"root_task_id":         g.RootTaskID,
			"owner_user_id":        g.OwnerUserID,
			"owner_username":       ownerUsername,
			"budget_tokens":        g.BudgetTokens,
			"budget_dollars_cents": g.BudgetDollarsCents,
			"max_spawn_depth":      g.MaxSpawnDepth,
			"alert_80pct_posted":   g.Alert80PctPosted,
			"created_at":           g.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		},
		"tasks": out,
		"rollup": map[string]any{
			"tokens":        rollupTokens,
			"dollars_cents": rollupCents,
			"task_count":    rollupCount,
		},
		"billing_breakdown": billingBreakdown,
		"spawned_agents":    agentList,
		"timeline":          timeline,
	})
}
