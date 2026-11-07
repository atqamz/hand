package cli

import (
	"context"
	"errors"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

const keysSettle = 3 * time.Second

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
	p, err := harness.LoadPolicy(w.r.home)
	if err != nil {
		return nil
	}
	wait := p.ApproveWait()
	signals, err := w.st.AttemptSignals(ctx)
	if err != nil {
		return err
	}
	denied := false
	for _, a := range asking {
		e := signals[a.ID]
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil || !due(e.Kind, w.r.env.Now().Sub(at), wait) {
			continue
		}
		d, err := w.approve(ctx, c, a, wait)
		if err != nil {
			return err
		}
		denied = denied || d
	}
	if denied {
		w.deliver(ctx, c, caps)
	}
	return nil
}

func due(kind string, age, wait time.Duration) bool {
	switch kind {
	case "attempt.keys":
		return age >= keysSettle
	case "attempt.blocked":
		return age >= wait
	}
	return false
}

func (w *watcher) approve(ctx context.Context, c luvus.Client, a state.Attempt, wait time.Duration) (bool, error) {
	unlock, ok, err := w.r.attemptLock(a.ID, false)
	if err != nil || !ok {
		return false, err
	}
	defer unlock()
	now := w.r.env.Now()
	e, ok, err := w.st.AttemptSignal(ctx, a.ID)
	if err != nil || !ok {
		return false, err
	}
	at, err := time.Parse(time.RFC3339Nano, e.At)
	if err != nil || !due(e.Kind, now.Sub(at), wait) {
		return false, nil
	}
	if e.Kind == "attempt.keys" {
		if s, err := c.Read(ctx, a.PaneID, luvus.ScreenLines); err == nil && s.TerminalID == a.TerminalID && !harness.ClaudeIdle(s.Text) {
			if err := w.noteBlocked(ctx, c, a, harness.ClaudeTitle(s.Text)); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	s, err := c.Read(ctx, a.PaneID, luvus.ScreenLines)
	if err != nil || s.TerminalID != a.TerminalID {
		return false, nil
	}
	deny := "esc"
	if perm, ok := harness.ClaudePermission(s.Text); ok {
		deny = perm.Deny
	} else if !harness.ClaudeDialog(s.Text) {
		return false, nil
	}
	if err := pressKeys(ctx, c, a.PaneID, a.TerminalID, []string{deny}, s.ContentRevision, luvus.ScreenDigest(s.Text)); err != nil {
		return false, nil
	}
	detail := "no answer in " + wait.String() + "; pressed " + deny
	if err := w.st.NoteAttempt(ctx, a.ID, "denied", detail); err != nil {
		if errors.Is(err, state.ErrConflict) {
			return false, nil
		}
		return false, err
	}
	ref := state.AttemptRef(a.ID)
	w.alert(ctx, "attempt.denied", ref, ref+" denied: "+detail)
	return true, nil
}
