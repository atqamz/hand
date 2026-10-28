package board

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

func shown(s string) string {
	var out strings.Builder
	for _, r := range strings.ReplaceAll(s, "\r\n", "\n") {
		if bidi(r) || unicode.IsControl(r) && r != '\n' && r != '\t' || r == 0x2028 || r == 0x2029 {
			fmt.Fprintf(&out, "U+%04X", r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func attemptState(a state.Attempt, done map[int64]bool) string {
	switch {
	case a.Live():
		return "running"
	case facts{done: done}.failing(a):
		return "failing"
	case done[a.ID]:
		return "passing"
	}
	return "idle"
}

func (b *Board) attempt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "a")
	if err != nil {
		b.failErr(w, err)
		return
	}
	a, err := b.st.Attempt(ctx, id)
	if err != nil {
		b.failErr(w, err)
		return
	}
	t, err := b.st.Task(ctx, a.TaskID)
	if err != nil {
		b.failErr(w, err)
		return
	}
	reports, err := b.st.Reports(ctx, state.ReportFilter{AttemptID: id}, historyLimit+1)
	if err != nil {
		b.failErr(w, err)
		return
	}
	moreReports := len(reports) > historyLimit
	if moreReports {
		reports = reports[1:]
	}
	slices.Reverse(reports)
	events, moreEvents, err := b.st.AttemptEvents(ctx, a.TaskID, id, historyLimit)
	if err != nil {
		b.failErr(w, err)
		return
	}
	self := state.AttemptRef(id)
	for i := range events {
		if events[i].Detail == self {
			events[i].Detail = ""
		}
		events[i].Detail = strings.TrimPrefix(events[i].Detail, self+": ")
	}
	done, err := b.st.DoneReportAttempts(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	end := b.now()
	if a.EndedAt != "" {
		end = parse(a.EndedAt)
	}
	var brief, launch string
	if n := len(a.Argv); n > 1 {
		brief, launch = a.Argv[n-1], strings.Join(a.Argv[:n-1], " ")
	} else {
		launch = strings.Join(a.Argv, " ")
	}
	turns, turnsNote, modelNote, answered := b.workerTurns(ctx, a)
	b.render(w, http.StatusOK, "attempt.html", map[string]any{
		"Title": state.AttemptRef(id), "Page": "attempt", "Fleets": b.fleets(ctx), "Attempt": a, "Task": t, "Token": b.csrf(ctx), "State": attemptState(a, done),
		"Brief": brief, "Launch": launch, "Reports": reports, "MoreReports": moreReports, "Events": events, "MoreEvents": moreEvents,
		"Delivered": b.linked(Newest(reports, nil)), "Duration": elapsed(end.Sub(parse(a.CreatedAt))),
		"Turns": turns, "TurnsNote": turnsNote, "ModelNote": modelNote, "Answered": answered, "Transcribed": a.Harness == "copilot" && b.o.Transcript != nil, "StripData": b.stripData(ctx), "Titles": b.refTitle(ctx),
	})
}

func (b *Board) workerTurns(ctx context.Context, a state.Attempt) ([]transcript.Entry, string, string, string) {
	i := slices.Index(a.Argv, "--session-id")
	if a.Harness != "copilot" || b.o.Transcript == nil || i < 0 || i+1 >= len(a.Argv) {
		return nil, "", "", ""
	}
	id := a.Argv[i+1]
	entries, err := b.o.Transcript.Read(ctx, a.Harness, id, b.o.Home)
	if err != nil {
		return nil, Scrub(err.Error()), "", ""
	}
	if n := len(entries) - historyLimit; n > 0 {
		entries = entries[n:]
	}
	st := b.o.Transcript.Status(a.Harness, id)
	var note string
	if st.Used != "" && !strings.EqualFold(st.Selected, "auto") && st.Selected != st.Used {
		note = "Asked for " + st.Selected + ", answered by " + st.Used + "."
	}
	return entries, "", note, st.Used
}
