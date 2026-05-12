// Dream-worker (consolidator) for feature 020 — periodically scans
// memory channels per-owner, evaluates trigger watermarks and the
// daily deep-pass schedule, and dispatches consolidation jobs to a
// Claude Code agent via the harness.
//
// Importantly: the worker NEVER sends a system DM. Per
// feedback_system_dm_no_trigger.md, system DMs would trigger reactive
// runs which would cascade through the stalemate worker. The harness
// dispatch path is the contractual non-DM route — see R1.
//
// To avoid an import cycle (harness imports messaging), the worker
// accepts a minimal HarnessDispatcher interface that mirrors the
// fragment of harness.Registry it needs. The cmd/synapbus wiring at
// startup adapts harness.Registry to this interface.
package messaging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DreamAgent is a minimal record passed to the harness dispatcher.
// We can't import internal/agents here (cycle: agents → messaging
// already) so the worker uses an interface-typed value for the agent
// record and the cmd/synapbus adapter unboxes it. This keeps the
// dependency direction (harness → messaging, agents → messaging)
// intact.
type DreamAgent interface {
	// AgentName returns the agent's stable SynapBus name.
	AgentName() string
}

// DreamAgentNamed is a tiny convenience wrapper for tests and the
// admin path that need a DreamAgent with just a name.
type DreamAgentNamed struct{ Name string }

// AgentName implements DreamAgent.
func (a DreamAgentNamed) AgentName() string { return a.Name }

// HarnessDispatcher is the minimal slice of harness.Registry the worker
// needs. cmd/synapbus wires a real registry behind this interface.
type HarnessDispatcher interface {
	Execute(ctx context.Context, agent DreamAgent, req *HarnessExecRequest) (*HarnessExecResult, error)
}

// HarnessExecRequest mirrors harness.ExecRequest's Env-bearing fields.
// We don't re-export the full struct to avoid pulling all of harness
// into messaging. The adapter in cmd/synapbus translates 1:1.
type HarnessExecRequest struct {
	RunID        string
	AgentName    string
	Agent        DreamAgent
	Env          map[string]string
	MaxWallClock time.Duration
	Body         string // populated into ExecRequest.Message if non-empty
}

// HarnessExecResult mirrors the fields the worker reads from
// harness.ExecResult.
type HarnessExecResult struct {
	ExitCode  int
	Logs      string
	TokensIn  int64
	TokensOut int64
}

// AgentLookup resolves an agent record by name. Implemented by
// agents.AgentService — declared as an interface here to avoid a
// hard dependency.
type AgentLookup interface {
	GetAgent(ctx context.Context, name string) (DreamAgent, error)
}

// OwnerLister returns the list of distinct owner_ids that have at
// least one recent memory-channel message worth scanning.
type OwnerLister func(ctx context.Context, db *sql.DB) ([]string, error)

// ConsolidatorWorker periodically evaluates per-owner triggers and
// dispatches dream-agent runs.
type ConsolidatorWorker struct {
	db        *sql.DB
	jobs      *JobsStore
	tokens    *DispatchTokenStore
	harness   HarnessDispatcher
	agentLook AgentLookup
	cfg       MemoryConfig
	logger    *slog.Logger

	// usage + gate provide the per-(owner, day) circuit breaker.
	// Optional: nil → all dispatches allowed. Wired by SetUsageGate.
	usage *DreamUsageStore
	gate  *UsageGate

	// Optional owner enumerator. Defaults to a query over `agents`.
	ownerLister OwnerLister

	// Optional injection cleanup hook. When non-nil, the worker calls
	// it once per hour. Leave nil if the stalemate worker is already
	// handling injection cleanup (the default in main.go).
	injections *MemoryInjections

	// sem caps simultaneous Execute calls.
	sem chan struct{}

	done chan struct{}
	wg   sync.WaitGroup
}

