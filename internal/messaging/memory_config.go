package messaging

import (
	"os"
	"strconv"
	"time"
)

// MemoryConfig holds feature-flag and tunable knobs for proactive memory
// injection and the dream worker. All fields are populated from
// environment variables by ParseMemoryConfig; defaults match the values
// listed in the 020-proactive-memory-dream-worker spec.
type MemoryConfig struct {
	// InjectionEnabled toggles the `relevant_context` packet on MCP tool
	// responses. When false, the wrapper is a no-op and the field is
	// omitted from responses entirely.
	InjectionEnabled bool

	// InjectionBudgetTokens is the soft cap for the merged packet
	// (memories + core_memory), counted as `(chars+3)/4`.
	InjectionBudgetTokens int

	// InjectionMaxItems caps the number of memory items in a single packet.
	InjectionMaxItems int

	// InjectionMinScore is the relevance floor; items below this are
	// dropped unless explicitly pinned.
	InjectionMinScore float64

	// CoreMemoryMaxBytes is the upper bound on a per-(owner, agent) core
	// memory blob. Enforced in the Set path.
	CoreMemoryMaxBytes int

	// DreamEnabled gates the entire ConsolidatorWorker lifecycle and the
	// six memory_* MCP tools.
	DreamEnabled bool

	// DreamInterval is the ticker period for the consolidator worker.
	DreamInterval time.Duration

	// DreamDeepCron is the cron expression for the nightly deep pass
	// (e.g. core_rewrite). Standard 5-field cron; not parsed here.
	DreamDeepCron string

	// DreamMaxConcurrent caps the number of owners with in-flight jobs.
	DreamMaxConcurrent int

	// DreamWallclockBudget caps how long a single consolidation job is
	// allowed to run before the worker terminates it with status=partial.
	DreamWallclockBudget time.Duration

	// DreamWatermark is the number of new memory messages required
	// before a reflection job triggers automatically for an owner.
	DreamWatermark int

	// DreamAgent is the name of the agent invoked as the dream worker
	// via harness.Harness.Execute.
	DreamAgent string

	// DreamRecentWindow bounds the lookback for the dream worker's
	// per-owner input set (unprocessed-count queries, recency
	// injection fallback, and memory_list_unprocessed). Messages older
	// than `now - DreamRecentWindow` are invisible to the worker — the
	// pool is effectively a rolling window of recent activity. Default
	// 14 days. Env: SYNAPBUS_DREAM_RECENT_WINDOW. Accepts Go
	// duration syntax plus the "Nd" days extension.
	DreamRecentWindow time.Duration

	// DreamDailyTokenLimitIn is the per-(owner, day) input-token
	// circuit-breaker threshold. When the running sum of TokensIn
	// across today's dream jobs exceeds this, the worker skips further
	// dispatches for that owner until the date rolls over. Default 1M.
	// Env: SYNAPBUS_DREAM_DAILY_TOKEN_LIMIT_IN.
	DreamDailyTokenLimitIn int64

	// DreamDailyTokenLimitOut is the per-(owner, day) output-token
	// circuit-breaker threshold. Default 200k.
	// Env: SYNAPBUS_DREAM_DAILY_TOKEN_LIMIT_OUT.
	DreamDailyTokenLimitOut int64

	// DreamDailyJobLimit is the per-(owner, day) ceiling on jobs
	// started. Default 100. Env: SYNAPBUS_DREAM_DAILY_JOB_LIMIT.
	DreamDailyJobLimit int
}

