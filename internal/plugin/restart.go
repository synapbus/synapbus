package plugin

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Restarter triggers a graceful restart of the host process.
// The interface keeps the plugin package independent of cloudflare/tableflip;
// the cmd/ layer provides a tableflip-backed implementation, while tests
// and non-restart paths use NoopRestarter.
type Restarter interface {
	// TriggerRestart initiates a graceful restart. The method returns
	// immediately; the actual restart happens asynchronously. Returns an
	// error only if the request could not be queued.
	TriggerRestart() error
}

// NoopRestarter does nothing; useful for tests and for CLI tools that do not
// run a long-lived HTTP server.
type NoopRestarter struct {
	mu     sync.Mutex
	called int
}

func (n *NoopRestarter) TriggerRestart() error {
	n.mu.Lock()
	n.called++
	n.mu.Unlock()
	return nil
}

func (n *NoopRestarter) CallCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.called
}

// SignalRestarter sends SIGHUP to the current process. Useful when the real
// restart is implemented elsewhere (e.g. by a supervisor, tableflip upgrader,
// or systemd socket-activated re-exec) and we just need to raise the signal.
type SignalRestarter struct{}

func (SignalRestarter) TriggerRestart() error {
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGHUP)
}

// WatchSignals blocks until the given signals fire, invokes fn, and returns.
// If the context cancels first, it returns ctx.Err().
//
// Typical use from main.go:
//
//	plugin.WatchSignals(ctx, func(sig os.Signal) {
//	    if sig == syscall.SIGHUP { upg.Upgrade() }
//	}, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
func WatchSignals(ctx context.Context, fn func(os.Signal), sigs ...os.Signal) error {
	if len(sigs) == 0 {
		return errors.New("WatchSignals: at least one signal required")
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	defer signal.Stop(ch)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s := <-ch:
		fn(s)
		return nil
	}
}

// DrainAndExit is a helper for the graceful-restart path. It waits up to
// timeout for fn to return, then exits with the given code.
func DrainAndExit(timeout time.Duration, fn func(), code int) {
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("drain complete")
	case <-time.After(timeout):
		slog.Warn("drain timed out", "timeout", timeout)
	}
	os.Exit(code)
}
