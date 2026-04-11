package mcp

// Agent marketplace MCP actions (spec 016 MVP — US1, US2, US3).
//
// New actions exposed via the execute tool's call(actionName, args) interface:
//
//   - post_auction      — create an auction task with marketplace metadata
//   - bid               — place a bid with estimated_tokens / confidence
//   - award             — accept a winning bid and notify the winner
//   - read_skill_card   — read an agent's capability manifest (wiki article)
//   - query_reputation  — query the reputation ledger by (agent, domain)
//
// These actions follow the exact dispatch pattern used by bridge.go:
// each call* method validates args, calls the marketplace service, and
// returns a JSON-serialisable map.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/synapbus/synapbus/internal/marketplace"
)

// attachMarketplace installs the marketplace service on a ServiceBridge.
// Call it before dispatching any marketplace action.
func (b *ServiceBridge) attachMarketplace(mkt *marketplace.Service) {
	b.marketplace = mkt
}

func (b *ServiceBridge) callPostAuction(ctx context.Context, args map[string]any) (any, error) {
	if b.marketplace == nil {
		return nil, fmt.Errorf("marketplace service not available")
	}
	if b.channelService == nil {
		return nil, fmt.Errorf("channel service not available")
	}

	channelName := getString(args, "channel_name", "")
	if channelName == "" {
		return nil, fmt.Errorf("'channel_name' parameter is required")
	}
	title := getString(args, "title", "")
	if title == "" {
		return nil, fmt.Errorf("'title' parameter is required")
	}
	description := getString(args, "description", "")
	acceptance := getString(args, "acceptance_criteria", "")

	meta := marketplace.AuctionTaskMeta{
		AcceptanceCriteria: acceptance,
		MaxBudgetTokens:    int64(getInt(args, "max_budget_tokens", 0)),
		DifficultyWeight:   getFloat(args, "difficulty_weight", 1.0),
	}

	// domains: accept comma-separated string or []any
	meta.Domains = parseStringList(args, "domains")

	deadlineStr := getString(args, "deadline", "")
	var deadline *time.Time
	if deadlineStr != "" {
		t, err := time.Parse(time.RFC3339, deadlineStr)
		if err != nil {
			return nil, fmt.Errorf("deadline must be ISO 8601 format: %s", err)
		}
		deadline = &t
	}

	ch, err := b.channelService.GetChannelByName(ctx, channelName)
	if err != nil {
		return nil, err
	}

	task, err := b.marketplace.PostAuction(ctx, ch.ID, b.agentName, title, description, meta, deadline)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"task_id":           task.ID,
		"channel_id":        task.ChannelID,
		"channel_name":      channelName,
		"title":             task.Title,
		"status":            task.Status,
		"posted_by":         task.PostedBy,
		"domains":           meta.Domains,
		"max_budget_tokens": meta.MaxBudgetTokens,
		"deadline":          task.Deadline,
		"created_at":        task.CreatedAt,
	}, nil
}

func (b *ServiceBridge) callBid(ctx context.Context, args map[string]any) (any, error) {
	if b.marketplace == nil {
		return nil, fmt.Errorf("marketplace service not available")
	}

	taskID := int64(getInt(args, "task_id", 0))
	if taskID == 0 {
		return nil, fmt.Errorf("'task_id' parameter is required")
	}

	meta := marketplace.BidMeta{
		EstimatedTokens:  int64(getInt(args, "estimated_tokens", 0)),
		Confidence:       getFloat(args, "confidence", 0),
		Approach:         getString(args, "approach", ""),
		ManifestRevision: getInt(args, "manifest_revision", 0),
	}
	timeEstimate := getString(args, "time_estimate", "")

	bid, err := b.marketplace.Bid(ctx, taskID, b.agentName, meta, timeEstimate)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"bid_id":           bid.ID,
		"task_id":          bid.TaskID,
		"agent_name":       bid.AgentName,
		"status":           bid.Status,
		"estimated_tokens": meta.EstimatedTokens,
		"confidence":       meta.Confidence,
		"approach":         meta.Approach,
	}, nil
}

