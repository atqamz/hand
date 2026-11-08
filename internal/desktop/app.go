package desktop

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Screen int

const (
	StatusScreen Screen = iota
	NeedsScreen
	TimelineScreen
	TasksScreen
	TaskScreen
	AttemptScreen
	DecisionScreen
	ReportScreen
)

type App struct {
	Connect func() (ReadModel, error)
	Model   ReadModel
	Context context.Context
	// NameTimeout limits each fleet's name lookup; zero means two seconds.
	NameTimeout time.Duration
	// Post runs fetched state on the window's thread and repaints it. Nil
	// reads inline, as the tests and Boot do.
	Post    func(func())
	OnFleet func(Fleet)

	Fleets []Fleet
	Fleet  Fleet

	Screen  Screen
	Ref     string
	Loading bool
	Busy    bool

	AnswerChoice string
	AnswerNote   string
	ActionErr    string

	Status   Status
	Timeline Timeline
	Tasks    Tasks
	Task     TaskDetail
	Attempt  AttemptDetail
	Decision DecisionDetail
	Report   ReportDetail

	Err    string
	loadID int
	cancel context.CancelFunc
	stop   context.CancelFunc
	gen    int
	back   []backStep
}

type backStep struct {
	Screen Screen
	Ref    string
}

type found struct {
	Model  ReadModel
	Fleets []Fleet
	Err    string
}

func New(connect func() (ReadModel, error)) *App {
	return &App{Connect: connect, Context: context.Background()}
}

func (a *App) Boot() {
	a.install(discover(a.Connect, a.Context, a.nameTimeout()), false)
}

func (a *App) Refresh() {
	a.invalidate()
	dctx, stop := context.WithCancel(a.Context)
	a.stop = stop
	post, connect, wait := a.Post, a.Connect, a.nameTimeout()
	if post == nil {
		f := discover(connect, dctx, wait)
		stop()
		a.install(f, true)
		return
	}
	a.gen++
	gen := a.gen
	a.Err = ""
	a.Loading = true
	post(func() {})
	go func() {
		f := discover(connect, dctx, wait)
		stop()
		post(func() {
			if gen != a.gen {
				return
			}
			a.Loading = false
			a.install(f, true)
		})
	}()
}

func (a *App) OpenFleet(ref string) {
	for _, f := range a.Fleets {
		if f.ID == ref || f.Name == ref {
			a.SelectFleet(f.ID)
			return
		}
	}
	a.Err = fmt.Sprintf("no registered fleet is named %q", ref)
}

func (a *App) hasFleet(id string) bool {
	_, ok := a.fleetByID(id)
	return ok
}

func (a *App) fleetByID(id string) (Fleet, bool) {
	for _, f := range a.Fleets {
		if f.ID == id {
			return f, true
		}
	}
	return Fleet{}, false
}

func (a *App) SelectFleet(id string) {
	if f, ok := a.fleetByID(id); ok {
		a.Fleet = f
	}
	a.Screen, a.Ref, a.back = StatusScreen, "", nil
	a.Status, a.Timeline, a.Tasks = Status{}, Timeline{}, Tasks{}
	a.Task, a.Attempt, a.Decision, a.Report = TaskDetail{}, AttemptDetail{}, DecisionDetail{}, ReportDetail{}
	a.AnswerChoice, a.AnswerNote, a.ActionErr, a.Busy = "", "", "", false
	if a.OnFleet != nil {
		a.OnFleet(a.Fleet)
	}
	a.Reload()
}

func (a *App) install(f found, keep bool) {
	a.Err = f.Err
	if f.Model == nil {
		a.Model, a.Fleets = nil, nil
		return
	}
	a.Model, a.Fleets = f.Model, f.Fleets
	switch {
	case len(a.Fleets) == 0:
		a.Fleet = Fleet{}
		a.Err = "no fleets are registered; run `hand init` in a folder to make one"
	case a.hasFleet(a.Fleet.ID) && keep:
		a.Fleet, _ = a.fleetByID(a.Fleet.ID)
		if a.OnFleet != nil {
			a.OnFleet(a.Fleet)
		}
		a.Reload()
	case a.hasFleet(a.Fleet.ID):
		a.SelectFleet(a.Fleet.ID)
	default:
		a.SelectFleet(a.Fleets[0].ID)
	}
}

func (a *App) Show(s Screen) {
	a.Screen, a.Ref, a.back = s, "", nil
	a.AnswerChoice, a.AnswerNote = "", ""
	a.Reload()
}

