package webhooks

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// AgentRateLimiter manages per-agent rate limiters for webhook delivery.
// Each agent gets a token bucket allowing 60 deliveries per minute (1/second sustained)
// with burst capacity of 60.
type AgentRateLimiter struct {
	limiters sync.Map // map[string]*rate.Limiter
	rate     rate.Limit
	burst    int
}

// NewAgentRateLimiter creates a new rate limiter with the given rate and burst.
// Default: 1 per second sustained, burst of 60.
func NewAgentRateLimiter(perMinute int) *AgentRateLimiter {
	return &AgentRateLimiter{
		rate:  rate.Every(time.Minute / time.Duration(perMinute)),
		burst: perMinute,
	}
}

// Wait blocks until the agent is allowed to make a delivery, or the context is cancelled.
func (r *AgentRateLimiter) Wait(ctx context.Context, agentName string) error {
	limiter := r.getLimiter(agentName)
	return limiter.Wait(ctx)
}

// Allow checks if the agent can make a delivery without blocking.
func (r *AgentRateLimiter) Allow(agentName string) bool {
	limiter := r.getLimiter(agentName)
	return limiter.Allow()
}

// Remove removes the rate limiter for an agent (cleanup when agent has no webhooks).
func (r *AgentRateLimiter) Remove(agentName string) {
	r.limiters.Delete(agentName)
}

func (r *AgentRateLimiter) getLimiter(agentName string) *rate.Limiter {
	if v, ok := r.limiters.Load(agentName); ok {
		return v.(*rate.Limiter)
	}
	limiter := rate.NewLimiter(r.rate, r.burst)
	actual, _ := r.limiters.LoadOrStore(agentName, limiter)
	return actual.(*rate.Limiter)
}
