package webhooks

import (
	"testing"
)

func TestRateLimiterAllow(t *testing.T) {
	// Create a limiter with 60 per minute (burst of 60)
	rl := NewAgentRateLimiter(60)

	// First call should be allowed (burst available)
	if !rl.Allow("agent-1") {
		t.Error("first Allow() should return true (burst available)")
	}

	// Multiple rapid calls should be allowed up to burst capacity
	allowed := 0
	for i := 0; i < 100; i++ {
		if rl.Allow("agent-2") {
			allowed++
		}
	}
	// The burst is 60, so we should get at most 60 allowed
	if allowed > 60 {
		t.Errorf("allowed %d calls, expected at most 60 (burst)", allowed)
	}
	if allowed < 50 {
		t.Errorf("allowed %d calls, expected around 60 (burst)", allowed)
	}

	// Different agents have independent limits
	if !rl.Allow("agent-3") {
		t.Error("new agent should have full burst available")
	}
}

func TestRateLimiterRemove(t *testing.T) {
	rl := NewAgentRateLimiter(60)

	// Exhaust burst for agent-1
	for i := 0; i < 100; i++ {
		rl.Allow("agent-remove")
	}

	// Remove the limiter
	rl.Remove("agent-remove")

	// After removal, a new limiter is created with full burst
	if !rl.Allow("agent-remove") {
		t.Error("after Remove(), agent should get a fresh limiter with full burst")
	}
}
