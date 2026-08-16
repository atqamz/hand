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
	if takeoverReady(requested) {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(watcher.ErrReplaced)
		return ctx, func() {}
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if takeoverReady(requested) {
			cancel(watcher.ErrReplaced)
			return
		}
		if parent.Err() != nil {
			if takeoverReady(requested) {
				cancel(watcher.ErrReplaced)
			} else {
				cancel(watcher.ErrInterrupted)
			}
			return
		}
		select {
		case <-requested:
			cancel(watcher.ErrReplaced)
		case <-sig:
			if takeoverReady(requested) {
				cancel(watcher.ErrReplaced)
				return
			}
			cancel(watcher.ErrInterrupted)
		case <-ctx.Done():
			return
		case <-parent.Done():
			if takeoverReady(requested) {
				cancel(watcher.ErrReplaced)
			} else {
				cancel(watcher.ErrInterrupted)
			}
		}
	}()
	return ctx, func() {
		cancel(watcher.ErrInterrupted)
		<-stopped
	}
}

func takeoverReady(requested <-chan struct{}) bool {
	if requested == nil {
		return false
	}
	select {
	case <-requested:
		return true
	default:
		return false
	}
}
