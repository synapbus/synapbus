package jsruntime

import (
	"context"
	"fmt"
	"sync"
)

// Pool manages a pool of reusable goja VMs for concurrent JavaScript execution.
// Each Execute call acquires a VM from the pool, runs the code, then releases
// the VM back. If all VMs are in use, callers block until one becomes available
// or the context is cancelled.
type Pool struct {
	size      int
	available chan struct{} // semaphore — each token represents a "slot"
	mu        sync.Mutex
	closed    bool
}

// NewPool creates a new runtime pool with the given concurrency limit.
// The size determines how many concurrent Execute calls can run simultaneously.
func NewPool(size int) *Pool {
	if size < 1 {
		size = 1
	}

	p := &Pool{
		size:      size,
		available: make(chan struct{}, size),
	}

	// Fill the semaphore
	for i := 0; i < size; i++ {
		p.available <- struct{}{}
	}

	return p
}

// Execute acquires a slot from the pool, runs the code, and releases the slot.
// A fresh goja VM is created for each execution to ensure clean state isolation.
// Blocks if all slots are in use; respects context cancellation.
func (p *Pool) Execute(ctx context.Context, code string, caller ToolCaller, opts ExecuteOptions) (*ExecuteResult, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("pool is closed")
	}
	p.mu.Unlock()

	// Acquire a slot (blocks if pool is exhausted)
	select {
	case <-p.available:
		// Got a slot
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Always release the slot when done
	defer func() {
		p.available <- struct{}{}
	}()

	// Execute with a fresh VM (created inside Execute)
	return Execute(ctx, code, caller, opts)
}

// Size returns the configured pool size.
func (p *Pool) Size() int {
	return p.size
}

// Available returns the number of available slots.
func (p *Pool) Available() int {
	return len(p.available)
}

// Close marks the pool as closed. Subsequent Execute calls will return an error.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}
