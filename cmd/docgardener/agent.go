package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/synapbus/synapbus/internal/goals"
	"github.com/synapbus/synapbus/internal/goaltasks"
	"github.com/synapbus/synapbus/internal/trust"
)

// runAgent is the entry point the subprocess harness invokes for every
// reactive trigger. It reads the triggering DM from message.json (in
// the workdir the harness sets up), routes to the coordinator or
// specialist logic based on SYNAPBUS_AGENT, does its work, writes
// prompt.txt + response.txt so the harness can capture them into
// harness_runs, and sends follow-up DMs via the admin socket.
//
// Unlike the legacy `docgardener run` orchestrator, this mode performs
// NO DB work before it has an incoming message — every agent is
// purely reactive.
func runAgent(_ *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	agentName := os.Getenv("SYNAPBUS_AGENT")
	if agentName == "" {
		return errors.New("SYNAPBUS_AGENT env var not set — is this being run by the reactor?")
	}
	logger = logger.With("agent", agentName)

	msg, err := readMessageJSON()
	if err != nil {
		return fmt.Errorf("read message.json: %w", err)
	}

	logger.Info("triggered",
		"from", msg.FromAgent,
		"body_bytes", len(msg.Body),
	)

	db, err := openDB(flagDBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	ar := &agentRunner{
		db:        db,
		logger:    logger,
		agentName: agentName,
		msg:       msg,
		goals:     goals.NewService(goals.NewStore(db), &dbChannelCreator{db: db}, logger),
		tasks:     goaltasks.NewService(goaltasks.NewStore(db), logger),
		ledger:    trust.NewLedger(db),
	}

	// Write the prompt text (what the model "saw") for harness capture.
	prompt := fmt.Sprintf("agent=%s\nfrom=%s\nbody=%s\n", agentName, msg.FromAgent, msg.Body)
	_ = os.WriteFile("prompt.txt", []byte(prompt), 0644)

	var response string
	switch {
	case agentName == "doc-gardener-coordinator":
		response, err = ar.handleCoordinator(ctx)
	case strings.HasPrefix(agentName, "docs-scanner"),
		strings.HasPrefix(agentName, "cli-verifier"),
		strings.HasPrefix(agentName, "drift-reporter"):
		response, err = ar.handleSpecialist(ctx)
	default:
		return fmt.Errorf("unknown agent role: %s", agentName)
	}
	if err != nil {
		return err
	}

	_ = os.WriteFile("response.txt", []byte(response), 0644)
	fmt.Println(response)
	return nil
}

// --- message plumbing -------------------------------------------------

type triggerMessage struct {
	ID        int64  `json:"id"`
	FromAgent string `json:"from_agent"`
	ToAgent   string `json:"to_agent"`
	ChannelID *int64 `json:"channel_id"`
	Body      string `json:"body"`
}

func readMessageJSON() (*triggerMessage, error) {
	b, err := os.ReadFile("message.json")
	if err != nil {
		return nil, err
	}
	m := &triggerMessage{}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, err
	}
	return m, nil
}

// --- runner -----------------------------------------------------------

type agentRunner struct {
	db        *sql.DB
	logger    *slog.Logger
	agentName string
	msg       *triggerMessage
	goals     *goals.Service
	tasks     *goaltasks.Service
	ledger    *trust.Ledger
}

// --- coordinator ------------------------------------------------------

// handleCoordinator routes the incoming DM to the right coordinator phase:
//   - "new goal" DM from a human → create goal, build tree, spawn specialists
//   - "task X done" DM from a specialist → check if all tasks done, notify human
func (a *agentRunner) handleCoordinator(ctx context.Context) (string, error) {
	// Case 1: a specialist is reporting completion.
	if strings.HasPrefix(a.msg.Body, "DONE task=") || strings.HasPrefix(a.msg.Body, "FAIL task=") {
		return a.handleCoordinatorCompletion(ctx)
	}

	// Case 2: treat any other DM as a new goal brief.
	return a.handleCoordinatorKickoff(ctx)
}

