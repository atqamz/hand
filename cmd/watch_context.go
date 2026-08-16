package cmd

import (
	"context"
	"os"

	"github.com/atqamz/hand/internal/watcher"
)

// Wires parent context, the OS signal channel, and the ownership takeover
// request into one context whose cancellation cause survives into watcher: a
// takeover is ErrReplaced, an external signal or parent cancel is ErrInterrupted.
func watchContext(parent context.Context, sig <-chan os.Signal, requested <-chan struct{}) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if ctx.Err() != nil {
			return
		}
		select {
		case <-sig:
			cancel(watcher.ErrInterrupted)
		case <-requested:
			cancel(watcher.ErrReplaced)
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		cancel(watcher.ErrInterrupted)
		<-stopped
	}
}
