package cli

import (
	"context"
	"errors"
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
	inputs, err := w.st.UnansweredSupervisorInputs(ctx, answerLookback)
	sups, err2 := w.st.Supervisors(ctx)
	if err != nil || err2 != nil {
		return
	}
	now := w.r.env.Now()
	groups, ended := map[answerSession][]transcript.Sent{}, map[answerSession]bool{}
	for _, in := range inputs {
		at, err := time.Parse(time.RFC3339Nano, in.DeliveredAt)
		if err != nil || now.Sub(at) > answerHorizon {
			continue
		}
		if sup, ok := deliveredTo(sups, at); ok && sup.Session != "" {
			k := answerSession{sup.Harness, sup.Session}
			groups[k] = append(groups[k], transcript.Sent{ID: in.ID, Body: in.Body, CreatedAt: in.CreatedAt})
			ended[k] = !sup.Live()
		}
	}
	if len(groups) == 0 {
		w.answers = nil
		return
	}
	if w.answers == nil {
		w.answers = &transcript.Reader{Paths: w.r.transcriptPaths()}
	}
	for k, sent := range groups {
		dir := w.r.home
		if k.harness == "omp" {
			dir = harness.OmpAgent(w.r.env.Getenv, w.r.home)
		}
		read := w.answers.Read
		if ended[k] {
			read = w.answers.ReadEnded
		}
		entries, err := read(ctx, k.harness, k.session, dir)
		if err != nil {
			continue
		}
		for id, at := range transcript.Answers(entries, sent) {
			if err := w.st.AnswerSupervisorInput(ctx, id, at); err != nil && !errors.Is(err, state.ErrConflict) {
				w.say("supervisor answer: " + err.Error())
			}
		}
	}
}
