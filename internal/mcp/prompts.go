package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/trace"
)

// PromptRegistrar registers MCP prompts on the server.
type PromptRegistrar struct {
	db             *sql.DB
	agentService   *agents.AgentService
	channelService *channels.Service
	traceStore     trace.TraceStore
	logger         *slog.Logger
}

// NewPromptRegistrar creates a new prompt registrar.
func NewPromptRegistrar(
	db *sql.DB,
	agentService *agents.AgentService,
	channelService *channels.Service,
	traceStore trace.TraceStore,
) *PromptRegistrar {
	return &PromptRegistrar{
		db:             db,
		agentService:   agentService,
		channelService: channelService,
		traceStore:     traceStore,
		logger:         slog.Default().With("component", "mcp-prompts"),
	}
}

// RegisterAllOnServer registers all 4 MCP prompts on an mcp-go MCPServer.
func (p *PromptRegistrar) RegisterAllOnServer(s *server.MCPServer) {
	s.AddPrompt(p.dailyDigestPrompt(), p.handleDailyDigest)
	s.AddPrompt(p.agentHealthCheckPrompt(), p.handleAgentHealthCheck)
	s.AddPrompt(p.channelOverviewPrompt(), p.handleChannelOverview)
	s.AddPrompt(p.debugAgentPrompt(), p.handleDebugAgent)

	p.logger.Info("MCP prompts registered", "count", 4)
}

// --- Prompt Definitions ---

func (p *PromptRegistrar) dailyDigestPrompt() mcplib.Prompt {
	return mcplib.NewPrompt("daily-digest",
		mcplib.WithPromptDescription("Summary of SynapBus activity in the last 24 hours: message counts, active agents, busiest channels, and high-priority messages."),
	)
}

func (p *PromptRegistrar) agentHealthCheckPrompt() mcplib.Prompt {
	return mcplib.NewPrompt("agent-health-check",
		mcplib.WithPromptDescription("Status overview of all registered agents: name, type, status, last activity, and pending DM count. Flags agents needing attention."),
	)
}

func (p *PromptRegistrar) channelOverviewPrompt() mcplib.Prompt {
	return mcplib.NewPrompt("channel-overview",
		mcplib.WithPromptDescription("Overview of all channels: name, type, member count, and recent message activity."),
	)
}

func (p *PromptRegistrar) debugAgentPrompt() mcplib.Prompt {
	return mcplib.NewPrompt("debug-agent",
		mcplib.WithPromptDescription("Detailed debug information for a specific agent: identity, recent traces, pending messages, and errors."),
		mcplib.WithArgument("agent_name",
			mcplib.ArgumentDescription("Name of the agent to debug"),
			mcplib.RequiredArgument(),
		),
	)
}

// --- Prompt Handlers ---

