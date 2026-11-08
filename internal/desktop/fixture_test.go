package desktop_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

const (
	fleetID  = "f00000000e0aa"
	secondID = "f00000000e0ab"
)

type fixture struct {
	fleets   []desktop.Fleet
	status   map[string]desktop.Status
	timeline map[string]desktop.Timeline
	older    map[string]desktop.Timeline
	tasks    map[string]desktop.Tasks
	task     map[string]desktop.TaskDetail
	attempt  map[string]desktop.AttemptDetail
	decision map[string]desktop.DecisionDetail
	extra    map[string]desktop.DecisionDetail
	report   map[string]desktop.ReportDetail
	fail     error
	stall    string
	nameIn   chan struct{}
	block    chan struct{}
	entered  chan struct{}
	mu       sync.Mutex
	calls    map[string]int
}

func (f *fixture) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

func (f *fixture) note(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[name]++
}

func (f *fixture) Fleets() []desktop.Fleet { return append([]desktop.Fleet(nil), f.fleets...) }

func (f *fixture) Name(ctx context.Context, id string) (string, error) {
	f.note("name:" + id)
	if f.fail != nil {
		return "", f.fail
	}
	if f.stall == id {
		if f.nameIn != nil {
			f.nameIn <- struct{}{}
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	for _, fleet := range f.fleets {
		if fleet.ID == id {
			return fleet.Name, nil
		}
	}
	return "", errors.New("no such fleet")
}

func (f *fixture) Status(ctx context.Context, id string) (desktop.Status, error) {
	if f.block != nil {
		if f.entered != nil {
			f.entered <- struct{}{}
		}
		<-f.block
	}
	if f.fail != nil {
		return desktop.Status{}, f.fail
	}
	return f.status[id], nil
}

func (f *fixture) Timeline(ctx context.Context, id string, before int) (desktop.Timeline, error) {
	if f.fail != nil {
		return desktop.Timeline{}, f.fail
	}
	if before > 0 {
		return f.older[id], nil
	}
	return f.timeline[id], nil
}

func (f *fixture) Tasks(ctx context.Context, id string, all bool) (desktop.Tasks, error) {
	f.note("tasks")
	if f.fail != nil {
		return desktop.Tasks{}, f.fail
	}
	return f.tasks[id], nil
}

func (f *fixture) Task(ctx context.Context, id, ref string) (desktop.TaskDetail, error) {
	if f.fail != nil {
		return desktop.TaskDetail{}, f.fail
	}
	d, ok := f.task[id]
	if !ok || (ref != "" && d.Task.Ref != ref) {
		return desktop.TaskDetail{}, &desktop.APIError{Status: 404, Code: "not_found", Message: "not found: task " + ref}
	}
	return d, nil
}

func (f *fixture) Attempt(ctx context.Context, id, ref string) (desktop.AttemptDetail, error) {
	if f.fail != nil {
		return desktop.AttemptDetail{}, f.fail
	}
	d, ok := f.attempt[id]
	if !ok || (ref != "" && d.Attempt.Ref != ref) {
		return desktop.AttemptDetail{}, &desktop.APIError{Status: 404, Code: "not_found", Message: "not found: attempt " + ref}
	}
	return d, nil
}

func (f *fixture) Decision(ctx context.Context, id, ref string) (desktop.DecisionDetail, error) {
	if f.fail != nil {
		return desktop.DecisionDetail{}, f.fail
	}
	if d, ok := f.extra[ref]; ok {
		return d, nil
	}
	d, ok := f.decision[id]
	if !ok || (ref != "" && d.Decision.Ref != ref) {
		return desktop.DecisionDetail{}, &desktop.APIError{Status: 404, Code: "not_found", Message: "not found: decision " + ref}
	}
	return d, nil
}

func (f *fixture) Report(ctx context.Context, id, ref string) (desktop.ReportDetail, error) {
	if f.fail != nil {
		return desktop.ReportDetail{}, f.fail
	}
	d, ok := f.report[id]
	if !ok || (ref != "" && d.Report.Ref != ref) {
		return desktop.ReportDetail{}, &desktop.APIError{Status: 404, Code: "not_found", Message: "not found: report " + ref}
	}
	return d, nil
}

func goldenFixture(t *testing.T) *fixture {
	t.Helper()
	load := func(name string, out any) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "board", "testdata", "api", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var env desktop.Envelope
		if err := json.Unmarshal(b, &env); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(env.Data, out); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{
		fleets:   []desktop.Fleet{{ID: fleetID, Name: "golden"}, {ID: secondID, Name: "second"}},
		status:   map[string]desktop.Status{},
		timeline: map[string]desktop.Timeline{},
		tasks:    map[string]desktop.Tasks{},
		task:     map[string]desktop.TaskDetail{},
		attempt:  map[string]desktop.AttemptDetail{},
		decision: map[string]desktop.DecisionDetail{},
		report:   map[string]desktop.ReportDetail{},
	}
	var st desktop.Status
	var tl desktop.Timeline
	var ts desktop.Tasks
	var td desktop.TaskDetail
	var ad desktop.AttemptDetail
	var dd desktop.DecisionDetail
	var rd desktop.ReportDetail
	load("status", &st)
	load("timeline", &tl)
	load("tasks", &ts)
	load("task", &td)
	load("attempt-running", &ad)
	load("decision", &dd)
	load("report", &rd)
	td.Decisions = append([]desktop.Decision{{
		Ref: "d0", Task: "t1", Status: "answered", Pill: "pass",
		Headline:   "Rename the helper?",
		Question:   "Rename the helper?\n\n1. Yes\n2. No",
		Answer:     "Yes",
		CreatedAt:  "2026-10-31T04:40:00Z",
		ClosedAt:   "2026-10-31T05:00:00Z",
		AnsweredBy: "you",
	}}, td.Decisions...)
	second := st
	second.Line = "the second fleet's status"
	second.Pill = "ready"
	for _, id := range []string{fleetID, secondID} {
		f.status[id] = st
		f.timeline[id] = tl
		f.tasks[id] = ts
		f.task[id] = td
		f.attempt[id] = ad
		f.decision[id] = dd
		f.report[id] = rd
	}
	f.status[secondID] = second
	return f
}

func emptyFixture() *fixture {
	return &fixture{
		fleets:   []desktop.Fleet{{ID: fleetID, Name: "quiet"}},
		status:   map[string]desktop.Status{fleetID: {AgentState: "idle", Pill: "ready", Line: "waiting", Needs: desktop.Needs{}}},
		timeline: map[string]desktop.Timeline{fleetID: {}},
		tasks:    map[string]desktop.Tasks{fleetID: {}},
		task:     map[string]desktop.TaskDetail{},
		attempt:  map[string]desktop.AttemptDetail{},
		decision: map[string]desktop.DecisionDetail{},
		report:   map[string]desktop.ReportDetail{},
	}
}

func errorFixture() *fixture {
	return &fixture{fleets: []desktop.Fleet{{ID: fleetID, Name: "golden"}}, fail: &desktop.APIError{
		Status: 401, Code: "unauthorized", Message: "this request needs the board's session cookie",
	}}
}

func newApp(t *testing.T, f *fixture) *desktop.App {
	t.Helper()
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.Boot()
	if app.Err != "" && f.fail == nil {
		t.Fatalf("boot: %s", app.Err)
	}
	return app
}
