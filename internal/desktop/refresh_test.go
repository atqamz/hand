package desktop_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Time
	fn      func()
	stopped bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) After(d time.Duration, fn func()) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), fn: fn}
	c.timers = append(c.timers, t)
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		t.stopped = true
	}
}

func (c *fakeClock) delay() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.timers {
		if !t.stopped {
			return t.at.Sub(c.now)
		}
	}
	return 0
}

func (c *fakeClock) Advance(d time.Duration) int {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due, keep []*fakeTimer
	for _, t := range c.timers {
		if t.stopped {
			continue
		}
		if !t.at.After(c.now) {
			due = append(due, t)
		} else {
			keep = append(keep, t)
		}
	}
	c.timers = keep
	c.mu.Unlock()
	for _, t := range due {
		t.fn()
	}
	return len(due)
}

type boardRequest struct {
	path, since, issued string
}

type fakeBoard struct {
	mu           sync.Mutex
	status       desktop.Status
	timeline     desktop.Timeline
	version      map[string]int
	issued       map[string]string
	requests     []boardRequest
	fail         bool
	failTimeline bool
	reject       bool

	block        chan struct{}
	blockEntered chan struct{}
	blockPath    string
}

func newFakeBoard() *fakeBoard {
	return &fakeBoard{
		status: desktop.Status{Pill: "ready", Line: "first", Needs: desktop.Needs{},
			Supervisor: &desktop.Supervisor{Ref: "s1", Status: "running", Live: true}},
		timeline: desktop.Timeline{Live: true, Entries: []desktop.Entry{{Role: "supervisor", Text: "first", No: 1}}},
		version:  map[string]int{},
		issued:   map[string]string{},
	}
}

func (b *fakeBoard) setStatus(f func(*desktop.Status)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f(&b.status)
	b.version["/api/v1/status"]++
}

func (b *fakeBoard) setTimeline(f func(*desktop.Timeline)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f(&b.timeline)
	b.version["/api/v1/timeline"]++
}

func (b *fakeBoard) setFail(fail bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fail = fail
}

func (b *fakeBoard) setFailTimeline(fail bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failTimeline = fail
}

func (b *fakeBoard) setReject(reject bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reject = reject
}

func (b *fakeBoard) mark() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.requests)
}

func (b *fakeBoard) seen(from int) []boardRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]boardRequest(nil), b.requests[from:]...)
}

func (b *fakeBoard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := "/" + fleetID
	if r.URL.Path == prefix+"/proof" {
		proof(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	if rest != "/api/v1/status" && rest != "/api/v1/timeline" {
		http.NotFound(w, r)
		return
	}
	if c, err := r.Cookie("hand_board_" + fleetID); err != nil || c.Value != testToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	b.mu.Lock()
	if b.block != nil && rest == b.blockPath {
		block, entered := b.block, b.blockEntered
		b.mu.Unlock()
		if entered != nil {
			entered <- struct{}{}
		}
		select {
		case <-block:
		case <-r.Context().Done():
			return
		}
		b.mu.Lock()
	}
	since := r.URL.Query().Get("since")
	b.requests = append(b.requests, boardRequest{path: rest, since: since, issued: b.issued[rest]})
	if b.reject && since != "" {
		b.mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"status": 400, "code": "bad_cursor", "message": "bad cursor"}})
		return
	}
	if (b.fail && rest == "/api/v1/status") || (b.failTimeline && rest == "/api/v1/timeline") {
		b.mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"status": 500, "code": "internal", "message": "the status store failed"}})
		return
	}
	cur := fmt.Sprintf("%s#%d", rest, b.version[rest])
	data := any(b.status)
	if rest == "/api/v1/timeline" {
		data = b.timeline
	}
	b.issued[rest] = cur
	body, err := json.Marshal(map[string]any{
		"version": 1, "fleet": map[string]string{"id": fleetID, "name": "fake"},
		"cursor": cur, "generated_at": "2026-11-09T12:00:00Z", "data": data,
	})
	b.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Hand-Cursor", cur)
	if since == cur {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(body)
}

