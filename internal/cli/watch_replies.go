package cli

import (
	"context"
	"time"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

type replyMark struct {
	at time.Time
	n  int
}

func (w *watcher) ingestReplies(ctx context.Context) {
	if !w.repliesSeen {
		w.repliesSeen = true
		if last, ok, err := w.st.LatestSupervisor(ctx); err == nil && ok {
			w.repliesPrev = last
		}
	}
	cur, _, err := w.st.LiveSupervisor(ctx)
	if err != nil {
		return
	}
	if prev := w.repliesPrev; prev.ID != 0 && prev.ID != cur.ID && prev.Session != "" {
		w.storeReplies(ctx, prev, true)
		if prev.Session != cur.Session && w.replies != nil {
			w.replies.Forget(prev.Harness, prev.Session)
			delete(w.replyMarks, answerSession{prev.Harness, prev.Session})
		}
	}
	w.repliesPrev = cur
	if cur.ID != 0 && cur.Session != "" && cur.Harness != "opencode" {
		w.storeReplies(ctx, cur, false)
	}
}

func (w *watcher) storeReplies(ctx context.Context, sup state.Supervisor, ended bool) {
	if w.replies == nil {
		w.replies = &transcript.Reader{Paths: w.r.transcriptPaths()}
	}
	read := w.replies.Read
	if ended {
		read = w.replies.ReadEnded
	}
	entries, err := read(ctx, sup.Harness, sup.Session, w.transcriptDir(sup.Harness))
	if err != nil {
		return
	}
	key := answerSession{sup.Harness, sup.Session}
	mark, marked := w.replyMarks[key]
	var batch []state.Reply
	var newest replyMark
	counts := map[string]int{}
	for _, e := range entries {
		if e.Role != "supervisor" {
			continue
		}
		n := counts[e.At]
		counts[e.At]++
		if at, err := time.Parse(time.RFC3339Nano, e.At); err == nil {
			if marked && (at.Before(mark.at) || at.Equal(mark.at) && n < mark.n) {
				continue
			}
			switch {
			case at.After(newest.at):
				newest = replyMark{at, n + 1}
			case at.Equal(newest.at):
				newest.n = max(newest.n, n+1)
			}
		}
		batch = append(batch, state.Reply{At: e.At, N: n, Body: e.Text})
	}
	if len(batch) == 0 {
		return
	}
	if _, err := w.st.AddSupervisorReplies(ctx, sup.ID, sup.Session, batch); err != nil {
		return
	}
	if !newest.at.IsZero() {
		if w.replyMarks == nil {
			w.replyMarks = map[answerSession]replyMark{}
		}
		w.replyMarks[key] = newest
	}
}
