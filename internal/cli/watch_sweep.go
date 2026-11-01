package cli

import (
	"cmp"
	"context"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

type modelSource interface {
	attemptSession(ctx context.Context, a state.Attempt) string
	labels(ctx context.Context) func(session string, since time.Time) (string, string)
	forget(session string)
}

type modelSession struct{ harness, id string }

func (w *watcher) sweepModels(ctx context.Context) {
	if !w.sweepBusy.CompareAndSwap(false, true) {
		return
	}
	w.pending.Add(1)
	go func() {
		defer w.pending.Done()
		defer w.sweepBusy.Store(false)
		if w.sources == nil {
			w.transcripts = &transcript.Reader{Paths: w.r.transcriptPaths()}
			w.modelSessions = map[int64]modelSession{}
			w.sources = map[string]modelSource{
				"opencode": &opencodeSource{w: w, read: map[string]bool{}},
				"codex":    &codexSource{w: w},
			}
		}
		labels := map[string]func(string, time.Time) (string, string){}
		labelOf := func(harness, session string, since time.Time) (string, string) {
			f := labels[harness]
			if f == nil {
				f = w.sources[harness].labels(ctx)
				labels[harness] = f
			}
			return f(session, since)
		}
		var sup modelSession
		if s, ok, err := w.st.LiveSupervisor(ctx); err == nil && ok && w.sources[s.Harness] != nil && s.Session != "" {
			sup = modelSession{s.Harness, s.Session}
			since, _ := time.Parse(time.RFC3339Nano, s.CreatedAt)
			model, effort := labelOf(s.Harness, s.Session, since)
			if model != "" && ctx.Err() == nil {
				_ = w.st.SetSupervisorModel(ctx, s.ID, model, effort)
			}
		}
		if w.supModel != sup {
			if old := w.sources[w.supModel.harness]; old != nil {
				old.forget(w.supModel.id)
			}
			w.supModel = sup
		}
		live, err := w.st.LiveAttempts(ctx)
		if err != nil {
			return
		}
		running := map[int64]bool{}
		for _, a := range live {
			running[a.ID] = true
			src := w.sources[a.Harness]
			if src == nil || ctx.Err() != nil {
				continue
			}
			id := cmp.Or(w.modelSessions[a.ID].id, src.attemptSession(ctx, a))
			if id == "" {
				continue
			}
			w.modelSessions[a.ID] = modelSession{a.Harness, id}
			if model, effort := labelOf(a.Harness, id, time.Time{}); model != "" && ctx.Err() == nil {
				_ = w.st.SetAttemptModel(ctx, a.ID, model, effort)
			}
		}
		for id, session := range w.modelSessions {
			if !running[id] {
				w.sources[session.harness].forget(session.id)
				delete(w.modelSessions, id)
			}
		}
	}()
}

type codexSource struct{ w *watcher }

func (c *codexSource) attemptSession(_ context.Context, a state.Attempt) string {
	created, err := time.Parse(time.RFC3339Nano, a.CreatedAt)
	if err != nil || len(a.Argv) == 0 {
		return ""
	}
	dir := a.Worktree
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	id, _ := harness.CodexSession(harness.CodexHome(c.w.r.env.Getenv), dir, created.Truncate(time.Second), a.Argv[len(a.Argv)-1])
	return id
}

func (c *codexSource) labels(ctx context.Context) func(string, time.Time) (string, string) {
	return func(session string, since time.Time) (string, string) {
		if _, err := c.w.transcripts.Read(ctx, "codex", session, c.w.r.home); err != nil {
			return "", ""
		}
		s := c.w.transcripts.Status("codex", session)
		if at, err := time.Parse(time.RFC3339Nano, s.ModelAt); err == nil && at.Before(since) {
			return "", ""
		}
		return s.Model, s.Effort
	}
}

func (c *codexSource) forget(session string) { c.w.transcripts.Forget("codex", session) }