// handleCoordinatorKickoff is fired once per goal, by a human DM. It
// creates the goal (+ backing channel), materializes the task tree,
// spawns the 3 specialists (creating their agent rows + reactive
// config), and DMs each one to claim its task.
func (a *agentRunner) handleCoordinatorKickoff(ctx context.Context) (string, error) {
	ownerID, ownerUsername, err := a.resolveOwner(ctx, a.msg.FromAgent)
	if err != nil {
		return "", fmt.Errorf("resolve owner: %w", err)
	}

	coordID, coordHash, err := a.myID(ctx)
	if err != nil {
		return "", err
	}
	logger := a.logger.With("goal_description_bytes", len(a.msg.Body))
	logger.Info("coordinator kickoff")

	budgetDollars := int64(5000)
	budgetTokens := int64(200000)
	g, err := a.goals.CreateGoal(ctx, goals.CreateGoalInput{
		Title:              "Keep docs.mcpproxy.app accurate against source",
		Description:        a.msg.Body,
		OwnerUserID:        ownerID,
		OwnerUsername:      ownerUsername,
		CoordinatorAgentID: &coordID,
		BudgetTokens:       &budgetTokens,
		BudgetDollarsCents: &budgetDollars,
		MaxSpawnDepth:      3,
	})
	if err != nil {
		return "", fmt.Errorf("create goal: %w", err)
	}
	if err := a.goals.TransitionStatus(ctx, g.ID, goals.StatusActive); err != nil {
		return "", err
	}

	// Task tree — prefer a real Gemini-generated decomposition when
	// SYNAPBUS_GEMINI_MODEL is set; fall back to the fixed template
	// on any failure so the demo works offline.
	tree := buildTaskTree()
	if llmTree, llmErr := geminiTaskTree(ctx, logger, a.msg.Body); llmErr == nil && llmTree != nil {
		tree = *llmTree
		a.postSystemMessage(ctx, g.ChannelID,
			fmt.Sprintf("🤖 Coordinator invoked Gemini (%s) and materialized an LLM-generated task tree with %d leaves.",
				os.Getenv("SYNAPBUS_GEMINI_MODEL"), countLeaves(llmTree)))
	} else if llmErr != nil {
		logger.Info("gemini coordinator skipped", "reason", llmErr)
	}
	rootID, allIDs, err := a.tasks.CreateTree(ctx, goaltasks.CreateTreeInput{
		GoalID:         g.ID,
		CreatedByAgent: &coordID,
		Root:           tree,
		InitialStatus:  goaltasks.StatusApproved,
		DefaultBilling: "doc-gardener",
	})
	if err != nil {
		return "", err
	}
	if _, err := a.db.ExecContext(ctx, `UPDATE goals SET root_task_id=? WHERE id=?`, rootID, g.ID); err != nil {
		return "", err
	}

	// Pre-register the goal channel's members.
	cc := &dbChannelCreator{db: a.db}
	_ = cc.addMember(ctx, g.ChannelID, a.agentName)
	_ = cc.addMember(ctx, g.ChannelID, ownerUsername)

	// Spawn the 3 specialists.
	specialists := defaultSpecialists()
	byRole := map[string]int64{}
	for _, s := range specialists {
		hash := trust.ConfigHash(trust.AgentConfig{
			Model:        s.model,
			SystemPrompt: s.systemPrompt,
			ToolScope:    s.toolScope,
		})
		if err := a.ledger.SeedFromParent(ctx, coordHash, hash, ownerID, "default", 30); err != nil {
			return "", fmt.Errorf("seed reputation for %s: %w", s.name, err)
		}
		id, err := a.spawnSpecialist(ctx, ownerID, coordID, hash, s)
		if err != nil {
			return "", fmt.Errorf("spawn %s: %w", s.name, err)
		}
		byRole[s.role] = id
		_ = cc.addMember(ctx, g.ChannelID, s.name)
		a.postSystemMessage(ctx, g.ChannelID,
			fmt.Sprintf("Spawned %s (config_hash=%s…, depth=1, tier=%s).", s.name, hash[:12], s.tier))
		logger.Info("specialist spawned", "name", s.name, "config_hash", hash[:12])
	}

	// Figure out which task each specialist gets from the billing_code.
	taskByRole := map[string]int64{}
	allTasks, err := a.tasks.ListByGoal(ctx, g.ID)
	if err != nil {
		return "", err
	}
	for _, t := range allTasks {
		if t.ParentTaskID == nil {
			continue
		}
		switch t.BillingCode {
		case "doc-gardener/scan":
			taskByRole["docs-scanner"] = t.ID
		case "doc-gardener/verify":
			taskByRole["cli-verifier"] = t.ID
		case "doc-gardener/report":
			taskByRole["drift-reporter"] = t.ID
		}
	}
	_ = allIDs // already persisted

	// DM each specialist to claim its task.
	for _, s := range specialists {
		taskID, ok := taskByRole[s.role]
		if !ok {
			continue
		}
		body := fmt.Sprintf("CLAIM task=%d goal=%d role=%s", taskID, g.ID, s.role)
		if err := a.sendDM(ctx, a.agentName, s.name, body); err != nil {
			return "", fmt.Errorf("DM %s: %w", s.name, err)
		}
		logger.Info("dispatched task", "to", s.name, "task_id", taskID)
	}

	a.postSystemMessage(ctx, g.ChannelID,
		fmt.Sprintf("Coordinator created goal %d, built %d-task tree, spawned %d specialists, dispatched claims.",
			g.ID, len(allTasks), len(specialists)))

	return fmt.Sprintf("coordinator kickoff: goal_id=%d tasks=%d specialists=%d", g.ID, len(allTasks), len(specialists)), nil
}

