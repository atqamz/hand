package cli

import (
	"context"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

const promptPrefix = 200

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
		}
		if sup, ok, err := w.st.LiveSupervisor(ctx); err == nil && ok && sup.Harness == "opencode" && sup.Session != "" {
			model, effort := w.opencodeModel(ctx, sup.Session)
			if model != "" && ctx.Err() == nil {
				_ = w.st.SetSupervisorModel(ctx, sup.ID, model, effort)
			}
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
			id := w.opencodeSessions[a.ID]
			if id == "" {
				id = w.workerSession(a)
				w.opencodeSessions[a.ID] = id
			}
			if id == "" {
				continue
			}
			if model, effort := w.opencodeModel(ctx, id); model != "" && ctx.Err() == nil {
				_ = w.st.SetAttemptModel(ctx, a.ID, model, effort)
			}
		}
		for id := range w.opencodeSessions {
			if !running[id] {
				delete(w.opencodeSessions, id)
			}
		}
	}()
}

func (w *watcher) opencodeModel(ctx context.Context, session string) (string, string) {
	if _, err := w.transcripts.Read(ctx, "opencode", session, w.r.home); err != nil {
		return "", ""
	}
	s := w.transcripts.Status("opencode", session)
	return s.Model, s.Effort
}

func (w *watcher) workerSession(a state.Attempt) string {
	bin := w.r.transcriptPaths().Opencode
	created, err := time.Parse(time.RFC3339Nano, a.CreatedAt)
	if bin == "" || a.Worktree == "" || len(a.Argv) == 0 || err != nil {
		return ""
	}
	dir := a.Worktree
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	prompt := a.Argv[len(a.Argv)-1]
	id, _ := harness.OpencodeSession(bin, dir, created.Truncate(time.Second), prompt[:min(len(prompt), promptPrefix)])
	return id
}
