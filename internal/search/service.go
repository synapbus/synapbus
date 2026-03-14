package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/search/embedding"
)

// SearchMode constants.
const (
	ModeAuto     = "auto"
	ModeSemantic = "semantic"
	ModeFulltext = "fulltext"
)

// SearchOptions for the unified search service.
type SearchOptions struct {
	Query      string
	Mode       string // "auto", "semantic", "fulltext"
	Limit      int
	ChannelID  *int64
	FromAgent  string
	MinPriority int
	After      *time.Time
	Before     *time.Time
}

// SearchResult represents a single search result.
type SearchResult struct {
	Message         *messaging.Message `json:"message"`
	SimilarityScore float64            `json:"similarity_score,omitempty"`
	RelevanceScore  float64            `json:"relevance_score,omitempty"`
	MatchType       string             `json:"match_type"` // "semantic" or "fulltext"
}

// SearchResponse is the response from the search service.
type SearchResponse struct {
	Results      []*SearchResult `json:"results"`
	SearchMode   string          `json:"search_mode"`
	TotalResults int             `json:"total_results"`
	Warning      string          `json:"warning,omitempty"`
}

// Service provides unified search combining semantic and full-text search.
type Service struct {
	db         *sql.DB
	provider   embedding.EmbeddingProvider // may be nil
	index      *VectorIndex               // may be nil
	msgService *messaging.MessagingService
	logger     *slog.Logger
}

// NewService creates a new search service.
func NewService(
	db *sql.DB,
	provider embedding.EmbeddingProvider,
	index *VectorIndex,
	msgService *messaging.MessagingService,
) *Service {
	return &Service{
		db:         db,
		provider:   provider,
		index:      index,
		msgService: msgService,
		logger:     slog.Default().With("component", "search"),
	}
}

// Search performs a search with the given options, respecting agent access control.
func (s *Service) Search(ctx context.Context, agentName string, opts SearchOptions) (*SearchResponse, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	mode := opts.Mode
	if mode == "" {
		mode = ModeAuto
	}

	// Determine effective search mode
	switch mode {
	case ModeAuto:
		if s.provider != nil && s.index != nil && s.index.Len() > 0 {
			return s.semanticSearch(ctx, agentName, opts, limit)
		}
		return s.fulltextSearch(ctx, agentName, opts, limit)

	case ModeSemantic:
		if s.provider == nil || s.index == nil {
			return nil, fmt.Errorf("semantic search unavailable: no embedding provider configured")
		}
		resp, err := s.semanticSearch(ctx, agentName, opts, limit)
		if err != nil {
			// Fall back to full-text on semantic error
			s.logger.Warn("semantic search failed, falling back to fulltext", "error", err)
			resp, ftErr := s.fulltextSearch(ctx, agentName, opts, limit)
			if ftErr != nil {
				return nil, fmt.Errorf("semantic search failed: %w; fulltext fallback also failed: %w", err, ftErr)
			}
			resp.Warning = fmt.Sprintf("semantic search failed: %s, using fulltext fallback", err)
			return resp, nil
		}
		return resp, nil

	case ModeFulltext:
		return s.fulltextSearch(ctx, agentName, opts, limit)

	default:
		return nil, fmt.Errorf("unknown search mode: %q", mode)
	}
}