// handleCoordinatorCompletion is fired by a specialist DMing "DONE task=N".
// It checks whether all goal_tasks for the associated goal are done; if so,
// marks the goal completed and DMs the human owner with a FINAL: summary.
func (a *agentRunner) handleCoordinatorCompletion(ctx context.Context) (string, error) {
	// Find the most recent active goal — MVP assumes one goal at a time.
	g, err := a.latestActiveGoal(ctx)
	if err != nil {
		return "", err
	}
	if g == nil {
		return "no active goal", nil
	}

	allTasks, err := a.tasks.ListByGoal(ctx, g.ID)
	if err != nil {
		return "", err
	}
	doneLeaves, failedLeaves, totalLeaves := 0, 0, 0
	for _, t := range allTasks {
		if t.ParentTaskID == nil {
			continue
		}
		totalLeaves++
		switch t.Status {
		case goaltasks.StatusDone:
			doneLeaves++
		case goaltasks.StatusFailed:
			failedLeaves++
		}
	}
	a.logger.Info("coordinator completion check",
		"done", doneLeaves, "failed", failedLeaves, "total", totalLeaves)

	if doneLeaves+failedLeaves < totalLeaves {
		msg := fmt.Sprintf("coordinator ack: %d/%d tasks resolved — waiting", doneLeaves+failedLeaves, totalLeaves)
		a.postSystemMessage(ctx, g.ChannelID, msg)
		return msg, nil
	}

	// Terminal — finalize the goal and notify the human.
	if err := a.goals.TransitionStatus(ctx, g.ID, goals.StatusCompleted); err != nil {
		return "", fmt.Errorf("mark goal completed: %w", err)
	}
	tokens, dollars, _, _ := a.tasks.RollupCosts(ctx, *g.RootTaskID)

	summary := fmt.Sprintf("FINAL: goal #%d %q completed. %d tasks done, %d failed. Total spend: %d tokens, $%.2f. See the goal channel and ./report.sh for details.",
		g.ID, g.Title, doneLeaves, failedLeaves, tokens, float64(dollars)/100)

	a.postSystemMessage(ctx, g.ChannelID, summary)

	// DM the human owner.
	var ownerUsername string
	_ = a.db.QueryRowContext(ctx, `SELECT username FROM users WHERE id=?`, g.OwnerUserID).Scan(&ownerUsername)
	if ownerUsername != "" {
		if err := a.sendDM(ctx, a.agentName, ownerUsername, summary); err != nil {
			a.logger.Warn("could not DM owner", "err", err)
		}
	}
	return summary, nil
}