func newBoardApp(t *testing.T, b *fakeBoard) (*desktop.App, *fakeClock) {
	t.Helper()
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)
	c := &desktop.HTTPClient{
		Base:  srv.URL,
		Known: []desktop.Fleet{{ID: fleetID, Name: "fake"}},
		Token: func(string) (string, error) { return testToken, nil },
		HTTP:  srv.Client(),
	}
	app := desktop.New(func() (desktop.ReadModel, error) { return c, nil })
	app.DraftDir = t.TempDir()
	clock := newFakeClock()
	app.Clock = clock
	app.Boot()
	if app.Err != "" {
		t.Fatalf("boot: %s", app.Err)
	}
	return app, clock
}

func TestRefreshSendsTheLastCursorAndLeavesA304Alone(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	app.Show(desktop.TimelineScreen)
	status, timeline := app.Status, app.Timeline

	start := b.mark()
	app.StartRefresh()
	if got := clock.delay(); got != desktop.FocusedRefresh {
		t.Fatalf("first interval = %v, want %v", got, desktop.FocusedRefresh)
	}
	if fired := clock.Advance(desktop.FocusedRefresh); fired != 1 {
		t.Fatalf("fired %d timers, want 1", fired)
	}
	if app.Stale != "" {
		t.Fatalf("stale = %q after 304", app.Stale)
	}
	if !reflect.DeepEqual(app.Status, status) || !reflect.DeepEqual(app.Timeline, timeline) {
		t.Fatal("a 304 repainted the screen")
	}
	seen := b.seen(start)
	if len(seen) != 2 {
		t.Fatalf("poll made %d requests, want 2: %+v", len(seen), seen)
	}
	for _, req := range seen {
		if req.since == "" || req.since != req.issued {
			t.Fatalf("%s sent since %q, want the last cursor %q", req.path, req.since, req.issued)
		}
	}

	b.setTimeline(func(tl *desktop.Timeline) {
		tl.Entries = append([]desktop.Entry{{Role: "supervisor", Text: "second", No: 2}}, tl.Entries...)
	})
	start = b.mark()
	clock.Advance(desktop.FocusedRefresh)
	seen = b.seen(start)
	if len(seen) != 2 || seen[0].since == "" || seen[0].since != seen[0].issued || seen[1].since != seen[1].issued {
		t.Fatalf("second poll sent stale cursors: %+v", seen)
	}
	if len(app.Timeline.Entries) == 0 || app.Timeline.Entries[0].Text != "second" {
		t.Fatalf("a 200 did not apply the timeline: %+v", app.Timeline.Entries)
	}
}

func TestRefreshAppliesAChangedStatusOffTheScreen(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	app.Show(desktop.TimelineScreen)
	b.setStatus(func(st *desktop.Status) { st.Pill, st.Line = "waiting", "blocked on a decision" })
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	if app.Status.Pill != "waiting" || app.Status.Line != "blocked on a decision" {
		t.Fatalf("status = %+v", app.Status)
	}
}

func TestRefreshIntervalFollowsFocus(t *testing.T) {
	app, clock := newBoardApp(t, newFakeBoard())
	app.StartRefresh()
	if got := clock.delay(); got != desktop.FocusedRefresh {
		t.Fatalf("focused interval = %v, want %v", got, desktop.FocusedRefresh)
	}
	app.SetFocused(false)
	if got := clock.delay(); got != desktop.UnfocusedRefresh {
		t.Fatalf("unfocused interval = %v, want %v", got, desktop.UnfocusedRefresh)
	}
	app.SetFocused(true)
	if got := clock.delay(); got != desktop.FocusedRefresh {
		t.Fatalf("refocused interval = %v, want %v", got, desktop.FocusedRefresh)
	}
}

func TestRefreshClearsAForegroundReadError(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	b.setFail(true)
	b.setStatus(func(st *desktop.Status) { st.Pill = "waiting" })
	app.Refresh()
	if app.Err == "" {
		t.Fatal("the failed refresh left no error")
	}
	b.setFail(false)
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	if app.Err != "" {
		t.Fatalf("error = %q after the poll recovered", app.Err)
	}
	if app.Status.Pill != "waiting" {
		t.Fatalf("status = %+v", app.Status)
	}
}