func (p *PromptRegistrar) handleDailyDigest(ctx context.Context, req mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
	since := time.Now().Add(-24 * time.Hour)

	var md strings.Builder
	md.WriteString("# SynapBus Daily Digest\n\n")
	md.WriteString(fmt.Sprintf("*Period: %s to %s*\n\n", since.Format(time.RFC3339), time.Now().Format(time.RFC3339)))

	// Total messages sent in last 24h
	var totalMessages int
	err := p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE created_at >= ?`, since,
	).Scan(&totalMessages)
	if err != nil {
		md.WriteString(fmt.Sprintf("Error querying messages: %s\n\n", err))
	} else {
		md.WriteString(fmt.Sprintf("## Messages\n\n**Total messages sent**: %d\n\n", totalMessages))
	}

	// Active agents (those who sent messages in last 24h)
	rows, err := p.db.QueryContext(ctx,
		`SELECT DISTINCT from_agent FROM messages WHERE created_at >= ? AND from_agent != '' ORDER BY from_agent`,
		since,
	)
	if err != nil {
		md.WriteString(fmt.Sprintf("Error querying active agents: %s\n\n", err))
	} else {
		var activeAgents []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				activeAgents = append(activeAgents, name)
			}
		}
		rows.Close()

		md.WriteString("## Active Agents\n\n")
		if len(activeAgents) == 0 {
			md.WriteString("No agents were active in the last 24 hours.\n\n")
		} else {
			md.WriteString(fmt.Sprintf("**%d agents** sent messages: %s\n\n", len(activeAgents), strings.Join(activeAgents, ", ")))
		}
	}

	// Top 3 busiest channels
	chanRows, err := p.db.QueryContext(ctx,
		`SELECT c.name, COUNT(m.id) as msg_count
		 FROM messages m
		 JOIN channels c ON m.channel_id = c.id
		 WHERE m.created_at >= ? AND m.channel_id IS NOT NULL
		 GROUP BY c.name
		 ORDER BY msg_count DESC
		 LIMIT 3`,
		since,
	)
	if err != nil {
		md.WriteString(fmt.Sprintf("Error querying channels: %s\n\n", err))
	} else {
		md.WriteString("## Top Channels\n\n")
		md.WriteString("| Channel | Messages |\n")
		md.WriteString("|---------|----------|\n")
		found := false
		for chanRows.Next() {
			var chName string
			var msgCount int
			if err := chanRows.Scan(&chName, &msgCount); err == nil {
				md.WriteString(fmt.Sprintf("| #%s | %d |\n", chName, msgCount))
				found = true
			}
		}
		chanRows.Close()
		if !found {
			md.WriteString("| (no channel activity) | - |\n")
		}
		md.WriteString("\n")
	}

	// High-priority messages (priority >= 7)
	highRows, err := p.db.QueryContext(ctx,
		`SELECT id, from_agent, COALESCE(to_agent, ''), body, priority, created_at
		 FROM messages
		 WHERE created_at >= ? AND priority >= 7
		 ORDER BY priority DESC, created_at DESC
		 LIMIT 20`,
		since,
	)
	if err != nil {
		md.WriteString(fmt.Sprintf("Error querying high-priority messages: %s\n\n", err))
	} else {
		md.WriteString("## High-Priority Messages (>= 7)\n\n")
		var highPriorityFound bool
		for highRows.Next() {
			var id int64
			var from, to, body string
			var priority int
			var createdAt time.Time
			if err := highRows.Scan(&id, &from, &to, &body, &priority, &createdAt); err == nil {
				if !highPriorityFound {
					md.WriteString("| ID | From | To | Priority | Time | Body (truncated) |\n")
					md.WriteString("|----|------|----|----------|------|------------------|\n")
					highPriorityFound = true
				}
				if len(body) > 80 {
					body = body[:80] + "..."
				}
				// Escape pipe characters in body for markdown table
				body = strings.ReplaceAll(body, "|", "\\|")
				body = strings.ReplaceAll(body, "\n", " ")
				target := to
				if target == "" {
					target = "(channel)"
				}
				md.WriteString(fmt.Sprintf("| %d | %s | %s | %d | %s | %s |\n",
					id, from, target, priority, createdAt.Format("15:04"), body))
			}
		}
		highRows.Close()
		if !highPriorityFound {
			md.WriteString("No high-priority messages in the last 24 hours.\n")
		}
		md.WriteString("\n")
	}

	return &mcplib.GetPromptResult{
		Description: "SynapBus activity summary for the last 24 hours",
		Messages: []mcplib.PromptMessage{
			{
				Role:    mcplib.RoleUser,
				Content: mcplib.NewTextContent(md.String()),
			},
		},
	}, nil
}

func (p *PromptRegistrar) handleAgentHealthCheck(ctx context.Context, req mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
	var md strings.Builder
	md.WriteString("# Agent Health Check\n\n")

	// Get all active agents
	agentList, err := p.agentService.DiscoverAgents(ctx, "")
	if err != nil {
		return &mcplib.GetPromptResult{
			Description: "Agent health check",
			Messages: []mcplib.PromptMessage{
				{
					Role:    mcplib.RoleUser,
					Content: mcplib.NewTextContent(fmt.Sprintf("Error listing agents: %s", err)),
				},
			},
		}, nil
	}

	md.WriteString("| Agent | Type | Status | Last Activity | Pending DMs | Notes |\n")
	md.WriteString("|-------|------|--------|---------------|-------------|-------|\n")

	for _, agent := range agentList {
		if agent.Name == "system" {
			continue
		}

		// Get last activity from traces
		lastActivity := "unknown"
		if p.traceStore != nil {
			traces, err := p.traceStore.GetTraces(ctx, agent.Name, 1)
			if err == nil && len(traces) > 0 {
				lastActivity = traces[0].CreatedAt.Format("2006-01-02 15:04")
			}
		}

		// Get pending DM count
		var pendingDMs int
		err := p.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM messages WHERE to_agent = ? AND status = 'pending'`,
			agent.Name,
		).Scan(&pendingDMs)
		if err != nil {
			pendingDMs = -1
		}

		notes := ""
		if pendingDMs > 10 {
			notes = "**NEEDS ATTENTION**"
		}

		pendingStr := fmt.Sprintf("%d", pendingDMs)
		if pendingDMs < 0 {
			pendingStr = "error"
		}

		md.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
			agent.Name, agent.Type, agent.Status, lastActivity, pendingStr, notes))
	}

	md.WriteString(fmt.Sprintf("\n*Total agents: %d*\n", len(agentList)-1)) // exclude system

	return &mcplib.GetPromptResult{
		Description: "Status of all registered agents",
		Messages: []mcplib.PromptMessage{
			{
				Role:    mcplib.RoleUser,
				Content: mcplib.NewTextContent(md.String()),
			},
		},
	}, nil
}

