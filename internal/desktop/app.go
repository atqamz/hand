package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

type Screen int

const (
	BoardScreen Screen = iota
	TaskScreen
	AttemptScreen
	DecisionScreen
	ReportScreen
)

// The auto-refresh intervals: short while the window has keyboard focus,
// long while it does not. A failed tick backs off to at most maxRefreshBackoff.
const (
	FocusedRefresh    = time.Second
	UnfocusedRefresh  = 5 * time.Second
	maxRefreshBackoff = 60 * time.Second
	StopWindow        = 4 * time.Second
)

// Clock schedules the auto-refresh tick; nil uses the wall clock.
type Clock interface {
	After(d time.Duration, fn func()) (stop func())
}

type wallClock struct{}

func (wallClock) After(d time.Duration, fn func()) (stop func()) {
	t := time.AfterFunc(d, fn)
	return func() { t.Stop() }
}

type App struct {
	Connect func() (ReadModel, error)
	Model   ReadModel
	Context context.Context
	// NameTimeout limits each fleet's name lookup; zero means two seconds.
	NameTimeout time.Duration
	// Clock schedules the auto-refresh tick; nil uses the wall clock.
	Clock Clock
	// Unfocused asks for the long refresh interval; SetFocused sets it.
	Unfocused bool
	Minimized bool
	// PollTimeout bounds each auto-refresh tick; zero means five seconds.
	PollTimeout time.Duration
	// Post runs fetched state on the window's thread and repaints it. Nil
	// reads inline, as the tests and Boot do.
	Post    func(func())
	OnFleet func(Fleet)
	// Now is the time the working line counts from; nil uses the wall clock.
	Now func() time.Time

	Fleets []Fleet
	Fleet  Fleet

	Screen      Screen
	RailOpen    bool
	Chat        float32
	paneW       float32
	chatH       float32
	StopArmed   bool
	TimelineErr string
	OlderErr    string
	Ref         string
	Loading     bool
	Busy        bool
	BusyAction  string

	needOpen string
	needSet  bool
	decTried string
	decErr   string
	receipt  string

	AnswerChoice string
	AnswerNote   string
	ActionErr    string
	ActionWarn   bool
	OpenErr      string
	OpenBusy     bool

	Draft     string
	TaskQuery string
	DraftDir  string
	DraftErr  string

	Status   Status
	Timeline Timeline
	Tasks    Tasks
	Task     TaskDetail
	Attempt  AttemptDetail
	Decision DecisionDetail
	Report   ReportDetail

	Err string
	// Stale is the last auto-refresh failure; empty when the board answers.
	Stale     string
	errStatus bool
	errScreen bool
	retry     string
	writer    *draftWriter
	md        markdown.Options
	loadID    int
	cancel    context.CancelFunc
	stop      context.CancelFunc
	gen       int
	back      []backStep
	stopTimer func()
	stopGen   int
	stopRef   string
	scroll    ui.ScrollState
	wakesOpen map[string]bool

	polling    bool
	closed     bool
	pollGen    int
	tickGen    int
	pollTimer  func()
	pollCancel context.CancelFunc
	pollBusy   bool

	reads            [3]readState
	pollSkipped      [3]bool
	pollStatus       string
	pollScreenFor    string
	pollScreenCursor string
	pollTimeline     string
}

const (
	readStatus = iota
	readScreen
	readTimeline
)

