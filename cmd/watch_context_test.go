package cmd

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/watcher"
)

func TestWatchContextCancelsWithReplacementCauseOnTakeover(t *testing.T) {
	sig := make(chan os.Signal)
	requested := make(chan struct{})
	ctx, cancel := watchContext(context.Background(), sig, requested)
	defer cancel()

	close(requested)
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), watcher.ErrReplaced) {
			t.Fatalf("cause = %v, want ErrReplaced", context.Cause(ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("context did not cancel after takeover request")
	}
}

func TestWatchContextCancelsWithInterruptionCauseOnSignal(t *testing.T) {
	sig := make(chan os.Signal, 1)
	ctx, cancel := watchContext(context.Background(), sig, make(chan struct{}))
	defer cancel()

	sig <- os.Interrupt
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), watcher.ErrInterrupted) {
			t.Fatalf("cause = %v, want ErrInterrupted", context.Cause(ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("context did not cancel on an ordinary signal")
	}
}

func TestWatchContextFollowsParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := watchContext(parent, make(chan os.Signal), make(chan struct{}))
	defer cancel()

	cancelParent()
	select {
	case <-ctx.Done():
		// Parent cancellation is generic: it must not read as a takeover, leaving
		// the distinction to the watcher layer, which maps it to ErrInterrupted.
		if errors.Is(context.Cause(ctx), watcher.ErrReplaced) {
			t.Fatal("parent cancellation surfaced as a takeover replacement cause")
		}
	case <-time.After(time.Second):
		t.Fatal("context did not cancel with its parent")
	}
}

func TestWatchContextPrefersObservableTakeoverOverBufferedSignal(t *testing.T) {
	sig := make(chan os.Signal, 1)
	sig <- os.Interrupt
	requested := make(chan struct{})
	close(requested)

	ctx, cancel := watchContext(context.Background(), sig, requested)
	defer cancel()
	if !errors.Is(context.Cause(ctx), watcher.ErrReplaced) {
		t.Fatalf("cause = %v, want ErrReplaced when takeover and signal are both ready", context.Cause(ctx))
	}
}

func TestWatchContextPrefersTakeoverOverAlreadyCanceledParent(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	requested := make(chan struct{})
	close(requested)

	ctx, cancel := watchContext(parent, make(chan os.Signal), requested)
	defer cancel()
	if !errors.Is(context.Cause(ctx), watcher.ErrReplaced) {
		t.Fatalf("cause = %v, want ErrReplaced when takeover is ready at the boundary", context.Cause(ctx))
	}
}
