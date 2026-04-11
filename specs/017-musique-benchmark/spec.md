# Feature Specification: MuSiQue Multi-Agent Benchmark Harness

**Feature Branch**: `017-musique-benchmark`
**Created**: 2026-04-11
**Status**: Draft
**Input**: MuSiQue-based MAS benchmark harness that integration-tests the 016-agent-marketplace. N=3 curated trio sharing a pivot entity, mixed-tier agent pool (Haiku + Sonnet + Opus), Pareto scoring vs single-agent baseline, optional 5-epoch learning tier, Python harness using Claude Agent SDK.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Single-shot Pareto verification (Priority: P1)

A developer wants to verify that the 016 agent marketplace actually delivers better quality-per-token than a single-agent baseline on a real multi-hop reasoning task. They run the benchmark harness in single-shot mode. The harness posts one MuSiQue 4-hop question to the marketplace, mixed-tier agents bid, one wins, executes the task using distractor paragraphs from the local MuSiQue corpus, and reports the final answer. The harness also runs the same question through a single-agent baseline (one Claude call with all distractors in context). It computes and plots both runs on a Pareto chart of quality vs tokens. The marketplace passes only if it lands strictly northwest of the baseline.

**Why this priority**: This is the irreducible proof-of-work for the marketplace. Without it, the 016 spec is unvalidated. With it, we have evidence that the auction + mixed-tier routing primitives actually produce a Pareto improvement on a real task.

**Independent Test**: Start a fresh SynapBus instance with the 016 MVP. Run `python benchmark/run.py --mode single-shot --question q1`. The script completes within a token budget, produces a results JSON with real token counts and F1 scores for both the marketplace and the baseline, and exits zero.

**Acceptance Scenarios**:

1. **Given** a fresh SynapBus instance with the 016 marketplace primitives loaded, **When** the harness posts the MuSiQue question as an auction, **Then** at least two agents submit structured bids as threaded replies within a configurable wait window.
2. **Given** bids have been submitted, **When** the harness awards the best bid via the `awarded` reaction, **Then** the winning agent receives a claim and begins executing against the MuSiQue distractor paragraphs.
3. **Given** the winning agent completes the task, **When** the harness compares the answer to the gold answer, **Then** an F1 score is recorded and the token count for the entire run is aggregated.
4. **Given** both the marketplace run and the baseline run have completed, **When** the harness generates the Pareto report, **Then** both data points are shown with their exact token counts and F1 scores, and the marketplace run is either strictly northwest of the baseline or an explicit warning is raised.

---

### User Story 2 — Curated trio with observable dedup (Priority: P2)

A developer wants to measure the shared-scratchpad duplicate-work detection primitive. The trio file contains three carefully selected MuSiQue questions sharing a pivot bridge entity (e.g., all involve the United States as an intermediate hop). When the harness runs the trio in sequence with a persistent shared scratchpad across questions, the second and third questions should hit cached entity lookups from the first, producing an observable cache-hit rate above 0%.

**Why this priority**: Dedup is one of the five MAS features the benchmark is supposed to exercise. Without the curated trio, dedup metrics are 0 at N=3 and the primitive is silent. This is P2 because US1 delivers single-question value first; the trio extends it.

**Independent Test**: Run `python benchmark/run.py --mode trio --scratchpad persistent`. The harness runs all three questions in sequence. The scratchpad stats show at least 5 cache hits on entity lookups across the three questions, and the aggregate token count is lower than 3× the single-question token count due to cache re-use.

**Acceptance Scenarios**:

1. **Given** the curated trio.jsonl exists with three questions sharing a pivot entity, **When** the harness runs the trio with persistent scratchpad, **Then** cache-hit count at the end of the run is greater than zero.
2. **Given** the same trio is run with a non-persistent scratchpad, **When** the run completes, **Then** total tokens are strictly greater than the persistent-scratchpad run.

---

### User Story 3 — Learning tier with reputation convergence (Priority: P3)

A developer wants to verify that reputation and reflection actually cause improvement over time. The learning tier runs the same trio for 5 epochs. Reputation and capability manifests persist; scratchpad resets per epoch. Tokens-per-correct-answer should decline monotonically across epochs as the marketplace learns which model tier is best for which sub-task.

**Why this priority**: This is the full marketplace proof-of-learning. It is P3 because US1 and US2 are load-bearing; the learning tier depends on both being stable first, and it is the most token-expensive mode (~500k tokens per full run).