func TestRefreshClearsAnErrorOnlyForTheHalfItRefreshed(t *testing.T) {
	t.Run("screen", func(t *testing.T) {
		b := newFakeBoard()
		app, clock := newBoardApp(t, b)
		app.Show(desktop.TimelineScreen)
		b.setStatus(func(st *desktop.Status) { st.Pill = "waiting" })
		app.StartRefresh()
		clock.Advance(desktop.FocusedRefresh)

		b.setFailTimeline(true)
		app.Refresh()
		if app.Err == "" {
			t.Fatal("a failed timeline read left no error")
		}
		if len(app.Timeline.Entries) != 0 {
			t.Fatalf("timeline = %+v, want the failed read's zero value", app.Timeline.Entries)
		}
		b.setFailTimeline(false)
		b.setStatus(func(st *desktop.Status) { st.Line = "still moving" })
		clock.Advance(desktop.FocusedRefresh)
		if app.Err == "" {
			t.Fatal("a fresh status cleared the timeline's error")
		}

		b.setTimeline(func(tl *desktop.Timeline) {
			tl.Entries = append([]desktop.Entry{{Role: "supervisor", Text: "recovered", No: 2}}, tl.Entries...)
		})
		clock.Advance(desktop.FocusedRefresh)
		if app.Err != "" {
			t.Fatalf("error = %q after the timeline recovered", app.Err)
		}
		if len(app.Timeline.Entries) == 0 || app.Timeline.Entries[0].Text != "recovered" {
			t.Fatalf("timeline = %+v", app.Timeline.Entries)
		}
	})

	t.Run("status", func(t *testing.T) {
		b := newFakeBoard()
		app, clock := newBoardApp(t, b)
		app.Show(desktop.TimelineScreen)
		b.setStatus(func(st *desktop.Status) { st.Pill = "waiting" })
		b.setTimeline(func(tl *desktop.Timeline) {
			tl.Entries = append([]desktop.Entry{{Role: "supervisor", Text: "first again", No: 2}}, tl.Entries...)
		})
		app.StartRefresh()
		clock.Advance(desktop.FocusedRefresh)

		b.setFail(true)
		app.Refresh()
		if app.Err == "" {
			t.Fatal("a failed status read left no error")
		}
		if app.Status.Pill != "" {
			t.Fatalf("status = %+v, want the failed read's zero value", app.Status)
		}
		b.setFail(false)
		b.setTimeline(func(tl *desktop.Timeline) {
			tl.Entries = append([]desktop.Entry{{Role: "supervisor", Text: "newer", No: 3}}, tl.Entries...)
		})
		clock.Advance(desktop.FocusedRefresh)
		if app.Err == "" {
			t.Fatal("a fresh timeline cleared the status's error")
		}
		if app.Status.Pill != "" {
			t.Fatal("the poll refilled a status it had not refreshed")
		}

		b.setStatus(func(st *desktop.Status) { st.Pill = "recovered" })
		clock.Advance(desktop.FocusedRefresh)
		if app.Err != "" {
			t.Fatalf("error = %q after the status recovered", app.Err)
		}
		if app.Status.Pill != "recovered" {
			t.Fatalf("status = %+v", app.Status)
		}
	})
}

func TestRefreshRecoversFromABadCursor(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	app.Show(desktop.TimelineScreen)
	b.setReject(true)
	b.setStatus(func(st *desktop.Status) { st.Pill = "waiting" })
	start := b.mark()
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	if app.Status.Pill != "waiting" {
		t.Fatalf("status = %+v, want the full read after bad_cursor", app.Status)
	}
	if app.Stale != "" {
		t.Fatalf("stale = %q after the retry succeeded", app.Stale)
	}
	seen := b.seen(start)
	if len(seen) != 4 {
		t.Fatalf("poll made %d requests, want a retry of each half: %+v", len(seen), seen)
	}
	for i, want := range []bool{true, false, true, false} {
		if got := seen[i].since != ""; got != want {
			t.Fatalf("request %d sent since %q, want the full read after bad_cursor: %+v", i, seen[i].since, seen)
		}
	}
}

