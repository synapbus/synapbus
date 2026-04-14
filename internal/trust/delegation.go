package trust

import (
	"fmt"
	"sort"
)

// Autonomy tier constants, ordered low → high.
//
// Higher tiers grant the agent more freedom: supervised requires human
// approval for outbound actions, assisted may act with logging, autonomous
// may act without per-action review (but always within tool/budget caps).
const (
	TierSupervised = "supervised"
	TierAssisted   = "assisted"
	TierAutonomous = "autonomous"
)

var tierRank = map[string]int{
	TierSupervised: 1,
	TierAssisted:   2,
	TierAutonomous: 3,
}

// Grant is a single delegation envelope: the maximum authority a parent
// confers to a child agent. The dynamic-spawning trust model enforces the
// "child ≤ parent" rule across every dimension of a Grant.
type Grant struct {
	AutonomyTier       string
	ToolScope          []string
	BudgetTokens       int64
	BudgetDollarsCents int64
	SpawnDepth         int
}

// DelegationCap computes the effective grant a child should receive given
// the parent's grant and the child's proposal.
//
// Rules:
//   - Autonomy tier: effective = min(parent, proposed). A proposed tier above
//     the parent's tier is a violation.
//   - Tool scope: effective = intersection of parent and proposed. Any tool in
//     proposed that is not in parent is a violation. Order does not matter.
//   - Budget tokens / dollars: effective = min, with 0 on parent meaning
//     unlimited and 0 on child meaning "any up to parent". Proposed > parent
//     (when parent is non-zero) is a violation.
//   - Spawn depth: child must have parent.SpawnDepth + 1 <= maxDepth, otherwise
//     a violation is emitted; effective.SpawnDepth is set to parent + 1 anyway.
//
// violations is a slice of human-readable strings, one per violated rule,
// in stable order. effective is always populated (best-effort cap).
func DelegationCap(parent, proposed Grant, maxDepth int) (effective Grant, violations []string) {
	// --- Autonomy tier --------------------------------------------------
	parentRank, parentOK := tierRank[parent.AutonomyTier]
	proposedRank, proposedOK := tierRank[proposed.AutonomyTier]
	if !parentOK {
		parentRank = tierRank[TierSupervised]
	}
	if !proposedOK {
		proposedRank = tierRank[TierSupervised]
		violations = append(violations,
			fmt.Sprintf("unknown autonomy tier %q (treating as supervised)", proposed.AutonomyTier))
	}
	if proposedRank > parentRank {
		violations = append(violations,
			fmt.Sprintf("autonomy tier %q exceeds parent tier %q",
				proposed.AutonomyTier, parent.AutonomyTier))
		effective.AutonomyTier = parent.AutonomyTier
	} else {
		effective.AutonomyTier = proposed.AutonomyTier
	}

	// --- Tool scope -----------------------------------------------------
	parentTools := make(map[string]struct{}, len(parent.ToolScope))
	for _, t := range parent.ToolScope {
		parentTools[t] = struct{}{}
	}

	var allowed []string
	var disallowed []string
	for _, t := range proposed.ToolScope {
		if _, ok := parentTools[t]; ok {
			allowed = append(allowed, t)
		} else {
			disallowed = append(disallowed, t)
		}
	}
	sort.Strings(disallowed)
	for _, t := range disallowed {
		violations = append(violations,
			fmt.Sprintf("tool %q not in parent scope", t))
	}
	sort.Strings(allowed)
	effective.ToolScope = allowed

	// --- Budget tokens --------------------------------------------------
	effective.BudgetTokens = capBudget("budget_tokens",
		parent.BudgetTokens, proposed.BudgetTokens, &violations)

	// --- Budget dollars (cents) -----------------------------------------
	effective.BudgetDollarsCents = capBudget("budget_dollars_cents",
		parent.BudgetDollarsCents, proposed.BudgetDollarsCents, &violations)

	// --- Spawn depth ----------------------------------------------------
	effective.SpawnDepth = parent.SpawnDepth + 1
	if effective.SpawnDepth > maxDepth {
		violations = append(violations,
			fmt.Sprintf("spawn depth %d exceeds max %d", effective.SpawnDepth, maxDepth))
	}

	return effective, violations
}

// capBudget enforces the parent ≥ child rule for a budget dimension, where
// 0 on the parent means "unlimited" and 0 on the child means "inherit any
// value up to the parent's cap".
func capBudget(label string, parent, proposed int64, violations *[]string) int64 {
	switch {
	case parent == 0:
		// Parent unlimited: any non-negative proposal is fine.
		if proposed < 0 {
			*violations = append(*violations,
				fmt.Sprintf("%s %d is negative", label, proposed))
			return 0
		}
		return proposed
	case proposed == 0:
		// Child wants "as much as parent allows".
		return parent
	case proposed > parent:
		*violations = append(*violations,
			fmt.Sprintf("%s %d exceeds parent cap %d", label, proposed, parent))
		return parent
	case proposed < 0:
		*violations = append(*violations,
			fmt.Sprintf("%s %d is negative", label, proposed))
		return 0
	default:
		return proposed
	}
}
