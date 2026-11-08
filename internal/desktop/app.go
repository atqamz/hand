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

	Fleets []Fleet
	Fleet  Fleet

	Screen Screen
	Ref    string

	Status   Status
	Timeline Timeline
	Tasks    Tasks
	Task     TaskDetail
	Attempt  AttemptDetail
	Decision DecisionDetail
	Report   ReportDetail

	Err  string
	back []Screen
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
	a.back = append(a.back, a.Screen)
	a.Screen, a.Ref = s, ref
	a.Reload()
}

func (a *App) Back() {
	if len(a.back) == 0 {
		a.Show(StatusScreen)
		return
	}
	s := a.back[len(a.back)-1]
	a.back = a.back[:len(a.back)-1]
	a.Screen = s
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
	a.Err = ""
	if st, err := a.Model.Status(a.Context, a.Fleet.ID); err != nil {
		a.Status = Status{}
		a.Err = friendly(err)
		return
	} else {
		a.Status = st
	}
	a.loadScreen()
}

func (a *App) loadScreen() {
	var err error
	switch a.Screen {
	case StatusScreen, NeedsScreen:
		return
	case TimelineScreen:
		a.Timeline, err = a.Model.Timeline(a.Context, a.Fleet.ID, 0)
	case TasksScreen:
		a.Tasks, err = a.Model.Tasks(a.Context, a.Fleet.ID, true)
	case TaskScreen:
		a.Task, err = a.Model.Task(a.Context, a.Fleet.ID, a.Ref)
	case AttemptScreen:
		a.Attempt, err = a.Model.Attempt(a.Context, a.Fleet.ID, a.Ref)
	case DecisionScreen:
		a.Decision, err = a.Model.Decision(a.Context, a.Fleet.ID, a.Ref)
	case ReportScreen:
		a.Report, err = a.Model.Report(a.Context, a.Fleet.ID, a.Ref)
	}
	if err != nil {
		a.Err = friendly(err)
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