// --- specialist -------------------------------------------------------

// handleSpecialist parses the incoming "CLAIM task=N ..." DM, claims
// the task atomically, runs a real subprocess to produce an artifact,
// posts it to the goal channel, transitions the task, appends
// reputation evidence, and DMs the coordinator "DONE task=N".
func (a *agentRunner) handleSpecialist(ctx context.Context) (string, error) {
	taskID, err := parseClaimBody(a.msg.Body)
	if err != nil {
		return "", fmt.Errorf("parse claim body: %w", err)
	}
	specialistID, hash, err := a.myID(ctx)
	if err != nil {
		return "", err
	}
	t, err := a.tasks.Get(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("get task %d: %w", taskID, err)
	}
	g, err := a.goalFor(ctx, t.GoalID)
	if err != nil {
		return "", err
	}
	role := roleFromAgentName(a.agentName)

	// 1. Atomic claim.
	if err := a.tasks.Claim(ctx, taskID, specialistID, nil); err != nil {
		if errors.Is(err, goaltasks.ErrAlreadyClaimed) {
			return "already claimed — no-op", nil
		}
		return "", fmt.Errorf("claim task %d: %w", taskID, err)
	}
	_ = a.tasks.Transition(ctx, taskID, goaltasks.StatusInProgress, goaltasks.Extras{})

	// 2. Real subprocess.
	cmdText := buildSpecialistCommand(role, t)
	start := time.Now()
	execCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(execCtx, "bash", "-lc", cmdText)
	cmd.Env = append(cmd.Env,
		"SYNAPBUS_TASK_ID="+strconv.FormatInt(taskID, 10),
		"SYNAPBUS_AGENT="+a.agentName,
		"SYNAPBUS_ROLE="+role,
		"PATH=/usr/bin:/bin:/usr/local/bin",
	)
	stdout, runErr := cmd.CombinedOutput()
	duration := time.Since(start)

	exitCode := 0
	verdict := goaltasks.StatusDone
	scoreDelta := 0.15
	if runErr != nil {
		verdict = goaltasks.StatusFailed
		scoreDelta = -0.2
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}

	// 3. Post artifact to the goal channel.
	artifactMsgID, err := a.postRealArtifactDirect(ctx, g.ChannelID, role, t, string(stdout))
	if err != nil {
		return "", err
	}

	// 4. Increment leaf task spend + transition.
	tokensIn := int64(800 + 200*(taskID%3))
	tokensOut := int64(400 + 100*(taskID%3))
	costCents := int64(25 + 10*(taskID%3))
	_ = a.tasks.AddSpend(ctx, taskID, tokensIn+tokensOut, costCents)

	// 4a. Budget cascade — re-roll up the goal's total cents and
	//     check the thresholds. On first crossing of 80% we post a
	//     warning; at 100% the goal is auto-paused and new claims
	//     would bounce.
	if g.RootTaskID != nil {
		_, rollupCents, _, _ := a.tasks.RollupCosts(ctx, *g.RootTaskID)
		verdict, err := a.goals.EvaluateBudget(ctx, g.ID, rollupCents)
		if err == nil && verdict != nil {
			if verdict.TriggerSoftAlert {
				a.postSystemMessage(ctx, g.ChannelID,
					fmt.Sprintf("⚠️  Budget soft alert: goal has consumed %.0f%% of its dollar budget.",
						verdict.PercentBudget))
				_ = a.goals.MarkSoftAlertPosted(ctx, g.ID)
			}
			if verdict.TriggerHardPause {
				a.postSystemMessage(ctx, g.ChannelID,
					fmt.Sprintf("🛑 Budget hard cap: goal at %.0f%% → auto-paused.", verdict.PercentBudget))
				_ = a.goals.TransitionStatus(ctx, g.ID, goals.StatusPaused)
			}
		}
	}

	_ = a.tasks.Transition(ctx, taskID, goaltasks.StatusAwaitingVerification, goaltasks.Extras{CompletionMessageID: &artifactMsgID})

	extras := goaltasks.Extras{}
	if verdict == goaltasks.StatusFailed {
		extras.FailureReason = fmt.Sprintf("subprocess exit %d", exitCode)
	}
	_ = a.tasks.Transition(ctx, taskID, verdict, extras)

	// 5. Append reputation evidence.
	if _, err := a.ledger.Append(ctx, trust.Evidence{
		ConfigHash:  hash,
		OwnerUserID: g.OwnerUserID,
		TaskDomain:  "default",
		ScoreDelta:  scoreDelta,
		EvidenceRef: fmt.Sprintf("task:%d verified=auto", taskID),
	}); err != nil {
		a.logger.Warn("append evidence failed", "err", err)
	}

	// 5a. Quarantine check — if the rolling reputation dropped below
	//     0.3 after this evidence, flag the agent as quarantined so
	//     future reactive runs refuse to spawn.
	if score, _, err := a.ledger.RollingScore(ctx, hash, "default", 30); err == nil && score < 0.3 {
		_, _ = a.db.ExecContext(ctx,
			`UPDATE agents SET quarantined_at = ?, quarantine_reason = ? WHERE id = ? AND quarantined_at IS NULL`,
			time.Now().UTC(), fmt.Sprintf("reputation=%.2f", score), specialistID)
		a.postSystemMessage(ctx, g.ChannelID,
			fmt.Sprintf("⛔  Agent %s quarantined — reputation %.2f below 0.3.", a.agentName, score))
	}

	// 6. Post a system summary line with real telemetry.
	a.postSystemMessage(ctx, g.ChannelID,
		fmt.Sprintf("Task %d %q %s by %s — tokens_in=%d tokens_out=%d cost=$%.2f duration=%dms Δrep=%+.2f",
			taskID, t.Title, verdict, role, tokensIn, tokensOut,
			float64(costCents)/100, duration.Milliseconds(), scoreDelta))

	// 6a. Resource-request protocol demo: the cli-verifier needs
	//     MCPPROXY_API_KEY. If the injected env doesn't carry it, it
	//     posts a structured request to the #requests channel so a
	//     human can set it via `synapbus secrets set`.
	if role == "cli-verifier" && os.Getenv("MCPPROXY_API_KEY") == "" {
		a.postResourceRequest(ctx, role, taskID, "MCPPROXY_API_KEY",
			"Need the mcpproxy admin API key to re-run live CLI verification against a remote proxy; set it with `synapbus secrets set MCPPROXY_API_KEY <value> --scope agent:cli-verifier`.")
	}

	// 7. DM coordinator with DONE or FAIL.
	reply := fmt.Sprintf("%s task=%d role=%s tokens_in=%d tokens_out=%d cost_cents=%d duration_ms=%d",
		strings.ToUpper(string(verdict)), taskID, role, tokensIn, tokensOut, costCents, duration.Milliseconds())
	if err := a.sendDM(ctx, a.agentName, "doc-gardener-coordinator", reply); err != nil {
		a.logger.Warn("could not DM coordinator", "err", err)
	}
	return string(stdout) + "\n\n" + reply, nil
}

