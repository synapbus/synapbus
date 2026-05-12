// MCP memory-consolidation tools (feature 020 — dream worker, US3).
//
// Registered only when SYNAPBUS_DREAM_ENABLED=1 via
// MemoryToolRegistrar.RegisterAllOnServer (see server.go SetDream).
//
// Every tool:
//
//  1. Pulls the dispatch token from the request context (set by
//     MCP middleware reading X-Synapbus-Dispatch-Token from the
//     transport header, or via the harness-propagated env var).
//  2. Validates the token against (caller-supplied owner_id, the
//     active consolidation_job_id carried alongside the token).
//  3. Performs the action against the appropriate messaging store.
//  4. Appends a structured action record to the job's `actions`
//     JSON array via JobsStore.AppendAction.
//
// All errors follow the MCP standard envelope; the contract codes
// (`dispatch_token_*`, `not_same_owner`, `core_memory_too_large`,
// `relation_type_reserved`, `source_not_found`, ...) are listed in
// `contracts/mcp-memory-tools.md` and mirrored verbatim here so the
// dream-agent can match on the string.
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/messaging"
)

// Context key for the dispatch token. The transport-layer middleware
// (or test harness) stuffs the token from the HTTP header
// `X-Synapbus-Dispatch-Token` into ctx via WithDispatchToken; the
// memory tool handlers read it via DispatchTokenFromContext.
type dispatchTokenKey struct{}

// WithDispatchToken returns a derived context carrying tok as the
// active dispatch token.
func WithDispatchToken(ctx context.Context, tok string) context.Context {
	return context.WithValue(ctx, dispatchTokenKey{}, tok)
}

// DispatchTokenFromContext returns the dispatch token, if any.
func DispatchTokenFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(dispatchTokenKey{}).(string)
	return v, ok && v != ""
}

// MemoryToolDeps bundles the dependencies the six memory tools need.
type MemoryToolDeps struct {
	DB        *sql.DB
	Msg       *messaging.MessagingService
	Agents    *agents.AgentService
	Core      *messaging.CoreMemoryStore
	Links     *messaging.LinkStore
	Pins      *messaging.PinStore
	Jobs      *messaging.JobsStore
	Tokens    *messaging.DispatchTokenStore
	MemConfig messaging.MemoryConfig
	Logger    *slog.Logger
}

// MemoryToolRegistrar registers the six memory_* MCP tools.
type MemoryToolRegistrar struct {
	deps   MemoryToolDeps
	logger *slog.Logger
}

// NewMemoryToolRegistrar returns a registrar over deps. RegisterAllOnServer
// is a no-op when deps.DB or deps.Jobs is nil (defensive — these are required).
func NewMemoryToolRegistrar(deps MemoryToolDeps) *MemoryToolRegistrar {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default().With("component", "mcp-memory-tools")
	}
	return &MemoryToolRegistrar{deps: deps, logger: logger}
}

// RegisterAllOnServer attaches the six tools to mcpSrv. The caller
// (server.go SetDream) is responsible for gating registration on
// SYNAPBUS_DREAM_ENABLED.
func (r *MemoryToolRegistrar) RegisterAllOnServer(s *server.MCPServer) {
	if s == nil || r.deps.DB == nil || r.deps.Jobs == nil || r.deps.Tokens == nil {
		return
	}
	s.AddTool(memoryListUnprocessedTool(), r.handleListUnprocessed)
	s.AddTool(memoryWriteReflectionTool(), r.handleWriteReflection)
	s.AddTool(memoryRewriteCoreTool(), r.handleRewriteCore)
	s.AddTool(memoryMarkDuplicateTool(), r.handleMarkDuplicate)
	s.AddTool(memorySupersedeTool(), r.handleSupersede)
	s.AddTool(memoryAddLinkTool(), r.handleAddLink)
	r.logger.Info("memory MCP tools registered", "count", 6)
}

// --- Tool definitions ---

func memoryListUnprocessedTool() mcplib.Tool {
	return mcplib.NewTool("memory_list_unprocessed",
		mcplib.WithDescription("List recent memory-eligible messages the owner's pool has not yet consolidated. Used by the dream agent to scan its inbox."),
		mcplib.WithString("owner_id", mcplib.Description("Caller's owner_id (must match the dispatch token's owner)"), mcplib.Required()),
		mcplib.WithNumber("since_message_id", mcplib.Description("Exclusive lower bound (defaults to 0)")),
		mcplib.WithNumber("limit", mcplib.Description("Max items to return (default 50, max 200)")),
	)
}

