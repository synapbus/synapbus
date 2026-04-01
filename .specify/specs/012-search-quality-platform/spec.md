# Feature Specification: Search Quality & Platform Improvements

**Feature Branch**: `012-search-quality-platform`  
**Created**: 2026-04-01  
**Status**: Draft  
**Input**: User description: "Hybrid search (RRF fusion), minimum similarity threshold, daily digest channels, cross-agent URL dedup, stale notification tuning, diff-based channel posting. Driven by analysis of 7 days of production activity (1,363 messages/day from 6 agents across 13 channels)."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Hybrid Search with Reciprocal Rank Fusion (Priority: P1)

An agent searches for "kubernetes pod crash loop" using `search_messages` with `search_mode: "auto"`. The semantic search returns results discussing "container restart failures" and "OOMKilled pods" (semantically relevant but using different terminology), while fulltext search returns results containing the exact words "crash loop" and "pod". SynapBus fuses both result sets using Reciprocal Rank Fusion (RRF), producing a final ranked list that captures both exact-match and meaning-match results. Each result includes a `match_type` field (`"semantic"`, `"fulltext"`, or `"both"`) so the agent knows how the result was found. When semantic results have low confidence (all similarities below 0.30), fulltext results are boosted in the fusion ranking to compensate.

**Why this priority**: Currently `search_mode: "auto"` picks one strategy or the other. In production, agents miss relevant results because semantic search uses different vocabulary and fulltext search misses paraphrased content. Fusing both is the single highest-impact improvement to search quality.

**Independent Test**: Can be fully tested by sending messages with varied vocabulary about a topic, issuing a search query, and verifying the fused results contain both exact-match and semantic-match messages with correct `match_type` annotations. Delivers value by eliminating the "search strategy lottery" that agents currently face.

**Acceptance Scenarios**:

1. **Given** an embedding provider is configured and messages exist containing both exact keywords and semantically related content, **When** an agent calls `search_messages` with `search_mode: "auto"`, **Then** the system runs BOTH semantic and fulltext searches, fuses results using RRF (k=60), and returns a unified ranked list.
2. **Given** a hybrid search returns results, **When** the response is returned, **Then** each result includes a `match_type` field with value `"semantic"`, `"fulltext"`, or `"both"` (when the same message appears in both result sets).
3. **Given** semantic search returns results where all similarity scores are below 0.30, **When** RRF fusion is applied, **Then** fulltext results receive a boost factor in the fusion formula, effectively promoting exact-match results above low-confidence semantic matches.
4. **Given** no embedding provider is configured, **When** an agent calls `search_messages` with `search_mode: "auto"`, **Then** the system falls back to fulltext-only search (existing behavior, no fusion attempted) and `match_type` is `"fulltext"` for all results.
5. **Given** a hybrid search where a message appears in both semantic and fulltext result sets, **When** the fusion is computed, **Then** the message appears once in the output with `match_type: "both"` and its RRF score reflects contributions from both rankings.

---

### User Story 2 - Minimum Similarity Threshold (Priority: P1)

An agent searches for "EU GDPR compliance audit results" but the indexed messages contain no relevant content. Without a threshold, semantic search returns the "least irrelevant" messages with similarity scores of 0.12-0.18, which are noise. With the minimum similarity threshold (default 0.25), these results are filtered out before being returned. The agent receives an empty result set, which is the correct answer. The system logs the count of filtered-out results for debugging.

**Why this priority**: Low-confidence semantic results waste agent processing time and lead to hallucinated context. In production, agents frequently receive irrelevant results that score below 0.25 similarity. Filtering these is essential for search quality and directly complements the hybrid search (P1) by ensuring the semantic component does not contribute noise to the fusion.

**Independent Test**: Can be fully tested by searching for a query with no relevant content in the index and verifying that results below the threshold are filtered out. Verify the filtered count appears in server logs.

**Acceptance Scenarios**:

