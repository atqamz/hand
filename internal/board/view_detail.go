package board

import (
	"context"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

type taskView struct {
	Card        card
	Pill        string
	Attempts    []check
	Decisions   []state.Decision
	Unread      []state.Report
	Reports     []state.Report
	MoreReports bool
	Events      []state.Event
	MoreEvents  bool
}

func (b *Board) buildTask(ctx context.Context, t state.Task) (taskView, error) {
	c, err := b.card(ctx, t, 50)
	if err != nil {
		return taskView{}, err
	}
	reports, err := b.st.Reports(ctx, state.ReportFilter{TaskID: t.ID}, historyLimit+1)
	if err != nil {
		return taskView{}, err
	}
	unread, err := b.st.Reports(ctx, state.ReportFilter{TaskID: t.ID, Unacked: true}, maxCards)
	if err != nil {
		return taskView{}, err
	}
	v := taskView{Card: c, Pill: taskPill[t.Status], Unread: unread}
	v.MoreReports = len(reports) > historyLimit
	if v.MoreReports {
		reports = reports[1:]
	}
	slices.Reverse(reports)
	v.Reports = reports
	events, err := b.st.TaskEvents(ctx, t.ID, historyLimit+1)
	if err != nil {
		return taskView{}, err
	}
	v.MoreEvents = len(events) > historyLimit
	if v.MoreEvents {
		events = events[1:]
	}
	slices.Reverse(events)
	v.Events = events
	done, err := b.st.DoneReportAttempts(ctx)
	if err != nil {
		return taskView{}, err
	}
	rows := make([]check, 0, len(c.Attempts))
	for _, a := range c.Attempts {
		rows = append(rows, check{Task: t, Attempt: &a, State: attemptState(a, done)})
	}
	v.Attempts = rows
	v.Decisions, err = b.st.Decisions(ctx, t.ID, historyLimit)
	if err != nil {
		return taskView{}, err
	}
	return v, nil
}

type attemptView struct {
	Attempt     state.Attempt
	ModelNote   string
	Task        state.Task
	State       string
	Brief       string
	Launch      string
	Reports     []state.Report
	MoreReports bool
	Events      []state.Event
	MoreEvents  bool
	Delivered   []Deliverable
	Duration    string
	Transcript  workerView
}

func (b *Board) buildAttempt(ctx context.Context, id int64) (attemptView, error) {
	a, err := b.st.Attempt(ctx, id)
	if err != nil {
		return attemptView{}, err
	}
	t, err := b.st.Task(ctx, a.TaskID)
	if err != nil {
		return attemptView{}, err
	}
	reports, err := b.st.Reports(ctx, state.ReportFilter{AttemptID: id}, historyLimit+1)
	if err != nil {
		return attemptView{}, err
	}
	moreReports := len(reports) > historyLimit
	if moreReports {
		reports = reports[1:]
	}
	slices.Reverse(reports)
	events, moreEvents, err := b.st.AttemptEvents(ctx, a.TaskID, id, historyLimit)
	if err != nil {
		return attemptView{}, err
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
		return attemptView{}, err
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
	note, err := b.st.ModelNote(ctx, state.AttemptRef(id), a.Model, a.Effort)
	if err != nil {
		return attemptView{}, err
	}
	return attemptView{
		Attempt: a, ModelNote: note, Task: t, State: attemptState(a, done),
		Brief: brief, Launch: launch, Reports: reports, MoreReports: moreReports, Events: events, MoreEvents: moreEvents,
		Delivered: b.linked(Newest(reports, nil)), Duration: elapsed(end.Sub(parse(a.CreatedAt))),
		Transcript: b.workerTurns(ctx, a),
	}, nil
}

type decisionView struct {
	Decision state.Decision
	Task     state.Task
	Pill     string
}

func (b *Board) buildDecision(ctx context.Context, id int64) (decisionView, error) {
	d, err := b.st.Decision(ctx, id)
	if err != nil {
		return decisionView{}, err
	}
	t, err := b.st.Task(ctx, d.TaskID)
	if err != nil {
		return decisionView{}, err
	}
	return decisionView{Decision: d, Task: t, Pill: decisionPill[d.Status]}, nil
}

type reportView struct {
	R   any
	Rep state.Report
}

func rep(root any, r state.Report) reportView { return reportView{R: root, Rep: r} }