func TestRefreshBacksOffOnErrors(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	app.Show(desktop.TimelineScreen)
	b.setFail(true)
	app.StartRefresh()

	clock.Advance(desktop.FocusedRefresh)
	if app.Stale == "" {
		t.Fatal("a failed tick left no stale note")
	}
	mustText(t, newTester(app), "Stale · retrying")
	if got, want := clock.delay(), 2*desktop.FocusedRefresh; got != want {
		t.Fatalf("first backoff = %v, want %v", got, want)
	}
	start := b.mark()
	clock.Advance(3 * time.Second)
	if got := b.mark(); got != start {
		t.Fatal("a tick fired before the backoff elapsed")
	}
	clock.Advance(time.Second)
	if got, want := clock.delay(), 4*desktop.FocusedRefresh; got != want {
		t.Fatalf("second backoff = %v, want %v", got, want)
	}

	b.setFail(false)
	clock.Advance(4 * desktop.FocusedRefresh)
	if app.Stale != "" {
		t.Fatalf("stale = %q after the board answered", app.Stale)
	}
	mustNotText(t, newTester(app), "Stale · retrying")
	if got := clock.delay(); got != desktop.FocusedRefresh {
		t.Fatalf("interval after recovery = %v, want %v", got, desktop.FocusedRefresh)
	}
}

func TestRefreshBacksOffUpToTheCap(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	b.setFail(true)
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	for _, want := range []time.Duration{4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second} {
		if got := clock.delay(); got != want {
			t.Fatalf("backoff = %v, want %v", got, want)
		}
		clock.Advance(want)
	}
}

func TestAManualRefreshClearsTheStaleNote(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	app.Show(desktop.TimelineScreen)
	b.setFail(true)
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	clock.Advance(2 * desktop.FocusedRefresh)
	if app.Stale == "" {
		t.Fatal("the failed polls left no stale note")
	}

	b.setFail(false)
	app.Refresh()
	if app.Stale != "" {
		t.Fatalf("stale = %q after a successful manual refresh", app.Stale)
	}
	if got := clock.delay(); got != desktop.FocusedRefresh {
		t.Fatalf("interval after the manual refresh = %v, want %v", got, desktop.FocusedRefresh)
	}
}

func TestRefreshPausesWithoutAFleet(t *testing.T) {
	f := &fixture{}
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = t.TempDir()
	clock := newFakeClock()
	app.Clock = clock
	app.Boot()
	app.StartRefresh()
	if fired := clock.Advance(time.Minute); fired != 0 {
		t.Fatalf("fired %d ticks with no fleet", fired)
	}
	if got := clock.delay(); got != 0 {
		t.Fatalf("armed a tick in %v with no fleet", got)
	}
	if got := f.count("poll:" + fleetID); got != 0 {
		t.Fatalf("polled %d times with no fleet", got)
	}
}

func TestAPollThatOutrunsItsTimeoutLeavesTheStaleNote(t *testing.T) {
	f := goldenFixture(t)
	f.pollBlock = make(chan struct{})
	app := newApp(t, f)
	app.PollTimeout = 20 * time.Millisecond
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	if app.Stale != "the board did not answer in time" {
		t.Fatalf("stale = %q after the tick timed out", app.Stale)
	}
	close(f.pollBlock)
	if got := clock.delay(); got != 2*desktop.FocusedRefresh {
		t.Fatalf("backoff after the timeout = %v, want %v", got, 2*desktop.FocusedRefresh)
	}
}

func TestFleetSwitchCancelsTheOldLoop(t *testing.T) {
	f := goldenFixture(t)
	f.pollBlock = make(chan struct{})
	f.pollEntered = make(chan struct{}, 1)
	f.pollExited = make(chan struct{}, 1)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	runPosted(t, posted)
	select {
	case <-f.pollEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("the poll never started")
	}
	app.SelectFleet(secondID)
	select {
	case <-f.pollExited:
	case <-time.After(2 * time.Second):
		t.Fatal("the fleet switch did not cancel the poll")
	}
	settle(t, posted)
	if got := f.count("poll:" + fleetID); got != 1 {
		t.Fatalf("the old fleet was polled %d times, want 1", got)
	}
	close(f.pollBlock)
	if fired := clock.Advance(desktop.FocusedRefresh); fired != 1 {
		t.Fatalf("fired %d timers for the new fleet, want 1", fired)
	}
	pumpUntil(t, posted, func() bool { return f.count("poll:"+secondID) > 0 })
	if got := f.count("poll:" + secondID); got == 0 {
		t.Fatal("the selected fleet was not polled")
	}
}