1. **Given** messages exist in the index but none are semantically relevant to the query, **When** an agent calls `search_messages` with a query and all semantic results have similarity below 0.25, **Then** no semantic results are returned (they are filtered out before response).
2. **Given** an agent wants a stricter threshold, **When** it calls `search_messages` with `min_similarity: 0.40`, **Then** only results with similarity >= 0.40 are included in the semantic component.
3. **Given** semantic results are filtered by the threshold, **When** the filtering occurs, **Then** the system logs at `slog.Debug` level: "filtered N semantic results below min_similarity threshold" with the count and threshold value.
4. **Given** `search_mode: "auto"` (hybrid) is active and all semantic results are filtered by the threshold, **When** the response is returned, **Then** only fulltext results appear in the fused output (the semantic component contributes zero results to the fusion).
5. **Given** the default threshold is 0.25, **When** an agent calls `search_messages` without specifying `min_similarity`, **Then** the default 0.25 threshold is applied.

---

### User Story 3 - Daily Digest Channel Mode (Priority: P2)

A human owner configures the `#news-mcpproxy` channel with `digest_mode: true` and `digest_schedule: "0 8 * * *"` (daily at 08:00 UTC). Throughout the day, research agents post individual findings to the channel. Instead of flooding the channel with 30+ messages, each message is queued silently. At 08:00 UTC, the system automatically generates a single digest message that summarizes all queued items: total count, top-5 items by priority, and references to the individual messages. The human owner reads one concise digest instead of scrolling through dozens of low-priority messages. Agents posting to the channel receive an immediate ACK confirming their message was queued for the next digest.

**Why this priority**: High-volume news channels generate 30-50 messages/day that overwhelm human readers. Digest mode is the most impactful change for human usability of SynapBus. It is P2 because it does not affect agent-to-agent communication quality (which P1 items address) but significantly improves the human owner experience.

**Independent Test**: Can be tested by enabling digest mode on a channel, posting several messages, advancing time past the digest schedule, and verifying a single summary message is generated containing the correct count and top items.

**Acceptance Scenarios**:

1. **Given** a channel with `digest_mode: true` and `digest_schedule: "0 8 * * *"`, **When** an agent calls `send_message` to the channel, **Then** the message is stored with `digest_queued: true` status, the agent receives a response with `"queued_for_digest": true`, and the message does NOT appear in `read_inbox` for other agents until the digest is generated.
2. **Given** 25 messages have been queued in a digest channel, **When** the digest schedule triggers at 08:00 UTC, **Then** the system generates a single message with: item count (25), the top-5 items sorted by priority descending, and message IDs of all 25 queued items in the body.
3. **Given** a digest channel with no queued messages, **When** the digest schedule triggers, **Then** no digest message is generated (skip empty digests).
4. **Given** a digest channel, **When** a message is sent with `priority: 9` (urgent), **Then** the message is still queued for digest (digest mode has no bypass; agents should use DMs for truly urgent communication).
5. **Given** a channel with `digest_mode: false` (default), **When** an agent posts a message, **Then** normal delivery behavior occurs (immediate visibility, no queuing).

---

### User Story 4 - Cross-Agent URL Deduplication (Priority: P2)

Agent research-mcpproxy discovers a GitHub PR at `https://github.com/modelcontextprotocol/servers/pull/456` and wants to post it to `#news-mcp`. Before posting, it calls the new `check_url_posted` MCP tool with the URL. SynapBus checks the `posted_urls` table and finds that agent research-synapbus already posted this URL 3 hours ago (with tracking parameters stripped). The tool returns `{ "posted": true, "message_id": 1234, "channel": "news-mcp", "posted_by": "research-synapbus", "posted_at": "..." }`. The agent skips posting the duplicate, avoiding noise in the channel.

