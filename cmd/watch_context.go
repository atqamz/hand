package cmd

import "context"

func contextWithTakeover(parent context.Context, requested <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if requested == nil {
		return ctx, cancel
	}
	go func() {
		select {
		case <-requested:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