func TestCloseCancelsAPollInFlight(t *testing.T) {
	f := goldenFixture(t)
	f.pollBlock = make(chan struct{})
	f.pollEntered = make(chan struct{}, 1)
	f.pollExited = make(chan struct{}, 1)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	runPosted(t, posted)
	select {
	case <-f.pollEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("the poll never started")
	}

	app.Close()
	select {
	case <-f.pollExited:
	case <-time.After(2 * time.Second):
		t.Fatal("Close left the poll running")
	}
	settle(t, posted)
	if fired := clock.Advance(time.Minute); fired != 0 {
		t.Fatalf("fired %d ticks after Close", fired)
	}
}

func TestRefreshDropsAScreenTheOperatorLeft(t *testing.T) {
	f := goldenFixture(t)
	f.pollBlock = make(chan struct{})
	f.pollEntered = make(chan struct{}, 1)
	f.pollDone = make(chan struct{}, 1)
	app := newApp(t, f)
	app.Clock = newFakeClock()
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	app.Show(desktop.TimelineScreen)
	settle(t, posted)
	app.StartRefresh()
	app.Clock.(*fakeClock).Advance(desktop.FocusedRefresh)
	runPosted(t, posted)
	select {
	case <-f.pollEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("the poll never started")
	}
	f.mu.Lock()
	tl := f.timeline[fleetID]
	tl.Entries = append([]desktop.Entry{{Role: "supervisor", Text: "while away", No: 9}}, tl.Entries...)
	f.timeline[fleetID] = tl
	f.mu.Unlock()

	app.Show(desktop.TasksScreen)
	settle(t, posted)
	tasks := app.Tasks
	close(f.pollBlock)
	select {
	case <-f.pollDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the released poll never returned")
	}
	settle(t, posted)

	if app.Screen != desktop.TasksScreen || !reflect.DeepEqual(app.Tasks, tasks) {
		t.Fatal("a poll of the old screen replaced the new one")
	}
	if len(app.Timeline.Entries) > 0 && app.Timeline.Entries[0].Text == "while away" {
		t.Fatal("a poll of the old screen changed the timeline")
	}
}

func TestRefreshDropsAPollOlderThanTheLastRead(t *testing.T) {
	f := goldenFixture(t)
	f.pollBlock = make(chan struct{})
	f.pollEntered = make(chan struct{}, 1)
	f.pollDone = make(chan struct{}, 1)
	app := newApp(t, f)
	app.Clock = newFakeClock()
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	app.Show(desktop.TimelineScreen)
	settle(t, posted)
	app.StartRefresh()
	app.Clock.(*fakeClock).Advance(desktop.FocusedRefresh)
	runPosted(t, posted)
	select {
	case <-f.pollEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("the poll never started")
	}

	app.Retry()
	settle(t, posted)
	reloaded := app.Timeline
	if len(reloaded.Entries) == 0 {
		t.Fatal("the retry read no timeline")
	}
	f.mu.Lock()
	tl := f.timeline[fleetID]
	tl.Entries = append([]desktop.Entry{{Role: "supervisor", Text: "newer than the poll", No: 8}}, tl.Entries...)
	f.timeline[fleetID] = tl
	f.mu.Unlock()
	close(f.pollBlock)
	select {
	case <-f.pollDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the released poll never returned")
	}
	settle(t, posted)
	if !reflect.DeepEqual(app.Timeline, reloaded) {
		t.Fatalf("a poll older than the last read replaced the screen: %+v", app.Timeline)
	}
}

func TestRearmDropsAQueuedTick(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	app.SetFocused(false)
	runPosted(t, posted)
	if got := f.count("poll:" + fleetID); got != 0 {
		t.Fatalf("the tick queued before the rearm ran: %d polls", got)
	}
	if got := clock.delay(); got != desktop.UnfocusedRefresh {
		t.Fatalf("rearmed interval = %v, want %v", got, desktop.UnfocusedRefresh)
	}
	clock.Advance(desktop.UnfocusedRefresh)
	pumpUntil(t, posted, func() bool { return f.count("poll:"+fleetID) > 0 })
}

