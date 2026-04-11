package mcp

import (
	"context"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/actions"
	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/jsruntime"
	"github.com/synapbus/synapbus/internal/marketplace"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/trace"
	"github.com/synapbus/synapbus/internal/wiki"
)

// newMarketplaceFixture builds a fully-wired bridge for marketplace tests.
// It seeds four agents (a poster + three bidders) and returns helpers for
// creating channels.
func newMarketplaceFixture(t *testing.T) (*HybridToolRegistrar, *ServiceBridge, *channels.Service, *marketplace.Service) {
	t.Helper()
	db := newTestDB(t)

	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, tracer)

	agentStore := agents.NewSQLiteAgentStore(db)
	agentService := agents.NewAgentService(agentStore, tracer)

	channelStore := channels.NewSQLiteChannelStore(db)
	channelService := channels.NewService(channelStore, msgService, tracer)

	taskStore := channels.NewSQLiteTaskStore(db)
	swarmService := channels.NewSwarmService(taskStore, channelStore, tracer)

	wikiService := wiki.NewService(db)

	mktStore := marketplace.NewStore(db)
	mkt := marketplace.NewService(mktStore, wikiService, swarmService, channelService, msgService, tracer)

	jsPool := jsruntime.NewPool(2)
	t.Cleanup(func() { jsPool.Close() })

	actionRegistry := actions.NewRegistry()
	actionIndex := actions.NewIndex(actionRegistry.List())

	registrar := NewHybridToolRegistrar(
		msgService,
		agentService,
		channelService,
		swarmService,
		nil, // attachmentService
		nil, // searchService
		nil, // reactionService
		nil, // trustService
		wikiService,
		jsPool,
		actionRegistry,
		actionIndex,
		db,
	)
	registrar.SetMarketplaceService(mkt)

	// Seed agents.
	ctx := context.Background()
	for _, name := range []string{"poster-agent", "bidder-alpha", "bidder-beta", "bidder-gamma"} {
		if _, _, err := agentService.Register(ctx, name, name, "ai", nil, 1); err != nil {
			t.Fatalf("seed agent %s: %v", name, err)
		}
	}

	bridge := NewServiceBridge(
		msgService,
		agentService,
		channelService,
		swarmService,
		nil, nil, nil, nil,
		wikiService,
		"poster-agent",
	)
	bridge.attachMarketplace(mkt)
	return registrar, bridge, channelService, mkt
}

// withAgent returns a clone of the bridge bound to a different agent name.
func (b *ServiceBridge) withAgent(name string) *ServiceBridge {
	clone := *b
	clone.agentName = name
	return &clone
}