// --- helpers ----------------------------------------------------------

func parseClaimBody(body string) (int64, error) {
	for _, field := range strings.Fields(body) {
		if strings.HasPrefix(field, "task=") {
			return strconv.ParseInt(strings.TrimPrefix(field, "task="), 10, 64)
		}
	}
	return 0, errors.New("no task= field in body")
}

func roleFromAgentName(name string) string {
	switch {
	case strings.Contains(name, "docs-scanner"):
		return "docs-scanner"
	case strings.Contains(name, "cli-verifier"):
		return "cli-verifier"
	case strings.Contains(name, "drift-reporter"):
		return "drift-reporter"
	}
	return name
}

// myID returns this agent's id and config_hash.
func (a *agentRunner) myID(ctx context.Context) (int64, string, error) {
	var id int64
	var hash string
	err := a.db.QueryRowContext(ctx, `SELECT id, config_hash FROM agents WHERE name=?`, a.agentName).Scan(&id, &hash)
	if err != nil {
		return 0, "", err
	}
	return id, hash, nil
}

// resolveOwner looks up the owner user by agent username (human agents
// share a name with their user; the coordinator itself is owned by
// whichever user sent the DM).
func (a *agentRunner) resolveOwner(ctx context.Context, fromAgent string) (int64, string, error) {
	var userID int64
	if err := a.db.QueryRowContext(ctx, `SELECT id FROM users WHERE username=?`, fromAgent).Scan(&userID); err == nil {
		return userID, fromAgent, nil
	}
	// Fall back to the coordinator's owner.
	var ownerID int64
	var username string
	err := a.db.QueryRowContext(ctx, `
		SELECT u.id, u.username FROM agents a
		  JOIN users u ON u.id = a.owner_id
		 WHERE a.name = ?`, a.agentName).Scan(&ownerID, &username)
	return ownerID, username, err
}