func memoryWriteReflectionTool() mcplib.Tool {
	return mcplib.NewTool("memory_write_reflection",
		mcplib.WithDescription("Write a higher-level abstraction back to the memory pool tagged 'reflection'. Inserts 'refines' links from the new memory to each source."),
		mcplib.WithString("owner_id", mcplib.Required()),
		mcplib.WithString("body", mcplib.Required()),
		mcplib.WithString("source_message_ids", mcplib.Description("Comma-separated message ids")),
		mcplib.WithString("tags", mcplib.Description("Comma-separated tags")),
	)
}

func memoryRewriteCoreTool() mcplib.Tool {
	return mcplib.NewTool("memory_rewrite_core",
		mcplib.WithDescription("Replace the per-(owner, agent) core memory blob wholesale (no merge)."),
		mcplib.WithString("owner_id", mcplib.Required()),
		mcplib.WithString("agent_name", mcplib.Required()),
		mcplib.WithString("blob", mcplib.Required()),
	)
}

func memoryMarkDuplicateTool() mcplib.Tool {
	return mcplib.NewTool("memory_mark_duplicate",
		mcplib.WithDescription("Mark two memories as duplicates; one is kept canonical, the other soft-deleted."),
		mcplib.WithString("owner_id", mcplib.Required()),
		mcplib.WithNumber("a_id", mcplib.Required()),
		mcplib.WithNumber("b_id", mcplib.Required()),
		mcplib.WithNumber("keep_id", mcplib.Required()),
		mcplib.WithString("reason"),
	)
}

func memorySupersedeTool() mcplib.Tool {
	return mcplib.NewTool("memory_supersede",
		mcplib.WithDescription("Mark memory A as obsoleted by memory B (temporal validity)."),
		mcplib.WithString("owner_id", mcplib.Required()),
		mcplib.WithNumber("a_id", mcplib.Required()),
		mcplib.WithNumber("b_id", mcplib.Required()),
		mcplib.WithString("reason"),
	)
}

func memoryAddLinkTool() mcplib.Tool {
	return mcplib.NewTool("memory_add_link",
		mcplib.WithDescription("Add a typed link between two memories. relation_type must be one of refines, contradicts, examples, related."),
		mcplib.WithString("owner_id", mcplib.Required()),
		mcplib.WithNumber("src_id", mcplib.Required()),
		mcplib.WithNumber("dst_id", mcplib.Required()),
		mcplib.WithString("relation_type", mcplib.Required()),
		mcplib.WithString("metadata", mcplib.Description("JSON object")),
	)
}

// --- Shared validation ---

// authorizeForOwner validates the dispatch token in ctx against the
// caller-supplied owner_id. Returns the active jobID (so the handler
// can call AppendAction) or an MCP error result.
func (r *MemoryToolRegistrar) authorizeForOwner(ctx context.Context, ownerID string) (jobID int64, errResult *mcplib.CallToolResult) {
	tok, ok := DispatchTokenFromContext(ctx)
	if !ok {
		return 0, memErrorf("dispatch_token_missing", "no dispatch token in request context")
	}
	// Find the consolidation_job_id this token is bound to.
	var (
		dbOwner   string
		dbJob     int64
		expiresAt time.Time
		revokedAt sql.NullTime
	)
	err := r.deps.DB.QueryRowContext(ctx,
		`SELECT owner_id, consolidation_job_id, expires_at, revoked_at
		   FROM memory_dispatch_tokens WHERE token = ?`, tok,
	).Scan(&dbOwner, &dbJob, &expiresAt, &revokedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, memErrorf("dispatch_token_missing", "token not found")
		}
		return 0, memErrorf("dispatch_token_missing", "token lookup failed: %s", err)
	}
	if revokedAt.Valid {
		return 0, memErrorf("dispatch_token_revoked", "token has been revoked")
	}
	if !expiresAt.After(time.Now().UTC()) {
		return 0, memErrorf("dispatch_token_expired", "token expired at %s", expiresAt.Format(time.RFC3339))
	}
	if dbOwner != ownerID {
		return 0, memErrorf("dispatch_token_owner_mismatch", "token bound to %q, request claims %q", dbOwner, ownerID)
	}
	// Run the canonical Validate path so used_at is stamped uniformly.
	if r.deps.Tokens != nil {
		if _, err := r.deps.Tokens.Validate(ctx, tok, ownerID, dbJob); err != nil {
			return 0, memErrorf("dispatch_token_missing", "validate: %s", err)
		}
	}
	return dbJob, nil
}