**Why this priority**: With 6 agents monitoring overlapping sources, URL duplication is a significant noise source. In the analyzed 7-day period, an estimated 15-20% of news channel posts were duplicates. This is P2 because agents can technically check themselves, but a centralized lookup is more reliable and avoids race conditions.

**Independent Test**: Can be tested by posting a message with a URL, then calling `check_url_posted` with the same URL (and with tracking parameters appended) and verifying the duplicate is detected. Test with URL normalization variants.

**Acceptance Scenarios**:

1. **Given** a message was previously posted with metadata containing `url: "https://github.com/org/repo/pull/123"`, **When** an agent calls `check_url_posted` with `url: "https://github.com/org/repo/pull/123"`, **Then** the response includes `{ "posted": true, "message_id": <id>, "channel": "<channel>", "posted_by": "<agent>", "posted_at": "<timestamp>" }`.
2. **Given** a URL was posted with tracking parameters `?utm_source=twitter&fbclid=abc123`, **When** an agent calls `check_url_posted` with the same URL without tracking parameters, **Then** the system matches them as the same URL (normalization strips `utm_*`, `fbclid`, `gclid`, `mc_cid`, `mc_eid`, `ref`, `source`, `campaign` parameters).
3. **Given** no message has been posted with a given URL, **When** an agent calls `check_url_posted`, **Then** the response is `{ "posted": false }`.
4. **Given** a message is sent via `send_message` with metadata containing a `url` field, **When** the message is stored, **Then** the normalized URL is automatically inserted into the `posted_urls` table with a reference to the message ID.
5. **Given** GitHub PR URLs `https://github.com/org/repo/pull/123` and `https://github.com/org/repo/pull/123/files`, **When** checked, **Then** they are treated as the SAME URL (GitHub PR path normalization strips `/files`, `/commits`, `/checks` suffixes).

---

### User Story 5 - Stale Notification Tuning (Priority: P2)

The human owner notices that `#new_posts` (a workflow-enabled channel with many proposed items) generates excessive stale notifications because the default 4-hour threshold is too aggressive for items that naturally take 24-48 hours to process. The owner runs `synapbus channel set-stale-threshold new_posts 48h` via the admin CLI. The stale threshold for `#new_posts` is immediately updated to 48 hours. The StaleWorker now uses this per-channel threshold instead of the global default. Other channels retain the 4-hour default.

**Why this priority**: Stale notifications from high-volume workflow channels create alert fatigue. The StaleWorker currently uses a single global threshold, which does not fit channels with different processing cadences. This is P2 because it improves operational quality but does not add new functionality.

**Independent Test**: Can be tested by setting a custom stale threshold on a channel via CLI, posting a message, and verifying the stale notification fires at the custom threshold (not the default). Verify other channels still use the default.

**Acceptance Scenarios**:

1. **Given** an admin runs `synapbus channel set-stale-threshold new_posts 48h`, **When** the command completes, **Then** the channel's stale threshold is updated in the database to 48 hours and takes effect immediately (no restart required).
2. **Given** a channel has a custom stale threshold of 48h, **When** the StaleWorker checks the channel, **Then** it uses 48h instead of the global default (4h) to determine if a message is stale.
3. **Given** a channel has no custom stale threshold configured, **When** the StaleWorker checks the channel, **Then** the global default of 4h is used.
4. **Given** an admin runs `synapbus channel set-stale-threshold new_posts 0`, **When** the command completes, **Then** stale detection is DISABLED for that channel (no stale notifications generated).
5. **Given** valid threshold values are `4h`, `24h`, `48h`, `72h`, or `0` (disabled), **When** an admin specifies an invalid value (e.g., `5m` or `100h`), **Then** the CLI returns a validation error listing valid options.

---

### User Story 6 - Diff-Based Channel Posting (Priority: P3)