// NewConsolidatorWorker builds a worker. Required: db, jobs, tokens,
// harness, agentLook. The semaphore is sized from cfg.DreamMaxConcurrent.
func NewConsolidatorWorker(
	db *sql.DB,
	jobs *JobsStore,
	tokens *DispatchTokenStore,
	harness HarnessDispatcher,
	agentLook AgentLookup,
	cfg MemoryConfig,
) *ConsolidatorWorker {
	if cfg.DreamMaxConcurrent <= 0 {
		cfg.DreamMaxConcurrent = 1
	}
	return &ConsolidatorWorker{
		db:        db,
		jobs:      jobs,
		tokens:    tokens,
		harness:   harness,
		agentLook: agentLook,
		cfg:       cfg,
		logger:    slog.Default().With("component", "consolidator-worker"),
		sem:       make(chan struct{}, cfg.DreamMaxConcurrent),
		done:      make(chan struct{}),
	}
}

// SetInjectionCleanup registers a memory_injections store the worker
// will Cleanup hourly. Pass nil to disable. The default main.go wiring
// leaves the stalemate worker handling injection cleanup and skips
// this.
func (w *ConsolidatorWorker) SetInjectionCleanup(store *MemoryInjections) {
	w.injections = store
}

// SetOwnerLister overrides the default owner enumerator (handy in
// tests).
func (w *ConsolidatorWorker) SetOwnerLister(fn OwnerLister) {
	w.ownerLister = fn
}

// SetUsageGate wires the per-(owner, day) circuit breaker. When set,
// the worker calls gate.Allow() before each dispatch and records a
// `circuit_broken` job + skips Execute when the gate denies. Token
// usage from successful runs is fed back into store so the next gate
// evaluation reflects today's spend. Pass (nil, nil) to disable.
func (w *ConsolidatorWorker) SetUsageGate(store *DreamUsageStore, gate *UsageGate) {
	w.usage = store
	w.gate = gate
}

// Start launches the ticker goroutine.
func (w *ConsolidatorWorker) Start() {
	w.wg.Add(1)
	go w.runLoop()
}

// Stop halts the worker. Idempotent.
func (w *ConsolidatorWorker) Stop() {
	select {
	case <-w.done:
		return
	default:
	}
	close(w.done)
	w.wg.Wait()
}

func (w *ConsolidatorWorker) runLoop() {
	defer w.wg.Done()
	w.logger.Info("consolidator worker started",
		"interval", w.cfg.DreamInterval.String(),
		"watermark", w.cfg.DreamWatermark,
		"max_concurrent", w.cfg.DreamMaxConcurrent,
		"wallclock_budget", w.cfg.DreamWallclockBudget.String(),
	)

	ticker := time.NewTicker(w.cfg.DreamInterval)
	defer ticker.Stop()

	// Track when we last ran the daily deep pass. TODO: parse
	// cfg.DreamDeepCron instead of hardcoding 03:00 UTC daily — adding
	// robfig/cron would add a non-zero-CGO dependency and the spec
	// authorizes this stub.
	var lastDeepPass time.Time
	var lastHourlyCleanup time.Time

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			w.tick(ctx, &lastDeepPass, &lastHourlyCleanup)
			cancel()
		case <-w.done:
			w.logger.Info("consolidator worker stopped")
			return
		}
	}
}

