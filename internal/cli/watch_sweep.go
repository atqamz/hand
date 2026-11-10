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
	labels(ctx context.Context, dir string) func(session string, since time.Time) (string, string)
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
				"codex":    &codexSource{transcriptSource{w, "codex"}},
				"claude":   &claudeSource{transcriptSource{w, "claude"}},
				"copilot":  &copilotSource{transcriptSource{w, "copilot"}},
				"omp":      &ompSource{transcriptSource{w, "omp"}},
			}
		}
		labels := map[string]func(string, time.Time) (string, string){}
		labelOf := func(harness, session string, since time.Time, dir string) (string, string) {
			key := harness + ":" + dir
			f := labels[key]
			if f == nil {
				f = w.sources[harness].labels(ctx, dir)
				labels[key] = f
			}
			return f(session, since)
		}
		var sup modelSession
		if s, ok, err := w.st.LiveSupervisor(ctx); err == nil && ok && w.sources[s.Harness] != nil && s.Session != "" {
			sup = modelSession{s.Harness, s.Session}
			since, _ := time.Parse(time.RFC3339Nano, s.CreatedAt)
			dir := w.r.home
			if s.Harness == "omp" {
				dir = harness.OmpAgent(w.r.env.Getenv, w.r.home)
			}
			model, effort := labelOf(s.Harness, s.Session, since, dir)
			w.supContext.Store(contextRead{s.ID, s.Session, w.transcripts.Status(s.Harness, s.Session).Context})
			if model != "" && ctx.Err() == nil {
				model, effort = settle(s.Harness, s.Model, s.Effort, model, effort)
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
			dir := w.r.home
			if a.Harness == "omp" {
				dir = harness.OmpAgent(w.r.env.Getenv, a.Worktree)
			}
			if model, effort := labelOf(a.Harness, id, time.Time{}, dir); model != "" && ctx.Err() == nil {
				model, effort = settle(a.Harness, a.Model, a.Effort, model, effort)
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

func settle(name, storedModel, storedEffort, model, effort string) (string, string) {
	if name == "claude" && harness.Resolves(storedModel, model) {
		model = storedModel
	}
	if name == "omp" {
		return model, storedEffort
	}
	if effort == "" && (name == "claude" || name == "copilot") {
		effort = storedEffort
	}
	return model, effort
}

type transcriptSource struct {
	w       *watcher
	harness string
}

func (t transcriptSource) labels(ctx context.Context, dir string) func(string, time.Time) (string, string) {
	return func(session string, since time.Time) (string, string) {
		if err := t.w.transcripts.Refresh(ctx, t.harness, session, dir); err != nil {
			return "", ""
		}
		s := t.w.transcripts.Status(t.harness, session)
		if !harness.ValidServed(s.Model, s.Effort) {
			return "", ""
		}
		if !since.IsZero() {
			if at, err := time.Parse(time.RFC3339Nano, s.ModelAt); err != nil || at.Before(since) {
				return "", ""
			}
		}
		return s.Model, s.Effort
	}
}

func (t transcriptSource) forget(session string) { t.w.transcripts.Forget(t.harness, session) }

type claudeSource struct{ transcriptSource }

func (claudeSource) attemptSession(_ context.Context, a state.Attempt) string {
	return argvValue(a.Argv, "--session-id")
}

type copilotSource struct{ transcriptSource }

func (copilotSource) attemptSession(_ context.Context, a state.Attempt) string {
	return argvValue(a.Argv, "--session-id")
}

type ompSource struct{ transcriptSource }

func (o *ompSource) attemptSession(_ context.Context, a state.Attempt) string {
	created, err := time.Parse(time.RFC3339Nano, a.CreatedAt)
	if err != nil || len(a.Argv) == 0 {
		return ""
	}
	dir := a.Worktree
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	id, _ := harness.OmpSession(harness.OmpAgent(o.w.r.env.Getenv, a.Worktree), dir, created.Truncate(time.Second), a.Argv[len(a.Argv)-1])
	return id
}

type codexSource struct{ transcriptSource }

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