func createAuctionChannel(t *testing.T, svc *channels.Service, name string, members ...string) int64 {
	t.Helper()
	ctx := context.Background()
	ch, err := svc.CreateChannel(ctx, channels.CreateChannelRequest{
		Name:      name,
		Type:      channels.TypeAuction,
		CreatedBy: members[0],
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	for _, m := range members[1:] {
		if err := svc.JoinChannel(ctx, ch.ID, m); err != nil {
			t.Fatalf("join channel %s by %s: %v", name, m, err)
		}
	}
	return ch.ID
}

// -------------- US2: capability manifest via wiki --------------

func TestMarketplace_SkillCard_CreateReadUpdate(t *testing.T) {
	_, bridge, _, _ := newMarketplaceFixture(t)
	ctx := context.Background()

	// 1. read_skill_card before any article exists → exists=false.
	res, err := bridge.Call(ctx, "read_skill_card", map[string]any{
		"agent_name": "poster-agent",
	})
	if err != nil {
		t.Fatalf("read_skill_card (empty): %v", err)
	}
	m := res.(map[string]any)
	if m["exists"].(bool) != false {
		t.Errorf("expected exists=false before publishing, got %v", m["exists"])
	}
	if m["slug"].(string) != "agent-poster-agent" {
		t.Errorf("unexpected slug %q", m["slug"])
	}

	// 2. publish a manifest using the existing wiki action (create_article).
	_, err = bridge.Call(ctx, "create_article", map[string]any{
		"slug":  "agent-poster-agent",
		"title": "poster-agent skill card",
		"body":  "# Skills\n- data-analysis\n- python\n\nExample: [[mcp-gateway-competitors]]",
	})
	if err != nil {
		t.Fatalf("create_article: %v", err)
	}

	// 3. read_skill_card now returns the article.
	res, err = bridge.Call(ctx, "read_skill_card", map[string]any{
		"agent_name": "poster-agent",
	})
	if err != nil {
		t.Fatalf("read_skill_card: %v", err)
	}
	m = res.(map[string]any)
	if m["exists"].(bool) != true {
		t.Fatalf("expected exists=true after publish")
	}
	if rev, _ := m["revision"].(int); rev != 1 {
		t.Errorf("expected revision=1, got %v", m["revision"])
	}

	// 4. update the article → revision increments (versioning for free via wiki).
	_, err = bridge.Call(ctx, "update_article", map[string]any{
		"slug": "agent-poster-agent",
		"body": "# Skills\n- data-analysis\n- python\n- go",
	})
	if err != nil {
		t.Fatalf("update_article: %v", err)
	}
	res, _ = bridge.Call(ctx, "read_skill_card", map[string]any{"agent_name": "poster-agent"})
	m = res.(map[string]any)
	if rev, _ := m["revision"].(int); rev != 2 {
		t.Errorf("expected revision=2 after update, got %v", m["revision"])
	}
}

// -------------- US1: full auction lifecycle --------------

func TestMarketplace_FullAuctionLifecycle(t *testing.T) {
	_, bridge, svc, _ := newMarketplaceFixture(t)
	ctx := context.Background()

	// Create an auction channel with the poster and two bidders joined.
	channelName := "task-marketplace-01"
	createAuctionChannel(t, svc, channelName, "poster-agent", "bidder-alpha", "bidder-beta")

	// Poster posts an auction task with marketplace metadata.
	res, err := bridge.Call(ctx, "post_auction", map[string]any{
		"channel_name":      channelName,
		"title":             "Q4 revenue analysis",
		"description":       "Trend analysis on Q4 revenue with two charts",
		"max_budget_tokens": 8000,
		"domains":           "data-analysis,python",
		"difficulty_weight": 1.5,
	})
	if err != nil {
		t.Fatalf("post_auction: %v", err)
	}
	taskInfo := res.(map[string]any)
	taskID := taskInfo["task_id"].(int64)
	if taskID == 0 {
		t.Fatal("expected non-zero task_id")
	}
	if domains, _ := taskInfo["domains"].([]string); len(domains) != 2 {
		t.Errorf("expected 2 domains, got %v", taskInfo["domains"])
	}

	// Two bidders submit bids.
	alpha := bridge.withAgent("bidder-alpha")
	beta := bridge.withAgent("bidder-beta")

	bidRes, err := alpha.Call(ctx, "bid", map[string]any{
		"task_id":           taskID,
		"estimated_tokens":  4200,
		"confidence":        0.9,
		"approach":          "pandas + matplotlib",
		"manifest_revision": 1,
	})
	if err != nil {
		t.Fatalf("alpha bid: %v", err)
	}
	alphaBidID := bidRes.(map[string]any)["bid_id"].(int64)

	bidRes, err = beta.Call(ctx, "bid", map[string]any{
		"task_id":          taskID,
		"estimated_tokens": 6000,
		"confidence":       0.7,
		"approach":         "R + ggplot",
	})
	if err != nil {
		t.Fatalf("beta bid: %v", err)
	}
	_ = bidRes

	// Self-bidding should be rejected (FR-011).
	if _, err := bridge.Call(ctx, "bid", map[string]any{
		"task_id":          taskID,
		"estimated_tokens": 500,
	}); err == nil {
		t.Error("expected self-bid to be rejected")
	}

	// Poster awards alpha's bid.
	awardRes, err := bridge.Call(ctx, "award", map[string]any{
		"task_id": taskID,
		"bid_id":  alphaBidID,
	})
	if err != nil {
		t.Fatalf("award: %v", err)
	}
	am := awardRes.(map[string]any)
	if am["winner"].(string) != "bidder-alpha" {
		t.Errorf("winner = %v, want bidder-alpha", am["winner"])
	}
	if am["claim_message_id"].(int64) == 0 {
		t.Error("expected non-zero claim_message_id (DM to winner)")
	}

	// Winner marks the task done with actual_tokens.
	doneRes, err := alpha.Call(ctx, "mark_task_done", map[string]any{
		"task_id":       taskID,
		"actual_tokens": 4500,
		"success_score": 1.0,
	})
	if err != nil {
		t.Fatalf("mark_task_done: %v", err)
	}
	dm := doneRes.(map[string]any)
	if dm["status"].(string) != channels.TaskStatusCompleted {
		t.Errorf("status = %v, want completed", dm["status"])
	}

	// -------------- US3: reputation ledger --------------

	// There should be 2 reputation entries — one per domain.
	entries := dm["reputation_entries"].([]*marketplace.ReputationEntry)
	if len(entries) != 2 {
		t.Errorf("expected 2 reputation entries (one per domain), got %d", len(entries))
	}
	foundDataAnalysis := false
	for _, e := range entries {
		if e.Domain == "data-analysis" {
			foundDataAnalysis = true
			if e.ActualTokens != 4500 {
				t.Errorf("data-analysis entry actual_tokens = %d, want 4500", e.ActualTokens)
			}
			if e.EstimatedTokens != 4200 {
				t.Errorf("data-analysis entry estimated_tokens = %d, want 4200", e.EstimatedTokens)
			}
			if e.DifficultyWeight != 1.5 {
				t.Errorf("data-analysis entry difficulty_weight = %f, want 1.5", e.DifficultyWeight)
			}
		}
	}
	if !foundDataAnalysis {
		t.Error("expected a data-analysis reputation entry")
	}

	// query_reputation returns the summary.
	qrRes, err := bridge.Call(ctx, "query_reputation", map[string]any{
		"agent_name": "bidder-alpha",
		"domain":     "data-analysis",
	})
	if err != nil {
		t.Fatalf("query_reputation: %v", err)
	}
	qm := qrRes.(map[string]any)
	summary := qm["summary"].(*marketplace.ReputationSummary)
	if summary.TasksCompleted != 1 {
		t.Errorf("tasks_completed = %d, want 1", summary.TasksCompleted)
	}
	if summary.AvgActualTokens != 4500 {
		t.Errorf("avg_actual_tokens = %d, want 4500", summary.AvgActualTokens)
	}
	if summary.AvgSuccessScore != 1.0 {
		t.Errorf("avg_success_score = %f, want 1.0", summary.AvgSuccessScore)
	}

	// query_reputation for a domain with no entries returns zero summary.
	qrRes, err = bridge.Call(ctx, "query_reputation", map[string]any{
		"agent_name": "bidder-alpha",
		"domain":     "unknown-domain",
	})
	if err != nil {
		t.Fatalf("query_reputation (empty): %v", err)
	}
	qm = qrRes.(map[string]any)
	summary = qm["summary"].(*marketplace.ReputationSummary)
	if summary.TasksCompleted != 0 {
		t.Errorf("empty-domain tasks_completed = %d, want 0", summary.TasksCompleted)
	}

	// Missing domain is rejected.
	if _, err := bridge.Call(ctx, "query_reputation", map[string]any{
		"agent_name": "bidder-alpha",
	}); err == nil {
		t.Error("expected error when domain is missing")
	}
}

// Ensure post_auction requires an auction-type channel (reuses swarm guard).
func TestMarketplace_PostAuction_RejectsNonAuctionChannel(t *testing.T) {
	_, bridge, svc, _ := newMarketplaceFixture(t)
	ctx := context.Background()

	ch, err := svc.CreateChannel(ctx, channels.CreateChannelRequest{
		Name:      "not-auction",
		Type:      channels.TypeStandard,
		CreatedBy: "poster-agent",
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	_ = ch

	_, err = bridge.Call(ctx, "post_auction", map[string]any{
		"channel_name": "not-auction",
		"title":        "nope",
		"domains":      "x",
	})
	if err == nil {
		t.Error("expected error when posting to a non-auction channel")
	}
}

// Quick smoke test to make sure SkillCardSlug normalises underscores/case.
func TestSkillCardSlug_Normalises(t *testing.T) {
	cases := map[string]string{
		"alice":          "agent-alice",
		"Bob":            "agent-bob",
		"research_alpha": "agent-research-alpha",
		"  spaced  ":     "agent-spaced",
	}
	for in, want := range cases {
		if got := marketplace.SkillCardSlug(in); got != want {
			t.Errorf("SkillCardSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
