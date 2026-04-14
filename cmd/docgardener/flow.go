package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/synapbus/synapbus/internal/goals"
	"github.com/synapbus/synapbus/internal/goaltasks"
	"github.com/synapbus/synapbus/internal/trust"
)

// flow wires the primitives for the demo. It reaches into the DB
// directly for bootstrap (users, channels, agents) because these are
// one-shot operations that would otherwise require embedding the full
// service wiring. For the domain logic (goals, tasks, trust) it uses
// the real services.
type flow struct {
	db       *sql.DB
	goals    *goals.Service
	tasks    *goaltasks.Service
	ledger   *trust.Ledger
	logger   *slog.Logger
	channels *dbChannelCreator
	// bootstrap state
	ownerUserID         int64
	ownerUsername       string
	coordinatorAgentID  int64
	coordinatorHash     string
	approvalsChannelID  int64
	conversationID      int64 // shared conversation for all system/artifact messages
}

func newFlow(db *sql.DB, logger *slog.Logger) *flow {
	cc := &dbChannelCreator{db: db}
	return &flow{
		db:       db,
		goals:    goals.NewService(goals.NewStore(db), cc, logger),
		tasks:    goaltasks.NewService(goaltasks.NewStore(db), logger),
		ledger:   trust.NewLedger(db),
		logger:   logger,
		channels: cc,
	}
}

// --- bootstrap --------------------------------------------------------

func (f *flow) bootstrap(ctx context.Context) error {
	// owner user
	if err := f.db.QueryRowContext(ctx,
		`SELECT id, username FROM users WHERE username='algis'`).Scan(&f.ownerUserID, &f.ownerUsername); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lookup user: %w", err)
		}
		hash, _ := bcrypt.GenerateFromPassword([]byte("algis-demo-pw"), bcrypt.DefaultCost)
		res, err := f.db.ExecContext(ctx,
			`INSERT INTO users (username, password_hash) VALUES ('algis', ?)`, string(hash))
		if err != nil {
			return fmt.Errorf("create user algis: %w", err)
		}
		f.ownerUserID, _ = res.LastInsertId()
		f.ownerUsername = "algis"
		f.logger.Info("user created", "username", f.ownerUsername, "id", f.ownerUserID)
	}

	// approvals and requests channels (idempotent).
	for _, name := range []string{"approvals", "requests"} {
		if _, err := f.channels.ensureChannel(ctx, name, "Auto-approved "+name+" queue", "blackboard", f.ownerUsername); err != nil {
			return fmt.Errorf("ensure channel %s: %w", name, err)
		}
	}
	var err error
	f.approvalsChannelID, err = f.channels.getByName(ctx, "approvals")
	if err != nil {
		return err
	}

	// Coordinator agent — always exists before a run starts.
	coord := coordinatorConfig()
	f.coordinatorHash = trust.ConfigHash(coord.ToTrustConfig())
	coordID, err := f.ensureAgent(ctx, ensureAgentInput{
		Name:         coord.Name,
		DisplayName:  coord.DisplayName,
		OwnerID:      f.ownerUserID,
		SystemPrompt: coord.SystemPrompt,
		ConfigHash:   f.coordinatorHash,
		AutonomyTier: trust.TierAssisted,
		ToolScope:    coord.ToolScope,
		SpawnDepth:   0,
		ParentAgentID: nil,
	})
	if err != nil {
		return fmt.Errorf("ensure coordinator: %w", err)
	}
	f.coordinatorAgentID = coordID

	// Seed the coordinator with some neutral evidence so spawned children
	// are seeded at 70 % of a sensible baseline instead of the 0.5 neutral
	// default.
	if _, err := f.ledger.Append(ctx, trust.Evidence{
		ConfigHash:  f.coordinatorHash,
		OwnerUserID: f.ownerUserID,
		TaskDomain:  "default",
		ScoreDelta:  0.8, // strong baseline for a pre-built meta agent
		EvidenceRef: "bootstrap:coordinator-baseline",
		Weight:      1.0,
	}); err != nil {
		return fmt.Errorf("seed coordinator reputation: %w", err)
	}

	f.logger.Info("bootstrap complete",
		"user_id", f.ownerUserID,
		"coordinator_id", coordID,
		"coordinator_hash", f.coordinatorHash[:12],
	)
	return nil
}

// --- demo flow --------------------------------------------------------

