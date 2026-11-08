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
	// Post runs fetched state on the window's thread and repaints it. Nil
	// reads inline, as the tests and Boot do.
	Post    func(func())
	OnFleet func(Fleet)

	Fleets []Fleet
	Fleet  Fleet

	Screen  Screen
	Ref     string
	Loading bool

	Status   Status
	Timeline Timeline
	Tasks    Tasks
	Task     TaskDetail
	Attempt  AttemptDetail
	Decision DecisionDetail
	Report   ReportDetail

	Err    string
	loadID int
	back   []backStep
}

type backStep struct {
	Screen Screen
	Ref    string
}

func New(connect func() (ReadModel, error)) *App {
	return &App{Connect: connect, Context: context.Background()}
}

func (a *App) Boot() {
	m, err := a.Connect()
	if err != nil {
		a.Model, a.Fleets, a.Err = nil, nil, err.Error()
		return
	}
	a.Model = m
	a.Err = ""
	a.Fleets = m.Fleets()
	ctx, cancel := context.WithTimeout(a.Context, 2*time.Second)
	defer cancel()
	for i := range a.Fleets {
		if name, err := m.Name(ctx, a.Fleets[i].ID); err == nil && name != "" {
			a.Fleets[i].Name = name
		}
	}
	switch {
	case len(a.Fleets) == 0:
		a.Err = "no fleets are registered; run `hand init` in a folder to make one"
	case a.Fleet.ID != "" && a.hasFleet(a.Fleet.ID):
		a.SelectFleet(a.Fleet.ID)
	default:
		a.SelectFleet(a.Fleets[0].ID)
	}
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
	for _, f := range a.Fleets {
		if f.ID == id {
			return true
		}
	}
	return false
}

func (a *App) SelectFleet(id string) {
	for _, f := range a.Fleets {
		if f.ID == id {
			a.Fleet = f
			break
		}
	}
	a.Screen, a.Ref, a.back = StatusScreen, "", nil
	a.Timeline, a.Tasks = Timeline{}, Tasks{}
	a.Task, a.Attempt, a.Decision, a.Report = TaskDetail{}, AttemptDetail{}, DecisionDetail{}, ReportDetail{}
	if a.OnFleet != nil {
		a.OnFleet(a.Fleet)
	}
	a.Reload()
}

func (a *App) Show(s Screen) {
	a.Screen, a.Ref, a.back = s, "", nil
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
	a.Reload()
}

func (a *App) Retry() {
	if a.Model == nil || a.Fleet.ID == "" {
		a.Boot()
		return
	}
	a.Reload()
}

func (a *App) Reload() {
	if a.Model == nil || a.Fleet.ID == "" {
		return
	}
	a.loadID++
	id := a.loadID
	a.Err = ""
	post := a.Post
	if post == nil {
		a.Loading = false
		a.apply(fetch(a.Model, a.Context, a.Fleet.ID, a.Screen, a.Ref))
		return
	}
	a.Loading = true
	post(func() {})
	model, ctx, fleet, screen, ref := a.Model, a.Context, a.Fleet.ID, a.Screen, a.Ref
	go func() {
		l := fetch(model, ctx, fleet, screen, ref)
		post(func() {
			if id != a.loadID {
				return
			}
			a.Loading = false
			a.apply(l)
		})
	}()
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
		l.Err = friendly(err)
		return l
	}
	l.StatusOK, l.Status = true, st
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
	if err != nil {
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
			return "the board's API answers only on its loopback address"
		}
		return api.Message
	}
	return err.Error()
}
