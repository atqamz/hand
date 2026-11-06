package cli

import (
	"context"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

type busyList struct {
	at   time.Time
	ids  map[string]bool
	idle map[string]time.Time
}

func (w *watcher) hold(ctx context.Context, a state.Attempt) {
	w.seen[a.ID] = "held"
	w.heldAt[a.ID] = w.r.env.Now()
	if w.busy == nil {
		w.sweepModels(ctx)
	}
}

func (w *watcher) listSessions(ctx context.Context) map[string]transcript.ModelLabel {
	at := w.r.env.Now()
	var models map[string]transcript.ModelLabel
	var busy map[string]bool
	var idle map[string]time.Time
	if bin := w.r.transcriptPaths().Opencode; bin != "" {
		bctx, cancel := context.WithTimeout(ctx, apiCheckWait)
		models, busy, idle, _ = transcript.OpencodeSessions(bctx, bin, w.r.home, at, longTurn)
		cancel()
	}
	select {
	case <-w.busyNow:
	default:
	}
	select {
	case w.busyNow <- busyList{at, busy, idle}:
	default:
	}
	return models
}

func (w *watcher) held(ctx context.Context, c luvus.Client, caps luvus.Capabilities, b busyList) error {
	if len(w.heldAt) == 0 {
		w.busyMisses = 0
		return nil
	}
	if b.ids != nil {
		w.busyMisses = 0
	} else if w.busyMisses++; w.busyMisses < 2 {
		return nil
	} else {
		w.busyMisses, b.ids = 0, map[string]bool{}
	}
	live, err := w.st.LiveAttempts(ctx)
	if err != nil {
		return err
	}
	w.busy = b.ids
	defer func() { w.busy = nil }()
	for _, a := range live {
		if a.Status != state.AttemptRunning || a.ServerGeneration != caps.ServerGeneration || w.heldAt[a.ID].After(b.at) {
			continue
		}
		switch w.seen[a.ID] {
		case "held":
		case "idle?":
			session := argvSession(a.Argv)
			if !b.ids[session] {
				since, _, err := w.st.TurnStart(ctx, a.ID)
				if err != nil {
					return err
				}
				if since.IsZero() || !b.idle[session].After(since) {
					delete(w.blockedPending, a.ID)
					w.seen[a.ID] = "idle"
					delete(w.heldAt, a.ID)
					continue
				}
			}
		default:
			continue
		}
		ag, err := c.Explain(ctx, a.PaneID)
		if err != nil {
			continue
		}
		w.seen[a.ID] = "working"
		delete(w.heldAt, a.ID)
		if err := w.observe(ctx, c, a, ag.Status); err != nil {
			return err
		}
	}
	return nil
}

type opencodeSource struct {
	w    *watcher
	read map[string]bool
}

func (o *opencodeSource) attemptSession(_ context.Context, a state.Attempt) string {
	return argvSession(a.Argv)
}

func (o *opencodeSource) labels(ctx context.Context, _ string) func(string, time.Time) (string, string) {
	var listed map[string]transcript.ModelLabel
	fetched := false
	return func(session string, _ time.Time) (string, string) {
		if !fetched {
			fetched = true
			listed = o.w.listSessions(ctx)
		}
		return o.label(ctx, listed, session)
	}
}

func (o *opencodeSource) forget(session string) {
	o.w.transcripts.Forget("opencode", session)
	delete(o.read, session)
}

func (o *opencodeSource) label(ctx context.Context, listed map[string]transcript.ModelLabel, session string) (string, string) {
	if l := listed[session]; l.Model != "" {
		return l.Model, l.Effort
	}
	if listed == nil || !o.read[session] {
		if _, err := o.w.transcripts.Read(ctx, "opencode", session, o.w.r.home); err != nil {
			return "", ""
		}
		o.read[session] = listed != nil && o.w.transcripts.Status("opencode", session).Model != ""
	}
	s := o.w.transcripts.Status("opencode", session)
	return s.Model, s.Effort
}
