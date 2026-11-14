package desktop_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

const (
	fleetID  = "f00000000e0aa"
	secondID = "f00000000e00d"
)

type fixture struct {
	fleets        []desktop.Fleet
	status        map[string]desktop.Status
	timeline      map[string]desktop.Timeline
	older         map[string]desktop.Timeline
	tasks         map[string]desktop.Tasks
	task          map[string]desktop.TaskDetail
	attempt       map[string]desktop.AttemptDetail
	attemptRefs   map[string]desktop.AttemptDetail
	attemptErr    error
	decision      map[string]desktop.DecisionDetail
	extra         map[string]desktop.DecisionDetail
	report        map[string]desktop.ReportDetail
	fail          error
	action        error
	taskErr       error
	keepNeeds     bool
	actionBlock   chan struct{}
	actionEntered chan struct{}
	choice        string
	answerNote    string
	sentText      string
	controlStatus *desktop.Status
	controlStale  bool
	controlRef    string
	keyStatus     *desktop.Status
	keyStale      bool
	keyRef        string
	keys          []string
	keyRevision   int64
	keyDigest     string
	keyAfter      int64
	attemptKey    string
	attemptKeyRef string
	attemptKeyRev int64
	attemptKeyAft int64
	attemptScreen string
	attemptKeyErr error
	stale         bool
	stall         string
	stallOnce     bool
	nameIn        chan struct{}
	nameOut       chan error
	block         chan struct{}
	entered       chan struct{}
	attemptIn     chan chan error
	decisionIn    chan chan error
	pollBlock     chan struct{}
	pollEntered   chan struct{}
	pollExited    chan struct{}
	pollDone      chan struct{}
	pollErr       error
	pollScreen    desktop.Screen
	pollRef       string
	mu            sync.Mutex
	calls         map[string]int
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
	f.mu.Lock()
	stalled := f.stall == id
	if stalled && f.stallOnce {
		f.stall = ""
	}
	f.mu.Unlock()
	if stalled {
		if f.nameIn != nil {
			f.nameIn <- struct{}{}
		}
		<-ctx.Done()
		if f.nameOut != nil {
			f.nameOut <- ctx.Err()
		}
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
	f.note("status:" + id)
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
	f.mu.Lock()
	defer f.mu.Unlock()
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
	if f.taskErr != nil {
		return desktop.TaskDetail{}, f.taskErr
	}
	d, ok := f.task[id]
	if !ok || (ref != "" && d.Task.Ref != ref) {
		return desktop.TaskDetail{}, &desktop.APIError{Status: 404, Code: "not_found", Message: "not found: task " + ref}
	}
	return d, nil
}

func (f *fixture) Attempt(ctx context.Context, id, ref string) (desktop.AttemptDetail, error) {
	f.note("attempt:" + ref)
	if f.attemptIn != nil {
		ch := make(chan error, 1)
		f.attemptIn <- ch
		if err := <-ch; err != nil {
			return desktop.AttemptDetail{}, err
		}
	}
	if f.attemptErr != nil {
		return desktop.AttemptDetail{}, f.attemptErr
	}
	if f.fail != nil {
		return desktop.AttemptDetail{}, f.fail
	}
	if d, ok := f.attemptRefs[ref]; ok {
		return d, nil
	}
	d, ok := f.attempt[id]
	if !ok || (ref != "" && d.Attempt.Ref != ref) {
		return desktop.AttemptDetail{}, &desktop.APIError{Status: 404, Code: "not_found", Message: "not found: attempt " + ref}
	}
	return d, nil
}

func (f *fixture) Decision(ctx context.Context, id, ref string) (desktop.DecisionDetail, error) {
	if f.decisionIn != nil {
		ch := make(chan error, 1)
		f.decisionIn <- ch
		if err := <-ch; err != nil {
			return desktop.DecisionDetail{}, err
		}
	}
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

func (f *fixture) Poll(ctx context.Context, id string, screen desktop.Screen, ref string, since desktop.PollCursors) (desktop.Poll, error) {
	f.note("poll:" + id)
	f.mu.Lock()
	f.pollScreen, f.pollRef = screen, ref
	f.mu.Unlock()
	if f.pollBlock != nil {
		if f.pollEntered != nil {
			f.pollEntered <- struct{}{}
		}
		select {
		case <-f.pollBlock:
			if f.pollDone != nil {
				f.pollDone <- struct{}{}
			}
		case <-ctx.Done():
			if f.pollExited != nil {
				f.pollExited <- struct{}{}
			}
			return desktop.Poll{}, ctx.Err()
		}
	}
	if f.pollErr != nil {
		return desktop.Poll{}, f.pollErr
	}
	if f.fail != nil {
		return desktop.Poll{}, f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := desktop.Poll{StatusFresh: true, StatusRead: true, ScreenRead: true, Status: f.status[id], TimelineFresh: true, TimelineRead: true, Timeline: f.timeline[id]}
	switch screen {
	case desktop.BoardScreen:
		p.ScreenFresh, p.Tasks = true, f.tasks[id]
	case desktop.TaskScreen:
		p.ScreenFresh, p.Task = true, f.task[id]
	case desktop.AttemptScreen:
		p.ScreenFresh, p.Attempt = true, f.attempt[id]
	case desktop.DecisionScreen:
		p.ScreenFresh, p.Decision = true, f.decision[id]
	case desktop.ReportScreen:
		p.ScreenFresh, p.Report = true, f.report[id]
	}
	return p, nil
}

func (f *fixture) stallAction() {
	if f.actionBlock != nil {
		if f.actionEntered != nil {
			f.actionEntered <- struct{}{}
		}
		<-f.actionBlock
	}
}

func (f *fixture) AnswerDecision(ctx context.Context, id, ref, choice, note string) (desktop.DecisionDetail, error) {
	f.note("answer:" + ref)
	f.mu.Lock()
	f.choice, f.answerNote = choice, note
	f.mu.Unlock()
	f.stallAction()
	if f.action != nil {
		return desktop.DecisionDetail{}, f.action
	}
	d, ok := f.decision[id]
	if !ok || (ref != "" && d.Decision.Ref != ref) {
		return desktop.DecisionDetail{}, &desktop.APIError{Status: 404, Code: "not_found", Message: "not found: decision " + ref}
	}
	d.Decision.Status = "answered"
	d.Decision.Pill = "passing"
	d.Decision.Answer = strings.TrimSpace(choice + "\n\n" + note)
	d.Decision.AnsweredBy = "operator (desktop)"
	d.Decision.ClosedAt = "2026-11-01T05:00:00Z"
	f.decision[id] = d
	f.dropNeed(id, d.Decision.Ref)
	return d, nil
}

func (f *fixture) AckReport(ctx context.Context, id, ref string) (desktop.ReportDetail, error) {
	f.note("ack:" + ref)
	f.stallAction()
	if f.action != nil {
		return desktop.ReportDetail{}, f.action
	}
	d, ok := f.report[id]
	if !ok || (ref != "" && d.Report.Ref != ref) {
		return desktop.ReportDetail{}, &desktop.APIError{Status: 404, Code: "not_found", Message: "not found: report " + ref}
	}
	d.Report.AckedAt = "2026-11-01T05:00:00Z"
	d.Report.AckedBy = "operator (desktop)"
	f.report[id] = d
	f.dropNeed(id, d.Report.Ref)
	return d, nil
}

func (f *fixture) SendMessage(ctx context.Context, id, text string) (desktop.Sent, error) {
	f.note("send:" + id)
	f.mu.Lock()
	f.sentText = text
	f.mu.Unlock()
	f.stallAction()
	if f.action != nil {
		return desktop.Sent{}, f.action
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	tl := f.timeline[id]
	tl.Entries = append([]desktop.Entry{{Role: "operator", Text: text, At: "2026-11-01T05:00:00Z", Queued: true}}, tl.Entries...)
	f.timeline[id] = tl
	return desktop.Sent{Timeline: tl, Fresh: !f.stale}, nil
}

func (f *fixture) Supervise(ctx context.Context, id, action, ref string) (desktop.Control, error) {
	f.note("control:" + action)
	f.mu.Lock()
	f.controlRef = ref
	f.mu.Unlock()
	f.stallAction()
	if f.action != nil {
		return desktop.Control{}, f.action
	}
	f.mu.Lock()
	if f.controlStatus != nil {
		st := *f.controlStatus
		f.status[id] = st
		f.mu.Unlock()
		return desktop.Control{Status: st}, nil
	}
	st, stale := f.status[id], f.controlStale
	f.mu.Unlock()
	return desktop.Control{Status: st, Stale: stale}, nil
}

func (f *fixture) PressKeys(ctx context.Context, id string, keys []string, revision int64, digest string, after int64, ref string) (desktop.Control, error) {
	f.note("keys:" + strings.Join(keys, " "))
	f.mu.Lock()
	f.keys, f.keyRevision, f.keyDigest, f.keyAfter, f.keyRef = append([]string(nil), keys...), revision, digest, after, ref
	f.mu.Unlock()
	f.stallAction()
	if f.action != nil {
		return desktop.Control{}, f.action
	}
	f.mu.Lock()
	if f.keyStatus != nil {
		st := *f.keyStatus
		f.status[id] = st
		f.mu.Unlock()
		return desktop.Control{Status: st}, nil
	}
	st, stale := f.status[id], f.keyStale
	f.mu.Unlock()
	return desktop.Control{Status: st, Stale: stale}, nil
}

func (f *fixture) PressAttemptKeys(ctx context.Context, id, ref, key string, revision, after int64, screen string) (desktop.AttemptDetail, error) {
	f.note("akeys:" + ref + ":" + key)
	f.mu.Lock()
	f.attemptKey, f.attemptKeyRef, f.attemptKeyRev, f.attemptKeyAft, f.attemptScreen = key, ref, revision, after, screen
	f.mu.Unlock()
	f.stallAction()
	if f.attemptKeyErr != nil {
		return desktop.AttemptDetail{}, f.attemptKeyErr
	}
	if f.action != nil {
		return desktop.AttemptDetail{}, f.action
	}
	return f.attempt[id], nil
}

func (f *fixture) dropNeed(id, ref string) {
	if f.keepNeeds {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.status[id]
	kept := make([]desktop.Need, 0, len(st.Needs.Items))
	for _, need := range st.Needs.Items {
		if need.Ref == ref || need.Report == ref || need.Decision == ref {
			continue
		}
		kept = append(kept, need)
	}
	st.Needs.Items = kept
	st.Needs.Waiting = len(kept)
	f.status[id] = st
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
	var af desktop.AttemptDetail
	var dd desktop.DecisionDetail
	var rd desktop.ReportDetail
	load("status", &st)
	st.Gauge = desktop.Gauge{Text: "610K / 1M", Pct: 61}
	sup := *st.Supervisor
	sup.Pending = 1
	st.Supervisor = &sup
	load("timeline", &tl)
	load("tasks", &ts)
	load("task", &td)
	load("attempt-running", &ad)
	load("attempt-failed", &af)
	f.attemptRefs = map[string]desktop.AttemptDetail{af.Attempt.Ref: af}
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
	app.DraftDir = t.TempDir()
	app.Boot()
	if app.Err != "" && f.fail == nil {
		t.Fatalf("boot: %s", app.Err)
	}
	return app
}

func newLandingApp(t *testing.T, f *fixture) *desktop.App {
	t.Helper()
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	return app
}
