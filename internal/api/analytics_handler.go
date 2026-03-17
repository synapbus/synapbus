package api

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
)

// AnalyticsHandler serves analytics endpoints for the Web UI dashboard.
type AnalyticsHandler struct {
	db             *sql.DB
	agentService   *agents.AgentService
	channelService *channels.Service
	logger         *slog.Logger
}

// NewAnalyticsHandler creates a new analytics handler.
func NewAnalyticsHandler(db *sql.DB, agentService *agents.AgentService, channelService *channels.Service) *AnalyticsHandler {
	return &AnalyticsHandler{
		db:             db,
		agentService:   agentService,
		channelService: channelService,
		logger:         slog.Default().With("component", "api.analytics"),
	}
}

// parseSpan parses a span parameter and returns the cutoff time and strftime format string.
func parseSpan(span string) (time.Time, string, error) {
	now := time.Now().UTC()
	switch span {
	case "1h":
		return now.Add(-1 * time.Hour), "%Y-%m-%d %H:%M", nil
	case "4h":
		return now.Add(-4 * time.Hour), "%Y-%m-%d %H:%M", nil
	case "24h":
		return now.Add(-24 * time.Hour), "%Y-%m-%d %H:00", nil
	case "7d":
		return now.Add(-7 * 24 * time.Hour), "%Y-%m-%d", nil
	case "30d":
		return now.Add(-30 * 24 * time.Hour), "%Y-%m-%d", nil
	default:
		return time.Time{}, "", fmt.Errorf("invalid span: %s (valid: 1h, 4h, 24h, 7d, 30d)", span)
	}
}

// Timeline handles GET /api/analytics/timeline?span=24h.
func (h *AnalyticsHandler) Timeline(w http.ResponseWriter, r *http.Request) {
	span := r.URL.Query().Get("span")
	if span == "" {
		span = "24h"
	}

	cutoff, strftimeFmt, err := parseSpan(span)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_span", err.Error()))
		return
	}

	// For 1h and 4h spans, bucket by 5-minute intervals using SQL expression
	var query string
	if span == "1h" || span == "4h" {
		// Round minutes down to nearest 5 using printf
		query = `SELECT strftime('%Y-%m-%d %H:', created_at) || printf('%02d', (CAST(strftime('%M', created_at) AS INTEGER) / 5) * 5) AS bucket, COUNT(*) AS count FROM messages WHERE created_at >= ? GROUP BY bucket ORDER BY bucket`
	} else {
		query = `SELECT strftime(?, created_at) AS bucket, COUNT(*) AS count FROM messages WHERE created_at >= ? GROUP BY bucket ORDER BY bucket`
	}

	type bucket struct {
		Time  string `json:"time"`
		Count int    `json:"count"`
	}

	var buckets []bucket
	var total int

	cutoffStr := cutoff.Format("2006-01-02 15:04:05")

	var rows *sql.Rows
	if span == "1h" || span == "4h" {
		rows, err = h.db.QueryContext(r.Context(), query, cutoffStr)
	} else {
		rows, err = h.db.QueryContext(r.Context(), query, strftimeFmt, cutoffStr)
	}
	if err != nil {
		h.logger.Error("timeline query failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to query timeline"))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var b bucket
		if err := rows.Scan(&b.Time, &b.Count); err != nil {
			h.logger.Error("timeline scan failed", "error", err)
			continue
		}
		buckets = append(buckets, b)
		total += b.Count
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("timeline rows error", "error", err)
	}

	if buckets == nil {
		buckets = []bucket{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"span":    span,
		"buckets": buckets,
		"total":   total,
	})
}

// TopAgents handles GET /api/analytics/top-agents?span=24h&limit=5.
func (h *AnalyticsHandler) TopAgents(w http.ResponseWriter, r *http.Request) {
	span := r.URL.Query().Get("span")
	if span == "" {
		span = "24h"
	}

	cutoff, _, err := parseSpan(span)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_span", err.Error()))
		return
	}

	limit := 5
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 100 {
				limit = 100
			}
		}
	}

	cutoffStr := cutoff.Format("2006-01-02 15:04:05")

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT from_agent, COUNT(*) as count FROM messages WHERE created_at >= ? AND from_agent != '' GROUP BY from_agent ORDER BY count DESC LIMIT ?`,
		cutoffStr, limit,
	)
	if err != nil {
		h.logger.Error("top-agents query failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to query top agents"))
		return
	}
	defer rows.Close()

	type agentStat struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		Count       int    `json:"count"`
	}

	var agentStats []agentStat
	for rows.Next() {
		var s agentStat
		if err := rows.Scan(&s.Name, &s.Count); err != nil {
			h.logger.Error("top-agents scan failed", "error", err)
			continue
		}

		// Look up display name from agent service
		if h.agentService != nil {
			if agent, err := h.agentService.GetAgent(r.Context(), s.Name); err == nil {
				s.DisplayName = agent.DisplayName
			}
		}
		if s.DisplayName == "" {
			s.DisplayName = s.Name
		}

		agentStats = append(agentStats, s)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("top-agents rows error", "error", err)
	}

	if agentStats == nil {
		agentStats = []agentStat{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"span":   span,
		"agents": agentStats,
	})
}

// TopChannels handles GET /api/analytics/top-channels?span=24h&limit=5.
func (h *AnalyticsHandler) TopChannels(w http.ResponseWriter, r *http.Request) {
	span := r.URL.Query().Get("span")
	if span == "" {
		span = "24h"
	}

	cutoff, _, err := parseSpan(span)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_span", err.Error()))
		return
	}

	limit := 5
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 100 {
				limit = 100
			}
		}
	}

	cutoffStr := cutoff.Format("2006-01-02 15:04:05")

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT c.name, COUNT(*) as count FROM messages m JOIN channels c ON m.channel_id = c.id WHERE m.created_at >= ? AND m.channel_id IS NOT NULL GROUP BY c.name ORDER BY count DESC LIMIT ?`,
		cutoffStr, limit,
	)
	if err != nil {
		h.logger.Error("top-channels query failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to query top channels"))
		return
	}
	defer rows.Close()

	type channelStat struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	var channelStats []channelStat
	for rows.Next() {
		var s channelStat
		if err := rows.Scan(&s.Name, &s.Count); err != nil {
			h.logger.Error("top-channels scan failed", "error", err)
			continue
		}
		channelStats = append(channelStats, s)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("top-channels rows error", "error", err)
	}

	if channelStats == nil {
		channelStats = []channelStat{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"span":     span,
		"channels": channelStats,
	})
}

// Summary handles GET /api/analytics/summary.
func (h *AnalyticsHandler) Summary(w http.ResponseWriter, r *http.Request) {
	var totalAgents, totalChannels, totalMessages int

	if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM agents WHERE status = 'active'`).Scan(&totalAgents); err != nil {
		h.logger.Error("summary agents count failed", "error", err)
	}

	if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM channels`).Scan(&totalChannels); err != nil {
		h.logger.Error("summary channels count failed", "error", err)
	}

	if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM messages`).Scan(&totalMessages); err != nil {
		h.logger.Error("summary messages count failed", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_agents":   totalAgents,
		"total_channels": totalChannels,
		"total_messages": totalMessages,
	})
}