**Independent Test**: Run `python benchmark/run.py --mode learning --epochs 5`. The harness completes all 5 epochs, writes a results JSON containing per-epoch tokens and F1, and plots a learning curve showing the tokens-per-correct-answer trend.

**Acceptance Scenarios**:

1. **Given** the learning tier has run 5 epochs on the trio, **When** the per-epoch metrics are plotted, **Then** the tokens-per-correct-answer for epoch 5 is less than epoch 1.
2. **Given** between-epoch state, **When** epoch N starts, **Then** reputation entries from epoch N-1 are present and influence bid scoring, while the scratchpad is empty.

---

### User Story 4 — Rich HTML report (Priority: P1)

The benchmark run produces a rich, self-contained HTML report that shows the task, the decomposition, the bids, the award, the execution trace, the gold answer, the marketplace answer, the baseline answer, the Pareto plot, and all token counts. Non-technical readers can open the file and understand what happened.

**Why this priority**: The benchmark has to serve as a compelling illustration for the `multiagent_systems_report.html`. Without a rendered report, the run is just a JSON file nobody reads. It is P1 because without it the benchmark fails its dual-use requirement.

**Independent Test**: After a successful run, `results/latest.html` exists, opens in a browser without errors, and visibly contains the question text, the Pareto plot (as SVG or inline data), and the per-run token counts.

**Acceptance Scenarios**:

1. **Given** a completed benchmark run, **When** the report generator runs, **Then** `results/latest.html` is written and contains the question, decomposition, answers, tokens, and Pareto data.
2. **Given** the HTML report is opened in a browser, **When** a reader scrolls through it, **Then** they can tell whether the marketplace passed without needing to open any JSON files.

---

### Edge Cases

- **MuSiQue download failure**: the setup script retries once, then fails with a clear error pointing at the canonical URL.
- **No agents bid within window**: the harness declares auction-timeout, records it as a test failure, and exits non-zero.
- **Agent returns malformed bid**: the bid is rejected by schema validation, the agent is not awarded the task, other bidders still compete.
- **Winner exceeds budget**: the harness hard-stops execution at 100% of declared budget, records the auto-fail, baseline wins the Pareto comparison.
- **Scratchpad cache conflict**: two sub-agents write different values for the same entity key — last-writer-wins with a logged warning; the answer reflects the final state.
- **Single-agent baseline also fails**: this is a valid outcome and means the question is genuinely beyond frontier model capability; the report marks the question as such and the benchmark still reports its tokens.
- **Learning tier plateau**: if epoch 5 is not strictly better than epoch 1, the report raises a warning and the test fails.
- **Model API rate limit**: the harness retries with exponential backoff, up to 3 attempts per call.
- **Claude Agent SDK unavailable**: falls back to direct Anthropic SDK calls with manual tool-use formatting.

## Requirements *(mandatory)*

### Functional Requirements

**Harness Core**

- **FR-001**: The harness MUST accept a `--mode` flag with values `single-shot`, `trio`, or `learning`.
- **FR-002**: The harness MUST read a configuration file defining agent pool composition (model tiers, initial skill cards).
- **FR-003**: The harness MUST talk to SynapBus via the existing MCP protocol using the 016 marketplace tools.
- **FR-004**: The harness MUST record per-run metrics in a structured JSON file under `results/`.

**Dataset Preparation**

- **FR-005**: A setup script MUST download the MuSiQue-Ans dev set from the canonical GitHub release.
- **FR-006**: A curation script MUST select three questions from the 4-hop subset that share a pivot bridge entity and write them to `benchmark/trio.jsonl` with gold answers and decomposition DAGs.
- **FR-007**: The curation rule MUST be deterministic with a fixed seed so the trio is reproducible.

**Agent Runner**

- **FR-008**: Agents MUST run as isolated Python processes using the Claude Agent SDK where available; when the SDK is unavailable, direct Anthropic SDK calls MUST be used as fallback.
- **FR-009**: Each agent MUST publish a capability manifest to SynapBus wiki at startup.
- **FR-010**: Each agent MUST poll the auction channel and submit bids when it sees a task in a declared domain.
- **FR-011**: Each agent MUST respect the max_budget_tokens of its awarded tasks and hard-stop at 100% of budget.

**Baseline**

