package trust

import (
	"strings"
	"testing"
)

func TestDelegationCap_TierMatrix(t *testing.T) {
	tiers := []string{TierSupervised, TierAssisted, TierAutonomous}

	tests := []struct {
		name           string
		parent         string
		proposed       string
		wantViolation  bool
		wantEffective  string
	}{
		{"sup→sup ok", TierSupervised, TierSupervised, false, TierSupervised},
		{"sup→assisted violation", TierSupervised, TierAssisted, true, TierSupervised},
		{"sup→autonomous violation", TierSupervised, TierAutonomous, true, TierSupervised},
		{"assisted→sup ok", TierAssisted, TierSupervised, false, TierSupervised},
		{"assisted→assisted ok", TierAssisted, TierAssisted, false, TierAssisted},
		{"assisted→autonomous violation", TierAssisted, TierAutonomous, true, TierAssisted},
		{"autonomous→sup ok", TierAutonomous, TierSupervised, false, TierSupervised},
		{"autonomous→assisted ok", TierAutonomous, TierAssisted, false, TierAssisted},
		{"autonomous→autonomous ok", TierAutonomous, TierAutonomous, false, TierAutonomous},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := Grant{AutonomyTier: tt.parent, ToolScope: []string{"search"}}
			proposed := Grant{AutonomyTier: tt.proposed, ToolScope: []string{"search"}}
			eff, viols := DelegationCap(parent, proposed, 5)

			gotViolation := false
			for _, v := range viols {
				if strings.Contains(v, "autonomy tier") {
					gotViolation = true
					break
				}
			}
			if gotViolation != tt.wantViolation {
				t.Errorf("violation = %v, want %v (viols=%v)", gotViolation, tt.wantViolation, viols)
			}
			if eff.AutonomyTier != tt.wantEffective {
				t.Errorf("effective tier = %q, want %q", eff.AutonomyTier, tt.wantEffective)
			}
		})
	}

	// sanity: ensure all known tiers are mapped
	for _, ti := range tiers {
		if _, ok := tierRank[ti]; !ok {
			t.Errorf("tier %q missing from tierRank", ti)
		}
	}
}

func TestDelegationCap_ToolScope(t *testing.T) {
	tests := []struct {
		name              string
		parentTools       []string
		proposedTools     []string
		wantEffective     []string
		wantDisallowed    []string
	}{
		{
			name:           "exact subset",
			parentTools:    []string{"search", "fetch", "summarize"},
			proposedTools:  []string{"search", "fetch"},
			wantEffective:  []string{"fetch", "search"},
			wantDisallowed: nil,
		},
		{
			name:           "full overlap",
			parentTools:    []string{"search", "fetch"},
			proposedTools:  []string{"search", "fetch"},
			wantEffective:  []string{"fetch", "search"},
			wantDisallowed: nil,
		},
		{
			name:           "single forbidden tool",
			parentTools:    []string{"search"},
			proposedTools:  []string{"search", "execute"},
			wantEffective:  []string{"search"},
			wantDisallowed: []string{"execute"},
		},
		{
			name:           "all forbidden",
			parentTools:    []string{"search"},
			proposedTools:  []string{"execute", "delete"},
			wantEffective:  nil,
			wantDisallowed: []string{"delete", "execute"},
		},
		{
			name:           "empty proposed",
			parentTools:    []string{"search"},
			proposedTools:  nil,
			wantEffective:  nil,
			wantDisallowed: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := Grant{AutonomyTier: TierAssisted, ToolScope: tt.parentTools}
			proposed := Grant{AutonomyTier: TierAssisted, ToolScope: tt.proposedTools}
			eff, viols := DelegationCap(parent, proposed, 10)

			if !equalSlices(eff.ToolScope, tt.wantEffective) {
				t.Errorf("effective tools = %v, want %v", eff.ToolScope, tt.wantEffective)
			}

			for _, want := range tt.wantDisallowed {
				found := false
				for _, v := range viols {
					if strings.Contains(v, want) && strings.Contains(v, "not in parent scope") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing violation for tool %q in %v", want, viols)
				}
			}
		})
	}
}

func TestDelegationCap_Budgets(t *testing.T) {
	tests := []struct {
		name             string
		parentTokens     int64
		proposedTokens   int64
		wantTokens       int64
		wantViolation    bool
	}{
		{"parent unlimited, child 100", 0, 100, 100, false},
		{"parent 100, child 50", 100, 50, 50, false},
		{"parent 100, child 100", 100, 100, 100, false},
		{"parent 100, child 200 (violation)", 100, 200, 100, true},
		{"parent 100, child 0 (inherit)", 100, 0, 100, false},
		{"parent 0, child 0 (both unlimited)", 0, 0, 0, false},
		{"parent 100, child negative", 100, -5, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := Grant{
				AutonomyTier: TierAssisted,
				ToolScope:    []string{"x"},
				BudgetTokens: tt.parentTokens,
			}
			proposed := Grant{
				AutonomyTier: TierAssisted,
				ToolScope:    []string{"x"},
				BudgetTokens: tt.proposedTokens,
			}
			eff, viols := DelegationCap(parent, proposed, 5)

			if eff.BudgetTokens != tt.wantTokens {
				t.Errorf("budget tokens = %d, want %d", eff.BudgetTokens, tt.wantTokens)
			}

			gotViolation := false
			for _, v := range viols {
				if strings.Contains(v, "budget_tokens") {
					gotViolation = true
					break
				}
			}
			if gotViolation != tt.wantViolation {
				t.Errorf("violation = %v, want %v (viols=%v)", gotViolation, tt.wantViolation, viols)
			}
		})
	}
}

func TestDelegationCap_SpawnDepth(t *testing.T) {
	tests := []struct {
		name          string
		parentDepth   int
		maxDepth      int
		wantEffective int
		wantViolation bool
	}{
		{"depth 0 → 1, max 5", 0, 5, 1, false},
		{"depth 4 → 5, max 5", 4, 5, 5, false},
		{"depth 5 → 6, max 5 (violation)", 5, 5, 6, true},
		{"depth 0 → 1, max 0 (violation)", 0, 0, 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := Grant{
				AutonomyTier: TierAssisted,
				ToolScope:    []string{"x"},
				SpawnDepth:   tt.parentDepth,
			}
			proposed := Grant{
				AutonomyTier: TierAssisted,
				ToolScope:    []string{"x"},
			}
			eff, viols := DelegationCap(parent, proposed, tt.maxDepth)

			if eff.SpawnDepth != tt.wantEffective {
				t.Errorf("spawn depth = %d, want %d", eff.SpawnDepth, tt.wantEffective)
			}

			gotViolation := false
			for _, v := range viols {
				if strings.Contains(v, "spawn depth") {
					gotViolation = true
					break
				}
			}
			if gotViolation != tt.wantViolation {
				t.Errorf("violation = %v, want %v (viols=%v)", gotViolation, tt.wantViolation, viols)
			}
		})
	}
}

func TestDelegationCap_MultipleViolations(t *testing.T) {
	parent := Grant{
		AutonomyTier: TierSupervised,
		ToolScope:    []string{"search"},
		BudgetTokens: 100,
		SpawnDepth:   5,
	}
	proposed := Grant{
		AutonomyTier: TierAutonomous, // violation: tier
		ToolScope:    []string{"execute"}, // violation: tool
		BudgetTokens: 1000,            // violation: budget
	}
	_, viols := DelegationCap(parent, proposed, 5) // violation: depth (5+1>5)

	if len(viols) < 4 {
		t.Errorf("expected at least 4 violations, got %d: %v", len(viols), viols)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
