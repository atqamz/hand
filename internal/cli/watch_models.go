package cli

import (
	"context"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

func (w *watcher) refreshModels(ctx context.Context) {
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
			if ctx.Err() != nil || !harness.StatusOf(name, env, opts.Now()).Due {
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
