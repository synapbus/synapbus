-- 027_remove_approval_noise.sql — one-shot cleanup for internal-only mode.
-- Drops messages produced by the (now-deleted) stalemate reminder/escalation
-- paths and any pending agent proposals. The agent_proposals table itself
-- stays in case the propose_agent flow is ever reinstated.
--
-- The #approvals channel row is intentionally NOT dropped: leaving it
-- keeps the migration trivially reversible (no need to recreate the
-- channel and re-grant memberships); the human can delete it via admin
-- CLI later.
--
-- Reminder/escalation messages are identified by their metadata JSON
-- (see internal/messaging/stalemate.go in git history), which is the
-- only field the historical worker stamped reliably — the conversation
-- subject prefix was used too but is not a hard guarantee.
--
-- FK NOTES: most refs to messages(id) are ON DELETE CASCADE / SET NULL,
-- but two are NO ACTION and would block this migration on a populated
-- DB:
--   * attachments.message_id  (001_initial.sql)
--   * messages.reply_to        (007_threads.sql)
-- We NULL those explicitly before deleting. Orphan attachment rows (with
-- message_id = NULL) are tolerated by the attachments service.

-- 1. Collect the message ids we're about to drop into a temp scratch
--    table so the cascading NULL/DELETE statements all reference the
--    same set even if the metadata predicates evolve later.
CREATE TEMP TABLE _approval_noise_msgs AS
SELECT id FROM messages
WHERE metadata LIKE '%"stalemate_reminder_for":%'
   OR metadata LIKE '%"stalemate_escalation_for":%'
   OR metadata LIKE '%"workflow_stalemate_reminder_for":%'
   OR metadata LIKE '%"workflow_stalemate_escalation_for":%'
   OR channel_id IN (SELECT id FROM channels WHERE name = 'approvals');

-- 2. Detach NO-ACTION FKs that would otherwise block the delete.
UPDATE attachments
   SET message_id = NULL
 WHERE message_id IN (SELECT id FROM _approval_noise_msgs);

UPDATE messages
   SET reply_to = NULL
 WHERE reply_to IN (SELECT id FROM _approval_noise_msgs);

-- 3. Drop the messages themselves. Cascading FKs (reactions, embeddings,
--    fts triggers, agent_listings) clean up automatically; SET NULL FKs
--    (goals.*_message_id, agent_proposals.*_message_id, goal_tasks.*)
--    forget the link gracefully.
DELETE FROM messages
 WHERE id IN (SELECT id FROM _approval_noise_msgs);

DROP TABLE _approval_noise_msgs;

-- Conversation rows for stalemate-prefixed subjects are intentionally
-- left in place. Multiple NO-ACTION FKs reference conversations(id)
-- (messages.conversation_id, inbox_state.conversation_id, possibly
-- others added by future migrations); cleaning them up reliably would
-- require chasing every ref. Empty / mostly-empty conversation rows
-- are harmless — they don't render as messages in the UI.

-- 4. Drop pending agent proposals. The table stays for reversibility.
DELETE FROM agent_proposals;
