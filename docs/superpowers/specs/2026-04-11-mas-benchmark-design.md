# MAS Benchmark — Design Document

**Date**: 2026-04-11
**Status**: Approved (autonomous mode)
**Author**: Claude Opus 4.6 (1M context) via brainstorming skill
**Next**: speckit specification at `specs/017-musique-benchmark/spec.md`
**Related**: `specs/016-agent-marketplace/spec.md`

## Problem

The current "Fermi piano tuners in Chicago" example in `multiagent_systems_report.html` is dated and rare as a profession. It needs to be replaced with a modern task that:

- Exercises the same MAS features (dynamic decomposition, dedup, uncertainty aggregation, cost accounting, orphaned-spawn recovery)
- Runs on local data only — no `WebSearch` tool required
- Serves both as a readable narrative example and a runnable integration test
- Measures the Pareto frontier of quality vs token cost — never quality alone
- Fits a tight dev-loop token budget (≤ 500k tokens per full learning run)

## Decisions (from brainstorming)

1. **Purpose**: dual-use — narrative example in the report AND integration test for `016-agent-marketplace`.
2. **Dataset**: **MuSiQue-Ans 4-hop** as primary (~100 MB, gold decomposition DAGs, anti-shortcut filtered). **FRAMES** as future cross-eval runner-up.
3. **Scale**: N=3 curated questions. Deliberately cherry-picked to share a bridge entity so sub-agents naturally re-lookup the same Wikipedia paragraphs (dedup metric becomes observable at N=3).
4. **Agent pool**: **mixed-tier** — Haiku + Sonnet + Opus. Each agent publishes its own capability manifest with per-domain cost profile. The auction has to learn when paying for Opus is worth it and when Haiku suffices.
5. **Run modes — tiered**:
   - **Single-shot** (CI smoke test): run the 3 questions once. ~100k tokens.
   - **Learning tier**: run the same 3 questions for 5 epochs. Reputation and skill cards persist; scratchpad resets per epoch. Measure tokens-per-correct-answer declining across epochs. ~500k tokens.
6. **Execution strategy**: harness talks to the spec-016 MCP tool surface. Runs against real SynapBus once 016 is implemented.
7. **Scoring is Pareto**: report both quality (F1, decomposition F1) AND cost (total tokens, tokens-per-correct). A passing marketplace strictly dominates a single-agent baseline.

## Architecture

### Components

1. **Curated trio file** (`benchmark/trio.jsonl`) — three MuSiQue-Ans questions with shared pivot entity, gold answers, gold decomposition DAGs. Selected by deterministic rule from the dev set and checked into the repo for reproducibility.

2. **Benchmark harness** (Python) — orchestrates a run:
   - Reads trio.jsonl and initial skill-card configuration
   - Seeds the marketplace (posts skill-card wiki articles for each agent, creates the `#bench-auction` auction channel)
   - For each question, posts an auction, waits for bids, awards via MCP, polls for completion
   - Collects metrics (per-question tokens, F1, cache-hit rate, decomposition F1, wall time)
   - Runs single-shot or 5-epoch learning tier per CLI flag

3. **Agent runner** (Python) — spawns N agents, each a Claude Agent SDK session with:
   - A system prompt built from the agent's skill card
   - SynapBus MCP tools configured
   - Distinct model tier (Haiku / Sonnet / Opus)
   - Token counter hook for real-time budget enforcement

4. **Baseline runner** — a single Claude call that receives the question and all 20 distractor paragraphs in one shot, using chain-of-thought, no decomposition, no marketplace. Produces reference `(tokens, F1)` for Pareto comparison.

5. **Scoring module** — computes metrics per run, writes `results/{run_id}.json`, generates Pareto plot data.

6. **Report generator** — renders a rich HTML with the narrative, Pareto chart, per-question trace, and learning curve.

### Data flow (single question)

```
trio.jsonl → harness.post_auction(question, max_budget, domains)
                            ↓
                SynapBus auction channel (reactive trigger)
                            ↓
         ┌────────────┬────────────┬──────────────┐
         ↓            ↓            ↓              ↓
    Haiku agent   Sonnet agent  Opus agent   (poller)
         ↓            ↓            ↓
       bid()        bid()        bid()
         └────────────┴────────────┘
                      ↓
              harness.award(best bid)
                      ↓
              winner.claim → execute
                      ↓
          (reads distractor paragraphs via MCP)
                      ↓
             shared scratchpad
          (dedup: same entity → cache hit)
                      ↓
              winner.mark_done(answer, tokens)
                      ↓
        reputation ledger update (per-domain tuple)
                      ↓
           harness.score(answer vs gold)
```