// tick performs one evaluation pass.
func (w *ConsolidatorWorker) tick(ctx context.Context, lastDeepPass, lastHourlyCleanup *time.Time) {
	now := time.Now().UTC()

	// Hourly: injection cleanup, only if explicitly registered AND
	// stalemate isn't already handling it (this is the safer default —
	// see SetInjectionCleanup docs).
	if w.injections != nil && now.Sub(*lastHourlyCleanup) >= time.Hour {
		if deleted, err := w.injections.Cleanup(ctx, 24*time.Hour); err == nil && deleted > 0 {
			w.logger.Info("memory_injections cleanup", "deleted", deleted)
		}
		*lastHourlyCleanup = now
	}

	// Per-owner trigger evaluation.
	owners, err := w.listOwners(ctx)
	if err != nil {
		w.logger.Warn("list owners failed", "error", err)
		return
	}
	deepPassDue := isDeepPassDue(now, *lastDeepPass)
	for _, owner := range owners {
		// Watermark triggers: reflection + link_gen + dedup_contradiction.
		if count, err := w.unprocessedCount(ctx, owner); err == nil && count >= w.cfg.DreamWatermark {
			w.tryDispatch(ctx, owner, JobTypeReflection, fmt.Sprintf("watermark:%d", count))
			w.tryDispatch(ctx, owner, JobTypeLinkGen, fmt.Sprintf("watermark:%d", count))
			w.tryDispatch(ctx, owner, JobTypeDedupContradiction, fmt.Sprintf("watermark:%d", count))
		}
		// Daily deep pass: sleep_time_rewrite (mapped to core_rewrite).
		// Skip when no agent owned by `owner` has produced any message
		// in the configured recent-window — there's nothing to refresh
		// the core blob from.
		if deepPassDue {
			window := w.cfg.DreamRecentWindow
			if window <= 0 {
				window = 14 * 24 * time.Hour
			}
			since := time.Now().UTC().Add(-window)
			if ownerActiveSince(ctx, w.db, owner, since) {
				w.tryDispatch(ctx, owner, JobTypeCoreRewrite, "cron:nightly")
			} else {
				w.logger.Debug("core_rewrite skipped — owner has no recent activity",
					"owner_id", owner, "since", since,
				)
			}
		}
	}
	if deepPassDue {
		*lastDeepPass = now
	}
}

// isDeepPassDue returns true when "now" is past 03:00 UTC of the same
// day AND lastDeepPass was earlier than that 03:00 mark. Hardcoded
// 03:00 UTC per the cron-stub TODO above.
func isDeepPassDue(now, last time.Time) bool {
	threeAM := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)
	if now.Before(threeAM) {
		return false
	}
	return last.Before(threeAM)
}

