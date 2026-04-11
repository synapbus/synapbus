package marketplace

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/trace"
	"github.com/synapbus/synapbus/internal/wiki"
)

// SkillCardSlug returns the wiki slug for an agent's capability manifest.
// Wiki slugs allow only [a-z0-9-], so we use "agent-<name>". Any underscores
// in the agent name are replaced with hyphens.
func SkillCardSlug(agentName string) string {
	a := strings.ToLower(strings.TrimSpace(agentName))
	a = strings.ReplaceAll(a, "_", "-")
	return "agent-" + a
}

// Service implements the marketplace MVP (spec 016).
type Service struct {
	store       *Store
	wikiService *wiki.Service
	swarm       *channels.SwarmService
	channels    *channels.Service
	messaging   *messaging.MessagingService
	tracer      *trace.Tracer
	logger      *slog.Logger
}

// NewService wires a marketplace service. All collaborators are required
// except tracer (nil-safe).
func NewService(
	store *Store,
	wikiService *wiki.Service,
	swarm *channels.SwarmService,
	channelService *channels.Service,
	msgService *messaging.MessagingService,
	tracer *trace.Tracer,
) *Service {
	return &Service{
		store:       store,
		wikiService: wikiService,
		swarm:       swarm,
		channels:    channelService,
		messaging:   msgService,
		tracer:      tracer,
		logger:      slog.Default().With("component", "marketplace"),
	}
}

// ---------- Types carried in task.Requirements / bid.Capabilities JSON. ----------

// AuctionTaskMeta is stored inside tasks.requirements as JSON. All fields
// are optional on the wire; missing values fall back to sane defaults.
type AuctionTaskMeta struct {
	AcceptanceCriteria string   `json:"acceptance_criteria,omitempty"`
	MaxBudgetTokens    int64    `json:"max_budget_tokens,omitempty"`
	Domains            []string `json:"domains,omitempty"`
	DifficultyWeight   float64  `json:"difficulty_weight,omitempty"`
	CurrentSpendTokens int64    `json:"current_spend_tokens,omitempty"`
}