func (p *PromptRegistrar) handleChannelOverview(ctx context.Context, req mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
	since := time.Now().Add(-24 * time.Hour)

	var md strings.Builder
	md.WriteString("# Channel Overview\n\n")

	// Query all channels with member counts and recent message counts
	rows, err := p.db.QueryContext(ctx,
		`SELECT c.id, c.name, c.type, c.is_private,
		        (SELECT COUNT(*) FROM channel_members cm WHERE cm.channel_id = c.id) as member_count,
		        (SELECT COUNT(*) FROM messages m WHERE m.channel_id = c.id AND m.created_at >= ?) as msg_count_24h
		 FROM channels c
		 ORDER BY c.name`,
		since,
	)
	if err != nil {
		return &mcplib.GetPromptResult{
			Description: "Channel overview",
			Messages: []mcplib.PromptMessage{
				{
					Role:    mcplib.RoleUser,
					Content: mcplib.NewTextContent(fmt.Sprintf("Error querying channels: %s", err)),
				},
			},
		}, nil
	}
	defer rows.Close()

	md.WriteString("| Channel | Type | Private | Members | Messages (24h) |\n")
	md.WriteString("|---------|------|---------|---------|----------------|\n")

	count := 0
	for rows.Next() {
		var id int64
		var name, chType string
		var isPrivate int
		var memberCount, msgCount int

		if err := rows.Scan(&id, &name, &chType, &isPrivate, &memberCount, &msgCount); err != nil {
			continue
		}

		privateStr := "no"
		if isPrivate != 0 {
			privateStr = "yes"
		}

		md.WriteString(fmt.Sprintf("| #%s | %s | %s | %d | %d |\n",
			name, chType, privateStr, memberCount, msgCount))
		count++
	}

	if count == 0 {
		md.WriteString("| (no channels) | - | - | - | - |\n")
	}

	md.WriteString(fmt.Sprintf("\n*Total channels: %d*\n", count))

	return &mcplib.GetPromptResult{
		Description: "Overview of all channels",
		Messages: []mcplib.PromptMessage{
			{
				Role:    mcplib.RoleUser,
				Content: mcplib.NewTextContent(md.String()),
			},
		},
	}, nil
}

