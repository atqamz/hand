package desktop

import (
	"strconv"
	"strings"
)

// Signal is what one fleet asks of the operator, read from a status the
// window already holds.
type Signal struct {
	Name      string
	Waiting   int
	Worst     string
	WorstWord string
	Ref       string
	Working   bool
	OK        bool
}

func (s Signal) Title() string {
	if s.Name == "" {
		return "Hand"
	}
	base := "Hand · " + s.Name
	switch {
	case !s.OK:
		return base + " · offline"
	case s.Waiting > 0 && toneOf(s.Worst) == "fail":
		what := s.WorstWord
		if s.Worst == "blocked" || s.Worst == "worker" {
			what = strings.TrimSpace(s.Ref + " " + what)
		}
		if what == "" {
			return "(!) " + base
		}
		return "(!) " + base + " · " + what
	case s.Waiting > 0:
		return "(" + strconv.Itoa(s.Waiting) + ") " + base
	case s.Working:
		return "● " + base
	}
	return base
}

func (a *App) signal() Signal {
	s := Signal{OK: a.Model != nil && a.Stale == ""}
	if a.Fleet.ID != "" {
		s.Name = a.Fleet.Name
	}
	n := a.Status.Needs
	s.Waiting, s.Worst, s.WorstWord = a.waitingNeeds(), n.Worst, n.WorstWord
	s.Working = a.Status.AgentState == "working"
	for _, it := range n.Items {
		if it.Kind == n.Worst {
			s.Ref = it.Ref
			break
		}
	}
	return s
}

// Title is the window title for the selected fleet's current signal.
func (a *App) Title() string {
	return a.signal().Title()
}

func (a *App) syncTitle() {
	t := a.Title()
	if t == a.title {
		return
	}
	a.title = t
	if a.OnTitle != nil {
		a.OnTitle(t)
	}
}