// BidMeta is stored inside task_bids.capabilities as JSON.
type BidMeta struct {
	EstimatedTokens  int64   `json:"estimated_tokens,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
	Approach         string  `json:"approach,omitempty"`
	ManifestRevision int     `json:"manifest_revision,omitempty"`
}

// ---------- Capability manifest (US2 via wiki). ----------

// ReadSkillCard returns the capability manifest article for the given agent.
// Returns a nil article + nil error if the manifest does not yet exist.
func (s *Service) ReadSkillCard(ctx context.Context, agentName string) (*wiki.Article, error) {
	if s.wikiService == nil {
		return nil, fmt.Errorf("wiki service not available")
	}
	slug := SkillCardSlug(agentName)
	art, err := s.wikiService.GetArticle(ctx, slug)
	if err != nil {
		// Treat "not found" as a nil result — the caller decides whether this
		// is an error. The wiki store returns an error string containing
		// "not found" when the slug is missing.
		if strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, err
	}
	return art, nil
}

// ---------- Auction posting / bidding / awarding (US1 on top of swarm). ----------

// PostAuction creates a new auction task in an auction-type channel with the
// supplied marketplace metadata serialised into task.requirements.
func (s *Service) PostAuction(
	ctx context.Context,
	channelID int64,
	agentName, title, description string,
	meta AuctionTaskMeta,
	deadline *time.Time,
) (*channels.Task, error) {
	if s.swarm == nil {
		return nil, fmt.Errorf("swarm service not available")
	}

	// Normalise meta — ensure domains slice is non-nil and difficulty defaults to 1.
	if meta.Domains == nil {
		meta.Domains = []string{}
	}
	if meta.DifficultyWeight == 0 {
		meta.DifficultyWeight = 1.0
	}

	reqs, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal auction meta: %w", err)
	}

	task, err := s.swarm.PostTask(ctx, channelID, agentName, title, description, reqs, deadline)
	if err != nil {
		return nil, err
	}

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "marketplace.auction_posted", map[string]any{
			"task_id":           task.ID,
			"channel_id":        channelID,
			"domains":           meta.Domains,
			"max_budget_tokens": meta.MaxBudgetTokens,
		})
	}
	return task, nil
}

// Bid submits a bid on an open auction task. Delegates to the swarm service
// for the core bid lifecycle and stores marketplace bid metadata in the
// bid.capabilities JSON blob.
func (s *Service) Bid(
	ctx context.Context,
	taskID int64,
	agentName string,
	meta BidMeta,
	timeEstimate string,
) (*channels.Bid, error) {
	if s.swarm == nil {
		return nil, fmt.Errorf("swarm service not available")
	}

	caps, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal bid meta: %w", err)
	}

	// Use meta.Approach as the bid message for human readability.
	bid, err := s.swarm.BidOnTask(ctx, taskID, agentName, caps, timeEstimate, meta.Approach)
	if err != nil {
		return nil, err
	}

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "marketplace.bid_submitted", map[string]any{
			"task_id":          taskID,
			"bid_id":           bid.ID,
			"estimated_tokens": meta.EstimatedTokens,
			"confidence":       meta.Confidence,
		})
	}
	return bid, nil
}

// Award accepts a bid and converts the auction into a claim on the winning
// agent by DM'ing them. The task body contains the task_id in metadata so the
// winning agent can use the existing claim/process/done lifecycle.
//
// Per spec 016 FR-009: "On award, the system MUST convert the auction into a
// claim on the winning agent using the existing claim/process/done lifecycle."
func (s *Service) Award(
	ctx context.Context,
	taskID, bidID int64,
	awarderAgent string,
) (winningAgent string, claimMessageID int64, err error) {
	if s.swarm == nil {
		return "", 0, fmt.Errorf("swarm service not available")
	}

	if err := s.swarm.AcceptBid(ctx, taskID, bidID, awarderAgent); err != nil {
		return "", 0, err
	}

	// Refresh task to find the assigned agent.
	task, bids, err := s.swarm.GetTaskWithBids(ctx, taskID)
	if err != nil {
		return "", 0, fmt.Errorf("load awarded task: %w", err)
	}
	winningAgent = task.AssignedTo
	if winningAgent == "" {
		// Fallback: find the accepted bid.
		for _, b := range bids {
			if b.ID == bidID {
				winningAgent = b.AgentName
				break
			}
		}
	}
	if winningAgent == "" {
		return "", 0, fmt.Errorf("could not determine winning agent for task %d", taskID)
	}

	// Send a DM to the winner that acts as the claimable work item.
	if s.messaging != nil {
		metaJSON, _ := json.Marshal(map[string]any{
			"marketplace":       "awarded",
			"task_id":           taskID,
			"bid_id":            bidID,
			"channel_id":        task.ChannelID,
			"awarded_by":        awarderAgent,
		})
		body := fmt.Sprintf("Auction awarded: task %d — %q. Use mark_task_done to complete.", task.ID, task.Title)
		msg, sendErr := s.messaging.SendMessage(ctx, awarderAgent, winningAgent, body, messaging.SendOptions{
			Subject:  "Awarded: " + task.Title,
			Priority: 8,
			Metadata: string(metaJSON),
		})
		if sendErr != nil {
			s.logger.Warn("failed to send award DM", "err", sendErr, "task_id", taskID, "winner", winningAgent)
		} else if msg != nil {
			claimMessageID = msg.ID
		}
	}

	if s.tracer != nil {
		s.tracer.Record(ctx, awarderAgent, "marketplace.auction_awarded", map[string]any{
			"task_id":          taskID,
			"bid_id":           bidID,
			"winner":           winningAgent,
			"claim_message_id": claimMessageID,
		})
	}
	return winningAgent, claimMessageID, nil
}

// MarkTaskDone marks a task completed and writes reputation ledger entries
// for every domain declared on the task. One entry per domain (FR-012).
func (s *Service) MarkTaskDone(
	ctx context.Context,
	taskID int64,
	agentName string,
	actualTokens int64,
	successScore float64,
) (*channels.Task, []*ReputationEntry, error) {
	if s.swarm == nil {
		return nil, nil, fmt.Errorf("swarm service not available")
	}

	task, bids, err := s.swarm.GetTaskWithBids(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}

	// Clamp success score.
	if successScore < 0 {
		successScore = 0
	}
	if successScore > 1 {
		successScore = 1
	}

	// Complete via existing swarm service (validates assignee & status).
	if err := s.swarm.CompleteTask(ctx, taskID, agentName); err != nil {
		return nil, nil, err
	}

	// Parse meta from the original task.
	var meta AuctionTaskMeta
	if len(task.Requirements) > 0 {
		_ = json.Unmarshal(task.Requirements, &meta)
	}
	if meta.DifficultyWeight == 0 {
		meta.DifficultyWeight = 1.0
	}

	// Find the winning bid to pick up estimated_tokens (if available).
	var estimatedTokens int64
	for _, b := range bids {
		if b.Status == channels.BidStatusAccepted {
			var bm BidMeta
			if len(b.Capabilities) > 0 {
				_ = json.Unmarshal(b.Capabilities, &bm)
			}
			estimatedTokens = bm.EstimatedTokens
			break
		}
	}

	// Default to a single "general" domain if none declared, so reputation
	// is still recorded (FR-012 / SC-010).
	domains := meta.Domains
	if len(domains) == 0 {
		domains = []string{"general"}
	}

	var entries []*ReputationEntry
	for _, d := range domains {
		entry := &ReputationEntry{
			AgentName:        agentName,
			Domain:           d,
			TaskID:           taskID,
			EstimatedTokens:  estimatedTokens,
			ActualTokens:     actualTokens,
			SuccessScore:     successScore,
			DifficultyWeight: meta.DifficultyWeight,
		}
		if err := s.store.RecordEntry(ctx, entry); err != nil {
			return nil, entries, fmt.Errorf("record reputation: %w", err)
		}
		entries = append(entries, entry)
	}

	if s.tracer != nil {
		s.tracer.Record(ctx, agentName, "marketplace.task_done", map[string]any{
			"task_id":       taskID,
			"actual_tokens": actualTokens,
			"success_score": successScore,
			"domains":       domains,
		})
	}

	task.Status = channels.TaskStatusCompleted
	return task, entries, nil
}

// QueryReputation returns the rolled-up reputation summary plus the most
// recent raw entries for (agent, domain).
func (s *Service) QueryReputation(ctx context.Context, agentName, domain string, limit int) (*ReputationSummary, []*ReputationEntry, error) {
	if s.store == nil {
		return nil, nil, fmt.Errorf("marketplace store not available")
	}
	sum, err := s.store.Summary(ctx, agentName, domain)
	if err != nil {
		return nil, nil, err
	}
	entries, err := s.store.ListEntries(ctx, agentName, domain, limit)
	if err != nil {
		return sum, nil, err
	}
	return sum, entries, nil
}