func TestPollWaitsWhileAnActionRuns(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	app.Clock = newFakeClock()
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	app.Busy = true
	app.StartRefresh()
	app.Clock.(*fakeClock).Advance(desktop.FocusedRefresh)
	runPosted(t, posted)
	if got := f.count("poll:" + fleetID); got != 0 {
		t.Fatalf("a poll overlapped an action: %d polls", got)
	}
	app.Busy = false
	app.Clock.(*fakeClock).Advance(desktop.FocusedRefresh)
	pumpUntil(t, posted, func() bool { return f.count("poll:"+fleetID) > 0 })
}

func TestPollWaitsWhileTheScreenLoads(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.Loading = true
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	if got := f.count("poll:" + fleetID); got != 0 {
		t.Fatalf("a poll overlapped a load: %d polls", got)
	}
	app.Loading = false
	clock.Advance(desktop.FocusedRefresh)
	if got := f.count("poll:" + fleetID); got != 1 {
		t.Fatalf("polls after the load = %d, want 1", got)
	}
}

func TestRefreshKeepsTimelineHistory(t *testing.T) {
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	tl.Older = 42
	f.timeline[fleetID] = tl
	f.older = map[string]desktop.Timeline{fleetID: {
		Entries: []desktop.Entry{{Role: "operator", Text: "the earlier message", At: "2026-10-31T04:40:00Z"}},
	}}
	app := newApp(t, f)
	app.Show(desktop.TimelineScreen)
	app.Older()
	f.mu.Lock()
	tl = f.timeline[fleetID]
	tl.Entries = append([]desktop.Entry{{Role: "supervisor", Text: "newer", No: 2}}, tl.Entries...)
	f.timeline[fleetID] = tl
	f.mu.Unlock()
	app.Clock = newFakeClock()
	app.StartRefresh()
	app.Clock.(*fakeClock).Advance(desktop.FocusedRefresh)
	if len(app.Timeline.Entries) == 0 || app.Timeline.Entries[0].Text != "newer" {
		t.Fatalf("the refresh did not apply: %+v", app.Timeline.Entries)
	}
	found := false
	for _, entry := range app.Timeline.Entries {
		if entry.Text == "the earlier message" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the refresh dropped the loaded history: %+v", app.Timeline.Entries)
	}
	if app.Timeline.Older != 0 {
		t.Fatalf("pagination boundary = %d, want the loaded end", app.Timeline.Older)
	}
}

func TestRefreshKeepsTheComposerDraft(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	app.Show(desktop.TimelineScreen)
	app.Draft = "half a message"
	b.setTimeline(func(tl *desktop.Timeline) {
		tl.Entries = append([]desktop.Entry{{Role: "supervisor", Text: "newer", No: 2}}, tl.Entries...)
	})
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	if app.Draft != "half a message" {
		t.Fatalf("draft = %q", app.Draft)
	}
	if len(app.Timeline.Entries) == 0 || app.Timeline.Entries[0].Text != "newer" {
		t.Fatalf("timeline = %+v", app.Timeline.Entries)
	}
}

func TestRefreshDoesNotBlockTheRenderPath(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	app.Show(desktop.TimelineScreen)
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	app.StartRefresh()

	b.mu.Lock()
	b.block, b.blockEntered, b.blockPath = make(chan struct{}), make(chan struct{}, 1), "/api/v1/timeline"
	b.mu.Unlock()
	clock.Advance(desktop.FocusedRefresh)

	render := func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			newTester(app).Image()
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("the render path waited for the poll")
		}
	}
	render()
	runPosted(t, posted)
	select {
	case <-b.blockEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("the poll never started")
	}
	render()
	b.mu.Lock()
	block := b.block
	b.mu.Unlock()
	close(block)
	settle(t, posted)
}

func runPosted(t *testing.T, posted <-chan func()) {
	t.Helper()
	select {
	case fn := <-posted:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("nothing was posted to the window")
	}
}

func settle(t *testing.T, posted <-chan func()) {
	t.Helper()
	for {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}

func pumpUntil(t *testing.T, posted <-chan func(), until func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !until() {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatal("the expected work never ran")
		}
	}
	settle(t, posted)
}