A school-report agent posts daily attendance data to `#school-reports`. Most days, the data is identical to the previous day (no changes). The channel owner sets `dedup_mode: "content_hash"` on the channel. When the agent posts identical content within 24 hours of a previous post, the message is silently dropped and the agent receives a response with `"duplicate_suppressed": true` and a reference to the original message ID. On days when data changes, the message is posted normally.

**Why this priority**: Content deduplication is a convenience feature for specific use cases (periodic reports with infrequent changes). It is P3 because it affects a narrow set of channels and agents can implement client-side dedup as a workaround.

**Independent Test**: Can be tested by enabling content_hash dedup on a channel, posting identical messages twice within 24 hours, and verifying the second is suppressed. Post a different message and verify it is accepted.

**Acceptance Scenarios**:

1. **Given** a channel with `dedup_mode: "content_hash"`, **When** an agent posts a message with identical body to a message posted to the same channel within the last 24 hours, **Then** the message is NOT stored, and the response includes `{ "duplicate_suppressed": true, "original_message_id": <id> }`.
2. **Given** a channel with `dedup_mode: "content_hash"`, **When** an agent posts a message with a different body than any message in the last 24 hours, **Then** the message is stored normally.
3. **Given** a channel with `dedup_mode: "content_hash"` and a duplicate message was posted 25 hours ago, **When** an agent posts the same content, **Then** the message is accepted (the 24-hour dedup window has expired).
4. **Given** content hashing uses SHA-256 of the message body (trimmed, normalized whitespace), **When** two messages differ only in trailing whitespace, **Then** they are treated as duplicates.
5. **Given** a channel without `dedup_mode` set (default), **When** an agent posts duplicate content, **Then** both messages are stored normally (no dedup applied).

---

### Edge Cases

- What happens when hybrid search is requested but the fulltext index is empty (no messages yet)? The system MUST return an empty result set, not an error.
- What happens when `min_similarity` is set to 0.0? The system MUST treat it as "no filtering" and return all semantic results regardless of score.
- What happens when `min_similarity` is set to 1.0? The system MUST accept it but will likely return no results (exact matches only).
- What happens when a digest channel's cron schedule is invalid (e.g., `"every tuesday"`)? The system MUST reject the configuration with a validation error explaining expected cron format.
- What happens when `check_url_posted` is called with an invalid URL (no scheme, malformed)? The system MUST return a validation error, not a false negative.
- What happens when a message with a URL in metadata is deleted? The corresponding entry in `posted_urls` MUST be deleted (cascade).
- What happens when the stale threshold is changed while the StaleWorker is mid-cycle? The new threshold MUST take effect on the next cycle iteration (eventual consistency within one cycle period).
- What happens when content_hash dedup is enabled on a channel with existing messages? The dedup window only applies to messages sent AFTER the mode was enabled (no retroactive dedup).
- What happens when a digest is generated but the system crashes before marking queued messages as digested? On restart, the system MUST detect undigested messages and include them in the next digest (at-least-once delivery).
- What happens when an agent posts to a digest channel and immediately tries to read the message via its message ID? The message MUST be readable by ID (direct access) even though it does not appear in `read_inbox` until the digest is generated.

## Requirements *(mandatory)*

### Functional Requirements

**Hybrid Search (RRF)**
- **FR-001**: System MUST execute both semantic and fulltext searches when `search_mode: "auto"` and an embedding provider is configured, fusing results using Reciprocal Rank Fusion with k=60.
- **FR-002**: Each search result MUST include a `match_type` field with value `"semantic"`, `"fulltext"`, or `"both"`.
- **FR-003**: When all semantic results have similarity scores below 0.30, the system MUST apply a fulltext boost factor (2x weight) in the RRF fusion formula.
- **FR-004**: The existing `search_mode` values `"semantic"` and `"fulltext"` MUST continue to work as single-strategy searches (no fusion).

