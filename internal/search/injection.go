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

// PinProvider returns the owner's pinned message ids. Set on
// InjectionOpts via US3 wiring; when nil the overlay is skipped.
type PinProvider interface {
	ListForOwner(ctx context.Context, ownerID string) ([]int64, error)
}

// StatusProvider returns the memory_status of each id in the input
// slice. Ids that do not appear in the returned map are implicitly
// `active`. Set on InjectionOpts via US3 wiring.
type StatusProvider interface {
	Statuses(ctx context.Context, msgIDs []int64) (map[int64]MemoryStatusInfo, error)
}

// MemoryStatusInfo mirrors messaging.MemoryStatus without importing
// the messaging package (avoids a cycle). The injection retrieval
// layer needs only the Status string and the active/non-active bit.
type MemoryStatusInfo struct {
	Status string
}

// MessageLookup resolves message ids → MemoryItem fields for the pin
// overlay. The overlay needs body / from_agent / channel for pinned
// messages that did NOT come back from the search; loading them
// directly from the messages table keeps this independent of the
// search index.
type MessageLookup interface {
	LookupForInjection(ctx context.Context, ids []int64) ([]MemoryItem, error)
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
	// PinProvider, when non-nil, supplies owner-pinned message ids
	// that are spliced into the packet with Score=1.0 regardless of
	// the score floor. Status filter still drops soft_deleted /
	// superseded pins so retrieval never surfaces tombstoned facts.
	PinProvider PinProvider
	// StatusProvider, when non-nil, supplies the memory_status of
	// each candidate; results with status soft_deleted/superseded are
	// dropped (unless pinned).
	StatusProvider StatusProvider
	// MessageLookup, when non-nil, resolves pinned message ids that
	// did not surface through retrieval. When nil, only pins already
	// present in the retrieval results are highlighted.
	MessageLookup MessageLookup
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
	if svc != nil && callerOwner != "" {
		if query != "" {
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
		} else {
			// FR-009 recency fallback: no explicit query → return the N
			// most recent memory-channel messages whose author belongs to
			// the caller's owner. Bypasses the hybrid index (which has no
			// notion of "no query") and goes directly to SQL.
			items, err := recentMemoriesForOwner(ctx, svc.db, callerOwner, wantedLimit)
			if err != nil {
				return nil, fmt.Errorf("build context packet: recency: %w", err)
			}
			memories = items
			searchMode = "recent"
		}
	}

	// Apply memory_status filter (US3 T031): drop soft_deleted /
	// superseded results unless they will be pinned in the next step.
	memories = applyStatusFilter(ctx, memories, nil, opts)

	// Pin overlay (US3 T029/T031): owner-pinned message ids bypass the
	// score floor and are spliced in with Score=1.0 / Pinned=true. We
	// build the pin set up-front so the status filter knows to spare
	// them.
	pinIDs, _ := loadPinIDs(ctx, opts, callerOwner)
	if len(pinIDs) > 0 {
		memories = applyStatusFilter(ctx, memories, pinIDs, opts)
		memories = applyPinOverlay(ctx, memories, pinIDs, opts)
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

// loadPinIDs queries the configured PinProvider, if any, and returns
// the owner's pinned message ids. Returns nil on any error so that pin
// retrieval failure never breaks the wider injection path.
func loadPinIDs(ctx context.Context, opts InjectionOpts, ownerID string) ([]int64, error) {
	if opts.PinProvider == nil || ownerID == "" {
		return nil, nil
	}
	ids, err := opts.PinProvider.ListForOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// applyStatusFilter drops items whose memory_status is soft_deleted or
// superseded. `sparedIDs` is the set of message ids that bypass the
// filter (pinned ids). When opts.StatusProvider is nil this is a no-op.
func applyStatusFilter(ctx context.Context, items []MemoryItem, sparedIDs []int64, opts InjectionOpts) []MemoryItem {
	if opts.StatusProvider == nil || len(items) == 0 {
		return items
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	statuses, err := opts.StatusProvider.Statuses(ctx, ids)
	if err != nil {
		return items
	}
	spared := map[int64]struct{}{}
	for _, id := range sparedIDs {
		spared[id] = struct{}{}
	}
	out := make([]MemoryItem, 0, len(items))
	for _, it := range items {
		st, ok := statuses[it.ID]
		if !ok || st.Status == "" || st.Status == "active" {
			out = append(out, it)
			continue
		}
		if _, isPinned := spared[it.ID]; isPinned {
			out = append(out, it)
			continue
		}
		// Drop soft_deleted / superseded non-pinned.
	}
	return out
}

// applyPinOverlay marks any item already present and whose id is pinned
// as Pinned=true / Score=1.0; pinned ids that are NOT in the input set
// are fetched via MessageLookup (if configured) and prepended.
func applyPinOverlay(ctx context.Context, items []MemoryItem, pinIDs []int64, opts InjectionOpts) []MemoryItem {
	if len(pinIDs) == 0 {
		return items
	}
	pinSet := map[int64]struct{}{}
	for _, id := range pinIDs {
		pinSet[id] = struct{}{}
	}
	// Mark items already present.
	present := map[int64]struct{}{}
	for i := range items {
		if _, ok := pinSet[items[i].ID]; ok {
			items[i].Pinned = true
			items[i].Score = 1.0
		}
		present[items[i].ID] = struct{}{}
	}
	// Fetch missing pinned ids via MessageLookup (if any).
	var missing []int64
	for id := range pinSet {
		if _, ok := present[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 && opts.MessageLookup != nil {
		extra, err := opts.MessageLookup.LookupForInjection(ctx, missing)
		if err == nil {
			// Status filter on the freshly-loaded pinned messages: if
			// the provider says they are soft_deleted / superseded, do
			// not surface them either, even though pinned.
			if opts.StatusProvider != nil {
				statuses, _ := opts.StatusProvider.Statuses(ctx, missing)
				filtered := extra[:0]
				for _, m := range extra {
					if st, ok := statuses[m.ID]; ok && st.Status != "" && st.Status != "active" {
						continue
					}
					filtered = append(filtered, m)
				}
				extra = filtered
			}
			// Prepend in stable id order (newest first by convention —
			// pins are sorted DESC by pinned_at in the store).
			for i := range extra {
				extra[i].Pinned = true
				extra[i].Score = 1.0
				extra[i].MatchType = "pinned"
			}
			items = append(extra, items...)
		}
	}
	return items
}

// recentMemoriesForOwner returns the N most recent messages on memory
// channels (open-brain, reflections-*, or any channel flagged
// is_memory=true) whose author is owned by `ownerID`. Used when the
// injection layer has no explicit retrieval query (e.g. my_status).
//
// Recency is approximated as "ORDER BY messages.id DESC" — id is
// monotonically increasing per SQLite INSERT and matches created_at
// ordering on this schema.
func recentMemoriesForOwner(ctx context.Context, db *sql.DB, ownerID string, limit int) ([]MemoryItem, error) {
	if db == nil || ownerID == "" || limit <= 0 {
		return nil, nil
	}
	const q = `
		SELECT m.id, m.from_agent, COALESCE(c.name,''), m.body, m.created_at
		FROM messages m
		JOIN agents a ON a.name = m.from_agent
		LEFT JOIN channels c ON c.id = m.channel_id
		WHERE CAST(a.owner_id AS TEXT) = ?
		  AND m.channel_id IS NOT NULL
		  AND c.name IN ('open-brain')
		  AND m.body IS NOT NULL AND m.body != ''
		ORDER BY m.id DESC
		LIMIT ?`
	rows, err := db.QueryContext(ctx, q, ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryItem
	for rows.Next() {
		var it MemoryItem
		if err := rows.Scan(&it.ID, &it.FromAgent, &it.Channel, &it.Body, &it.CreatedAt); err != nil {
			return nil, err
		}
		it.Score = 1.0
		it.MatchType = "recent"
		out = append(out, it)
	}
	return out, rows.Err()
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
