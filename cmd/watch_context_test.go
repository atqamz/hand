package cmd

import (
	"context"
	"testing"
	"time"
)

func TestContextWithTakeoverCancelsWhenRequested(t *testing.T) {
	requested := make(chan struct{})
	ctx, cancel := contextWithTakeover(context.Background(), requested)
	defer cancel()

	close(requested)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context did not cancel after takeover request")
	}
}

func TestContextWithTakeoverFollowsParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := contextWithTakeover(parent, make(chan struct{}))
	defer cancel()

	cancelParent()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context did not cancel with its parent")
	}
}
