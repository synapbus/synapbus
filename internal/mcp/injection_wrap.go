// Proactive-memory injection middleware — wraps MCP tool handlers so
// that successful JSON responses gain a `relevant_context` field per
// `specs/020-proactive-memory-dream-worker/contracts/mcp-injection.md`.
package mcp

import (
	"context"
	"encoding/json"
	"log/slog"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/search"
)

// ToolHandler is the mcp-go tool handler signature. Re-exported as an
// alias so the wrapper signature reads cleanly at registration sites.
type ToolHandler = server.ToolHandlerFunc

// QuerySource derives the retrieval query for one tool invocation. It
// is given the inner handler's parsed args (best-effort: nil when args
// don't fit map[string]any) and the inner handler's parsed JSON result
// (nil on error or non-JSON). It must be cheap; called on every wrapped
// tool call.
type QuerySource func(ctx context.Context, toolName string, args map[string]any, result map[string]any) string

// WrapConfig parameterizes WrapInjection.
type WrapConfig struct {
	// Cfg is the messaging.MemoryConfig snapshot taken at server
	// startup. When Cfg.InjectionEnabled is false, WrapInjection
	// returns the handler unchanged.
	Cfg messaging.MemoryConfig

	// SearchSvc drives retrieval. Required when Cfg.InjectionEnabled.
	SearchSvc *search.Service

	// Injections is the 24h audit ring. May be nil — Record errors are
	// logged and the wrapper continues.
	Injections *messaging.MemoryInjections

	// QuerySource derives the retrieval query for this tool. Required.
	QuerySource QuerySource

	// IncludeCore is true for session-start tools (e.g. my_status).
	// Only those get the per-(owner, agent) core-memory blob injected.
	IncludeCore bool

	// CoreProvider is consulted when IncludeCore=true. May be nil
	// (US2 not yet wired) — wrapper still functions, just skips core.
	CoreProvider search.CoreMemoryProvider

	// Logger is used for non-fatal failures. Defaults to slog.Default.
	Logger *slog.Logger
}

// WrapInjection returns a ToolHandler that wraps `inner` with the
// proactive-memory injection middleware described in
// `contracts/mcp-injection.md`.
//
// When Cfg.InjectionEnabled is false, the original handler is returned
// unchanged so the response payload exactly matches the pre-feature
// shape (FR-012, SC-009).
//
// Otherwise, the wrapper:
//  1. Runs the inner handler.
//  2. If the result is an error or not a single TextContent of JSON
//     object shape, returns the result unchanged.
//  3. Builds a ContextPacket via search.BuildContextPacket using the
//     query derived from cfg.QuerySource.
//  4. If the packet is non-empty (>=1 memory or core memory set), merges
//     `relevant_context: <packet>` into the JSON body and re-marshals.
//  5. Records the injection to the 24h audit ring asynchronously.
func WrapInjection(inner ToolHandler, toolName string, cfg WrapConfig) ToolHandler {
	if !cfg.Cfg.InjectionEnabled {
		return inner
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default().With("component", "mcp-injection")
	}
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		res, err := inner(ctx, req)
		if err != nil {
			return res, err
		}
		if res == nil || res.IsError {
			return res, nil
		}

		// Locate the JSON text content. Non-JSON or multi-content
		// payloads pass through unchanged.
		idx, text, ok := singleJSONText(res)
		if !ok {
			return res, nil
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(text), &body); err != nil {
			return res, nil
		}

		agent, ok := callerAgent(ctx)
		if !ok || agent == nil {
			// No identity → no owner scope → no injection.
			return res, nil
		}

		// Derive the retrieval query. nil args is fine; nil result is
		// fine — the source decides what to do.
		argsMap, _ := req.Params.Arguments.(map[string]any)
		query := ""
		if cfg.QuerySource != nil {
			query = cfg.QuerySource(ctx, toolName, argsMap, body)
		}

		opts := search.InjectionOpts{
			BudgetTokens: cfg.Cfg.InjectionBudgetTokens,
			MaxItems:     cfg.Cfg.InjectionMaxItems,
			MinScore:     cfg.Cfg.InjectionMinScore,
			IncludeCore:  cfg.IncludeCore,
			CoreProvider: cfg.CoreProvider,
		}
		pkt, err := search.BuildContextPacket(ctx, cfg.SearchSvc, agent, query, opts)
		if err != nil {
			logger.Debug("build context packet failed", "tool", toolName, "error", err)
			return res, nil
		}
		if pkt == nil {
			// Empty packet → omit `relevant_context` entirely.
			return res, nil
		}
		if len(pkt.Memories) == 0 && pkt.CoreMemory == "" {
			return res, nil
		}

		body["relevant_context"] = pkt

		merged, err := json.Marshal(body)
		if err != nil {
			logger.Debug("re-marshal failed", "tool", toolName, "error", err)
			return res, nil
		}
		res.Content[idx] = mcplib.TextContent{Type: "text", Text: string(merged)}

		// Audit-ring write is best-effort; never blocks the response.
		recordInjection(cfg.Injections, logger, agent, toolName, pkt)

		return res, nil
	}
}