// recordAction appends to the job's actions JSON array. Logged at
// warn-level on failure; never blocks the tool's user-visible response.
func (r *MemoryToolRegistrar) recordAction(ctx context.Context, jobID int64, tool string, targetID int64, args map[string]any) {
	if r.deps.Jobs == nil {
		return
	}
	action := map[string]any{
		"tool":              tool,
		"target_message_id": targetID,
		"args":              args,
		"at":                time.Now().UTC().Format(time.RFC3339),
	}
	if err := r.deps.Jobs.AppendAction(ctx, jobID, action); err != nil {
		r.logger.Warn("append action failed",
			"job_id", jobID,
			"tool", tool,
			"error", err,
		)
	}
}

// memErrorf returns an MCP CallToolResult carrying a contractual error
// code + human-readable message. The MCP framework already wraps the
// `error` field in the JSON envelope; we render `code: ...` as the
// leading line of the message so the dream-agent can pattern-match.
func memErrorf(code, format string, args ...any) *mcplib.CallToolResult {
	msg := fmt.Sprintf("%s: %s", code, fmt.Sprintf(format, args...))
	return mcplib.NewToolResultError(msg)
}

// --- Handlers ---

func (r *MemoryToolRegistrar) handleListUnprocessed(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	owner := req.GetString("owner_id", "")
	if owner == "" {
		return memErrorf("invalid_request", "owner_id required"), nil
	}
	jobID, errR := r.authorizeForOwner(ctx, owner)
	if errR != nil {
		return errR, nil
	}

	since := int64(req.GetInt("since_message_id", 0))
	limit := req.GetInt("limit", 50)
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	memIDs, err := messaging.MemoryChannelIDs(ctx, r.deps.DB)
	if err != nil {
		return memErrorf("internal", "list memory channels: %s", err), nil
	}
	if len(memIDs) == 0 {
		_ = jobID // record nothing — empty
		return resultJSON(map[string]any{"memories": []any{}, "max_id_returned": since})
	}

	placeholders := strings.Repeat("?,", len(memIDs))
	placeholders = placeholders[:len(placeholders)-1]
	queryArgs := []any{}
	for _, id := range memIDs {
		queryArgs = append(queryArgs, id)
	}
	// Apply the same 14d (configurable) recency window the worker uses
	// so the consolidation agent only ever sees a bounded input set.
	windowDays := int(r.deps.MemConfig.DreamRecentWindow / (24 * time.Hour))
	if windowDays < 1 {
		windowDays = 14
	}
	windowExpr := fmt.Sprintf("-%d days", windowDays)
	queryArgs = append(queryArgs, owner, since, windowExpr, limit)

	// Contract guarantees this list excludes:
	//   - messages already linked as the dst_message_id of a
	//     refines/duplicate_of/superseded_by edge (already
	//     consolidated by an earlier dream pass), AND
	//   - the dream worker's own output (from_agent prefix "dream:")
	//     so the agent never re-refines its own reflections.
	// Without these filters the agent loops on the same oldest-50
	// messages every cycle and progress flat-lines.
	q := `SELECT m.id, m.from_agent, c.name, m.body, m.created_at
	        FROM messages m
	        JOIN agents a ON m.from_agent = a.name
	        JOIN channels c ON m.channel_id = c.id
	       WHERE m.channel_id IN (` + placeholders + `)
	         AND CAST(a.owner_id AS TEXT) = ?
	         AND m.id > ?
	         AND m.created_at > datetime('now', ?)
	         AND m.from_agent NOT LIKE 'dream:%'
	         AND m.id NOT IN (
	             SELECT dst_message_id FROM memory_links
	              WHERE relation_type IN ('refines','duplicate_of','superseded_by')
	         )
	       ORDER BY m.id ASC
	       LIMIT ?`

	rows, err := r.deps.DB.QueryContext(ctx, q, queryArgs...)
	if err != nil {
		return memErrorf("internal", "query: %s", err), nil
	}
	defer rows.Close()

	type item struct {
		ID        int64     `json:"id"`
		FromAgent string    `json:"from_agent"`
		Channel   string    `json:"channel"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}
	var items []item
	var maxID = since
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.FromAgent, &it.Channel, &it.Body, &it.CreatedAt); err != nil {
			return memErrorf("internal", "scan: %s", err), nil
		}
		items = append(items, it)
		if it.ID > maxID {
			maxID = it.ID
		}
	}
	if err := rows.Err(); err != nil {
		return memErrorf("internal", "iterate: %s", err), nil
	}

	r.recordAction(ctx, jobID, "memory_list_unprocessed", 0, map[string]any{
		"since_message_id": since, "limit": limit, "returned": len(items),
	})
	return resultJSON(map[string]any{"memories": items, "max_id_returned": maxID})
}

func (r *MemoryToolRegistrar) handleWriteReflection(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	owner := req.GetString("owner_id", "")
	body := req.GetString("body", "")
	if owner == "" || body == "" {
		return memErrorf("invalid_request", "owner_id and body required"), nil
	}
	jobID, errR := r.authorizeForOwner(ctx, owner)
	if errR != nil {
		return errR, nil
	}
	sourceIDs := parseInt64CSV(req.GetString("source_message_ids", ""))

	// Verify every source belongs to caller's owner.
	for _, sid := range sourceIDs {
		ok, sameOwner, _ := r.messageBelongsTo(ctx, sid, owner)
		if !ok {
			return memErrorf("source_not_found", "message %d not found", sid), nil
		}
		if !sameOwner {
			return memErrorf("not_same_owner", "source %d belongs to a different owner", sid), nil
		}
	}

	// Pick a destination channel: prefer `#reflections-<owner>` if it
	// exists, else `#open-brain`.
	channelID, channelName, err := r.pickReflectionChannel(ctx, owner)
	if err != nil {
		return memErrorf("internal", "pick channel: %s", err), nil
	}
	if channelID == 0 {
		return memErrorf("internal", "no reflection channel available"), nil
	}

	dreamAgent := "dream:" + owner
	// Ensure conversation + message inserts (lightweight direct SQL —
	// the MessagingService path would trigger reactive runs which we
	// must avoid per feedback_system_dm_no_trigger.md).
	convRes, err := r.deps.DB.ExecContext(ctx,
		`INSERT INTO conversations (created_by, channel_id) VALUES (?, ?)`,
		dreamAgent, channelID,
	)
	if err != nil {
		return memErrorf("internal", "create conversation: %s", err), nil
	}
	convID, _ := convRes.LastInsertId()

	res, err := r.deps.DB.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, from_agent, channel_id, body, priority, status, metadata)
		 VALUES (?, ?, ?, ?, 5, 'pending', ?)`,
		convID, dreamAgent, channelID, body, `{"tags":["reflection"]}`,
	)
	if err != nil {
		return memErrorf("internal", "insert message: %s", err), nil
	}
	newID, _ := res.LastInsertId()

	// Add `refines` links from new memory → each source.
	created := 0
	for _, sid := range sourceIDs {
		if r.deps.Links == nil {
			break
		}
		if _, err := r.deps.Links.Add(ctx, newID, sid, "refines", owner, "agent:dream:"+owner, nil); err == nil {
			created++
		}
	}

	r.recordAction(ctx, jobID, "memory_write_reflection", newID, map[string]any{
		"source_message_ids": sourceIDs,
		"channel":            channelName,
	})

	return resultJSON(map[string]any{
		"memory_id":     newID,
		"channel":       channelName,
		"links_created": created,
	})
}

func (r *MemoryToolRegistrar) handleRewriteCore(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	owner := req.GetString("owner_id", "")
	agent := req.GetString("agent_name", "")
	blob := req.GetString("blob", "")
	if owner == "" || agent == "" {
		return memErrorf("invalid_request", "owner_id and agent_name required"), nil
	}
	jobID, errR := r.authorizeForOwner(ctx, owner)
	if errR != nil {
		return errR, nil
	}
	if r.deps.Core == nil {
		return memErrorf("internal", "core memory store not configured"), nil
	}
	// Confirm target agent is owned by caller's owner.
	targetOwner, err := agents.OwnerFor(ctx, r.deps.DB, agent)
	if err != nil {
		if errors.Is(err, agents.ErrAgentNotFound) {
			return memErrorf("source_not_found", "agent %q not found", agent), nil
		}
		return memErrorf("internal", "owner lookup: %s", err), nil
	}
	if targetOwner != owner {
		return memErrorf("not_same_owner", "agent %q owner=%q != caller %q", agent, targetOwner, owner), nil
	}
	prev, _, _, _ := r.deps.Core.Get(ctx, owner, agent)
	if err := r.deps.Core.Set(ctx, owner, agent, blob, "agent:dream:"+owner); err != nil {
		if errors.Is(err, messaging.ErrCoreMemoryTooLarge) {
			return memErrorf("core_memory_too_large", "blob %d bytes exceeds cap", len(blob)), nil
		}
		return memErrorf("internal", "set core: %s", err), nil
	}
	r.recordAction(ctx, jobID, "memory_rewrite_core", 0, map[string]any{
		"owner_id": owner, "agent_name": agent, "new_chars": len(blob),
	})
	return resultJSON(map[string]any{
		"owner_id":       owner,
		"agent_name":     agent,
		"previous_blob":  prev,
		"new_blob_chars": len(blob),
		"updated_at":     time.Now().UTC().Format(time.RFC3339),
	})
}

func (r *MemoryToolRegistrar) handleMarkDuplicate(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	owner := req.GetString("owner_id", "")
	aID := int64(req.GetInt("a_id", 0))
	bID := int64(req.GetInt("b_id", 0))
	keepID := int64(req.GetInt("keep_id", 0))
	reason := req.GetString("reason", "")
	if owner == "" || aID == 0 || bID == 0 || keepID == 0 {
		return memErrorf("invalid_request", "owner_id, a_id, b_id, keep_id required"), nil
	}
	if keepID != aID && keepID != bID {
		return memErrorf("keep_id_not_in_pair", "keep_id must be a_id or b_id"), nil
	}
	jobID, errR := r.authorizeForOwner(ctx, owner)
	if errR != nil {
		return errR, nil
	}
	for _, id := range []int64{aID, bID} {
		ok, sameOwner, _ := r.messageBelongsTo(ctx, id, owner)
		if !ok {
			return memErrorf("source_not_found", "message %d not found", id), nil
		}
		if !sameOwner {
			return memErrorf("not_same_owner", "message %d belongs to a different owner", id), nil
		}
	}
	loserID := aID
	if keepID == aID {
		loserID = bID
	}
	if r.deps.Links == nil {
		return memErrorf("internal", "link store not configured"), nil
	}
	linkID, err := r.deps.Links.AddConsolidationLink(ctx, loserID, keepID, "duplicate_of", owner, "agent:dream:"+owner, map[string]any{"reason": reason})
	if err != nil {
		return memErrorf("internal", "add link: %s", err), nil
	}
	r.recordAction(ctx, jobID, "memory_mark_duplicate", loserID, map[string]any{
		"a_id": aID, "b_id": bID, "keep_id": keepID, "reason": reason,
	})
	return resultJSON(map[string]any{
		"keep_id":         keepID,
		"soft_deleted_id": loserID,
		"link_created_id": linkID,
	})
}

func (r *MemoryToolRegistrar) handleSupersede(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	owner := req.GetString("owner_id", "")
	aID := int64(req.GetInt("a_id", 0))
	bID := int64(req.GetInt("b_id", 0))
	reason := req.GetString("reason", "")
	if owner == "" || aID == 0 || bID == 0 {
		return memErrorf("invalid_request", "owner_id, a_id, b_id required"), nil
	}
	jobID, errR := r.authorizeForOwner(ctx, owner)
	if errR != nil {
		return errR, nil
	}
	for _, id := range []int64{aID, bID} {
		ok, sameOwner, _ := r.messageBelongsTo(ctx, id, owner)
		if !ok {
			return memErrorf("source_not_found", "message %d not found", id), nil
		}
		if !sameOwner {
			return memErrorf("not_same_owner", "message %d belongs to a different owner", id), nil
		}
	}
	if r.deps.Links == nil {
		return memErrorf("internal", "link store not configured"), nil
	}
	linkID, err := r.deps.Links.AddConsolidationLink(ctx, aID, bID, "superseded_by", owner, "agent:dream:"+owner, map[string]any{"reason": reason})
	if err != nil {
		return memErrorf("internal", "add link: %s", err), nil
	}
	r.recordAction(ctx, jobID, "memory_supersede", aID, map[string]any{
		"a_id": aID, "b_id": bID, "reason": reason,
	})
	return resultJSON(map[string]any{
		"superseded_id":   aID,
		"by_id":           bID,
		"link_created_id": linkID,
	})
}

func (r *MemoryToolRegistrar) handleAddLink(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	owner := req.GetString("owner_id", "")
	srcID := int64(req.GetInt("src_id", 0))
	dstID := int64(req.GetInt("dst_id", 0))
	relType := req.GetString("relation_type", "")
	if owner == "" || srcID == 0 || dstID == 0 || relType == "" {
		return memErrorf("invalid_request", "owner_id, src_id, dst_id, relation_type required"), nil
	}
	jobID, errR := r.authorizeForOwner(ctx, owner)
	if errR != nil {
		return errR, nil
	}
	for _, id := range []int64{srcID, dstID} {
		ok, sameOwner, _ := r.messageBelongsTo(ctx, id, owner)
		if !ok {
			return memErrorf("source_not_found", "message %d not found", id), nil
		}
		if !sameOwner {
			return memErrorf("not_same_owner", "message %d belongs to a different owner", id), nil
		}
	}
	if r.deps.Links == nil {
		return memErrorf("internal", "link store not configured"), nil
	}
	var meta map[string]any
	if mraw := req.GetString("metadata", ""); mraw != "" {
		_ = json.Unmarshal([]byte(mraw), &meta)
	}
	linkID, err := r.deps.Links.Add(ctx, srcID, dstID, relType, owner, "agent:dream:"+owner, meta)
	if err != nil {
		if errors.Is(err, messaging.ErrLinkTypeReserved) {
			return memErrorf("relation_type_reserved", "type %q is reserved", relType), nil
		}
		return memErrorf("internal", "add link: %s", err), nil
	}
	r.recordAction(ctx, jobID, "memory_add_link", dstID, map[string]any{
		"src_id": srcID, "dst_id": dstID, "relation_type": relType,
	})
	return resultJSON(map[string]any{"link_id": linkID})
}

// --- helpers ---

func parseInt64CSV(s string) []int64 {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var v int64
		if _, err := fmt.Sscanf(p, "%d", &v); err == nil && v > 0 {
			out = append(out, v)
		}
	}
	return out
}

// messageBelongsTo reports (exists, ownerMatches, dbErr).
func (r *MemoryToolRegistrar) messageBelongsTo(ctx context.Context, msgID int64, ownerID string) (bool, bool, error) {
	var fromAgent string
	err := r.deps.DB.QueryRowContext(ctx,
		`SELECT from_agent FROM messages WHERE id = ?`, msgID,
	).Scan(&fromAgent)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, false, nil
		}
		return false, false, err
	}
	owner, err := agents.OwnerFor(ctx, r.deps.DB, fromAgent)
	if err != nil {
		return true, false, nil
	}
	return true, owner == ownerID, nil
}

// pickReflectionChannel picks a destination channel for a reflection.
// Preference: `reflections-<owner>` if any such channel exists, else
// `open-brain`.
func (r *MemoryToolRegistrar) pickReflectionChannel(ctx context.Context, ownerID string) (int64, string, error) {
	// Try reflections-* the owner has authored to (best heuristic).
	var (
		id   int64
		name string
	)
	err := r.deps.DB.QueryRowContext(ctx,
		`SELECT id, name FROM channels WHERE name LIKE 'reflections-%' ORDER BY id ASC LIMIT 1`,
	).Scan(&id, &name)
	if err == nil {
		return id, name, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, "", err
	}
	// Fallback: open-brain.
	err = r.deps.DB.QueryRowContext(ctx,
		`SELECT id, name FROM channels WHERE name = 'open-brain' LIMIT 1`,
	).Scan(&id, &name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", nil
		}
		return 0, "", err
	}
	return id, name, nil
}