func (p *PromptRegistrar) handleDebugAgent(ctx context.Context, req mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
	agentName := req.Params.Arguments["agent_name"]
	if agentName == "" {
		return &mcplib.GetPromptResult{
			Description: "Debug agent",
			Messages: []mcplib.PromptMessage{
				{
					Role:    mcplib.RoleUser,
					Content: mcplib.NewTextContent("Error: agent_name argument is required"),
				},
			},
		}, nil
	}

	var md strings.Builder
	md.WriteString(fmt.Sprintf("# Debug: Agent `%s`\n\n", agentName))

	// Agent details
	agent, err := p.agentService.GetAgent(ctx, agentName)
	if err != nil {
		return &mcplib.GetPromptResult{
			Description: fmt.Sprintf("Debug info for agent %s", agentName),
			Messages: []mcplib.PromptMessage{
				{
					Role:    mcplib.RoleUser,
					Content: mcplib.NewTextContent(fmt.Sprintf("Error: agent not found: %s", err)),
				},
			},
		}, nil
	}

	// Resolve owner name
	ownerName := "unknown"
	if p.db != nil {
		var username sql.NullString
		_ = p.db.QueryRowContext(ctx,
			`SELECT username FROM users WHERE id = ?`, agent.OwnerID,
		).Scan(&username)
		if username.Valid {
			ownerName = username.String
		}
	}

	md.WriteString("## Identity\n\n")
	md.WriteString(fmt.Sprintf("| Field | Value |\n"))
	md.WriteString(fmt.Sprintf("|-------|-------|\n"))
	md.WriteString(fmt.Sprintf("| Name | %s |\n", agent.Name))
	md.WriteString(fmt.Sprintf("| Display Name | %s |\n", agent.DisplayName))
	md.WriteString(fmt.Sprintf("| Type | %s |\n", agent.Type))
	md.WriteString(fmt.Sprintf("| Status | %s |\n", agent.Status))
	md.WriteString(fmt.Sprintf("| Owner | %s (ID: %d) |\n", ownerName, agent.OwnerID))
	md.WriteString(fmt.Sprintf("| Capabilities | `%s` |\n", string(agent.Capabilities)))
	md.WriteString(fmt.Sprintf("| Created | %s |\n", agent.CreatedAt.Format(time.RFC3339)))
	md.WriteString("\n")

	// Pending messages count
	var pendingCount int
	err = p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE to_agent = ? AND status = 'pending'`,
		agentName,
	).Scan(&pendingCount)
	if err != nil {
		md.WriteString(fmt.Sprintf("Error querying pending messages: %s\n\n", err))
	} else {
		md.WriteString(fmt.Sprintf("## Pending Messages\n\n**Count**: %d\n\n", pendingCount))
	}

	// Recent traces (last 10)
	if p.traceStore != nil {
		traces, err := p.traceStore.GetTraces(ctx, agentName, 10)
		if err != nil {
			md.WriteString(fmt.Sprintf("Error querying traces: %s\n\n", err))
		} else {
			md.WriteString("## Recent Traces (last 10)\n\n")
			if len(traces) == 0 {
				md.WriteString("No traces found.\n\n")
			} else {
				md.WriteString("| Time | Action | Details | Error |\n")
				md.WriteString("|------|--------|---------|-------|\n")
				for _, t := range traces {
					details := t.Details
					if len(details) > 80 {
						details = details[:80] + "..."
					}
					details = strings.ReplaceAll(details, "|", "\\|")
					details = strings.ReplaceAll(details, "\n", " ")

					errStr := ""
					if t.Error.Valid {
						errStr = t.Error.String
						if len(errStr) > 60 {
							errStr = errStr[:60] + "..."
						}
						errStr = strings.ReplaceAll(errStr, "|", "\\|")
					}

					md.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
						t.CreatedAt.Format("15:04:05"), t.Action, details, errStr))
				}
				md.WriteString("\n")
			}
		}

		// Recent errors from traces
		errorTraces, err := p.traceStore.GetTraces(ctx, agentName, 50)
		if err == nil {
			md.WriteString("## Recent Errors\n\n")
			errorFound := false
			for _, t := range errorTraces {
				if t.Error.Valid && t.Error.String != "" {
					if !errorFound {
						md.WriteString("| Time | Action | Error |\n")
						md.WriteString("|------|--------|-------|\n")
						errorFound = true
					}
					errStr := t.Error.String
					if len(errStr) > 100 {
						errStr = errStr[:100] + "..."
					}
					errStr = strings.ReplaceAll(errStr, "|", "\\|")
					errStr = strings.ReplaceAll(errStr, "\n", " ")
					md.WriteString(fmt.Sprintf("| %s | %s | %s |\n",
						t.CreatedAt.Format("2006-01-02 15:04:05"), t.Action, errStr))
				}
			}
			if !errorFound {
				md.WriteString("No recent errors found.\n")
			}
			md.WriteString("\n")
		}
	} else {
		md.WriteString("## Traces\n\n*Trace store not available*\n\n")
	}

	return &mcplib.GetPromptResult{
		Description: fmt.Sprintf("Debug information for agent %s", agentName),
		Messages: []mcplib.PromptMessage{
			{
				Role:    mcplib.RoleUser,
				Content: mcplib.NewTextContent(md.String()),
			},
		},
	}, nil
}
