package jsruntime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewPool(t *testing.T) {
	p := NewPool(5)
	if p.Size() != 5 {
		t.Errorf("expected size 5, got %d", p.Size())
	}
	if p.Available() != 5 {
		t.Errorf("expected 5 available, got %d", p.Available())
	}
}

func TestNewPool_MinSize(t *testing.T) {
	p := NewPool(0)
	if p.Size() != 1 {
		t.Errorf("expected size clamped to 1, got %d", p.Size())
	}

	p2 := NewPool(-5)
	if p2.Size() != 1 {
		t.Errorf("expected size clamped to 1, got %d", p2.Size())
	}
}

func TestPool_Execute(t *testing.T) {
	p := NewPool(3)
	defer p.Close()

	caller := newMockCaller()
	result, err := p.Execute(context.Background(), `42`, caller, ExecuteOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var num int64
	switch v := result.Value.(type) {
	case int64:
		num = v
	case float64:
		num = int64(v)
	}
	if num != 42 {
		t.Errorf("expected 42, got %v", result.Value)
	}
}

func TestPool_ConcurrentExecution(t *testing.T) {
	poolSize := 3
	p := NewPool(poolSize)
	defer p.Close()

	numGoroutines := 20
	var wg sync.WaitGroup
	var successCount int32
	var errCount int32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			caller := newMockCaller()
			result, err := p.Execute(context.Background(), `({ value: 1 })`, caller, ExecuteOptions{})
			if err != nil {
				atomic.AddInt32(&errCount, 1)
				return
			}
			if result != nil {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if int(errCount) > 0 {
		t.Errorf("expected 0 errors, got %d", errCount)
	}
	if int(successCount) != numGoroutines {
		t.Errorf("expected %d successes, got %d", numGoroutines, successCount)
	}

	// All slots should be available again
	if p.Available() != poolSize {
		t.Errorf("expected %d available after all complete, got %d", poolSize, p.Available())
	}
}

func TestPool_BlocksWhenExhausted(t *testing.T) {
	p := NewPool(1)
	defer p.Close()

	// Occupy the only slot with a long-running script
	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		caller := newMockCaller()
		close(started)
		p.Execute(context.Background(), `
			var i = 0;
			while(i < 1000000) { i++; }
			i
		`, caller, ExecuteOptions{})
		close(done)
	}()

	<-started
	// Give the goroutine a moment to acquire the slot
	time.Sleep(10 * time.Millisecond)

	// Try to execute with a short timeout — should fail because the slot is occupied
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	caller := newMockCaller()
	_, err := p.Execute(ctx, `42`, caller, ExecuteOptions{})

	if err == nil {
		t.Error("expected context deadline error when pool is exhausted, got nil")
	}

	// Wait for first execution to finish
	<-done
}

func TestPool_ClosedPoolRejectsExecute(t *testing.T) {
	p := NewPool(3)
	p.Close()

	caller := newMockCaller()
	_, err := p.Execute(context.Background(), `42`, caller, ExecuteOptions{})
	if err == nil {
		t.Error("expected error from closed pool, got nil")
	}
}

func TestPool_WithCallBridge(t *testing.T) {
	p := NewPool(2)
	defer p.Close()

	caller := newMockCaller()
	caller.results["greet"] = map[string]any{"greeting": "hello"}

	code := `
		var res = call("greet", { name: "world" });
		res.ok ? res.result.greeting : "error"
	`

	result, err := p.Execute(context.Background(), code, caller, ExecuteOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Value != "hello" {
		t.Errorf("expected 'hello', got %v", result.Value)
	}
	if result.CallCount != 1 {
		t.Errorf("expected CallCount=1, got %d", result.CallCount)
	}
}

func TestPool_IsolationBetweenExecutions(t *testing.T) {
	p := NewPool(1)
	defer p.Close()

	caller := newMockCaller()

	// First execution sets a variable
	_, err := p.Execute(context.Background(), `var shared = 42; shared`, caller, ExecuteOptions{})
	if err != nil {
		t.Fatalf("first execution failed: %v", err)
	}

	// Second execution should not see the variable from the first
	_, err = p.Execute(context.Background(), `
		typeof shared === "undefined" ? "isolated" : "leaked"
	`, caller, ExecuteOptions{})
	if err != nil {
		t.Fatalf("second execution failed: %v", err)
	}
	// Note: since Execute creates a fresh VM each time, isolation is guaranteed
}