type readState struct {
	fails, skip int
	err         string
	fresh       bool
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

// StartRefresh starts the per-fleet auto-refresh loop; the window calls it
// once Post is set. It does nothing until a fleet is selected.
func (a *App) StartRefresh() {
	a.polling, a.closed = true, false
	a.armPoll()
}

// Close stops the auto-refresh loop and cancels a poll in flight.
func (a *App) Close() {
	a.polling, a.closed = false, true
	a.resetPoll()
}

func (a *App) SetMinimized(minimized bool) {
	if a.Minimized == minimized {
		return
	}
	a.Minimized = minimized
	if !a.pollBusy {
		a.armPoll()
	}
}

// SetFocused switches the loop between the focused and unfocused intervals.
func (a *App) SetFocused(focused bool) {
	if a.Unfocused == !focused {
		return
	}
	a.Unfocused = !focused
	if !a.pollBusy {
		a.armPoll()
	}
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
	a.setErr("")
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
	a.setErr(fmt.Sprintf("no registered fleet is named %q", ref))
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
	a.resetPoll()
	if f, ok := a.fleetByID(id); ok {
		a.Fleet = f
	}
	a.Screen, a.Ref, a.back = BoardScreen, "", nil
	a.scroll.Y = math.MaxFloat32
	a.disarmStop()
	a.Status, a.Timeline, a.Tasks = Status{}, Timeline{}, Tasks{}
	a.needOpen, a.needSet, a.decTried, a.decErr = "", false, "", ""
	a.TimelineErr, a.OlderErr = "", ""
	a.Task, a.Attempt, a.Decision, a.Report = TaskDetail{}, AttemptDetail{}, DecisionDetail{}, ReportDetail{}
	a.AnswerChoice, a.AnswerNote, a.ActionErr, a.ActionWarn, a.OpenErr = "", "", "", false, ""
	a.OpenBusy = false
	a.DraftErr, a.retry, a.Busy, a.BusyAction = "", "", false, ""
	if a.writer != nil {
		a.writer.Flush()
	}
	a.Draft = a.drafts().Load(a.Fleet.ID)
	if a.writer != nil {
		if u, ok := a.writer.unsaved(a.Fleet.ID); ok {
			a.Draft = u.text
			a.DraftErr = "the draft was not saved: " + friendly(u.err)
		}
	}
	if a.OnFleet != nil {
		a.OnFleet(a.Fleet)
	}
	a.Reload()
	a.armPoll()
}

func (a *App) install(f found, keep bool) {
	a.setErr(f.Err)
	if f.Model == nil {
		a.resetPoll()
		a.Model, a.Fleets = nil, nil
		return
	}
	a.Model, a.Fleets = f.Model, f.Fleets
	switch {
	case len(a.Fleets) == 0:
		a.resetPoll()
		a.Fleet = Fleet{}
		a.setErr("no fleets are registered; run `hand init` in a folder to make one")
	case a.hasFleet(a.Fleet.ID) && keep:
		a.Fleet, _ = a.fleetByID(a.Fleet.ID)
		if a.OnFleet != nil {
			a.OnFleet(a.Fleet)
		}
		a.Reload()
		a.armPoll()
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
	s, ok := refScreen(ref)
	if !ok {
		return
	}
	a.back = append(a.back, backStep{Screen: a.Screen, Ref: a.Ref})
	a.Screen, a.Ref = s, ref
	a.AnswerChoice, a.AnswerNote = "", ""
	a.Reload()
}

func refScreen(ref string) (Screen, bool) {
	switch ref[0] {
	case 't':
		return TaskScreen, true
	case 'a':
		return AttemptScreen, true
	case 'd':
		return DecisionScreen, true
	case 'r':
		return ReportScreen, true
	}
	return 0, false
}

func (a *App) next(done string) {
	if a.Ref != done || len(a.back) == 0 || a.back[len(a.back)-1].Screen != BoardScreen {
		return
	}
	for _, need := range a.Status.Needs.Items {
		ref := needRef(need)
		if ref == done {
			continue
		}
		if ref == "" {
			break
		}
		if s, ok := refScreen(ref); ok {
			a.Screen, a.Ref = s, ref
			a.AnswerChoice, a.AnswerNote = "", ""
			a.Reload()
			return
		}
		break
	}
	a.Back()
}

func (a *App) Back() {
	if len(a.back) == 0 {
		a.Show(BoardScreen)
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
		if id != a.loadID {
			return
		}
		if err != nil {
			a.OlderErr = friendly(err)
			return
		}
		a.OlderErr = ""
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
	ref := a.Ref
	if a.Screen == BoardScreen {
		ref = a.Decision.Decision.Ref
	}
	if a.Busy || a.Model == nil || a.Fleet.ID == "" || a.Screen != DecisionScreen && a.Screen != BoardScreen || ref == "" {
		return
	}
	choice, note := a.AnswerChoice, a.AnswerNote
	id, ctx, cancel := a.nextLoad()
	post := a.Post
	model, fleet := a.Model, a.Fleet.ID
	a.Busy = true
	detail := func(d DecisionDetail) {
		if id != a.loadID {
			return
		}
		a.Busy = false
		a.Decision = d
		a.receipt = a.answerReceipt(ref)
	}
	status := func(st Status) {
		if id != a.loadID {
			return
		}
		a.Status = st
		a.next(ref)
		a.nextNeed(ref)
	}
	failed := func(err error) {
		if id != a.loadID {
			return
		}
		a.Busy = false
		a.ActionErr = friendly(err)
		a.ActionWarn = false
	}
	stale := func(err error) {
		if id != a.loadID {
			return
		}
		a.ActionErr = "the answer was saved, but the fleet status could not be read: " + friendly(err)
		a.ActionWarn = true
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
		a.receipt = ref + " marked read"
	}
	status := func(st Status) {
		if id != a.loadID {
			return
		}
		a.Status = st
		if screen == ReportScreen {
			a.next(ref)
		}
		a.nextNeed(ref)
	}
	failed := func(err error) {
		if id != a.loadID {
			return
		}
		a.Busy = false
		a.ActionErr = friendly(err)
		a.ActionWarn = false
	}
	stale := func(err error) {
		if id != a.loadID {
			return
		}
		a.ActionErr = "the report was acknowledged, but the fleet status could not be read: " + friendly(err)
		a.ActionWarn = true
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

func (a *App) Send() {
	text := a.Draft
	if a.Busy || a.Model == nil || a.Fleet.ID == "" || strings.TrimSpace(text) == "" {
		return
	}
	id, ctx, cancel := a.nextLoad()
	post := a.Post
	model, fleet, writer := a.Model, a.Fleet.ID, a.draftWriter()
	a.Busy = true
	a.DraftErr, a.retry = "", ""
	applied := func(sent Sent) {
		if a.Fleet.ID == fleet && a.Draft == text {
			a.Draft = ""
		}
		if id != a.loadID {
			return
		}
		a.Busy = false
		if !sent.Fresh {
			a.ActionErr = "the message was sent, but the conversation could not be read; refresh to see it"
			a.ActionWarn = true
			return
		}
		a.Timeline = mergeTimeline(a.Timeline, sent.Timeline)
	}
	uncleared := func(err error) {
		if id != a.loadID {
			return
		}
		a.DraftErr = "the message was sent, but its stored draft could not be removed: " + friendly(err)
		a.retry = text
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
		a.ActionWarn = false
	}
	stale := func(err error) {
		if id != a.loadID {
			return
		}
		a.ActionErr = "the message was sent, but the fleet status could not be read: " + friendly(err)
		a.ActionWarn = true
	}
	fetch := func() {
		sent, err := model.SendMessage(ctx, fleet, text)
		if err != nil {
			cancel()
			if post == nil {
				failed(err)
			} else {
				post(func() { failed(err) })
			}
			return
		}
		if err := writer.ClearIf(fleet, text); err != nil {
			if post == nil {
				uncleared(err)
			} else {
				post(func() { uncleared(err) })
			}
		}
		if post == nil {
			applied(sent)
		} else {
			post(func() { applied(sent) })
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

func (a *App) Supervise(action string) {
	if a.Busy || a.Model == nil || a.Fleet.ID == "" {
		return
	}
	sup := a.Status.Supervisor
	if sup == nil || !canSupervise(sup, action) {
		return
	}
	model, fleet, ref := a.Model, a.Fleet.ID, sup.Ref
	a.runControl(action, func(ctx context.Context) (Control, error) {
		return model.Supervise(ctx, fleet, action, ref)
	})
}

func (a *App) PressStop() {
	sup := a.Status.Supervisor
	if a.StopArmed && sup != nil && sup.Ref == a.stopRef {
		a.disarmStop()
		a.Supervise("stop")
		return
	}
	a.disarmStop()
	if a.Busy || sup == nil || !canSupervise(sup, "stop") {
		return
	}
	a.StopArmed, a.stopRef = true, sup.Ref
	if a.Clock == nil && a.Post == nil {
		return
	}
	a.stopGen++
	gen := a.stopGen
	disarm := func() {
		if gen == a.stopGen {
			a.StopArmed, a.stopTimer = false, nil
		}
	}
	a.stopTimer = a.clock().After(StopWindow, func() {
		if a.Post != nil {
			a.Post(disarm)
			return
		}
		disarm()
	})
}

func (a *App) disarmStop() {
	a.StopArmed = false
	a.stopGen++
	if a.stopTimer != nil {
		a.stopTimer()
		a.stopTimer = nil
	}
}

func (a *App) Press(keys ...string) {
	if a.Busy || a.Model == nil || a.Fleet.ID == "" {
		return
	}
	sup := a.Status.Supervisor
	if sup == nil || !sup.Blocked || sup.Keys == nil || len(keys) == 0 || slices.ContainsFunc(keys, func(k string) bool { return !supervisorKey(k) }) {
		return
	}
	guard, ref := *sup.Keys, sup.Ref
	model, fleet := a.Model, a.Fleet.ID
	a.runControl("key:"+strings.Join(keys, " "), func(ctx context.Context) (Control, error) {
		return model.PressKeys(ctx, fleet, keys, guard.Revision, guard.Digest, guard.Pressed, ref)
	})
}

var supervisorControlKeys = []string{"enter", "esc", "up", "down", "1", "2", "3"}

func supervisorKey(key string) bool {
	for _, k := range supervisorControlKeys {
		if k == key {
			return true
		}
	}
	return false
}

func canSupervise(sup *Supervisor, action string) bool {
	switch action {
	case "start":
		return !sup.Live
	case "resume":
		return sup.Resumable
	case "stop", "interrupt":
		return sup.Status == "running"
	case "force":
		return sup.Blocked
	}
	return false
}

func (a *App) runControl(name string, call func(context.Context) (Control, error)) {
	id, ctx, cancel := a.nextLoad()
	post := a.Post
	a.Busy, a.BusyAction = true, name
	applied := func(c Control) {
		if id != a.loadID {
			return
		}
		a.Busy, a.BusyAction = false, ""
		if c.Stale {
			a.ActionErr = "the action went through, but the fleet status could not be read; refresh to see it"
			a.ActionWarn = true
			return
		}
		a.Status = c.Status
	}
	failed := func(err error) {
		if id != a.loadID {
			return
		}
		a.Busy, a.BusyAction = false, ""
		a.ActionErr = friendly(err)
		a.ActionWarn = false
	}
	fetch := func() {
		st, err := call(ctx)
		cancel()
		if err != nil {
			if post == nil {
				failed(err)
			} else {
				post(func() { failed(err) })
			}
			return
		}
		if post == nil {
			applied(st)
		} else {
			post(func() { applied(st) })
		}
	}
	if post == nil {
		fetch()
		return
	}
	post(func() {})
	go fetch()
}

func (a *App) ClearDraft() {
	if a.Busy {
		return
	}
	fleet, draft, retry := a.Fleet.ID, a.Draft, a.retry
	a.Draft, a.DraftErr, a.retry = "", "", ""
	if fleet == "" {
		return
	}
	var err error
	if draft == "" && retry != "" {
		err = a.draftWriter().ClearIf(fleet, retry)
	} else {
		err = a.draftWriter().Clear(fleet)
	}
	if err != nil {
		a.DraftErr = "the stored draft could not be removed: " + friendly(err)
		a.retry = retry
	}
}

func (a *App) drafts() Drafts {
	return Drafts{Dir: a.DraftDir}
}

func (a *App) draftWriter() *draftWriter {
	if a.writer == nil {
		a.writer = newDraftWriter(a.drafts(), a.draftSaved, a.Post == nil)
	}
	return a.writer
}

func (a *App) saveDraft() {
	if a.Fleet.ID == "" {
		return
	}
	a.draftWriter().Save(a.Fleet.ID, a.Draft)
}

// FlushDrafts waits until every queued draft write has run.
func (a *App) FlushDrafts() {
	if a.writer != nil {
		a.writer.Flush()
	}
}

func (a *App) draftSaved(fleet string, err error) {
	if a.Post != nil {
		a.Post(func() { a.applyDraftSave(fleet) })
		return
	}
	a.applyDraftSave(fleet)
}

func (a *App) applyDraftSave(fleet string) {
	if fleet != a.Fleet.ID {
		return
	}
	if u, ok := a.draftWriter().unsaved(fleet); ok {
		a.DraftErr = "the draft was not saved: " + friendly(u.err)
		return
	}
	a.DraftErr = ""
}

func (a *App) Reload() {
	if a.Model == nil || a.Fleet.ID == "" {
		return
	}
	id, ctx, cancel := a.nextLoad()
	a.setErr("")
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
	a.BusyAction = ""
	a.ActionErr = ""
	a.ActionWarn = false
	a.OpenErr = ""
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

func (a *App) clock() Clock {
	if a.Clock != nil {
		return a.Clock
	}
	return wallClock{}
}

func (a *App) pollTimeout() time.Duration {
	if a.PollTimeout > 0 {
		return a.PollTimeout
	}
	return 5 * time.Second
}

func (a *App) pollDelay() time.Duration {
	d := FocusedRefresh
	if a.Unfocused {
		d = UnfocusedRefresh
	}
	return d
}

// resetPoll cancels the armed tick and the request in flight, and forgets
// the backoff; the next loop starts clean for the fleet just selected.
func (a *App) resetPoll() {
	a.pollGen++
	a.tickGen++
	if a.pollTimer != nil {
		a.pollTimer()
		a.pollTimer = nil
	}
	if a.pollCancel != nil {
		a.pollCancel()
		a.pollCancel = nil
	}
	a.pollBusy = false
	a.reads, a.pollSkipped = [3]readState{}, [3]bool{}
	a.pollStatus, a.pollScreenFor, a.pollScreenCursor, a.pollTimeline = "", "", "", ""
	a.Stale = ""
}

func (a *App) armPoll() {
	a.tickGen++
	if a.pollTimer != nil {
		a.pollTimer()
		a.pollTimer = nil
	}
	if !a.polling || a.closed || a.Minimized || a.pollBusy || a.Model == nil || a.Fleet.ID == "" || (a.Clock == nil && a.Post == nil) {
		return
	}
	gen, tick := a.pollGen, a.tickGen
	a.pollTimer = a.clock().After(a.pollDelay(), func() { a.pollAt(gen, tick) })
}

func (a *App) pollAt(gen, tick int) {
	if post := a.Post; post != nil {
		post(func() { a.pollOnce(gen, tick) })
		return
	}
	a.pollOnce(gen, tick)
}

func (a *App) pollOnce(gen, tick int) {
	if gen != a.pollGen || tick != a.tickGen || a.closed || a.pollBusy || a.Model == nil || a.Fleet.ID == "" {
		return
	}
	if a.Busy || a.Loading {
		a.armPoll()
		return
	}
	model, fleet, screen, ref := a.Model, a.Fleet.ID, a.Screen, a.Ref
	load := a.loadID
	key := pollScreenID(screen, ref)
	since := PollCursors{Worker: slices.ContainsFunc(a.Status.Needs.Items, func(n Need) bool { return n.Kind == "worker" })}
	cursors := [3]*ReadCursor{&since.Status, &since.Screen, &since.Timeline}
	held := [3]string{a.pollStatus, "", a.pollTimeline}
	if a.pollScreenFor == key {
		held[readScreen] = a.pollScreenCursor
	}
	for i, rc := range cursors {
		rs := &a.reads[i]
		a.pollSkipped[i] = rs.skip > 0
		switch {
		case rs.skip > 0:
			rs.skip--
			rc.Skip = true
		case rs.fresh:
			rc.Fresh = true
		case i == readStatus && since.Worker:
		default:
			rc.Since = held[i]
		}
	}
	if a.pollSkipped == [3]bool{true, true, true} {
		a.armPoll()
		return
	}
	ctx := a.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, a.pollTimeout())
	a.pollCancel, a.pollBusy = cancel, true
	post := a.Post
	fetch := func() {
		p, err := model.Poll(ctx, fleet, screen, ref, since)
		cancel()
		if post == nil {
			a.polled(gen, load, fleet, screen, ref, key, p, err)
			return
		}
		post(func() { a.polled(gen, load, fleet, screen, ref, key, p, err) })
	}
	if post == nil {
		fetch()
		return
	}
	go fetch()
}

func pollScreenID(screen Screen, ref string) string {
	return fmt.Sprintf("%d/%s", screen, ref)
}

func (a *App) polled(gen, load int, fleet string, screen Screen, ref, key string, p Poll, err error) {
	if gen != a.pollGen || a.closed {
		return
	}
	a.pollBusy, a.pollCancel = false, nil
	if load != a.loadID {
		a.armPoll()
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	errs := [3]error{p.StatusErr, p.ScreenErr, p.TimelineErr}
	read := [3]bool{p.StatusRead, p.ScreenRead, p.TimelineRead}
	for i := range a.reads {
		switch {
		case read[i]:
			a.reads[i] = readState{}
		case a.pollSkipped[i]:
		case errs[i] != nil || err != nil:
			e := errs[i]
			if e == nil {
				e = err
			}
			a.failRead(i, e)
		}
	}
	a.Stale = a.reads[readStatus].err
	if a.Stale == "" {
		a.Stale = a.reads[readScreen].err
	}
	statusFresh, screenFresh := false, false
	if a.Fleet.ID == fleet && p.StatusFresh {
		a.Status, a.pollStatus, statusFresh = p.Status, p.StatusCursor, true
	}
	if a.Fleet.ID == fleet && p.TimelineFresh {
		a.Timeline, a.pollTimeline = mergeTimeline(a.Timeline, p.Timeline), p.TimelineCursor
	}
	if a.Fleet.ID == fleet {
		a.TimelineErr = a.reads[readTimeline].err
	}
	if a.Fleet.ID == fleet && a.Screen == screen && a.Ref == ref && p.ScreenFresh {
		a.setScreen(screen, key, &p)
		screenFresh = true
	}
	if (a.errStatus || a.errScreen) && (!a.errStatus || statusFresh) && (!a.errScreen || screenFresh) {
		a.setErr("")
	}
	a.armPoll()
}

func (a *App) failRead(i int, err error) {
	rs := &a.reads[i]
	rs.fails++
	rs.err = friendly(err)
	ticks := int(maxRefreshBackoff / a.pollDelay())
	rs.skip = min(1<<min(rs.fails, 16), max(ticks, 1)) - 1
}

func (a *App) setScreen(screen Screen, key string, p *Poll) {
	switch screen {
	case BoardScreen:
		a.Tasks = p.Tasks
	case TaskScreen:
		a.Task = p.Task
	case AttemptScreen:
		a.Attempt = p.Attempt
	case DecisionScreen:
		a.Decision = p.Decision
	case ReportScreen:
		a.Report = p.Report
	}
	a.pollScreenFor, a.pollScreenCursor = key, p.ScreenCursor
}

func mergeTimeline(have, next Timeline) Timeline {
	if have.Older >= next.Older {
		return next
	}
	oldest := ""
	seen := map[string]bool{}
	for _, entry := range next.Entries {
		seen[entryKey(entry)] = true
		if entry.At != "" && (oldest == "" || entry.At < oldest) {
			oldest = entry.At
		}
	}
	if oldest == "" {
		return next
	}
	held := false
	for _, entry := range have.Entries {
		if !seen[entryKey(entry)] && entry.At != "" && entry.At <= oldest {
			next.Entries = append(next.Entries, entry)
			held = true
		}
	}
	if held {
		next.Older = have.Older
	}
	return next
}

func entryKey(e Entry) string {
	if e.Input != 0 {
		return fmt.Sprintf("i%d", e.Input)
	}
	return fmt.Sprintf("%s/%s/%s", e.Role, e.At, e.Text)
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

	TimelineOK  bool
	TimelineErr string
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
	case BoardScreen:
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
	if ctx.Err() != nil {
		return l
	}
	tl, err := model.Timeline(ctx, fleet, 0)
	if err != nil {
		l.TimelineErr = friendly(err)
		return l
	}
	l.Timeline, l.TimelineOK = tl, true
	return l
}

func (a *App) apply(l loaded) {
	a.Err, a.errStatus, a.errScreen = l.Err, l.Err != "" && !l.StatusOK, l.Err != "" && l.StatusOK
	a.TimelineErr = l.TimelineErr
	a.Stale, a.reads = l.Err, [3]readState{}
	for i := range a.reads {
		a.reads[i].fresh = l.Err != "" || i == readTimeline && l.TimelineErr != ""
	}
	if !l.StatusOK {
		return
	}
	a.Status = l.Status
	switch {
	case l.TimelineOK && len(a.Timeline.Entries) == 0:
		a.Timeline = l.Timeline
	case l.TimelineOK:
		a.Timeline = mergeTimeline(a.Timeline, l.Timeline)
	}
	switch l.Screen {
	case BoardScreen:
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

func (a *App) setErr(msg string) {
	a.Err, a.errStatus, a.errScreen = msg, false, false
}

func friendly(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "the board did not answer in time"
	}
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
