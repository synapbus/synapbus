// Proactive-memory injection retrieval — builds the relevant-context
// packet attached to every injection-eligible MCP tool response (per
// `contracts/mcp-injection.md`).
package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
)

// MemoryItem is one entry in the `relevant_context.memories[]` array.
// Field shape is contractual (`contracts/mcp-injection.md`).
type MemoryItem struct {
	ID        int64     `json:"id"`
	FromAgent string    `json:"from_agent"`
	Channel   string    `json:"channel,omitempty"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	Score     float64   `json:"score"`
	MatchType string    `json:"match_type"`
	Pinned    bool      `json:"pinned"`
	Truncated bool      `json:"truncated,omitempty"`
}

// ContextPacket is the body of the `relevant_context` field. Returned
// from BuildContextPacket; rendered verbatim into the wrapped tool
// response.
type ContextPacket struct {
	Memories            []MemoryItem `json:"memories"`
	CoreMemory          string       `json:"core_memory,omitempty"`
	PacketChars         int          `json:"packet_chars"`
	PacketTokenEstimate int          `json:"packet_token_estimate"`
	RetrievalQuery      string       `json:"retrieval_query"`
	SearchMode          string       `json:"search_mode"`
}

// CoreMemoryProvider is the seam US2 plugs into. When non-nil and
// `opts.IncludeCore` is true, BuildContextPacket calls Get() and
// includes the result verbatim in the packet.
type CoreMemoryProvider interface {
	Get(ctx context.Context, ownerID, agentName string) (string, error)
}

// InjectionOpts captures the per-call configuration for
// BuildContextPacket. Sourced from messaging.MemoryConfig at wrap time.
type InjectionOpts struct {
	// BudgetTokens is the soft cap on the assembled packet. 0 disables
	// injection: BuildContextPacket returns (nil, nil).
	BudgetTokens int
	// MaxItems caps the number of memory items in the packet.
	MaxItems int
	// MinScore is the relevance floor. Items below are dropped.
	MinScore float64
	// IncludeCore enables the core-memory lookup. Only session-start
	// tools (i.e. my_status) should set this.
	IncludeCore bool
	// CoreProvider is consulted when IncludeCore is true. May be nil
	// (US2 not yet wired) — then no core memory is included.
	CoreProvider CoreMemoryProvider
	// Now is overridable for tests. Defaults to time.Now.
	Now func() time.Time
}

// EstimateTokens returns the char-based token estimate. Matches R4:
// `(chars + 3) / 4`, ceil-equivalent for positive integers.
func EstimateTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

// BuildContextPacket retrieves owner-scoped memories matching `query`,
// applies the score floor + token budget + max items cap, optionally
// resolves the per-(owner, agent) core memory blob, and returns a
// ContextPacket ready to merge into the tool response.
//
// Returns (nil, nil) when `opts.BudgetTokens == 0` (feature disabled)
// or when no memories pass the filter AND no core memory is set —
// callers omit the `relevant_context` field entirely in that case.
func BuildContextPacket(
	ctx context.Context,
	svc *Service,
	agent *agents.Agent,
	query string,
	opts InjectionOpts,
) (*ContextPacket, error) {
	if opts.BudgetTokens == 0 {
		return nil, nil
	}
	if agent == nil {
		return nil, fmt.Errorf("build context packet: nil agent")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	maxItems := opts.MaxItems
	if maxItems <= 0 {
		maxItems = 5
	}

	callerOwner := strconv.FormatInt(agent.OwnerID, 10)
	if agent.OwnerID == 0 {
		// Unowned agent: skip retrieval. We still surface a (possibly
		// non-empty) core memory if the provider returns one for the
		// empty owner — but that's an edge case the provider can decide
		// on.
		callerOwner = ""
	}

	// Over-fetch x3 to absorb owner-scope filtering + score floor drop.
	wantedLimit := maxItems * 3
	if wantedLimit < 15 {
		wantedLimit = 15
	}

	searchMode := ModeAuto

	var memories []MemoryItem
	if svc != nil && callerOwner != "" && query != "" {
		// Drive retrieval through the existing hybrid path so we inherit
		// access control + ranking. We still need a stricter owner filter
		// on top of canAgentAccessMessage because the memory pool
		// (open-brain) is broadly readable across agents within the same
		// system, and we must enforce owner isolation (SC-008).
		resp, err := svc.Search(ctx, agent.Name, SearchOptions{
			Query:         query,
			Mode:          ModeAuto,
			Limit:         wantedLimit,
			MinSimilarity: opts.MinScore,
		})
		if err != nil {
			return nil, fmt.Errorf("build context packet: search: %w", err)
		}
		if resp != nil {
			searchMode = resp.SearchMode
		}
		if resp != nil && len(resp.Results) > 0 {
			items, err := filterAndScore(ctx, svc, resp.Results, callerOwner, opts)
			if err != nil {
				return nil, err
			}
			memories = items
		}
	}

	// Apply token budget: greedy fill in descending score (results are
	// already sorted). Truncate the last admitted item to fit when it
	// would otherwise overflow.
	memories = applyTokenBudget(memories, maxItems, opts.BudgetTokens)

	// Core memory lookup (US2 hook). Provider may be nil — that's fine.
	var coreBlob string
	if opts.IncludeCore && opts.CoreProvider != nil && callerOwner != "" {
		blob, err := opts.CoreProvider.Get(ctx, callerOwner, agent.Name)
		if err == nil {
			coreBlob = blob
		} else if !errors.Is(err, sql.ErrNoRows) {
			// Surface unexpected errors so callers can log; an absent
			// core blob (typical "no rows") is not an error.
			return nil, fmt.Errorf("build context packet: core memory: %w", err)
		}
	}

	if len(memories) == 0 && coreBlob == "" {
		// Empty + no core → caller should omit the relevant_context field.
		return nil, nil
	}

	// TODO(US3-T029): pin overlay — when ListPins lands, mark and
	// always-include pinned memories regardless of the score floor.

	if memories == nil {
		memories = []MemoryItem{}
	}
	packet := &ContextPacket{
		Memories:       memories,
		CoreMemory:     coreBlob,
		RetrievalQuery: query,
		SearchMode:     searchMode,
	}
	packet.PacketChars = packetChars(packet)
	packet.PacketTokenEstimate = EstimateTokens(packet.PacketChars)
	return packet, nil
}

// filterAndScore drops non-owner messages and items below MinScore,
// then assembles MemoryItem entries in the existing RRF-sorted order.
//
// Owner of each candidate message is resolved via agents.OwnerFor on
// `from_agent`. This is the stricter filter referenced in
// `contracts/mcp-injection.md`'s cross-owner safety note.
func filterAndScore(
	ctx context.Context,
	svc *Service,
	results []*SearchResult,
	callerOwnerID string,
	opts InjectionOpts,
) ([]MemoryItem, error) {
	out := make([]MemoryItem, 0, len(results))
	for _, r := range results {
		if r == nil || r.Message == nil {
			continue
		}
		// Score selection: prefer SimilarityScore (semantic / hybrid),
		// fall back to RelevanceScore (fulltext).
		score := r.SimilarityScore
		if score == 0 {
			score = r.RelevanceScore
		}
		if opts.MinScore > 0 && score < opts.MinScore {
			continue
		}

		owner, err := agents.OwnerFor(ctx, svc.db, r.Message.FromAgent)
		if err != nil {
			// Unowned or unknown sender → exclude from injection pool.
			continue
		}
		if owner != callerOwnerID {
			continue
		}

		item := MemoryItem{
			ID:        r.Message.ID,
			FromAgent: r.Message.FromAgent,
			Body:      r.Message.Body,
			CreatedAt: r.Message.CreatedAt,
			Score:     score,
			MatchType: r.MatchType,
		}
		if r.Message.ChannelID != nil {
			if name, err := channelName(ctx, svc.db, *r.Message.ChannelID); err == nil {
				item.Channel = name
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// applyTokenBudget enforces both MaxItems and the token budget. Items
// are admitted greedily in input order (caller passes them already
// score-sorted). The first item that would overflow is truncated to
// fit; all later items are skipped.
func applyTokenBudget(items []MemoryItem, maxItems, budgetTokens int) []MemoryItem {
	if len(items) == 0 {
		return nil
	}
	if budgetTokens <= 0 {
		return nil
	}
	out := make([]MemoryItem, 0, len(items))
	used := 0
	for i, it := range items {
		if i >= maxItems {
			break
		}
		cost := EstimateTokens(itemChars(it))
		if used+cost <= budgetTokens {
			used += cost
			out = append(out, it)
			continue
		}
		// Truncate this item to whatever remains in the budget.
		remaining := budgetTokens - used
		if remaining <= 0 {
			break
		}
		// Reserve the per-item overhead in the remaining budget so that
		// post-truncate EstimateTokens(itemChars(it)) <= remaining.
		overhead := itemChars(it) - len(it.Body) // = from_agent + channel + 32
		// max chars we can place into Body so that total item tokens fit.
		maxBodyChars := remaining*4 - overhead
		if maxBodyChars <= 0 {
			break
		}
		if maxBodyChars >= len(it.Body) {
			// Whole body still fits — admit unchanged.
			used += EstimateTokens(itemChars(it))
			out = append(out, it)
			continue
		}
		truncated := it
		truncated.Body = it.Body[:maxBodyChars]
		truncated.Truncated = true
		used += EstimateTokens(itemChars(truncated))
		out = append(out, truncated)
		break
	}
	return out
}

// itemChars approximates the rendered size of one MemoryItem so the
// budget gate stays self-consistent with packetChars.
func itemChars(it MemoryItem) int {
	// Body dominates; from_agent + channel + delimiters add a small
	// per-item overhead we approximate at 32 chars.
	return len(it.Body) + len(it.FromAgent) + len(it.Channel) + 32
}

func packetChars(p *ContextPacket) int {
	total := 0
	for _, m := range p.Memories {
		total += itemChars(m)
	}
	total += len(p.CoreMemory)
	return total
}

// channelName resolves a channel ID to a name for the optional
// `MemoryItem.Channel` field. Best-effort: returns ("", err) on lookup
// failure and BuildContextPacket then omits the field entirely.
func channelName(ctx context.Context, db *sql.DB, id int64) (string, error) {
	var name string
	err := db.QueryRowContext(ctx,
		`SELECT name FROM channels WHERE id = ?`, id,
	).Scan(&name)
	if err != nil {
		return "", err
	}
	return name, nil
}
