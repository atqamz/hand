package cli

import (
	"context"
	"errors"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

const answerLookback = 20

func (w *watcher) stampAnswers(ctx context.Context) {
	sup, ok, err := w.st.LiveSupervisor(ctx)
	if err != nil || !ok || sup.Session == "" {
		return
	}
	inputs, err := w.st.UnansweredSupervisorInputs(ctx, answerLookback)
	start, found, err2 := w.st.SessionStart(ctx, sup.Session)
	if err != nil || err2 != nil || !found {
		return
	}
	var sent []transcript.Sent
	for _, in := range inputs {
		if at, err := time.Parse(time.RFC3339Nano, in.DeliveredAt); err == nil && !at.Before(start) {
			sent = append(sent, transcript.Sent{ID: in.ID, Body: in.Body, CreatedAt: in.CreatedAt})
		}
	}
	if len(sent) == 0 {
		if w.answers != nil {
			w.answers.Forget(sup.Harness, sup.Session)
		}
		return
	}
	if w.answers == nil {
		w.answers = &transcript.Reader{Paths: w.r.transcriptPaths()}
	}
	dir := w.r.home
	if sup.Harness == "omp" {
		dir = harness.OmpAgent(w.r.env.Getenv, w.r.home)
	}
	entries, err := w.answers.Read(ctx, sup.Harness, sup.Session, dir)
	if err != nil {
		return
	}
	for id, at := range transcript.Answers(entries, sent) {
		if err := w.st.AnswerSupervisorInput(ctx, id, at); err != nil && !errors.Is(err, state.ErrConflict) {
			w.say("supervisor answer: " + err.Error())
		}
	}
}