// listOwners returns the distinct owner_ids with at least one agent.
// Override via SetOwnerLister in tests.
func (w *ConsolidatorWorker) listOwners(ctx context.Context) ([]string, error) {
	if w.ownerLister != nil {
		return w.ownerLister(ctx, w.db)
	}
	rows, err := w.db.QueryContext(ctx,
		`SELECT DISTINCT CAST(owner_id AS TEXT) FROM agents WHERE owner_id > 0`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// unprocessedCount returns how many memory-channel messages exist for
// this owner that are newer than the most-recent succeeded job's
// finished_at. (Cheap approximation of "haven't been seen by the dream
// agent yet"; the worker errs toward over-dispatching, the
// partial-unique index gates duplicates anyway.)
func (w *ConsolidatorWorker) unprocessedCount(ctx context.Context, ownerID string) (int, error) {
	channels, err := MemoryChannelIDs(ctx, w.db)
	if err != nil || len(channels) == 0 {
		return 0, err
	}
	placeholders := ""
	args := []any{}
	for i, id := range channels {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, id)
	}
	// Window: messages newer than the latest completed reflection job
	// for this owner.
	var lastFinished sql.NullTime
	_ = w.db.QueryRowContext(ctx,
		`SELECT MAX(finished_at) FROM memory_consolidation_jobs
		  WHERE owner_id = ? AND job_type = ? AND status IN ('succeeded','partial')`,
		ownerID, JobTypeReflection,
	).Scan(&lastFinished)

	since := time.Time{}
	if lastFinished.Valid {
		since = lastFinished.Time
	}
	// Cap "since" at the configured 14d (default) recency window so we
	// never sweep historical pool — the worker only consolidates
	// recent activity per the T3 contract.
	window := w.cfg.DreamRecentWindow
	if window <= 0 {
		window = 14 * 24 * time.Hour
	}
	windowStart := time.Now().UTC().Add(-window)
	if since.Before(windowStart) {
		since = windowStart
	}
	args = append(args, ownerID, since)
	q := `SELECT COUNT(*)
	        FROM messages m JOIN agents a ON m.from_agent = a.name
	       WHERE m.channel_id IN (` + placeholders + `)
	         AND CAST(a.owner_id AS TEXT) = ?
	         AND m.created_at > ?`
	var count int
	err = w.db.QueryRowContext(ctx, q, args...).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// ForceRun bypasses watermark/cron triggers and dispatches one job
// for (ownerID, jobType) immediately. Used by the admin CLI
// `synapbus memory dream-run` command. Returns the created job_id (or
// the existing in-flight one).
//
// The circuit breaker still applies — admins who want to override
// must clear today's usage row directly. Manual override on top of a
// blown budget defeats the safety net.
func (w *ConsolidatorWorker) ForceRun(ctx context.Context, ownerID, jobType string) (int64, error) {
	if w.gate != nil {
		allowed, reason, _ := w.gate.Allow(ctx, ownerID)
		if !allowed {
			jobID, cerr := w.jobs.Create(ctx, ownerID, jobType, "circuit_broken:"+reason)
			if cerr == nil {
				_ = w.jobs.Complete(ctx, jobID, JobStatusCircuitBroken, "circuit_broken: "+reason, "")
				if w.usage != nil {
					_ = w.usage.RecordCompletion(ctx, ownerID, 0, 0, JobStatusCircuitBroken)
				}
				recordCircuitBrokenMetric(ownerID, jobType, reason)
				recordJobMetric(ownerID, jobType, JobStatusCircuitBroken)
				return jobID, fmt.Errorf("circuit broken: %s", reason)
			}
			return 0, fmt.Errorf("circuit broken: %s", reason)
		}
	}
	jobID, err := w.jobs.Create(ctx, ownerID, jobType, "manual:"+ownerID)
	if err != nil {
		if errors.Is(err, ErrJobAlreadyInFlight) {
			if active, _ := w.jobs.ActiveJob(ctx, ownerID, jobType); active != nil {
				return active.ID, nil
			}
		}
		return 0, err
	}
	if w.usage != nil {
		_ = w.usage.RecordStart(ctx, ownerID)
	}
	tok, _, err := w.tokens.Issue(ctx, ownerID, jobID)
	if err != nil {
		_ = w.jobs.Complete(ctx, jobID, JobStatusFailed, "", "token issue: "+err.Error())
		return 0, fmt.Errorf("issue token: %w", err)
	}
	agent, err := w.agentLook.GetAgent(ctx, w.cfg.DreamAgent)
	if err != nil || agent == nil {
		_ = w.jobs.Complete(ctx, jobID, JobStatusFailed, "", "dream agent not found")
		return 0, fmt.Errorf("dream agent %q not found: %w", w.cfg.DreamAgent, err)
	}
	runID := uuid.NewString()
	if err := w.jobs.Dispatch(ctx, jobID, runID, tok); err != nil {
		_ = w.jobs.Complete(ctx, jobID, JobStatusFailed, "", "dispatch flip: "+err.Error())
		return 0, fmt.Errorf("dispatch flip: %w", err)
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		select {
		case w.sem <- struct{}{}:
			defer func() { <-w.sem }()
		case <-w.done:
			return
		}
		w.runJob(ownerID, jobID, jobType, tok, runID, agent)
	}()
	return jobID, nil
}

// tryDispatch attempts to create+dispatch one job. Idempotent —
// ErrJobAlreadyInFlight is logged at debug and skipped.
func (w *ConsolidatorWorker) tryDispatch(ctx context.Context, ownerID, jobType, trigger string) {
	// Circuit breaker: skip when today's per-(owner) usage exceeds any
	// configured limit. We still create+complete a job row so the
	// audit log records the attempt; this also makes the
	// circuit_broken_total metric easy to chart.
	if w.gate != nil {
		allowed, reason, gerr := w.gate.Allow(ctx, ownerID)
		if gerr != nil {
			w.logger.Warn("usage gate check failed; failing open",
				"owner_id", ownerID, "job_type", jobType, "error", gerr,
			)
		} else if !allowed {
			w.logger.Warn("dream dispatch circuit broken",
				"owner_id", ownerID, "job_type", jobType, "reason", reason,
			)
			jobID, cerr := w.jobs.Create(ctx, ownerID, jobType, "circuit_broken:"+reason)
			if cerr != nil {
				if !errors.Is(cerr, ErrJobAlreadyInFlight) {
					w.logger.Warn("circuit-broken job create failed",
						"owner_id", ownerID, "job_type", jobType, "error", cerr,
					)
				}
				if w.usage != nil {
					_ = w.usage.RecordCompletion(ctx, ownerID, 0, 0, JobStatusCircuitBroken)
				}
				recordCircuitBrokenMetric(ownerID, jobType, reason)
				return
			}
			_ = w.jobs.Complete(ctx, jobID, JobStatusCircuitBroken, "circuit_broken: "+reason, "")
			if w.usage != nil {
				_ = w.usage.RecordCompletion(ctx, ownerID, 0, 0, JobStatusCircuitBroken)
			}
			recordCircuitBrokenMetric(ownerID, jobType, reason)
			recordJobMetric(ownerID, jobType, JobStatusCircuitBroken)
			return
		}
	}
	jobID, err := w.jobs.Create(ctx, ownerID, jobType, trigger)
	if err != nil {
		if errors.Is(err, ErrJobAlreadyInFlight) {
			w.logger.Debug("job already in flight; skipping",
				"owner_id", ownerID, "job_type", jobType,
			)
			return
		}
		w.logger.Warn("create job failed",
			"owner_id", ownerID, "job_type", jobType, "error", err,
		)
		return
	}
	if w.usage != nil {
		_ = w.usage.RecordStart(ctx, ownerID)
	}
	tok, _, err := w.tokens.Issue(ctx, ownerID, jobID)
	if err != nil {
		w.logger.Warn("issue token failed", "job_id", jobID, "error", err)
		_ = w.jobs.Complete(ctx, jobID, JobStatusFailed, "", "token issue: "+err.Error())
		return
	}

	// Resolve the dream-agent record (e.g. claude-code) so the
	// harness can pick the right backend.
	agent, err := w.agentLook.GetAgent(ctx, w.cfg.DreamAgent)
	if err != nil || agent == nil {
		w.logger.Warn("dream agent not found",
			"agent", w.cfg.DreamAgent, "error", err,
		)
		_ = w.jobs.Complete(ctx, jobID, JobStatusFailed, "", "dream agent not found")
		return
	}

	runID := uuid.NewString()
	if err := w.jobs.Dispatch(ctx, jobID, runID, tok); err != nil {
		w.logger.Warn("dispatch flip failed", "job_id", jobID, "error", err)
		_ = w.jobs.Complete(ctx, jobID, JobStatusFailed, "", "dispatch flip: "+err.Error())
		return
	}

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		select {
		case w.sem <- struct{}{}:
			defer func() { <-w.sem }()
		case <-w.done:
			return
		}
		w.runJob(ownerID, jobID, jobType, tok, runID, agent)
	}()
}

// runJob calls the harness and updates the job status.
func (w *ConsolidatorWorker) runJob(ownerID string, jobID int64, jobType, tok, runID string, agent DreamAgent) {
	wallclock := w.cfg.DreamWallclockBudget
	if wallclock <= 0 {
		wallclock = 10 * time.Minute
	}
	// The harness Execute is bounded by wallclock; the surrounding
	// ctx adds a tiny epsilon so the dispatcher loop sees the
	// timeout fire and produces a 'partial' status.
	ctx, cancel := context.WithTimeout(context.Background(), wallclock)
	defer cancel()

	// Lease the row so the deep-link Web UI can show it as 'running'.
	leaseUntil := time.Now().Add(wallclock).UTC()
	if err := w.jobs.Lease(ctx, jobID, leaseUntil); err != nil {
		w.logger.Warn("lease failed", "job_id", jobID, "error", err)
	}

	prompt := PromptFor(jobType)
	req := &HarnessExecRequest{
		RunID:     runID,
		AgentName: agent.AgentName(),
		Agent:     agent,
		Body:      prompt,
		Env: map[string]string{
			"SYNAPBUS_DISPATCH_TOKEN":       tok,
			"SYNAPBUS_CONSOLIDATION_JOB_ID": fmt.Sprintf("%d", jobID),
			"SYNAPBUS_JOB_TYPE":             jobType,
			"SYNAPBUS_OWNER_ID":             ownerID,
			"SYNAPBUS_DREAM_PROMPT":         prompt,
		},
		MaxWallClock: wallclock,
	}

	w.logger.Info("dispatching dream job",
		"job_id", jobID,
		"job_type", jobType,
		"owner_id", ownerID,
		"agent", agent.AgentName(),
		"run_id", runID,
	)

	start := time.Now()
	res, err := w.harness.Execute(ctx, agent, req)
	status, summary, errMsg := mapHarnessResult(res, err, ctx.Err())
	duration := time.Since(start)

	if err := w.jobs.Complete(context.Background(), jobID, status, summary, errMsg); err != nil {
		w.logger.Warn("complete job failed", "job_id", jobID, "error", err)
	}
	// Token revoke is best-effort.
	_ = w.tokens.Revoke(context.Background(), tok)

	// Per-(owner, day) usage accounting (T4): feed back tokens for the
	// circuit breaker. Failure here is non-fatal.
	if w.usage != nil {
		var tIn, tOut int64
		if res != nil {
			tIn, tOut = res.TokensIn, res.TokensOut
		}
		_ = w.usage.RecordCompletion(context.Background(), ownerID, tIn, tOut, status)
	}

	recordJobMetric(ownerID, jobType, status)
	recordJobDurationMetric(ownerID, jobType, duration)
	if res != nil {
		recordTokensMetric(ownerID, res.TokensIn, res.TokensOut)
	}

	w.logger.Info("dream job completed",
		"job_id", jobID, "status", status, "summary", summary,
		"duration", duration.String(),
	)
}

// ownerActiveSince returns true when any agent owned by ownerID has
// authored at least one message after `since`. Used by core_rewrite
// dispatch to short-circuit when an owner's fleet has been quiet — the
// nightly deep pass has nothing to refresh against and would waste
// tokens.
func ownerActiveSince(ctx context.Context, db *sql.DB, ownerID string, since time.Time) bool {
	if db == nil || ownerID == "" {
		return false
	}
	var one int
	err := db.QueryRowContext(ctx,
		`SELECT 1
		   FROM messages m JOIN agents a ON m.from_agent = a.name
		  WHERE CAST(a.owner_id AS TEXT) = ?
		    AND m.created_at > ?
		  LIMIT 1`,
		ownerID, since.UTC(),
	).Scan(&one)
	return err == nil && one == 1
}

// agentActiveSince returns true when the named agent (owned by
// ownerID) has authored at least one message after `since`. Reserved
// for future per-agent gating; currently the dispatcher uses
// ownerActiveSince above to gate the whole core_rewrite pass.
func agentActiveSince(ctx context.Context, db *sql.DB, ownerID, agentName string, since time.Time) bool {
	if db == nil || ownerID == "" || agentName == "" {
		return false
	}
	var one int
	err := db.QueryRowContext(ctx,
		`SELECT 1
		   FROM messages m JOIN agents a ON m.from_agent = a.name
		  WHERE CAST(a.owner_id AS TEXT) = ?
		    AND a.name = ?
		    AND m.created_at > ?
		  LIMIT 1`,
		ownerID, agentName, since.UTC(),
	).Scan(&one)
	return err == nil && one == 1
}

// mapHarnessResult translates harness output to a job status. Context
// timeouts → 'partial' (the agent ran but was killed by the budget).
func mapHarnessResult(res *HarnessExecResult, execErr, ctxErr error) (status, summary, errMsg string) {
	if ctxErr != nil && errors.Is(ctxErr, context.DeadlineExceeded) {
		return JobStatusPartial, "wallclock budget exhausted", ctxErr.Error()
	}
	if execErr != nil {
		return JobStatusFailed, "", execErr.Error()
	}
	if res == nil {
		return JobStatusFailed, "", "nil result"
	}
	if res.ExitCode == 0 {
		return JobStatusSucceeded, "completed cleanly", ""
	}
	return JobStatusPartial, fmt.Sprintf("exit code %d", res.ExitCode), ""
}