// singleJSONText reports the index of the single TextContent in `res`
// when its Text is a JSON object. Anything else (multiple contents,
// non-text, non-object JSON) returns ok=false → pass through.
func singleJSONText(res *mcplib.CallToolResult) (int, string, bool) {
	if res == nil || len(res.Content) != 1 {
		return 0, "", false
	}
	tc, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		return 0, "", false
	}
	// Quick sanity check that the text starts with `{` — avoids
	// allocating a map for a known-non-object payload.
	for i := 0; i < len(tc.Text); i++ {
		switch tc.Text[i] {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return 0, tc.Text, true
		default:
			return 0, "", false
		}
	}
	return 0, "", false
}

// callerAgent unpacks *agents.Agent from the request context. Uses the
// agents middleware ContextWithAgent, populated by the auth path.
func callerAgent(ctx context.Context) (*agents.Agent, bool) {
	return agents.AgentFromContext(ctx)
}

// recordInjection writes one audit-ring row. Runs in a fresh goroutine
// so it cannot block the response, but inherits a detached context with
// a short timeout via the inner call site. Failures are logged at debug
// level since they're non-fatal for the request.
func recordInjection(store *messaging.MemoryInjections, logger *slog.Logger, agent *agents.Agent, toolName string, pkt *search.ContextPacket) {
	if store == nil || agent == nil || pkt == nil {
		return
	}
	ids := make([]int64, 0, len(pkt.Memories))
	for _, m := range pkt.Memories {
		ids = append(ids, m.ID)
	}
	rec := messaging.InjectionRecord{
		OwnerID:          ownerIDString(agent.OwnerID),
		AgentName:        agent.Name,
		ToolName:         toolName,
		PacketSizeChars:  pkt.PacketChars,
		PacketItemsCount: len(pkt.Memories),
		MessageIDs:       ids,
		CoreBlobIncluded: pkt.CoreMemory != "",
	}
	go func() {
		// Detached background context: the request context may already
		// be canceled by the time this goroutine runs.
		ctx := context.Background()
		if err := store.Record(ctx, rec); err != nil {
			logger.Debug("audit-ring insert failed", "tool", toolName, "error", err)
		}
	}()
}

func ownerIDString(id int64) string {
	if id == 0 {
		return ""
	}
	// strconv.FormatInt is faster than fmt.Sprintf; mirror what
	// agents.OwnerFor produces so comparisons in the search layer line
	// up.
	return formatInt64(id)
}

// formatInt64 is a tiny helper to avoid pulling strconv into the public
// surface area for one line.
func formatInt64(v int64) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	neg := false
	if v < 0 {
		neg = true
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = digits[v%10]
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
