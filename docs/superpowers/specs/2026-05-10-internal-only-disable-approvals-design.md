# Internal-only mode: remove approvals & escalations

**Date:** 2026-05-10
**Status:** Design
**Owner:** Algis

## Problem

SynapBus today assumes a human is in the loop: a stalemate worker DMs reminders after 4h, escalates to `#approvals` after 48h, and the dynamic-agent-spawning flow (spec 018) gates new agents and task trees on human approval. In practice the user is the only operator, the approval queue stalls, and the volume of reminder/escalation messages drowns out signal. The user is moving to a single daily summary (separate `#summary-daily` channel + summarizer agent already in progress) and treats SynapBus as an internal-only comms + data store — nothing publishes externally.

The goal is to remove the human-in-the-loop surfaces so the message stream stops generating noise the user will never read.

## Scope

### Removed

1. **Stalemate reminders** — `StalemateWorker.sendPendingReminders` and supporting helpers (`reminderExists`, the 4h ReminderAfter knob).
2. **Stalemate escalations** — `StalemateWorker.escalatePendingMessages` and `checkWorkflowStalemates`, plus the 48h EscalateAfter knob and `#approvals` lookup path.
3. **`propose_agent` MCP tool** (spec 018). It writes a `pending` row to `agent_proposals` for human approval via `#approvals` and there is no automated consumer of that table. Removing the tool leaves agent creation to the admin CLI, which matches the internal-only stance.

   **Note:** `propose_task_tree` is intentionally KEPT despite its name — it is not an approval gate. It directly inserts tasks in `approved` status and auto-transitions the goal to `active`. Removing it would break the spec-018 goal/task flow.
4. *(Reactions service intentionally untouched — it's a generic workflow primitive that also drives trust adjustments. Once no upstream feature creates approval-bearing messages, the `approve` / `reject` reaction paths become dormant on their own.)*

### Kept

- **`StalemateWorker.ProcessingTimeout`** (24h auto-fail of claimed-but-abandoned messages). Protects the inbox from crashed agents; not human-facing.
- **The `#approvals` channel row** in the `channels` table. Cheaper to leave than to migrate; user can drop via admin CLI later.
- **Webhook / K8s runner approval gates** (spec 003). User confirmed these are out of scope.
- **Trust system** (spec 011). No approval surface, just delegation.

### One-shot DB cleanup

New migration `internal/storage/schema/027_remove_approval_noise.sql`:

```sql
-- Drop reminder and escalation system DMs.
DELETE FROM messages
WHERE subject LIKE 'stalemate-reminder:%'
   OR subject LIKE 'stalemate-escalation:%';

-- Drop everything in the #approvals channel.
DELETE FROM messages
WHERE channel_id = (SELECT id FROM channels WHERE name = 'approvals');

-- Drop pending agent proposals (table itself stays for reversibility).
DELETE FROM agent_proposals;
```

`VACUUM` cannot run inside a migration transaction, so reclaiming disk is a separate `synapbus admin vacuum` command (or a manual `kubectl exec ... sqlite3 ... 'VACUUM;'`). Out of scope for this change unless trivial to wire up.

## Architecture impact

```
Before:
  agent → MCP propose_agent → agent_proposals row → human reacts in #approvals
                                                  → spawn or reject
  message claimed → StalemateWorker (every 15m) → 4h reminder DM
                                                → 48h escalation to #approvals
                                                → 24h auto-fail (KEEP)

After:
  agent → MCP create_agent (existing direct path) → agent registered
  message claimed → StalemateWorker (every 15m) → 24h auto-fail
```

Net code deletion. No new components, no new config surface, no new dependencies.

## Components touched

| File | Change |
|------|--------|
| `internal/messaging/stalemate.go` | Delete `sendPendingReminders`, `escalatePendingMessages`, `checkWorkflowStalemates`, `reminderExists`, `escalationExists`. Trim `StalemateConfig` to `ProcessingTimeout` + `Interval`. Remove `ReminderAfter` / `EscalateAfter` env vars. |
| `internal/messaging/stalemate_test.go` | Delete tests for removed methods; keep ProcessingTimeout tests. |
| `internal/messaging/options.go` | Remove channelLookup wiring if it's only used by escalation. |
| `internal/messaging/service.go` | Remove escalation hooks if any. |
| `internal/mcp/goals_tools.go` (spec 018) | Delete `propose_agent` tool registration (`proposeAgentTool`) and its `handleProposeAgent` handler. Keep `propose_task_tree` and the rest of the registrar. |
| `internal/storage/schema/027_remove_approval_noise.sql` | New migration. |
| `cmd/synapbus/admin.go`, `cmd/synapbus/main.go` | Remove any escalation-related flags. |
| `CLAUDE.md` (project + user) | Update SynapBus protocol section to drop "#approvals" + "stalemate auto-fails after 24h" mention of escalation. Keep claim-process-done loop. |
| User's `~/.claude/CLAUDE.md` | Same — drop approval-channel references and the auto-report trigger for "Need approval → #approvals". |

## Testing

- Existing `stalemate_test.go` cases for `ProcessingTimeout` continue to pass.
- New test: confirm `StalemateWorker.tick()` no longer queries pending messages for reminder/escalation candidates (no rows touched, no DMs sent).
- New test: confirm `propose_agent` MCP tool returns "tool not found" / is unregistered.
- Migration test: apply `027_remove_approval_noise.sql` to a fixture DB containing stalemate DMs + an `#approvals` message + an `agent_proposals` row; assert all three are gone, other messages untouched.
- No UI testing required — Web UI just stops showing approval-channel content because the channel is empty.

## Risks & mitigations

- **An external agent calls `propose_agent` after deletion.** MCP returns an unknown-tool error; agent's runbook should tolerate this. Acceptable because the user controls all agents.
- **Hidden consumer of escalation messages.** Search confirms reminders/escalations are only produced by `StalemateWorker` and consumed by humans. Low risk.
- **Migration deletes too much.** The `LIKE 'stalemate-%'` pattern is narrow and the `#approvals` channel is internal-only; nothing user-authored lives there. Take a `data/synapbus.db` backup before applying in prod (kubic).

## Out of scope

- Webhook/K8s runner human gates (spec 003).
- Removing the `#approvals` channel row.
- Adding `SYNAPBUS_APPROVALS_DISABLED` env flag — code deletion is reversible via git revert.
- Daily summarizer agent + `#summary-daily` channel — already in progress in a separate effort.
- Reclaiming disk via `VACUUM` — separate admin command if needed.