// DefaultMemoryConfig returns the defaults exactly as listed in the
// 020-proactive-memory-dream-worker spec.
func DefaultMemoryConfig() MemoryConfig {
	return MemoryConfig{
		InjectionEnabled:      false,
		InjectionBudgetTokens: 500,
		InjectionMaxItems:     5,
		InjectionMinScore:     0.25,
		CoreMemoryMaxBytes:    2048,
		DreamEnabled:          false,
		DreamInterval:         1 * time.Hour,
		DreamDeepCron:         "0 3 * * *",
		DreamMaxConcurrent:    4,
		DreamWallclockBudget:  10 * time.Minute,
		DreamWatermark:        20,
		DreamAgent:            "claude-code",
		// 14 days of recent activity.
		DreamRecentWindow:       336 * time.Hour,
		DreamDailyTokenLimitIn:  1_000_000,
		DreamDailyTokenLimitOut: 200_000,
		DreamDailyJobLimit:      100,
	}
}

// ParseMemoryConfig reads MemoryConfig fields from environment
// variables, falling back to the spec defaults for any unset or
// unparseable value.
func ParseMemoryConfig() MemoryConfig {
	cfg := DefaultMemoryConfig()

	if v := os.Getenv("SYNAPBUS_INJECTION_ENABLED"); v != "" {
		if b, ok := parseBoolFlag(v); ok {
			cfg.InjectionEnabled = b
		}
	}
	if v := os.Getenv("SYNAPBUS_INJECTION_BUDGET_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.InjectionBudgetTokens = n
		}
	}
	if v := os.Getenv("SYNAPBUS_INJECTION_MAX_ITEMS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.InjectionMaxItems = n
		}
	}
	if v := os.Getenv("SYNAPBUS_INJECTION_MIN_SCORE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			cfg.InjectionMinScore = f
		}
	}
	if v := os.Getenv("SYNAPBUS_CORE_MEMORY_MAX_BYTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.CoreMemoryMaxBytes = n
		}
	}
	if v := os.Getenv("SYNAPBUS_DREAM_ENABLED"); v != "" {
		if b, ok := parseBoolFlag(v); ok {
			cfg.DreamEnabled = b
		}
	}
	if v := os.Getenv("SYNAPBUS_DREAM_INTERVAL"); v != "" {
		if d, err := parseDurationWithDays(v); err == nil && d > 0 {
			cfg.DreamInterval = d
		}
	}
	if v := os.Getenv("SYNAPBUS_DREAM_DEEP_CRON"); v != "" {
		cfg.DreamDeepCron = v
	}
	if v := os.Getenv("SYNAPBUS_DREAM_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.DreamMaxConcurrent = n
		}
	}
	if v := os.Getenv("SYNAPBUS_DREAM_WALLCLOCK_BUDGET"); v != "" {
		if d, err := parseDurationWithDays(v); err == nil && d > 0 {
			cfg.DreamWallclockBudget = d
		}
	}
	if v := os.Getenv("SYNAPBUS_DREAM_WATERMARK"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.DreamWatermark = n
		}
	}
	if v := os.Getenv("SYNAPBUS_DREAM_AGENT"); v != "" {
		cfg.DreamAgent = v
	}
	if v := os.Getenv("SYNAPBUS_DREAM_RECENT_WINDOW"); v != "" {
		if d, err := parseDurationWithDays(v); err == nil && d > 0 {
			cfg.DreamRecentWindow = d
		}
	}
	if v := os.Getenv("SYNAPBUS_DREAM_DAILY_TOKEN_LIMIT_IN"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.DreamDailyTokenLimitIn = n
		}
	}
	if v := os.Getenv("SYNAPBUS_DREAM_DAILY_TOKEN_LIMIT_OUT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.DreamDailyTokenLimitOut = n
		}
	}
	if v := os.Getenv("SYNAPBUS_DREAM_DAILY_JOB_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.DreamDailyJobLimit = n
		}
	}

	return cfg
}

// parseBoolFlag accepts the common 0/1/true/false/yes/no spellings.
func parseBoolFlag(v string) (bool, bool) {
	switch v {
	case "1", "true", "TRUE", "True", "yes", "YES", "y", "on", "ON":
		return true, true
	case "0", "false", "FALSE", "False", "no", "NO", "n", "off", "OFF":
		return false, true
	}
	return false, false
}