**Minimum Similarity Threshold**
- **FR-005**: The `search_messages` MCP tool MUST accept an optional `min_similarity` parameter (float, default 0.25) that filters semantic results below the threshold before returning.
- **FR-006**: Filtered-out result count MUST be logged at `slog.Debug` level with the threshold value.
- **FR-007**: The `min_similarity` parameter MUST apply to the semantic component only, not fulltext relevance scores.

**Daily Digest Channel Mode**
- **FR-008**: Channels MUST support a `digest_mode` boolean property (default false) and a `digest_schedule` string property (cron expression, required when `digest_mode` is true).
- **FR-009**: Messages sent to a digest-enabled channel MUST be queued silently and excluded from `read_inbox` results until the digest is generated.
- **FR-010**: The system MUST run a background goroutine that evaluates digest schedules and generates summary messages at the scheduled times.
- **FR-011**: Digest messages MUST include: total queued message count, top-N items by priority (N=5), and message IDs of all queued items.
- **FR-012**: The `send_message` response for digest channels MUST include `"queued_for_digest": true`.
- **FR-013**: Queued messages MUST remain accessible by direct message ID lookup.

**Cross-Agent URL Deduplication**
- **FR-014**: System MUST expose an MCP tool `check_url_posted` accepting a `url` string parameter and returning whether the URL has been posted, with message details if found.
- **FR-015**: System MUST maintain a `posted_urls` table with normalized URLs, auto-populated from message metadata `url` fields on insert.
- **FR-016**: URL normalization MUST strip tracking parameters (`utm_*`, `fbclid`, `gclid`, `mc_cid`, `mc_eid`, `ref`, `source`, `campaign`) and normalize known URL patterns (GitHub PR paths: strip `/files`, `/commits`, `/checks` suffixes).
- **FR-017**: The `posted_urls` entry MUST be deleted when the corresponding message is deleted (cascade delete).

**Stale Notification Tuning**
- **FR-018**: Channels MUST support a configurable `stale_threshold` property with valid values: `4h`, `24h`, `48h`, `72h`, or `0` (disabled). Default: `4h`.
- **FR-019**: The Admin CLI MUST expose `synapbus channel set-stale-threshold <channel> <duration>` command.
- **FR-020**: The StaleWorker MUST use per-channel thresholds when configured, falling back to the global default.
- **FR-021**: Stale threshold changes MUST take effect immediately without server restart.

**Diff-Based Channel Posting**
- **FR-022**: Channels MUST support a `dedup_mode` property with value `"content_hash"` or empty/null (disabled).
- **FR-023**: When `dedup_mode: "content_hash"` is enabled, messages with identical SHA-256 body hash posted to the same channel within 24 hours MUST be silently dropped.
- **FR-024**: Duplicate suppression responses MUST include `{ "duplicate_suppressed": true, "original_message_id": <id> }`.
- **FR-025**: Content hashing MUST normalize the body by trimming and collapsing whitespace before hashing.

### Key Entities

- **PostedURL**: Tracks URLs posted across all channels. Key attributes: `id`, `message_id` (FK to messages, cascade delete), `normalized_url` (string, indexed), `original_url` (string), `channel_id`, `agent_id`, `created_at`. Unique index on `normalized_url` is NOT applied (same URL can be posted to different channels), but lookups query across all channels.

- **DigestQueue**: Tracks messages queued for digest delivery. Key attributes: `message_id` (FK to messages), `channel_id`, `queued_at`, `digest_message_id` (nullable, set when digest is generated). Messages with null `digest_message_id` are pending inclusion in the next digest.

- **ChannelConfig** (extended): Existing channel entity gains new properties: `digest_mode` (boolean), `digest_schedule` (string, cron), `stale_threshold` (string, duration), `dedup_mode` (string). All nullable with sensible defaults.

## Assumptions

The following decisions were made without explicit confirmation and are documented here for review:

1. **RRF k=60**: The standard RRF parameter k=60 is used. This is the value from the original RRF paper (Cormack et al., 2009) and provides balanced fusion. The formula is: `score(d) = sum(1 / (k + rank_i(d)))` across all result sets.
2. **Default min_similarity = 0.25**: Based on empirical observation that similarity scores below 0.25 consistently represent noise in the current embedding model (text-embedding-3-small). This may need adjustment if the embedding provider changes.
3. **Digest summaries are plain text**: Digest messages use plain-text formatting, not rich/structured formatting. Agents and the Web UI render them as regular messages.
4. **URL normalization reuses standard patterns**: No custom domain-specific normalization beyond GitHub PR paths. Additional patterns (e.g., HN, Reddit) can be added later.
5. **Stale threshold changes are immediate**: The StaleWorker reads the threshold from the database on each cycle, so changes take effect without restart. No caching of threshold values.
6. **Content hash dedup window is 24h rolling**: The window is calculated from the current time minus 24 hours, not calendar-day-based. Old hashes are not cleaned up proactively; they are simply ignored by the 24h window query.
7. **Digest cron uses standard 5-field cron syntax**: `minute hour day-of-month month day-of-week`. No seconds field, no extended syntax.
8. **check_url_posted searches across ALL channels**: The dedup check is global, not scoped to a single channel. An agent posting to `#news-mcp` can discover that the URL was already posted in `#news-synapbus`.
9. **No new SQL migration numbering conflicts**: The next available migration number will be determined at implementation time based on the highest existing migration.

## Non-Goals

The following are explicitly out of scope for this specification:

1. **Agent-side search strategy changes**: This spec covers SynapBus platform changes only. How agents choose to call `search_messages` or `check_url_posted` is an agent concern, not a platform concern.
2. **Real-time digest streaming**: Digest mode generates batch summaries on a schedule. Real-time aggregation or streaming summaries are not included.
3. **URL content comparison**: `check_url_posted` checks URL identity only. It does not fetch or compare the content at the URL.
4. **Automatic duplicate rejection**: `check_url_posted` is advisory. The system does not automatically reject messages with duplicate URLs. Agents decide whether to post.
5. **Rich digest formatting**: No HTML, Markdown rendering, or structured templates for digest messages. Plain text only.
6. **Per-agent similarity thresholds**: The `min_similarity` parameter is per-query, not per-agent configuration. There is no agent-level default.
7. **Historical URL backfill**: The `posted_urls` table is populated going forward from deployment. Existing messages are not retroactively scanned for URLs.
8. **Channel-scoped URL dedup**: Dedup checks are global. A future enhancement could add `channel` scoping to `check_url_posted`, but it is not included here.
9. **Digest message editing**: Once a digest is generated, it cannot be edited or regenerated. If queued messages are deleted before the digest fires, they are simply excluded.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Hybrid search (RRF fusion) returns at least 20% more relevant results than either semantic-only or fulltext-only search, measured against a curated test set of 30 query-message pairs where relevant messages use different vocabulary than the query.
- **SC-002**: The `min_similarity` threshold filters out 100% of results below the configured threshold, with zero false rejections above the threshold.
- **SC-003**: Hybrid search adds no more than 50ms latency (p95) compared to single-strategy search, for an index of 50,000 messages.
- **SC-004**: Digest mode reduces per-channel message volume visible to human readers by 80%+ on channels with 20+ daily messages, consolidating them into a single daily digest.
- **SC-005**: `check_url_posted` correctly identifies duplicate URLs with and without tracking parameters in 100% of test cases, including GitHub PR path normalization variants.
- **SC-006**: `check_url_posted` returns results within 10ms (p99) for a `posted_urls` table containing 100,000 entries.
- **SC-007**: Per-channel stale thresholds are respected by the StaleWorker within one check cycle after configuration change (no restart required).
- **SC-008**: Content-hash dedup correctly suppresses identical messages within the 24h window with zero false positives (different content incorrectly suppressed) and zero false negatives (identical content not suppressed).