func (f *flow) run(ctx context.Context) (int64, error) {
	f.logger.Info("=== Phase 1: goal creation ===")
	budget := int64(5000) // $50.00 in cents
	goalTokens := int64(200000)
	g, err := f.goals.CreateGoal(ctx, goals.CreateGoalInput{
		Title:              "Keep docs.mcpproxy.app accurate against source",
		Description:        `Verify every CLI flag and config option mentioned in docs.mcpproxy.app actually exists in the mcpproxy binary, flag any drift, and propose doc patches.`,
		OwnerUserID:        f.ownerUserID,
		OwnerUsername:      f.ownerUsername,
		CoordinatorAgentID: &f.coordinatorAgentID,
		BudgetTokens:       &goalTokens,
		BudgetDollarsCents: &budget,
		MaxSpawnDepth:      3,
	})
	if err != nil {
		return 0, err
	}
	// Activate the goal.
	if err := f.goals.TransitionStatus(ctx, g.ID, goals.StatusActive); err != nil {
		return 0, err
	}
	// Create the conversation used for all subsequent messages in this channel.
	convRes, err := f.db.ExecContext(ctx,
		`INSERT INTO conversations (subject, created_by, channel_id) VALUES (?, 'system', ?)`,
		"Doc-gardener demo run", g.ChannelID)
	if err != nil {
		return 0, fmt.Errorf("create conversation: %w", err)
	}
	f.conversationID, _ = convRes.LastInsertId()
	f.postSystemMessage(ctx, g.ChannelID, fmt.Sprintf("Goal %q created (id=%d, budget=$%.2f).", g.Title, g.ID, float64(budget)/100))

	// Coordinator is expected to be a member of its goal's channel.
	_ = f.channels.addMember(ctx, g.ChannelID, "doc-gardener-coordinator")
	_ = f.channels.addMember(ctx, g.ChannelID, f.ownerUsername)

	f.logger.Info("=== Phase 2: task tree decomposition ===")
	tree := buildTaskTree()
	rootTaskID, allTaskIDs, err := f.tasks.CreateTree(ctx, goaltasks.CreateTreeInput{
		GoalID:         g.ID,
		CreatedByAgent: &f.coordinatorAgentID,
		Root:           tree,
		InitialStatus:  goaltasks.StatusApproved,
		DefaultBilling: "doc-gardener",
	})
	if err != nil {
		return 0, err
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE goals SET root_task_id=? WHERE id=?`, rootTaskID, g.ID); err != nil {
		return 0, err
	}
	f.postSystemMessage(ctx, g.ChannelID, fmt.Sprintf("Coordinator proposed a tree of %d tasks rooted at task %d. Auto-approved.", len(allTaskIDs), rootTaskID))

	f.logger.Info("=== Phase 3: specialist agent spawning ===")
	// Spawn three specialists. Each goes through delegation-cap validation
	// against the coordinator's grant before being materialized.
	coordGrant := trust.Grant{
		AutonomyTier:       trust.TierAssisted,
		ToolScope:          []string{"messages:read", "messages:send", "channels:read", "reactions:add"},
		BudgetTokens:       goalTokens / 2,
		BudgetDollarsCents: budget / 2,
		SpawnDepth:         0,
	}

	type specialistSpec struct {
		name        string
		display     string
		role        string
		tier        string
		toolScope   []string
		model       string
		systemPrompt string
		billingCode string
	}
	specialists := []specialistSpec{
		{
			name: "docs-scanner", display: "Docs Scanner", role: "docs-scanner",
			tier:        trust.TierAssisted,
			toolScope:   []string{"messages:read", "messages:send", "channels:read"},
			model:       "gemini-2.5-flash",
			systemPrompt: "You are docs-scanner: fetch pages from docs.mcpproxy.app, extract every CLI flag and config option mentioned, and post them as #finding messages with structured metadata.",
			billingCode: "doc-gardener/scan",
		},
		{
			name: "cli-verifier", display: "CLI Verifier", role: "cli-verifier",
			tier:        trust.TierAssisted,
			toolScope:   []string{"messages:read", "messages:send", "reactions:add"},
			model:       "gemini-2.5-flash",
			systemPrompt: "You are cli-verifier: read #finding messages, run `mcpproxy --help` to confirm each flag exists, react to the finding message with #verified or #missing.",
			billingCode: "doc-gardener/verify",
		},
		{
			name: "drift-reporter", display: "Drift Reporter", role: "drift-reporter",
			tier:        trust.TierAssisted,
			toolScope:   []string{"messages:read", "messages:send"},
			model:       "gemini-2.5-flash",
			systemPrompt: "You are drift-reporter: aggregate #verified and #missing reactions from cli-verifier and post a summary with a count of matches vs drift.",
			billingCode: "doc-gardener/report",
		},
	}

	specialistsByRole := map[string]int64{}
	for _, spec := range specialists {
		proposed := trust.Grant{
			AutonomyTier:       spec.tier,
			ToolScope:          spec.toolScope,
			BudgetTokens:       goalTokens / 6,
			BudgetDollarsCents: budget / 6,
			SpawnDepth:         1, // child's proposed depth
		}
		effective, violations := trust.DelegationCap(coordGrant, proposed, g.MaxSpawnDepth)
		if len(violations) > 0 {
			return 0, fmt.Errorf("delegation cap violation for %s: %v", spec.name, violations)
		}
		hash := trust.ConfigHash(trust.AgentConfig{
			Model:        spec.model,
			SystemPrompt: spec.systemPrompt,
			ToolScope:    spec.toolScope,
		})
		// Child reputation is seeded at 70 % of parent's.
		if err := f.ledger.SeedFromParent(ctx, f.coordinatorHash, hash, f.ownerUserID, "default", 30); err != nil {
			return 0, fmt.Errorf("seed reputation for %s: %w", spec.name, err)
		}
		id, err := f.ensureAgent(ctx, ensureAgentInput{
			Name:          spec.name,
			DisplayName:   spec.display,
			OwnerID:       f.ownerUserID,
			SystemPrompt:  spec.systemPrompt,
			ConfigHash:    hash,
			AutonomyTier:  effective.AutonomyTier,
			ToolScope:     effective.ToolScope,
			SpawnDepth:    1,
			ParentAgentID: &f.coordinatorAgentID,
		})
		if err != nil {
			return 0, fmt.Errorf("spawn %s: %w", spec.name, err)
		}
		specialistsByRole[spec.role] = id
		_ = f.channels.addMember(ctx, g.ChannelID, spec.name)
		f.postSystemMessage(ctx, g.ChannelID,
			fmt.Sprintf("Spawned specialist %q (config_hash=%s..., spawn_depth=1, tier=%s).",
				spec.name, hash[:12], effective.AutonomyTier))
		f.logger.Info("specialist spawned",
			"name", spec.name,
			"config_hash", hash[:12],
			"tier", effective.AutonomyTier,
		)
	}

	f.logger.Info("=== Phase 4: claim + work + verify ===")
	tasks, err := f.tasks.ListByGoal(ctx, g.ID)
	if err != nil {
		return 0, err
	}
	// We drive only the leaf tasks — the root is a parent and doesn't get claimed.
	for _, t := range tasks {
		if t.ParentTaskID == nil {
			continue
		}
		role := leafRoleFor(t)
		agentID, ok := specialistsByRole[role]
		if !ok {
			continue
		}
		// Claim atomically.
		if err := f.tasks.Claim(ctx, t.ID, agentID, nil); err != nil {
			return 0, fmt.Errorf("claim task %d by %s: %w", t.ID, role, err)
		}
		// Move through the state machine.
		if err := f.tasks.Transition(ctx, t.ID, goaltasks.StatusInProgress, goaltasks.Extras{}); err != nil {
			return 0, err
		}
		// Simulate the agent doing work: post an artifact, burn some tokens.
		tokensUsed := int64(1500 + 500*(t.ID%3))
		costCents := int64(25 + 10*(t.ID%3))
		if err := f.tasks.AddSpend(ctx, t.ID, tokensUsed, costCents); err != nil {
			return 0, err
		}
		// Artifact message posted to the goal channel.
		artifactMsgID, err := f.postArtifact(ctx, g.ChannelID, role, t)
		if err != nil {
			return 0, err
		}
		if err := f.tasks.Transition(ctx, t.ID, goaltasks.StatusAwaitingVerification, goaltasks.Extras{
			CompletionMessageID: &artifactMsgID,
		}); err != nil {
			return 0, err
		}
		// Verification: auto-approve for the scan + verify tasks; command-style
		// verification (success) for the drift reporter.
		verdict := goaltasks.StatusDone
		scoreDelta := 0.15
		evidenceRef := fmt.Sprintf("task:%d verified=auto", t.ID)
		if role == "drift-reporter" {
			// Simulate a command verifier — assume exit 0.
			scoreDelta = 0.2
			evidenceRef = fmt.Sprintf("task:%d verified=command(exit=0)", t.ID)
		}
		if err := f.tasks.Transition(ctx, t.ID, verdict, goaltasks.Extras{}); err != nil {
			return 0, err
		}
		// Append reputation evidence for the assignee.
		var hash string
		if err := f.db.QueryRowContext(ctx, `SELECT config_hash FROM agents WHERE id=?`, agentID).Scan(&hash); err != nil {
			return 0, err
		}
		if _, err := f.ledger.Append(ctx, trust.Evidence{
			ConfigHash:  hash,
			OwnerUserID: f.ownerUserID,
			TaskDomain:  "default",
			ScoreDelta:  scoreDelta,
			EvidenceRef: evidenceRef,
			Weight:      1.0,
		}); err != nil {
			return 0, err
		}
		f.postSystemMessage(ctx, g.ChannelID,
			fmt.Sprintf("Task %d %q completed by %s (tokens=%d, cost=$%.2f, Δrep=%+.2f).",
				t.ID, t.Title, role, tokensUsed, float64(costCents)/100, scoreDelta))
	}

	// Roll the root task up, finalize the goal.
	_, _, _, err = f.tasks.RollupCosts(ctx, rootTaskID)
	if err != nil {
		return 0, err
	}
	// Transition the root task to done via its parent chain — skip transition
	// for the root because the MVP demo isn't finalizing parents; they
	// remain 'approved' to keep the demo data realistic.
	if err := f.goals.TransitionStatus(ctx, g.ID, goals.StatusCompleted); err != nil {
		return 0, err
	}
	f.postSystemMessage(ctx, g.ChannelID, "Goal marked completed.")
	return g.ID, nil
}

// --- helpers ----------------------------------------------------------

func buildTaskTree() goaltasks.TreeNode {
	return goaltasks.TreeNode{
		Title:              "Verify docs.mcpproxy.app against source",
		Description:        "Root task for the doc-gardener goal.",
		AcceptanceCriteria: "A drift report exists citing count of matches vs. missing items.",
		BillingCode:        "doc-gardener",
		Children: []goaltasks.TreeNode{
			{
				Title:              "Scan docs for CLI flags and config keys",
				Description:        "Fetch all pages under docs.mcpproxy.app/*; extract flags/options into #finding messages.",
				AcceptanceCriteria: "At least one #finding message per documented flag.",
				BillingCode:        "doc-gardener/scan",
				VerifierConfig: &goaltasks.VerifierConfig{Kind: goaltasks.VerifierKindAuto},
			},
			{
				Title:              "Verify flags exist in mcpproxy binary",
				Description:        "Run `mcpproxy --help` and react #verified or #missing on each #finding.",
				AcceptanceCriteria: "Every #finding has a #verified or #missing reaction.",
				BillingCode:        "doc-gardener/verify",
				VerifierConfig: &goaltasks.VerifierConfig{Kind: goaltasks.VerifierKindAuto},
			},
			{
				Title:              "Produce drift report",
				Description:        "Aggregate reactions from cli-verifier; post a final summary message.",
				AcceptanceCriteria: "Summary message contains counts of matches, drifts, and recommended patches.",
				BillingCode:        "doc-gardener/report",
				VerifierConfig: &goaltasks.VerifierConfig{
					Kind:       goaltasks.VerifierKindCommand,
					Cmd:        "test -s report.txt",
					TimeoutSec: 10,
				},
			},
		},
	}
}

func leafRoleFor(t *goaltasks.Task) string {
	switch t.BillingCode {
	case "doc-gardener/scan":
		return "docs-scanner"
	case "doc-gardener/verify":
		return "cli-verifier"
	case "doc-gardener/report":
		return "drift-reporter"
	}
	return ""
}

type ensureAgentInput struct {
	Name          string
	DisplayName   string
	OwnerID       int64
	SystemPrompt  string
	ConfigHash    string
	AutonomyTier  string
	ToolScope     []string
	SpawnDepth    int
	ParentAgentID *int64
}

// ensureAgent upserts an agent row, creating it with a fresh API key
// on first call and updating the new dynamic-spawning columns on
// every call. Returns the agent id.
func (f *flow) ensureAgent(ctx context.Context, in ensureAgentInput) (int64, error) {
	var existingID int64
	err := f.db.QueryRowContext(ctx, `SELECT id FROM agents WHERE name=?`, in.Name).Scan(&existingID)
	toolScopeJSON, _ := json.Marshal(in.ToolScope)
	if errors.Is(err, sql.ErrNoRows) {
		// Mint an API key.
		buf := make([]byte, 24)
		if _, err := rand.Read(buf); err != nil {
			return 0, err
		}
		apiKey := "sk-dg-" + hex.EncodeToString(buf)
		hashed, err := bcrypt.GenerateFromPassword([]byte(apiKey), bcrypt.DefaultCost)
		if err != nil {
			return 0, err
		}
		res, err := f.db.ExecContext(ctx, `
			INSERT INTO agents (
				name, display_name, type, capabilities, owner_id, api_key_hash, status,
				config_hash, parent_agent_id, spawn_depth, system_prompt, autonomy_tier, tool_scope_json
			) VALUES (?, ?, 'ai', '{}', ?, ?, 'active', ?, ?, ?, ?, ?, ?)`,
			in.Name, in.DisplayName, in.OwnerID, string(hashed),
			in.ConfigHash, in.ParentAgentID, in.SpawnDepth, in.SystemPrompt, in.AutonomyTier, string(toolScopeJSON),
		)
		if err != nil {
			return 0, err
		}
		id, _ := res.LastInsertId()
		return id, nil
	}
	if err != nil {
		return 0, err
	}
	// Update the new columns on an existing row.
	_, err = f.db.ExecContext(ctx, `
		UPDATE agents
		   SET config_hash       = ?,
		       parent_agent_id   = ?,
		       spawn_depth       = ?,
		       system_prompt     = ?,
		       autonomy_tier     = ?,
		       tool_scope_json   = ?,
		       display_name      = COALESCE(NULLIF(display_name, ''), ?)
		 WHERE id = ?`,
		in.ConfigHash, in.ParentAgentID, in.SpawnDepth, in.SystemPrompt, in.AutonomyTier, string(toolScopeJSON),
		in.DisplayName, existingID)
	return existingID, err
}

func (f *flow) postSystemMessage(ctx context.Context, channelID int64, body string) int64 {
	now := time.Now().UTC()
	res, err := f.db.ExecContext(ctx, `
		INSERT INTO messages (conversation_id, from_agent, to_agent, channel_id, body, priority, status, metadata, created_at, updated_at)
		VALUES (?, 'system', NULL, ?, ?, 3, 'done', '{"kind":"system"}', ?, ?)`,
		f.conversationID, channelID, body, now, now)
	if err != nil {
		f.logger.Warn("post system message failed", "err", err)
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

func (f *flow) postArtifact(ctx context.Context, channelID int64, role string, t *goaltasks.Task) (int64, error) {
	var body string
	switch role {
	case "docs-scanner":
		body = fmt.Sprintf("#finding artifact for task %d: found 12 flags on docs.mcpproxy.app (--port, --config, --socket, --data-dir, --log-format, --log-level, --otel-endpoint, --tls-cert, --tls-key, --metrics-port, --retention, --version).", t.ID)
	case "cli-verifier":
		body = fmt.Sprintf("#verified artifact for task %d: 10/12 flags confirmed present in `mcpproxy --help`. #missing: --otel-endpoint, --retention.", t.ID)
	case "drift-reporter":
		body = fmt.Sprintf("#summary artifact for task %d: 10 matches, 2 drifts (--otel-endpoint and --retention documented but not implemented). Recommend patching the docs or filing bugs.", t.ID)
	default:
		body = fmt.Sprintf("artifact for task %d from %s", t.ID, role)
	}
	now := time.Now().UTC()
	res, err := f.db.ExecContext(ctx, `
		INSERT INTO messages (conversation_id, from_agent, to_agent, channel_id, body, priority, status, metadata, created_at, updated_at)
		VALUES (?, ?, NULL, ?, ?, 5, 'done', '{"kind":"artifact"}', ?, ?)`,
		f.conversationID, role, channelID, body, now, now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// --- coordinator config ----------------------------------------------

type coordCfg struct {
	Name         string
	DisplayName  string
	SystemPrompt string
	ToolScope    []string
}

func (c coordCfg) ToTrustConfig() trust.AgentConfig {
	return trust.AgentConfig{
		Model:        "coordinator/v1",
		SystemPrompt: c.SystemPrompt,
		ToolScope:    c.ToolScope,
	}
}

func coordinatorConfig() coordCfg {
	return coordCfg{
		Name:        "doc-gardener-coordinator",
		DisplayName: "Doc-gardener Coordinator",
		SystemPrompt: `You are the doc-gardener coordinator. Your job is to decompose a high-level goal ("keep docs accurate against the source code") into a tree of sub-tasks, propose specialist agents to carry out the leaf tasks, monitor progress via the goal channel, and iterate. You never act on leaf tasks directly. You communicate via SynapBus MCP tools.`,
		ToolScope: []string{
			"messages:read", "messages:send", "channels:read", "reactions:add",
			"goals:create", "tasks:propose_tree", "agents:propose",
		},
	}
}
