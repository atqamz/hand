package desktop

import (
	"context"
	"strconv"
	"strings"
	"time"
)

const (
	AttentionRefresh          = 5 * time.Second
	MinimizedAttentionRefresh = 15 * time.Second
)

type FleetSignal struct {
	Fleet string
	Signal
}

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

func (s Signal) Mark() (tone, status string) {
	switch {
	case !s.OK:
		return "unavailable", "unavailable"
	case s.Waiting == 0:
		return "", "quiet"
	}
	status = "waiting"
	switch toneOf(s.Worst) {
	case "fail":
		if s.WorstWord != "" {
			status = s.WorstWord
		}
		return "fail", status
	case "wait":
		return "wait", status
	}
	return "neutral", status
}

func (a *App) mark(f Fleet) (tone, status string) {
	switch sig, ok := a.attn[f.ID]; {
	case f.ID == a.Fleet.ID && a.Loading:
		return "", ""
	case f.ID == a.Fleet.ID:
		return a.signal().Mark()
	case a.attnDown:
		sig.OK = false
		return sig.Mark()
	case !ok:
		return "", ""
	default:
		return sig.Mark()
	}
}

func (a *App) signal() Signal {
	if a.attnSel {
		if s, ok := a.attn[a.Fleet.ID]; ok {
			s.Name, s.OK = a.Fleet.Name, s.OK && !a.attnDown && (a.Minimized || a.Stale == "")
			return s
		}
	}
	s := Signal{OK: a.Model != nil && a.Stale == "" && !(a.Minimized && a.attnDown)}
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

func (a *App) Title() string {
	return a.signal().Title()
}

func (a *App) syncAttention() {
	s := a.signal()
	if !s.OK {
		delete(a.seen, a.Fleet.ID)
	}
	if t := s.Title(); t != a.title {
		a.title = t
		if a.OnTitle != nil {
			a.OnTitle(t)
		}
	}
	n := 0
	if s.OK {
		n = s.Waiting
	}
	if a.OnBadge != nil && n != a.badge {
		a.badge = n
		a.OnBadge(n)
	}
}

func (s Signal) rank() int {
	if s.Waiting == 0 {
		return 0
	}
	return map[string]int{"neutral": 1, "wait": 2, "fail": 3}[toneOf(s.Worst)]
}

func (a *App) observe(fleet string, s Signal) {
	prev, seen := a.seen[fleet]
	if !s.OK {
		delete(a.seen, fleet)
		a.dropFlash(fleet)
		return
	}
	if a.seen == nil {
		a.seen = map[string]Signal{}
	}
	a.seen[fleet] = s
	if s.Waiting == 0 {
		a.dropFlash(fleet)
		return
	}
	if seen && (s.Waiting > prev.Waiting || s.rank() > prev.rank()) && s.rank() >= 2 && (a.Unfocused || a.Minimized) {
		if a.flashed == nil {
			a.flashed = map[string]bool{}
		}
		a.flashed[fleet] = true
		a.setFlash(true)
	}
}

func (a *App) dropFlash(fleet string) {
	delete(a.flashed, fleet)
	if len(a.flashed) == 0 {
		a.setFlash(false)
	}
}

func (a *App) pruneAttention() {
	for id := range a.seen {
		if !a.hasFleet(id) {
			delete(a.seen, id)
		}
	}
	for id := range a.flashed {
		if !a.hasFleet(id) {
			a.dropFlash(id)
		}
	}
}

func (a *App) clearAttention() {
	a.setFlash(false)
	if a.OnBadge != nil && a.badge != 0 {
		a.badge = 0
		a.OnBadge(0)
	}
}

func (a *App) setFlash(on bool) {
	if !on {
		clear(a.flashed)
	}
	if on == a.flashing {
		return
	}
	a.flashing = on
	if a.OnFlash != nil {
		a.OnFlash(on)
	}
}

func (a *App) attending() bool {
	return a.polling && !a.closed && a.Model != nil && (len(a.Fleets) > 1 || a.Minimized) && (a.Clock != nil || a.Post != nil)
}

func (a *App) attentionDelay() time.Duration {
	if a.Minimized && a.attnFails == 0 {
		return MinimizedAttentionRefresh
	}
	if a.attnFails == 0 {
		if len(a.attn) == 0 || a.attnSoon {
			return FocusedRefresh
		}
		return AttentionRefresh
	}
	return min(AttentionRefresh<<min(a.attnFails, 16), maxRefreshBackoff)
}

func (a *App) armAttention() {
	if a.attnTimer != nil || a.attnBusy || !a.attending() {
		return
	}
	gen := a.attnGen
	a.attnTimer = a.clock().After(a.attentionDelay(), func() { a.attendAt(gen) })
}

func (a *App) resetAttention() {
	a.attnGen++
	if a.attnTimer != nil {
		a.attnTimer()
		a.attnTimer = nil
	}
	if a.attnCancel != nil {
		a.attnCancel()
		a.attnCancel = nil
	}
	a.attnBusy = false
}

func (a *App) attendAt(gen int) {
	if post := a.Post; post != nil {
		post(func() { a.attendOnce(gen) })
		return
	}
	a.attendOnce(gen)
}

func (a *App) attendOnce(gen int) {
	if gen != a.attnGen {
		return
	}
	a.attnTimer, a.attnSoon = nil, false
	ctx := a.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, a.pollTimeout())
	a.attnCancel, a.attnBusy = cancel, true
	model, post := a.Model, a.Post
	fetch := func() {
		sigs, err := model.Attention(ctx)
		cancel()
		if post == nil {
			a.attended(gen, sigs, err)
			return
		}
		post(func() { a.attended(gen, sigs, err) })
	}
	if post == nil {
		fetch()
		return
	}
	go fetch()
}

func (a *App) attended(gen int, sigs []FleetSignal, err error) {
	if gen != a.attnGen || a.closed {
		return
	}
	a.attnBusy, a.attnCancel = false, nil
	if err != nil {
		a.attnFails++
		a.attnDown = a.attnFails >= 2
	} else {
		a.attnFails, a.attnDown = 0, false
		a.attn = make(map[string]Signal, len(sigs))
		for _, s := range sigs {
			a.attn[s.Fleet] = s.Signal
			if s.Fleet != a.Fleet.ID || a.Minimized {
				a.observe(s.Fleet, s.Signal)
			}
		}
		_, ok := a.attn[a.Fleet.ID]
		a.attnSel = a.Minimized && ok
	}
	if a.attnDown {
		for id := range a.seen {
			if id != a.Fleet.ID || a.Minimized {
				delete(a.seen, id)
			}
		}
	}
	a.syncAttention()
	a.armAttention()
}
