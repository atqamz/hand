package cli

import (
	"context"
	"errors"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

func (w *watcher) approvals(ctx context.Context, c luvus.Client, caps luvus.Capabilities) error {
	live, err := w.st.LiveAttempts(ctx)
	if err != nil {
		return err
	}
	var asking []state.Attempt
	for _, a := range live {
		if a.Status == state.AttemptRunning && a.Approve == state.ApproveAsk {
			asking = append(asking, a)
		}
	}
	if len(asking) == 0 {
		return nil
	}
	wait := harness.DefaultApproveTimeout
	if p, err := harness.LoadPolicy(w.r.home); err == nil {
		wait = p.ApproveWait()
	}
	signals, err := w.st.AttemptSignals(ctx)
	if err != nil {
		return err
	}
	denied := false
	for _, a := range asking {
		e := signals[a.ID]
		if e.Kind != "attempt.blocked" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil || w.r.env.Now().Sub(at) < wait {
			continue
		}
		s, err := c.Read(ctx, a.PaneID, luvus.ScreenLines)
		if err != nil || s.TerminalID != a.TerminalID {
			continue
		}
		deny := "esc"
		if perm, ok := harness.ClaudePermission(s.Text); ok {
			deny = perm.Deny
		} else if !harness.ClaudeDialog(s.Text) {
			continue
		}
		if err := pressKeys(ctx, c, a.PaneID, a.TerminalID, []string{deny}, s.ContentRevision, luvus.ScreenDigest(s.Text)); err != nil {
			continue
		}
		detail := "no answer in " + wait.String() + "; pressed " + deny
		if err := w.st.NoteAttempt(ctx, a.ID, "denied", detail); err != nil {
			if errors.Is(err, state.ErrConflict) {
				continue
			}
			return err
		}
		ref := state.AttemptRef(a.ID)
		w.alert(ctx, "attempt.denied", ref, ref+" denied: "+detail)
		denied = true
	}
	if denied {
		w.deliver(ctx, c, caps)
	}
	return nil
}