func (a *App) OpenRef(ref string) {
	if ref == "" || a.Fleet.ID == "" {
		return
	}
	var s Screen
	switch ref[0] {
	case 't':
		s = TaskScreen
	case 'a':
		s = AttemptScreen
	case 'd':
		s = DecisionScreen
	case 'r':
		s = ReportScreen
	default:
		return
	}
	a.back = append(a.back, backStep{Screen: a.Screen, Ref: a.Ref})
	a.Screen, a.Ref = s, ref
	a.AnswerChoice, a.AnswerNote = "", ""
	a.Reload()
}

func (a *App) Back() {
	if len(a.back) == 0 {
		a.Show(StatusScreen)
		return
	}
	step := a.back[len(a.back)-1]
	a.back = a.back[:len(a.back)-1]
	a.Screen, a.Ref = step.Screen, step.Ref
	a.AnswerChoice, a.AnswerNote = "", ""
	a.Reload()
}

func (a *App) Retry() {
	if a.Model == nil || a.Fleet.ID == "" {
		a.Refresh()
		return
	}
	a.Reload()
}

func (a *App) Older() {
	if a.Model == nil || a.Fleet.ID == "" || a.Timeline.Older <= 0 {
		return
	}
	id, ctx, cancel := a.nextLoad()
	post := a.Post
	model, fleet, before := a.Model, a.Fleet.ID, a.Timeline.Older
	apply := func(tl Timeline, err error) {
		if id != a.loadID || a.Screen != TimelineScreen {
			return
		}
		if err != nil {
			a.Err = friendly(err)
			return
		}
		merged := a.Timeline
		merged.Entries = append(append([]Entry(nil), a.Timeline.Entries...), tl.Entries...)
		merged.Older = tl.Older
		a.Timeline = merged
	}
	if post == nil {
		tl, err := model.Timeline(ctx, fleet, before)
		cancel()
		apply(tl, err)
		return
	}
	go func() {
		tl, err := model.Timeline(ctx, fleet, before)
		cancel()
		post(func() { apply(tl, err) })
	}()
}

func (a *App) Answer() {
	if a.Busy || a.Model == nil || a.Fleet.ID == "" || a.Screen != DecisionScreen || a.Ref == "" {
		return
	}
	choice, note := a.AnswerChoice, a.AnswerNote
	id, ctx, cancel := a.nextLoad()
	post := a.Post
	model, fleet, ref := a.Model, a.Fleet.ID, a.Ref
	a.Busy = true
	detail := func(d DecisionDetail) {
		if id != a.loadID {
			return
		}
		a.Busy = false
		a.Decision = d
	}
	status := func(st Status) {
		if id != a.loadID {
			return
		}
		a.Status = st
	}
	failed := func(err error) {
		if id != a.loadID {
			return
		}
		a.Busy = false
		a.ActionErr = friendly(err)
	}
	stale := func(err error) {
		if id != a.loadID {
			return
		}
		a.ActionErr = "the answer was saved, but the fleet status could not be read: " + friendly(err)
	}
	fetch := func() {
		d, err := model.AnswerDecision(ctx, fleet, ref, choice, note)
		if err != nil {
			cancel()
			if post == nil {
				failed(err)
			} else {
				post(func() { failed(err) })
			}
			return
		}
		if post == nil {
			detail(d)
		} else {
			post(func() { detail(d) })
		}
		st, serr := model.Status(ctx, fleet)
		cancel()
		if serr != nil {
			if post == nil {
				stale(serr)
			} else {
				post(func() { stale(serr) })
			}
			return
		}
		if post == nil {
			status(st)
		} else {
			post(func() { status(st) })
		}
	}
	if post == nil {
		fetch()
		return
	}
	post(func() {})
	go fetch()
}

func (a *App) Ack(ref string) {
	if a.Busy || a.Model == nil || a.Fleet.ID == "" || ref == "" {
		return
	}
	screen := a.Screen
	id, ctx, cancel := a.nextLoad()
	post := a.Post
	model, fleet := a.Model, a.Fleet.ID
	a.Busy = true
	report := func(r ReportDetail) {
		if id != a.loadID {
			return
		}
		a.Busy = false
		if screen == ReportScreen {
			a.Report = r
		}
	}
	status := func(st Status) {
		if id != a.loadID {
			return
		}
		a.Status = st
	}
	failed := func(err error) {
		if id != a.loadID {
			return
		}
		a.Busy = false
		a.ActionErr = friendly(err)
	}
	stale := func(err error) {
		if id != a.loadID {
			return
		}
		a.ActionErr = "the report was acknowledged, but the fleet status could not be read: " + friendly(err)
	}
	fetch := func() {
		r, err := model.AckReport(ctx, fleet, ref)
		if err != nil {
			cancel()
			if post == nil {
				failed(err)
			} else {
				post(func() { failed(err) })
			}
			return
		}
		if post == nil {
			report(r)
		} else {
			post(func() { report(r) })
		}
		st, serr := model.Status(ctx, fleet)
		cancel()
		if serr != nil {
			if post == nil {
				stale(serr)
			} else {
				post(func() { stale(serr) })
			}
			return
		}
		if post == nil {
			status(st)
		} else {
			post(func() { status(st) })
		}
	}
	if post == nil {
		fetch()
		return
	}
	post(func() {})
	go fetch()
}