// semanticSearch performs vector similarity search.
func (s *Service) semanticSearch(ctx context.Context, agentName string, opts SearchOptions, limit int) (*SearchResponse, error) {
	if opts.Query == "" {
		return s.fulltextSearch(ctx, agentName, opts, limit)
	}

	// Embed the query
	queryVec, err := s.provider.Embed(ctx, opts.Query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Over-fetch to account for access control filtering
	overfetch := limit * 5
	if overfetch < 50 {
		overfetch = 50
	}

	results, err := s.index.Search(queryVec, overfetch)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	if len(results) == 0 {
		// No vectors in index, fall back to FTS
		resp, err := s.fulltextSearch(ctx, agentName, opts, limit)
		if err != nil {
			return nil, err
		}
		resp.Warning = "no vectors in index, using fulltext"
		return resp, nil
	}

	// Fetch messages and apply access control + filters
	var searchResults []*SearchResult
	for _, vr := range results {
		msg, err := s.getMessageByID(ctx, vr.ID)
		if err != nil {
			continue // message may have been deleted
		}

		// Access control: only messages this agent can see
		if !s.canAgentAccessMessage(ctx, agentName, msg) {
			continue
		}

		// Apply filters
		if !s.matchesFilters(msg, opts) {
			continue
		}

		similarity := float64(1.0 - vr.Distance) // cosine distance -> similarity
		if similarity < 0 {
			similarity = 0
		}

		searchResults = append(searchResults, &SearchResult{
			Message:         msg,
			SimilarityScore: similarity,
			MatchType:       ModeSemantic,
		})

		if len(searchResults) >= limit {
			break
		}
	}

	return &SearchResponse{
		Results:      searchResults,
		SearchMode:   ModeSemantic,
		TotalResults: len(searchResults),
	}, nil
}

// fulltextSearch performs FTS5 full-text search with access control.
func (s *Service) fulltextSearch(ctx context.Context, agentName string, opts SearchOptions, limit int) (*SearchResponse, error) {
	// Use the messaging service's SearchMessages which already handles access control
	msgOpts := messaging.SearchOptions{
		FromAgent:   opts.FromAgent,
		MinPriority: opts.MinPriority,
		Limit:       limit,
	}
	if opts.ChannelID != nil {
		msgOpts.ChannelID = opts.ChannelID
	}

	messages, err := s.msgService.SearchMessages(ctx, agentName, opts.Query, msgOpts)
	if err != nil {
		return nil, fmt.Errorf("fulltext search: %w", err)
	}

	results := make([]*SearchResult, len(messages))
	for i, msg := range messages {
		results[i] = &SearchResult{
			Message:        msg,
			RelevanceScore: 1.0 - float64(i)*0.05, // simple rank-based score
			MatchType:      ModeFulltext,
		}
	}

	return &SearchResponse{
		Results:      results,
		SearchMode:   ModeFulltext,
		TotalResults: len(results),
	}, nil
}

// getMessageByID fetches a message by ID from the database.
func (s *Service) getMessageByID(ctx context.Context, id int64) (*messaging.Message, error) {
	var msg messaging.Message
	var toAgent, claimedBy sql.NullString
	var channelID sql.NullInt64
	var claimedAt sql.NullTime
	var metadata string

	err := s.db.QueryRowContext(ctx,
		`SELECT id, conversation_id, from_agent, to_agent, channel_id,
		        body, priority, status, metadata, claimed_by, claimed_at,
		        created_at, updated_at
		 FROM messages WHERE id = ?`, id,
	).Scan(
		&msg.ID, &msg.ConversationID, &msg.FromAgent, &toAgent, &channelID,
		&msg.Body, &msg.Priority, &msg.Status, &metadata, &claimedBy, &claimedAt,
		&msg.CreatedAt, &msg.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if toAgent.Valid {
		msg.ToAgent = toAgent.String
	}
	if channelID.Valid {
		msg.ChannelID = &channelID.Int64
	}
	if claimedBy.Valid {
		msg.ClaimedBy = claimedBy.String
	}
	if claimedAt.Valid {
		msg.ClaimedAt = &claimedAt.Time
	}
	msg.Metadata = json.RawMessage(metadata)

	return &msg, nil
}

// canAgentAccessMessage checks if the agent has access to the message.
func (s *Service) canAgentAccessMessage(ctx context.Context, agentName string, msg *messaging.Message) bool {
	// Direct messages: agent must be sender or recipient
	if msg.ToAgent != "" {
		if msg.FromAgent == agentName || msg.ToAgent == agentName {
			return true
		}
		return false
	}

	// Channel messages: agent must be a member of the channel
	if msg.ChannelID != nil {
		var count int
		err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM channel_members WHERE channel_id = ? AND agent_name = ?`,
			*msg.ChannelID, agentName,
		).Scan(&count)
		if err != nil {
			return false
		}
		return count > 0
	}

	// Messages from or to the agent in conversations
	if msg.FromAgent == agentName {
		return true
	}

	return false
}

// matchesFilters checks if a message matches the given filter options.
func (s *Service) matchesFilters(msg *messaging.Message, opts SearchOptions) bool {
	if opts.ChannelID != nil && (msg.ChannelID == nil || *msg.ChannelID != *opts.ChannelID) {
		return false
	}
	if opts.FromAgent != "" && msg.FromAgent != opts.FromAgent {
		return false
	}
	if opts.MinPriority > 0 && msg.Priority < opts.MinPriority {
		return false
	}
	if opts.After != nil && msg.CreatedAt.Before(*opts.After) {
		return false
	}
	if opts.Before != nil && msg.CreatedAt.After(*opts.Before) {
		return false
	}
	return true
}

// HasSemanticSearch returns true if semantic search is available.
func (s *Service) HasSemanticSearch() bool {
	return s.provider != nil && s.index != nil
}

// IndexSize returns the number of vectors in the index.
func (s *Service) IndexSize() int {
	if s.index == nil {
		return 0
	}
	return s.index.Len()
}

// ProviderName returns the name of the configured provider, or empty string.
func (s *Service) ProviderName() string {
	if s.provider == nil {
		return ""
	}
	return s.provider.Name()
}

// SearchMessagesCompat provides a compatibility method that returns []*messaging.Message
// for use by the existing MCP tool handler when semantic search is not requested.
func (s *Service) SearchMessagesCompat(ctx context.Context, agentName, query string, opts messaging.SearchOptions) ([]*messaging.Message, string, error) {
	searchOpts := SearchOptions{
		Query:       query,
		Mode:        ModeAuto,
		Limit:       opts.Limit,
		FromAgent:   opts.FromAgent,
		MinPriority: opts.MinPriority,
		ChannelID:   opts.ChannelID,
	}

	resp, err := s.Search(ctx, agentName, searchOpts)
	if err != nil {
		return nil, "", err
	}

	messages := make([]*messaging.Message, len(resp.Results))
	for i, r := range resp.Results {
		messages[i] = r.Message
	}

	// Clean up query for search mode display
	searchMode := resp.SearchMode
	if strings.TrimSpace(query) == "" {
		searchMode = ModeFulltext
	}

	return messages, searchMode, nil
}
