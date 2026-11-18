package cli

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

const (
	answerLookback = 20
	answerHorizon  = 30 * time.Minute
)

type answerSession struct{ harness, session string }

func deliveredTo(sups []state.Supervisor, at time.Time) (state.Supervisor, bool) {
	var out state.Supervisor
	found := false
	for _, s := range sups {
		born, err := time.Parse(time.RFC3339Nano, s.CreatedAt)
		if err != nil || at.Before(born) {
			continue
		}
		if end, err := time.Parse(time.RFC3339Nano, s.EndedAt); s.EndedAt != "" && (err != nil || at.After(end)) {
			continue
		}
		out, found = s, true
	}
	return out, found
}

func (w *watcher) stampAnswers(ctx context.Context) {
	inputs, err := w.st.DeliveredSupervisorInputs(ctx, answerLookback)
	sups, err2 := w.st.Supervisors(ctx)
	if err != nil || err2 != nil {
		return
	}
	slices.Reverse(inputs)
	now := w.r.env.Now()
	open := map[int64]bool{}
	for _, in := range inputs {
		if in.AnsweredAt == "" {
			open[in.ID] = true
		}
	}
	for id := range w.matched {
		if !open[id] {
			delete(w.matched, id)
		}
	}
	groups, ended, live := map[answerSession][]transcript.Sent{}, map[answerSession]bool{}, map[answerSession]bool{}
	for _, in := range inputs {
		at, err := time.Parse(time.RFC3339Nano, in.DeliveredAt)
		if err != nil {
			continue
		}
		if sup, ok := deliveredTo(sups, at); ok && sup.Session != "" {
			k := answerSession{sup.Harness, sup.Session}
			groups[k] = append(groups[k], transcript.Sent{ID: in.ID, Body: in.Body, CreatedAt: in.CreatedAt})
			ended[k] = !sup.Live()
			if open[in.ID] && (now.Sub(at) <= answerHorizon || w.matched[in.ID]) {
				live[k] = true
			}
		}
	}
	if len(live) == 0 {
		w.answers = nil
		return
	}
	if w.answers == nil {
		w.answers = &transcript.Reader{Paths: w.r.transcriptPaths()}
	}
	if w.matched == nil {
		w.matched = map[int64]bool{}
	}
	for k, sent := range groups {
		if !live[k] {
			continue
		}
		dir := w.transcriptDir(k.harness)
		read := w.answers.Read
		if ended[k] {
			read = w.answers.ReadEnded
		}
		entries, err := read(ctx, k.harness, k.session, dir)
		if err != nil {
			continue
		}
		for id := range transcript.Pair(entries, sent) {
			if open[id] {
				w.matched[id] = true
			}
		}
		for id, at := range transcript.Answers(entries, sent) {
			if !open[id] {
				continue
			}
			if err := w.st.AnswerSupervisorInput(ctx, id, at); err != nil && !errors.Is(err, state.ErrConflict) {
				w.say("supervisor answer: " + err.Error())
			}
		}
	}
}

func (w *watcher) transcriptDir(name string) string {
	if name == "omp" {
		return harness.OmpAgent(w.r.env.Getenv, w.r.home)
	}
	return w.r.home
}