func (a *App) Reload() {
	if a.Model == nil || a.Fleet.ID == "" {
		return
	}
	id, ctx, cancel := a.nextLoad()
	a.Err = ""
	post := a.Post
	if post == nil {
		a.Loading = false
		l := fetch(a.Model, ctx, a.Fleet.ID, a.Screen, a.Ref)
		cancel()
		a.apply(l)
		return
	}
	a.Loading = true
	post(func() {})
	model, fleet, screen, ref := a.Model, a.Fleet.ID, a.Screen, a.Ref
	go func() {
		l := fetch(model, ctx, fleet, screen, ref)
		cancel()
		post(func() {
			if id != a.loadID {
				return
			}
			a.Loading = false
			a.apply(l)
		})
	}()
}

func (a *App) nextLoad() (int, context.Context, context.CancelFunc) {
	if a.cancel != nil {
		a.cancel()
	}
	ctx, cancel := context.WithCancel(a.Context)
	a.cancel = cancel
	a.loadID++
	a.Busy = false
	a.ActionErr = ""
	return a.loadID, ctx, cancel
}

func (a *App) invalidate() {
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if a.stop != nil {
		a.stop()
		a.stop = nil
	}
	a.loadID++
}

func (a *App) nameTimeout() time.Duration {
	if a.NameTimeout > 0 {
		return a.NameTimeout
	}
	return 2 * time.Second
}

func discover(connect func() (ReadModel, error), ctx context.Context, wait time.Duration) found {
	m, err := connect()
	if err != nil {
		return found{Err: err.Error()}
	}
	fleets := m.Fleets()
	for i := range fleets {
		if ctx.Err() != nil {
			break
		}
		lookup, cancel := context.WithTimeout(ctx, wait)
		if name, err := m.Name(lookup, fleets[i].ID); err == nil && name != "" {
			fleets[i].Name = name
		}
		cancel()
	}
	return found{Model: m, Fleets: fleets}
}

type loaded struct {
	Screen   Screen
	StatusOK bool
	Status   Status
	Timeline Timeline
	Tasks    Tasks
	Task     TaskDetail
	Attempt  AttemptDetail
	Decision DecisionDetail
	Report   ReportDetail
	Err      string
}

func fetch(model ReadModel, ctx context.Context, fleet string, screen Screen, ref string) loaded {
	l := loaded{Screen: screen}
	st, err := model.Status(ctx, fleet)
	if err != nil {
		if ctx.Err() == nil {
			l.Err = friendly(err)
		}
		return l
	}
	l.StatusOK, l.Status = true, st
	if ctx.Err() != nil {
		return l
	}
	switch screen {
	case StatusScreen, NeedsScreen:
	case TimelineScreen:
		l.Timeline, err = model.Timeline(ctx, fleet, 0)
	case TasksScreen:
		l.Tasks, err = model.Tasks(ctx, fleet, true)
	case TaskScreen:
		l.Task, err = model.Task(ctx, fleet, ref)
	case AttemptScreen:
		l.Attempt, err = model.Attempt(ctx, fleet, ref)
	case DecisionScreen:
		l.Decision, err = model.Decision(ctx, fleet, ref)
	case ReportScreen:
		l.Report, err = model.Report(ctx, fleet, ref)
	}
	if err != nil && ctx.Err() == nil {
		l.Err = friendly(err)
	}
	return l
}

func (a *App) apply(l loaded) {
	a.Err = l.Err
	if !l.StatusOK {
		a.Status = Status{}
		return
	}
	a.Status = l.Status
	switch l.Screen {
	case TimelineScreen:
		a.Timeline = l.Timeline
	case TasksScreen:
		a.Tasks = l.Tasks
	case TaskScreen:
		a.Task = l.Task
	case AttemptScreen:
		a.Attempt = l.Attempt
	case DecisionScreen:
		a.Decision = l.Decision
	case ReportScreen:
		a.Report = l.Report
	}
}

func friendly(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		switch api.Status {
		case 401:
			return "the board did not accept this fleet's token; open the fleet once with `hand open`"
		case 403:
			if api.Code == "csrf_required" {
				return api.Message
			}
			return "the board's API answers only on its loopback address"
		}
		return api.Message
	}
	return err.Error()
}