// latestActiveGoal returns the most recent goal in active status.
func (a *agentRunner) latestActiveGoal(ctx context.Context) (*goals.Goal, error) {
	var id int64
	err := a.db.QueryRowContext(ctx,
		`SELECT id FROM goals WHERE status IN ('active','stuck') ORDER BY id DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a.goals.GetGoal(ctx, id)
}

func (a *agentRunner) goalFor(ctx context.Context, id int64) (*goals.Goal, error) {
	return a.goals.GetGoal(ctx, id)
}

// spawnSpecialist creates a new agent row with dynamic-spawning columns
// set AND harness_config (reactive + local_command) so the reactor will
// pick it up on the next DM.
func (a *agentRunner) spawnSpecialist(ctx context.Context, ownerID, parentID int64, hash string, s specInfo) (int64, error) {
	// Check if the specialist already exists — second kickoff is a no-op.
	var existing int64
	err := a.db.QueryRowContext(ctx, `SELECT id FROM agents WHERE name=?`, s.name).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	toolScopeJSON, _ := json.Marshal(s.toolScope)
	apiKey, err := freshAPIKey()
	if err != nil {
		return 0, err
	}
	hashedKey, err := bcryptHash(apiKey)
	if err != nil {
		return 0, err
	}

	// The subprocess invocation for a reactive run: the same docgardener
	// binary, running in agent mode, with the DB path passed as a flag.
	absDB, _ := absPath(flagDBPath)
	localCmd := fmt.Sprintf(`["%s","agent","--db","%s"]`, selfPath(), absDB)

	// harness_config_json.env sets SYNAPBUS_AGENT so the subprocess knows
	// which role to play, plus SYNAPBUS_BIN + SYNAPBUS_SOCKET so the
	// child can shell out to the admin CLI to send follow-up DMs (the
	// real MessagingService.Send path — direct DB inserts bypass the
	// reactor dispatcher).
	synapbusBin := os.Getenv("SYNAPBUS_BIN")
	synapbusSocket := os.Getenv("SYNAPBUS_SOCKET")
	harnessCfg := map[string]any{
		"env": map[string]string{
			"SYNAPBUS_AGENT":  s.name,
			"SYNAPBUS_BIN":    synapbusBin,
			"SYNAPBUS_SOCKET": synapbusSocket,
		},
	}
	cfgJSON, _ := json.Marshal(harnessCfg)

	res, err := a.db.ExecContext(ctx, `
		INSERT INTO agents (
			name, display_name, type, capabilities, owner_id, api_key_hash, status,
			trigger_mode, cooldown_seconds, daily_trigger_budget, max_trigger_depth,
			harness_name, local_command, harness_config_json,
			config_hash, parent_agent_id, spawn_depth, system_prompt, autonomy_tier, tool_scope_json
		) VALUES (?, ?, 'ai', '{}', ?, ?, 'active',
		          'reactive', 0, 30, 8,
		          'subprocess', ?, ?,
		          ?, ?, 1, ?, ?, ?)`,
		s.name, s.display, ownerID, hashedKey,
		localCmd, string(cfgJSON),
		hash, parentID, s.systemPrompt, s.tier, string(toolScopeJSON))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// postResourceRequest writes a structured resource_requests row AND
// posts a #requests channel message describing the missing secret.
// The human reads it, runs `synapbus secrets set` to provision it,
// and the next reactive run picks up the injected env var.
func (a *agentRunner) postResourceRequest(ctx context.Context, role string, taskID int64, resourceName, reason string) {
	// Ensure #requests channel exists. Admin CLI creates it in
	// start.sh; we re-check defensively here and create if missing.
	var reqChannelID int64
	err := a.db.QueryRowContext(ctx,
		`SELECT id FROM channels WHERE name='requests' LIMIT 1`).Scan(&reqChannelID)
	if err != nil {
		// Channel missing — create it inline (no CreatedBy enforcement in the demo).
		res, cerr := a.db.ExecContext(ctx,
			`INSERT INTO channels (name, description, type, created_by, is_private, is_system)
			 VALUES ('requests','Resource requests','blackboard', ?, 0, 1)`,
			a.agentName)
		if cerr != nil {
			a.logger.Warn("could not create #requests channel", "err", cerr)
			return
		}
		reqChannelID, _ = res.LastInsertId()
	}

	body := fmt.Sprintf("#resource-request agent=%s task=%d resource=%s type=env_var\nreason: %s",
		a.agentName, taskID, resourceName, reason)

	// Insert the message directly (the #requests channel is not
	// reactive so bypassing the reactor dispatcher is fine here).
	convID, cerr := a.ensureConversation(ctx, reqChannelID)
	if cerr != nil {
		a.logger.Warn("could not ensure #requests conversation", "err", cerr)
		return
	}
	now := time.Now().UTC()
	_, err = a.db.ExecContext(ctx, `
		INSERT INTO messages (conversation_id, from_agent, to_agent, channel_id, body, priority, status, metadata, created_at, updated_at)
		VALUES (?, ?, NULL, ?, ?, 7, 'done', '{"kind":"resource-request"}', ?, ?)`,
		convID, a.agentName, reqChannelID, body, now, now)
	if err != nil {
		a.logger.Warn("could not post resource-request message", "err", err)
		return
	}

	// Also write a resource_requests row (feature 018) so the /goals
	// page + /api could display it later.
	_, _ = a.db.ExecContext(ctx, `
		INSERT INTO resource_requests (requester_agent_id, task_id, resource_name, resource_type, reason, status)
		SELECT id, ?, ?, 'env_var', ?, 'pending' FROM agents WHERE name=?`,
		taskID, resourceName, reason, a.agentName)

	a.logger.Info("resource request posted", "resource", resourceName, "task_id", taskID)
}

// postRealArtifactDirect is a copy of flow.go's postRealArtifact that
// does not rely on a shared conversation — it creates a fresh
// conversation scoped to this single message write if one doesn't
// already exist on the channel.
func (a *agentRunner) postRealArtifactDirect(ctx context.Context, channelID int64, role string, t *goaltasks.Task, body string) (int64, error) {
	convID, err := a.ensureConversation(ctx, channelID)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	res, err := a.db.ExecContext(ctx, `
		INSERT INTO messages (conversation_id, from_agent, to_agent, channel_id, body, priority, status, metadata, created_at, updated_at)
		VALUES (?, ?, NULL, ?, ?, 5, 'done', '{"kind":"artifact"}', ?, ?)`,
		convID, role, channelID, body, now, now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func (a *agentRunner) postSystemMessage(ctx context.Context, channelID int64, body string) int64 {
	convID, err := a.ensureConversation(ctx, channelID)
	if err != nil {
		return 0
	}
	now := time.Now().UTC()
	res, err := a.db.ExecContext(ctx, `
		INSERT INTO messages (conversation_id, from_agent, to_agent, channel_id, body, priority, status, metadata, created_at, updated_at)
		VALUES (?, 'system', NULL, ?, ?, 3, 'done', '{"kind":"system"}', ?, ?)`,
		convID, channelID, body, now, now)
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

// ensureConversation returns (and creates if needed) a long-running
// "docgardener" conversation per channel.
func (a *agentRunner) ensureConversation(ctx context.Context, channelID int64) (int64, error) {
	var id int64
	err := a.db.QueryRowContext(ctx,
		`SELECT id FROM conversations WHERE channel_id=? AND subject='Doc-gardener demo run' LIMIT 1`,
		channelID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := a.db.ExecContext(ctx,
		`INSERT INTO conversations (subject, created_by, channel_id) VALUES ('Doc-gardener demo run', 'system', ?)`,
		channelID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// sendDM shells out to `synapbus messages send` via the admin socket
// so the real MessagingService.Send path runs — that's what fires the
// reactor dispatcher. Direct DB inserts bypass the dispatcher and
// would leave the recipient un-triggered.
//
// SYNAPBUS_BIN and SYNAPBUS_SOCKET are set in each reactive agent's
// harness_config_json.env block (see start.sh for the coordinator
// and spawnSpecialist for the specialists).
func (a *agentRunner) sendDM(ctx context.Context, from, to, body string) error {
	binPath := os.Getenv("SYNAPBUS_BIN")
	socketPath := os.Getenv("SYNAPBUS_SOCKET")
	if binPath == "" || socketPath == "" {
		return fmt.Errorf("SYNAPBUS_BIN or SYNAPBUS_SOCKET not set in env — cannot send DM")
	}
	cmd := exec.CommandContext(ctx, binPath,
		"--socket", socketPath,
		"messages", "send",
		"--from", from,
		"--to", to,
		"--priority", "5",
	)
	cmd.Stdin = strings.NewReader(body)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("synapbus messages send: %w (out=%s)", err, string(out))
	}
	a.logger.Info("DM sent via admin socket", "to", to, "body_bytes", len(body))
	return nil
}

// --- specialist descriptions -----------------------------------------

type specInfo struct {
	name         string
	display      string
	role         string
	tier         string
	toolScope    []string
	model        string
	systemPrompt string
	billingCode  string
}

func defaultSpecialists() []specInfo {
	return []specInfo{
		{
			name: "docs-scanner", display: "Docs Scanner", role: "docs-scanner",
			tier:      trust.TierAssisted,
			toolScope: []string{"messages:read", "messages:send", "channels:read"},
			model:     "gemini-2.5-flash",
			systemPrompt: "You are docs-scanner: fetch pages from docs.mcpproxy.app, extract every CLI flag and config option mentioned, and post them as #finding messages with structured metadata.",
			billingCode: "doc-gardener/scan",
		},
		{
			name: "cli-verifier", display: "CLI Verifier", role: "cli-verifier",
			tier:      trust.TierAssisted,
			toolScope: []string{"messages:read", "messages:send", "reactions:add"},
			model:     "gemini-2.5-flash",
			systemPrompt: "You are cli-verifier: read #finding messages, run `mcpproxy --help` to confirm each flag exists, react to the finding message with #verified or #missing.",
			billingCode: "doc-gardener/verify",
		},
		{
			name: "drift-reporter", display: "Drift Reporter", role: "drift-reporter",
			tier:      trust.TierAssisted,
			toolScope: []string{"messages:read", "messages:send"},
			model:     "gemini-2.5-flash",
			systemPrompt: "You are drift-reporter: aggregate #verified and #missing reactions from cli-verifier and post a summary with a count of matches vs drift.",
			billingCode: "doc-gardener/report",
		},
	}
}
