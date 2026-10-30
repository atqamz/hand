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
		w.opencodeModels(ctx)
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

func (w *watcher) opencodeModels(ctx context.Context) {
	if !w.opencodeBusy.CompareAndSwap(false, true) {
		return
	}
	w.pending.Add(1)
	go func() {
		defer w.pending.Done()
		defer w.opencodeBusy.Store(false)
		if w.transcripts == nil {
			w.transcripts = &transcript.Reader{Paths: w.r.transcriptPaths()}
			w.opencodeSessions = map[int64]string{}
			w.opencodeRead = map[string]bool{}
		}
		var listed map[string]transcript.ModelLabel
		fetched := false
		modelOf := func(session string) (string, string) {
			if !fetched {
				fetched = true
				listed = w.listSessions(ctx)
			}
			return w.opencodeModel(ctx, listed, session)
		}
		supSession := ""
		if sup, ok, err := w.st.LiveSupervisor(ctx); err == nil && ok && sup.Harness == "opencode" && sup.Session != "" {
			supSession = sup.Session
			model, effort := modelOf(sup.Session)
			if model != "" && ctx.Err() == nil {
				_ = w.st.SetSupervisorModel(ctx, sup.ID, model, effort)
			}
		}
		if w.opencodeSup != supSession {
			w.forgetOpencode(w.opencodeSup)
			w.opencodeSup = supSession
		}
		live, err := w.st.LiveAttempts(ctx)
		if err != nil {
			return
		}
		running := map[int64]bool{}
		for _, a := range live {
			running[a.ID] = true
			if a.Harness != "opencode" || ctx.Err() != nil {
				continue
			}
			id := argvSession(a.Argv)
			if id == "" {
				continue
			}
			w.opencodeSessions[a.ID] = id
			if model, effort := modelOf(id); model != "" && ctx.Err() == nil {
				_ = w.st.SetAttemptModel(ctx, a.ID, model, effort)
			}
		}
		for id, session := range w.opencodeSessions {
			if !running[id] {
				w.forgetOpencode(session)
				delete(w.opencodeSessions, id)
			}
		}
	}()
}

func (w *watcher) forgetOpencode(session string) {
	w.transcripts.Forget("opencode", session)
	delete(w.opencodeRead, session)
}

func (w *watcher) opencodeModel(ctx context.Context, listed map[string]transcript.ModelLabel, session string) (string, string) {
	if l := listed[session]; l.Model != "" {
		return l.Model, l.Effort
	}
	if listed == nil || !w.opencodeRead[session] {
		if _, err := w.transcripts.Read(ctx, "opencode", session, w.r.home); err != nil {
			return "", ""
		}
		w.opencodeRead[session] = listed != nil && w.transcripts.Status("opencode", session).Model != ""
	}
	s := w.transcripts.Status("opencode", session)
	return s.Model, s.Effort
}
