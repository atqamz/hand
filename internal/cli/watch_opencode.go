package cli

import (
	"context"
	"slices"

	"github.com/atqamz/hand/internal/transcript"
)

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
				if bin := w.r.transcriptPaths().Opencode; bin != "" {
					listed, _ = transcript.OpencodeModels(ctx, bin, w.r.home)
				}
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
			id := ""
			if i := slices.Index(a.Argv, "--session"); i >= 0 && i+1 < len(a.Argv) {
				id = a.Argv[i+1]
			}
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
