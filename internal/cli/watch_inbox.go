package cli

import (
	"context"
	"errors"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/inbox"
	"github.com/atqamz/hand/internal/state"
)

func (w *watcher) pruneInbox(ctx context.Context) {
	day := w.r.env.Now().UTC().Format(snapshotLayout)
	if day == w.pruned {
		return
	}
	p, err := harness.LoadPolicy(w.r.home)
	if err != nil {
		if !w.inboxWarned {
			w.inboxWarned = true
			w.say(harness.PolicyFile + " unreadable, inbox pruning stays off: " + err.Error())
		}
		return
	}
	if p.Inbox.Keep == 0 {
		return
	}
	if inbox.Marked(w.r.home, day) {
		w.pruned = day
		return
	}
	res, err := w.r.pruneInbox(ctx, w.st, p.Inbox.Keep, false)
	if errors.Is(err, state.ErrConflict) || ctx.Err() != nil {
		return
	}
	w.pruned = day
	if err == nil {
		err = inbox.Mark(w.r.home, day)
	}
	for _, f := range res.Failed {
		w.say("inbox prune: " + f.Name + ": " + f.Err.Error())
	}
	if err != nil {
		w.say("inbox prune: " + err.Error())
	}
}
