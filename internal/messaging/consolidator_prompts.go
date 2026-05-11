// Dream-agent prompts (feature 020 — US3). One short prompt per
// consolidation job type. Passed to the dispatched Claude Code agent
// as the task body via harness.ExecRequest. Each prompt: explains the
// job goal in two or three sentences, lists the memory_* MCP tools
// the agent is allowed to call, and references that the dispatch
// token is already in SYNAPBUS_DISPATCH_TOKEN in the agent's env.
package messaging

const (
	promptReflection = `You are running a memory-reflection pass on the open-brain pool.
Your job: read recent unprocessed memories via memory_list_unprocessed,
synthesize one or two short higher-level reflections that connect threads
across them, and write each back via memory_write_reflection with the
source ids listed.
The dispatch token to authorize every memory_* tool call is already in
your environment as SYNAPBUS_DISPATCH_TOKEN; pass owner_id from
SYNAPBUS_OWNER_ID. Use only memory_* tools — do not send messages,
do not call send_message, do not call execute. Keep each reflection
under 600 chars. When done, exit 0.`

	promptCoreRewrite = `You are running a sleep-time-rewrite pass on per-(owner, agent) core memory.
Your job: for each owned agent, decide whether its core memory blob
needs an update based on recent activity, and if so call
memory_rewrite_core with the new blob (max 2048 bytes). The blob must
be a tight, second-person identity-and-focus statement (e.g. "You are
research-mcpproxy. Currently focused on benchmarking against ...").
The dispatch token is in SYNAPBUS_DISPATCH_TOKEN. Use only memory_*
tools. When done, exit 0.`

	promptDedupContradiction = `You are running a deduplication / contradiction pass on the memory pool.
Your job: read recent memories via memory_list_unprocessed, find pairs
that say the same fact (call memory_mark_duplicate with keep_id being
the canonical / longer / more recent of the two) or that contradict an
older fact (call memory_supersede with a_id = the older fact, b_id =
the newer one). Always provide a short reason. The dispatch token is
in SYNAPBUS_DISPATCH_TOKEN. Use only memory_* tools. When done, exit 0.`

	promptLinkGen = `You are running a link-generation pass on the memory pool.
Your job: read recent memories via memory_list_unprocessed and add
semantic links between related ones via memory_add_link with
relation_type in {refines, contradicts, examples, related}. Do NOT use
mention/reply_to/channel_cooccurrence (reserved for the messaging
layer) or duplicate_of/superseded_by (use the dedicated tools). The
dispatch token is in SYNAPBUS_DISPATCH_TOKEN. Use only memory_* tools.
When done, exit 0.`
)

// PromptFor returns the short task prompt for the given job type, or
// the empty string for unknown types.
func PromptFor(jobType string) string {
	switch jobType {
	case JobTypeReflection:
		return promptReflection
	case JobTypeCoreRewrite:
		return promptCoreRewrite
	case JobTypeDedupContradiction:
		return promptDedupContradiction
	case JobTypeLinkGen:
		return promptLinkGen
	default:
		return ""
	}
}