### Scoring (Pareto)

Three metrics plotted together, one point per run configuration:

- **Quality**: final-answer exact-match F1 (0.0 / 0.33 / 0.67 / 1.0 at N=3)
- **Cost**: total tokens consumed (orchestrator + all sub-agents across all 3 questions)
- **Efficiency**: tokens-per-correct-answer = total_tokens / max(F1 × 3, 1)

Four configurations plotted on the Pareto chart:

| # | Config | Expected quality | Expected cost |
|---|---|---|---|
| 1 | Single-agent baseline (Opus, all distractors in context) | high (~2/3) | high (~30k) |
| 2 | Single-agent baseline (Sonnet, same) | medium (~2/3) | medium (~15k) |
| 3 | Naive marketplace (no reputation, no reflection, no dedup) | medium (~2/3) | medium-high (~40k) |
| 4 | Full 016 marketplace (reputation + dedup + mixed-tier routing) | ≥ baseline | should be **strictly less** than baseline |

The marketplace passes only if it lands **strictly northwest** of Sonnet baseline on the Pareto plot.

### Learning tier

5 epochs of the same 3 questions. What persists between epochs:

- Reputation ledger entries (accumulate)
- Capability manifest revisions (reflection loop proposes diffs — auto-approved for benchmark)
- Per-agent skill-card example-tasks list (grows monotonically)

What resets between epochs:

- Shared scratchpad (within-task coordination, not long-term memory)
- Auction channel contents (each epoch creates fresh auctions)

**Expected learning curve**: tokens-per-correct-answer should drop monotonically from epoch 1 (all agents uncalibrated, ε-greedy bootstrap dominates) to epoch 5 (reputation converged, routing stable). If it doesn't — the marketplace has a bug.

### Failure injection

For orphaned-spawn recovery: one epoch runs with a 10% random sub-agent failure rate (agents randomly return "timeout" instead of bid). Measure accuracy degradation. Target: ≤ 5 percentage-point drop.

## Realistic MVP scope

Given execution constraints, the MVP for **today's autonomous run** scopes down:

- **Questions**: N=1 instead of N=3 (save 3× tokens on the actual run; the trio.jsonl file still contains all 3 for future runs)
- **Agents**: 2 (Haiku + Sonnet) instead of 3 (Haiku + Sonnet + Opus). Mixed-tier proved on 2 tiers.
- **Epochs**: 1 single-shot run, no learning tier. Design doc describes the 5-epoch protocol for future runs.
- **Reflection loop**: skipped. Full 016 spec has it; MVP implementation focuses on US1 + US2 + US3 (auction + manifests + reputation).

**Still measured and reported**:
- Dynamic decomposition on one real 4-hop MuSiQue question
- Auction → bid → award → claim → done full lifecycle
- Per-model cost differentials (Haiku vs Sonnet on same task)
- Pareto comparison against single-agent baseline
- Reputation ledger write-through

**Documented-but-deferred**:
- Reflection loop + skill-card diff proposals (US4 of spec 016)
- Tombstoning on failure rate (FR-020a/b)
- Full 5-epoch learning tier
- 3-question curated trio dedup measurement
- FRAMES cross-eval

## Acceptance criteria for autonomous run

1. `016-agent-marketplace` MVP compiles, passes its own Go tests, and exposes the required MCP tools.
2. Benchmark harness downloads MuSiQue, curates trio.jsonl, runs 1 question end-to-end against local SynapBus with the 016 implementation.
3. Real token counts and real F1 recorded.
4. Pareto plot generated comparing full marketplace vs Sonnet baseline.
5. HTML report renders with live numbers, not placeholders.
6. `autonomous_summary.md` written documenting what shipped, what passed, what deferred.

## Honest caveats

- N=1 cannot support statistical claims. The benchmark's purpose at this scale is **mechanism verification**, not efficacy proof.
- Claude Agent SDK integration is a known pain point — may need fallback to direct Anthropic SDK if MCP wiring fails.
- Single-epoch run cannot show the learning curve. Design doc + spec describe the full protocol for future scaling.
