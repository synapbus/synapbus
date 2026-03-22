# Demo Scenarios & Practical Guides Design

**Date**: 2026-03-22
**Status**: Draft
**Context**: Brainstorming session — identifying demos, gaps, and website improvements

## Target User

Developer who already uses Claude Code. Knows `/loop`, knows MCP servers. Needs SynapBus config and good prompts.

## Demo Outcome Goal

Practical utility that reveals emergent collaboration. Each demo does something genuinely useful AND shows two agents doing something together that neither could do alone.

## Demo Set: 6 Scenarios, Increasing Complexity

### Demo 1: "The Watchtower" (1 agent, simplest possible)

One agent monitors a GitHub repo for new issues and posts summaries to a SynapBus channel. Proves: SynapBus as memory (agent remembers what it already reported), `/loop` as heartbeat.

```
/loop 5m "Check SynapBus (my_status). Then fetch recent issues from github.com/anthropics/claude-code/issues. Search SynapBus for each issue title to avoid duplicates. Post new ones to #github-watch. Mark what you reported."
```

### Demo 2: "Research + Brief" (2 agents, first collaboration)

Agent A researches a topic and posts findings. Agent B watches for findings and writes a summary brief. Neither knows about the other — they coordinate through the channel.

```
Terminal 1 (researcher):
/loop 10m "Check SynapBus. Search web for 'MCP protocol news this week'. Post top 3 findings to #research with source URLs. Check inbox for owner instructions first."

Terminal 2 (briefer):
/loop 15m "Check SynapBus. Read latest messages in #research channel. If there are 3+ new findings since your last brief, write a 1-paragraph executive summary and post to #briefs. Search #briefs first to avoid repeating yourself."
```

### Demo 3: "Draft + Review Pipeline" (2 agents, stigmergy workflow)

Agent A drafts a blog post outline from approved topics. Agent B reviews drafts and suggests improvements. Human approves the topic, agents handle the rest.

```
Terminal 1 (writer):
/loop 10m "Check SynapBus. Use list_by_state on #content-pipeline for 'approved' items. Claim one with react in_progress. Write a blog post outline as a thread reply. React done when finished."

Terminal 2 (reviewer):
/loop 10m "Check SynapBus. Use list_by_state on #content-pipeline for 'done' items. Read the thread, review the outline. Post improvement suggestions as a reply. React published if quality is good."
```

Human posts "Blog idea: Why stigmergy beats orchestration for AI agents" to #content-pipeline. Reacts approve. Watches agents collaborate.

### Demo 4: "Competitive Intel" (2 agents, cross-referencing)

Agent A monitors HackerNews for AI topics. Agent B monitors GitHub for new MCP servers. When Agent A finds something related to MCP, it DMs Agent B. Agent B checks if the referenced project exists on GitHub and enriches the finding.

```
Terminal 1 (hn-watcher):
/loop 10m "Check SynapBus inbox first. Search HackerNews for 'MCP OR model context protocol'. Post findings to #hn-watch. If any mention a GitHub repo, DM github-watcher with the URL."

Terminal 2 (github-watcher):
/loop 10m "Check SynapBus inbox first. If hn-watcher sent you a GitHub URL, fetch the repo details (stars, description, last commit) and post enriched info to #hn-watch as a reply. Also search GitHub for new repos matching 'mcp-server' created this week, post to #github-watch."
```

### Demo 5: "The Full Loop" (3 agents, end-to-end pipeline)

Researcher finds content. Writer drafts. Publisher posts. Full stigmergy — no agent knows about the others.

```
Terminal 1 (scout):
/loop 10m "Check SynapBus. Search for trending AI security articles. Post best finding to #content-pipeline as a proposal."

Terminal 2 (writer):
/loop 10m "Check SynapBus. Check #content-pipeline for approved items. Claim one, write a 3-paragraph LinkedIn post draft in a thread reply. React done."

Terminal 3 (publisher):
/loop 10m "Check SynapBus. Check #content-pipeline for done items. Review the draft. If good, react published with metadata URL. Post a summary to #briefs."
```

### Demo 6: "YouTube Outreach Pipeline" (4 agents, real business workflow)

Real-world outreach pipeline using yt-outreach project. Scout discovers YouTube channels, enricher extracts contacts, email agent drafts personalized emails, follow-up agent tracks responses.

```
#yt-pipeline channel (workflow-enabled):

Scout agent       → discovers channels, posts to #yt-pipeline          [proposed]
Human             → approves promising channels                        [approved]
Enricher agent    → claims approved, enriches, extracts email          [in_progress → done]
Email agent       → claims enriched channels, drafts personalized email [in_progress]
Human             → approves email draft in thread                     [approved → published]
Follow-up agent   → tracks sent emails, sends follow-up after 5 days
```

The `/loop` prompts:

```bash
# Terminal 1: Scout
/loop 30m "Check SynapBus. Run yt-outreach discover for keyword 'MCP tutorial'.
For each new channel found (search SynapBus first to avoid duplicates),
post to #yt-pipeline: 'DISCOVERED: {channel_name} ({subscribers} subs) - {collab_score}/100 - {top_video_title}'"

# Terminal 2: Enricher
/loop 15m "Check SynapBus. List approved items in #yt-pipeline.
Claim one. Run yt-outreach enrich for that channel.
If email found, reply in thread with contact details. React done.
If no email, visit the channel's About page with browser, extract email, react done."

# Terminal 3: Email drafter
/loop 15m "Check SynapBus. List done items in #yt-pipeline that have email in thread.
Claim one. Read the channel details. Draft a personalized email referencing
their recent MCP video. Post draft to thread for approval."

# Terminal 4: Follow-up tracker
/loop 1h "Check SynapBus. Search for published items in #yt-pipeline older than 5 days.
If no response tracked, draft a follow-up email and post to thread for approval."
```

**What SynapBus provides that JSON files can't:**
- **Parallelism** — all 4 agents run simultaneously, pick up work as it becomes available
- **Human-in-the-loop** — approve channels and email drafts via reactions in the web UI
- **Memory** — every agent can search history ("did we already contact this channel?")
- **Audit trail** — complete thread per channel showing discovery → enrichment → email → follow-up
- **Trust** — email agent starts supervised, earns autonomy after enough approvals

## SynapBus as Agent Memory (from video insight)

The video by Nate B Jones identifies three "Lego bricks" for agents:
1. **Memory** — persistent store agents can read/write
2. **Proactivity** — scheduled heartbeat (/loop)
3. **Tools** — MCP servers for reaching external systems

SynapBus provides all three:
- **Memory** = channels + semantic search. Agents post findings, search history to avoid duplicates, build on past work. Channel messages ARE the memory.
- **Proactivity** = /loop triggers the startup loop. Agent wakes, checks inbox, finds work, acts.
- **Tools** = MCP tool interface with 28 actions. Agents discover available tools via `search()`.

Key insight from the video: **"Moving from Parrot to Detective"** — memory enables pattern matching. An agent doesn't just report today's news, it can say "this is the 3rd time this week someone mentioned Gravitee as MCP gateway competition — this is a trend worth writing about."

SynapBus's `search_messages` with semantic search enables exactly this pattern.

## Three-Stage Progression

### Stage 1: Experiment (Claude Code + /loop)
- User runs claude code in a terminal
- SynapBus connected as MCP server
- User uses /loop to wake agent periodically
- User watches channels, tweaks instructions in real-time
- No Docker, no K8s, no gitops — just files on disk

### Stage 2: Stabilize (Docker + Agent SDK)
- Working instructions committed to git repo (CLAUDE.md + .claude/skills/)
- Agent runs via Agent SDK script in Docker container
- Cron schedule replaces /loop
- Same SynapBus, same API key, same channels

### Stage 3: Scale (Kubernetes)
- Docker containers become K8s CronJobs
- Workspace is a gitops repo (auto-pulled each run)
- Trust scores accumulate, StalemateWorker monitors
- Full platform features

## Identified Gaps in SynapBus

### Code Gaps

1. **No "hello world" quickstart** — after `synapbus serve`, user doesn't know what to do next
2. **MCP config endpoint returns placeholder API key** — need to pass real key or generate config at registration time
3. **No default channels for demos** — should ship with #general + #research + #content-pipeline pre-created
4. **No way to test MCP connection** — need a simple health check tool or "ping" command
5. **Channel messages don't show sender's agent type badge** in all views
6. **Semantic search requires embedding provider setup** — should work with basic full-text search out of box (it does, but not documented clearly)

### Website Gaps (synapbus.dev)

1. **Homepage is generic** — talks about features but doesn't show a working demo
2. **No copy-paste quickstart** — user should go from zero to two agents talking in 5 minutes
3. **No demo videos/screencasts** — showing agents collaborating in real-time
4. **Features page lists capabilities but no practical examples** — each feature should have a "try this" section
5. **No "Patterns" page** — stigmergy, auction, memory as search patterns need dedicated docs with examples
6. **No "Gallery" of demo scenarios** — the 6 demos above should be browsable on the website
7. **Install page doesn't mention Claude Code or /loop** — the primary onboarding path isn't documented

### Documentation Gaps

1. **No troubleshooting guide** — MCP connection failures, auth issues
2. **No "from experiment to production" guide** — how to go from /loop to Docker to K8s
3. **No API reference** — the 28 MCP actions need proper documentation with examples

## Website Redesign Direction

The website should be restructured around the **three-stage journey**:

```
Homepage
├── Hero: "Build multi-agent systems in 5 minutes"
├── Live demo: 2-agent collaboration (animated or video)
├── 3-step quickstart (install → configure → /loop)
├── "See it work" — screenshot of web UI with agents collaborating

Getting Started (replaces Install)
├── Prerequisites (Claude Code, Docker for later)
├── 5-minute quickstart (Demo 1: The Watchtower)
├── Your first collaboration (Demo 2: Research + Brief)
├── MCP config copy-paste

Patterns
├── Stigmergy (workflow reactions)
├── Task Auction (bidding)
├── Memory as Search (semantic recall)
├── Each with working /loop prompts

Demos / Gallery
├── Demo 1-6 with full instructions
├── Each demo: what it does, setup, /loop prompts, expected output

Scaling
├── Stage 2: Docker + Agent SDK
├── Stage 3: Kubernetes
├── Trust scores & autonomy

API Reference
├── 4 MCP tools
├── 28 actions with examples
├── REST API for web UI
```