func (b *ServiceBridge) callAward(ctx context.Context, args map[string]any) (any, error) {
	if b.marketplace == nil {
		return nil, fmt.Errorf("marketplace service not available")
	}
	taskID := int64(getInt(args, "task_id", 0))
	if taskID == 0 {
		return nil, fmt.Errorf("'task_id' parameter is required")
	}
	bidID := int64(getInt(args, "bid_id", 0))
	if bidID == 0 {
		return nil, fmt.Errorf("'bid_id' parameter is required")
	}

	winner, claimMsgID, err := b.marketplace.Award(ctx, taskID, bidID, b.agentName)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"task_id":          taskID,
		"bid_id":           bidID,
		"winner":           winner,
		"claim_message_id": claimMsgID,
		"status":           "awarded",
	}, nil
}

func (b *ServiceBridge) callMarkTaskDone(ctx context.Context, args map[string]any) (any, error) {
	if b.marketplace == nil {
		return nil, fmt.Errorf("marketplace service not available")
	}
	taskID := int64(getInt(args, "task_id", 0))
	if taskID == 0 {
		return nil, fmt.Errorf("'task_id' parameter is required")
	}

	actualTokens := int64(getInt(args, "actual_tokens", 0))
	successScore := getFloat(args, "success_score", 1.0)

	task, entries, err := b.marketplace.MarkTaskDone(ctx, taskID, b.agentName, actualTokens, successScore)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"task_id":            taskID,
		"status":             task.Status,
		"actual_tokens":      actualTokens,
		"success_score":      successScore,
		"reputation_entries": entries,
	}, nil
}

func (b *ServiceBridge) callReadSkillCard(ctx context.Context, args map[string]any) (any, error) {
	if b.marketplace == nil {
		return nil, fmt.Errorf("marketplace service not available")
	}

	agentName := getString(args, "agent_name", "")
	if agentName == "" {
		agentName = b.agentName
	}

	art, err := b.marketplace.ReadSkillCard(ctx, agentName)
	if err != nil {
		return nil, err
	}
	if art == nil {
		return map[string]any{
			"agent_name": agentName,
			"exists":     false,
			"slug":       marketplace.SkillCardSlug(agentName),
		}, nil
	}
	return map[string]any{
		"agent_name":    agentName,
		"exists":        true,
		"slug":          art.Slug,
		"title":         art.Title,
		"body":          art.Body,
		"revision":      art.Revision,
		"word_count":    art.WordCount,
		"updated_by":    art.UpdatedBy,
		"updated_at":    art.UpdatedAt,
		"outgoing_links": art.OutgoingLinks,
		"backlinks":     art.Backlinks,
	}, nil
}

func (b *ServiceBridge) callQueryReputation(ctx context.Context, args map[string]any) (any, error) {
	if b.marketplace == nil {
		return nil, fmt.Errorf("marketplace service not available")
	}
	agentName := getString(args, "agent_name", "")
	if agentName == "" {
		agentName = b.agentName
	}
	domain := getString(args, "domain", "")
	if domain == "" {
		return nil, fmt.Errorf("'domain' parameter is required (reputation is scoped by agent and domain)")
	}
	limit := getInt(args, "limit", 20)

	summary, entries, err := b.marketplace.QueryReputation(ctx, agentName, domain, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"agent_name":        agentName,
		"domain":            domain,
		"summary":           summary,
		"recent_entries":    entries,
		"recent_count":      len(entries),
	}, nil
}

// parseStringList reads args[key] as either a comma-separated string or a
// JSON/array-of-any and returns a trimmed list of non-empty strings.
func parseStringList(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok || v == nil {
		return []string{}
	}
	var out []string
	switch x := v.(type) {
	case string:
		// Accept either "a,b,c" or JSON-encoded ["a","b"]
		s := strings.TrimSpace(x)
		if s == "" {
			return []string{}
		}
		if strings.HasPrefix(s, "[") {
			var arr []string
			if err := json.Unmarshal([]byte(s), &arr); err == nil {
				for _, a := range arr {
					a = strings.TrimSpace(a)
					if a != "" {
						out = append(out, a)
					}
				}
				return out
			}
		}
		for _, p := range strings.Split(s, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
	case []any:
		for _, item := range x {
			if s, ok := item.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
	case []string:
		for _, s := range x {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}
