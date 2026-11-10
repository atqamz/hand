package cli

import (
	"context"
	"os"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

func (r *runner) homeExists() bool {
	_, err := os.Stat(r.home)
	return err == nil
}

func (w *watcher) refreshModels(ctx context.Context) {
	if !w.r.homeExists() {
		return
	}
	if !w.modelsBusy.CompareAndSwap(false, true) {
		return
	}
	env, opts := w.r.modelOptions()
	w.pending.Add(1)
	go func() {
		defer w.pending.Done()
		defer w.modelsBusy.Store(false)
		if w.modelsSaid == nil {
			w.modelsSaid = map[string]string{}
		}
		for _, name := range state.Harnesses {
			if ctx.Err() != nil || !w.r.homeExists() || !harness.StatusOf(name, env, opts.Now()).Due {
				continue
			}
			l, err := harness.Refresh(ctx, name, env, opts)
			reason := ""
			switch {
			case ctx.Err() != nil:
				return
			case err != nil:
				reason = err.Error()
			case l.Failed != nil:
				reason = l.Failed.Reason
			}
			if reason == "" {
				delete(w.modelsSaid, name)
			} else if w.modelsSaid[name] != reason {
				w.modelsSaid[name] = reason
				w.say("models " + name + ": " + reason)
			}
		}
	}()
}
