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
			cancelWithTakeoverPriority(cancel, requested)
			return
		}
		select {
		case <-requested:
			cancel(watcher.ErrReplaced)
		case <-sig:
			cancelWithTakeoverPriority(cancel, requested)
		case <-ctx.Done():
			return
		case <-parent.Done():
			cancelWithTakeoverPriority(cancel, requested)
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

func cancelWithTakeoverPriority(cancel context.CancelCauseFunc, requested <-chan struct{}) {
	// This select is the cancellation boundary: a later request belongs to the
	// next lifecycle, while an already-observable request wins this one.
	select {
	case <-requested:
		cancel(watcher.ErrReplaced)
	default:
		cancel(watcher.ErrInterrupted)
	}
}