- **FR-012**: The harness MUST run a single-agent baseline for every question, passing all 20 distractor paragraphs plus the question in one prompt with chain-of-thought instructions.
- **FR-013**: The baseline MUST record its token count and final F1 so the Pareto comparison is apples-to-apples.

**Scoring**

- **FR-014**: For each question, the harness MUST compute exact-match F1 against the gold answer string after normalization (lowercasing, punctuation removal, article stripping).
- **FR-015**: The harness MUST compute decomposition-F1 by comparing the marketplace's actual sub-questions with the gold DAG.
- **FR-016**: The harness MUST record total tokens consumed per run, separated by orchestrator, sub-agents, and baseline.
- **FR-017**: The harness MUST compute tokens-per-correct-answer as `total_tokens / max(correct_count, 1)`.
- **FR-018**: The harness MUST report a Pareto verdict: `PASS` if the marketplace is strictly northwest of the baseline, `FAIL` otherwise.

**Report Generation**

- **FR-019**: After every run, the harness MUST generate a self-contained HTML report at `results/{run_id}.html` showing the question, decomposition, bids, award, answer, gold, tokens, Pareto plot, and verdict.
- **FR-020**: The report MUST be readable without requiring any external server or CDN.

**Learning Tier (P3, deferred for MVP)**

- **FR-021**: In learning mode, the harness MUST run the trio for N epochs with reputation and skill-card state persisting across epochs.
- **FR-022**: In learning mode, the scratchpad MUST reset between epochs.
- **FR-023**: The learning report MUST plot tokens-per-correct-answer across epochs.

### Key Entities

- **Question record**: One entry in trio.jsonl. Contains `id`, `question`, `gold_answer`, `gold_decomposition` (list of sub-questions), `distractor_paragraphs` (20 Wikipedia snippets), `pivot_entity`.
- **Agent config**: One agent in the pool. Contains `name`, `model_id`, `system_prompt_template`, `skill_card_markdown`, `domains`, `initial_confidence_per_domain`, `initial_cost_per_domain`.
- **Run result**: One benchmark run output. Contains `run_id`, `timestamp`, `mode`, `per_question_results`, `baseline_results`, `pareto_verdict`, `total_tokens`, `wall_time_seconds`.
- **Scratchpad entry**: One cached `(entity, attribute, value)` tuple with a `cache_hit_count` counter.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a fresh SynapBus instance with 016 MVP loaded, `python benchmark/run.py --mode single-shot --question q1` completes successfully within 10 minutes wall-time and 150k total tokens.
- **SC-002**: The Pareto verdict on the single-shot run is `PASS` — the marketplace lands strictly northwest of the Sonnet-baseline point.
- **SC-003**: The HTML report is generated, is self-contained (no external asset fetches), and visibly shows the question, both answers, tokens, and Pareto plot.
- **SC-004**: The benchmark can be re-run deterministically on the same machine and produce the same Pareto verdict (token counts may vary ±5% due to sampling, but verdict must be stable).
- **SC-005**: The benchmark identifies and reports per-agent, per-domain reputation entries after the run, writable back to SynapBus.
- **SC-006**: In trio mode, the scratchpad cache hit count is greater than zero — the dedup primitive is observably exercised.
- **SC-007** (deferred): In learning mode, tokens-per-correct-answer declines from epoch 1 to epoch 5 by at least 15%.

## Assumptions

- The 016-agent-marketplace MVP is implemented before this benchmark runs, or this benchmark provides a mock marketplace stub for early validation.
- MuSiQue-Ans is available at its canonical GitHub release URL and is downloadable without authentication.
- Anthropic API access is available via `ANTHROPIC_API_KEY` environment variable.
- Claude Agent SDK may or may not be available in the target environment; the harness degrades gracefully to direct Anthropic SDK if not.
- Tokens reported by the Anthropic SDK are trusted as ground truth.
- F1 computed via string normalization is sufficient for MVP; semantic similarity matching is future work.
- The MuSiQue license (CC BY 4.0) permits redistribution of the curated trio file as a derivative work.

## Out of Scope

- Training any model or fine-tuning.
- Non-English questions (MuSiQue is English-only).
- Multi-modal questions (images, audio).
- FRAMES cross-eval — scheduled as a follow-up once MuSiQue pipeline is stable.
- Adversarial or byzantine agent behavior; agents in the pool are assumed cooperative.
- Replacing the MuSiQue corpus with a live Wikipedia API.
- Multi-run statistical power analysis; N=3 is mechanism verification only.
