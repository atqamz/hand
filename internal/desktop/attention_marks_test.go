package desktop_test

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func markApp(t *testing.T, f *fixture) (*desktop.App, *fakeClock, *ui.Tester) {
	t.Helper()
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	return app, clock, newTester(app)
}

func hasMark(tt *ui.Tester, name string) bool {
	_, ok := tt.Find(name)
	return ok
}

func mustMark(t *testing.T, tt *ui.Tester, want ...string) {
	t.Helper()
	tt.Frame()
	for _, name := range want {
		if !hasMark(tt, name) {
			t.Errorf("no mark named %q in %q", name, tt.Texts())
		}
	}
}

func mustNotMark(t *testing.T, tt *ui.Tester, names ...string) {
	t.Helper()
	tt.Frame()
	for _, name := range names {
		if hasMark(tt, name) {
			t.Errorf("unexpected mark %q", name)
		}
	}
}

func markFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	f.status[fleetID] = desktop.Status{AgentState: "idle", Needs: desktop.Needs{
		Waiting: 1, Worst: "blocked", WorstWord: "blocked", Items: []desktop.Need{{Kind: "blocked", Ref: "s1"}},
	}}
	f.status[secondID] = desktop.Status{AgentState: "idle"}
	f.attnNeeds = map[string]desktop.Needs{
		fleetID:  {Waiting: 3, Worst: "decision", WorstWord: "waiting"},
		secondID: {Waiting: 2, Worst: "decision", WorstWord: "waiting"},
	}
	return f
}

func TestTheSelectedMarkReadsItsOwnStatusAndTheOthersTheAttentionRead(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			f := markFixture(t)
			app, clock, _ := markApp(t, f)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustMark(t, tt, "golden, blocked", "second")
			mustNotMark(t, tt, "second, waiting", "second, quiet")
			clock.Advance(desktop.FocusedRefresh)
			mustMark(t, tt, "golden, blocked", "second, waiting")
			mustNotMark(t, tt, "golden, waiting", "second")
			if got := f.count("attention"); got != 1 {
				t.Fatalf("%d attention reads, want 1", got)
			}
		})
	}
}

func TestMarkWordsFollowTheWorstNeed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		needs desktop.Needs
		want  string
	}{
		{"blocked", desktop.Needs{Waiting: 1, Worst: "blocked", WorstWord: "blocked"}, "second, blocked"},
		{"worker", desktop.Needs{Waiting: 1, Worst: "worker", WorstWord: "blocked"}, "second, blocked"},
		{"failure", desktop.Needs{Waiting: 2, Worst: "failure", WorstWord: "failed"}, "second, failed"},
		{"resume", desktop.Needs{Waiting: 1, Worst: "resume", WorstWord: "interrupted"}, "second, interrupted"},
		{"decision", desktop.Needs{Waiting: 4, Worst: "decision", WorstWord: "waiting"}, "second, waiting"},
		{"limited", desktop.Needs{Waiting: 1, Worst: "limited", WorstWord: "waiting"}, "second, waiting"},
		{"report", desktop.Needs{Waiting: 1, Worst: "report", WorstWord: "waiting"}, "second, waiting"},
		{"quiet", desktop.Needs{}, "second, quiet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := markFixture(t)
			f.attnNeeds[secondID] = tc.needs
			_, clock, tt := markApp(t, f)
			clock.Advance(desktop.FocusedRefresh)
			mustMark(t, tt, tc.want)
		})
	}
}

func TestAnUnavailableFleetIsMarkedUnavailable(t *testing.T) {
	f := markFixture(t)
	f.attnGone = map[string]bool{secondID: true}
	_, clock, tt := markApp(t, f)
	clock.Advance(desktop.FocusedRefresh)
	mustMark(t, tt, "second, unavailable", "golden, blocked")
}

func TestTheAttentionReadRunsEveryFiveSecondsFocusedOrNot(t *testing.T) {
	f := markFixture(t)
	app, clock, _ := markApp(t, f)
	clock.Advance(desktop.FocusedRefresh)
	for _, focused := range []bool{true, false} {
		app.SetFocused(focused)
		before := f.count("attention")
		clock.Advance(5*time.Second - time.Millisecond)
		if got := f.count("attention"); got != before {
			t.Fatalf("focused=%v: read %d times before the interval", focused, got-before)
		}
		clock.Advance(time.Millisecond)
		if got := f.count("attention"); got != before+1 {
			t.Fatalf("focused=%v: %d reads after the interval, want 1", focused, got-before)
		}
	}
}

func TestOneFleetMakesNoAttentionRead(t *testing.T) {
	f := emptyFixture()
	_, clock, _ := markApp(t, f)
	clock.Advance(10 * time.Minute)
	if got := f.count("attention"); got != 0 {
		t.Fatalf("%d attention reads with one fleet", got)
	}
}

func TestAHiddenWindowMakesNoAttentionRead(t *testing.T) {
	f := markFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.SetMinimized(true)
	app.StartRefresh()
	clock.Advance(10 * time.Minute)
	if got := f.count("attention"); got != 0 {
		t.Fatalf("%d attention reads while minimized", got)
	}
	app.SetMinimized(false)
	clock.Advance(desktop.FocusedRefresh)
	if got := f.count("attention"); got != 1 {
		t.Fatalf("%d attention reads after restore, want 1", got)
	}
	app.SetMinimized(true)
	if d := clock.delay(); d != 0 {
		t.Fatalf("a timer is still armed %v after minimizing", d)
	}
	clock.Advance(10 * time.Minute)
	if got := f.count("attention"); got != 1 {
		t.Fatalf("%d attention reads after minimizing again, want still 1", got)
	}
}

func TestAClosedWindowMakesNoAttentionRead(t *testing.T) {
	f := markFixture(t)
	app, clock, _ := markApp(t, f)
	clock.Advance(desktop.FocusedRefresh)
	app.Close()
	if d := clock.delay(); d != 0 {
		t.Fatalf("a timer is still armed %v after Close", d)
	}
	clock.Advance(10 * time.Minute)
	if got := f.count("attention"); got != 1 {
		t.Fatalf("%d attention reads, want 1 before Close", got)
	}
}

func TestAFailingReadKeepsTheMarksForOneCycleThenMarksThemUnavailable(t *testing.T) {
	f := markFixture(t)
	_, clock, tt := markApp(t, f)
	clock.Advance(desktop.FocusedRefresh)
	mustMark(t, tt, "second, waiting")
	f.attnErr = errors.New("the list failed")
	clock.Advance(desktop.AttentionRefresh)
	mustMark(t, tt, "second, waiting")
	before := f.count("attention")
	clock.Advance(2*desktop.AttentionRefresh - time.Millisecond)
	if got := f.count("attention"); got != before {
		t.Fatalf("retried after %v, want a backoff of %v", 2*desktop.AttentionRefresh-time.Millisecond, 2*desktop.AttentionRefresh)
	}
	clock.Advance(time.Millisecond)
	mustMark(t, tt, "second, unavailable", "golden, blocked")
	mustNotMark(t, tt, "second, waiting")
	f.attnErr = nil
	for range 20 {
		clock.Advance(desktop.AttentionRefresh)
	}
	mustMark(t, tt, "second, waiting")
	mustNotMark(t, tt, "second, unavailable")
}

func TestAFleetNeverReadIsUnavailableAfterTwoFailedReads(t *testing.T) {
	f := markFixture(t)
	f.attnErr = errors.New("down")
	_, clock, tt := markApp(t, f)
	clock.Advance(desktop.FocusedRefresh)
	mustMark(t, tt, "second")
	mustNotMark(t, tt, "second, unavailable")
	clock.Advance(2 * desktop.AttentionRefresh)
	mustMark(t, tt, "second, unavailable", "golden, blocked")
}

func TestBackoffStopsAtOneMinute(t *testing.T) {
	f := markFixture(t)
	f.attnErr = errors.New("down")
	_, clock, _ := markApp(t, f)
	for range 12 {
		clock.Advance(time.Minute)
	}
	before := f.count("attention")
	clock.Advance(time.Minute)
	if got := f.count("attention") - before; got != 1 {
		t.Fatalf("%d reads in a minute at the cap, want 1", got)
	}
}

func TestTheSelectedMarkGoesUnavailableWithItsStatus(t *testing.T) {
	f := markFixture(t)
	app, clock, tt := markApp(t, f)
	mustMark(t, tt, "golden, blocked")
	f.fail = errors.New("down")
	for range 6 {
		clock.Advance(desktop.FocusedRefresh)
	}
	if app.Stale == "" {
		t.Fatal("the window never went stale")
	}
	mustMark(t, tt, "golden, unavailable")
	mustNotMark(t, tt, "golden, blocked")
}

func TestViewMakesNoRequestAndNoSinkCall(t *testing.T) {
	f := markFixture(t)
	app, clock, tt := markApp(t, f)
	clock.Advance(desktop.FocusedRefresh)
	titles := 0
	app.OnTitle = func(string) { titles++ }
	snapshot := func() map[string]int {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := map[string]int{}
		for k, v := range f.calls {
			out[k] = v
		}
		return out
	}
	before := snapshot()
	for range 200 {
		tt.Frame()
	}
	after := snapshot()
	for k, v := range after {
		if v != before[k] {
			t.Errorf("View made %d %q requests", v-before[k], k)
		}
	}
	if titles != 0 {
		t.Fatalf("View pushed %d titles", titles)
	}
}

func TestARefreshKeepsTheMarksAndTheReadsGoOn(t *testing.T) {
	f := markFixture(t)
	app, clock, tt := markApp(t, f)
	clock.Advance(desktop.FocusedRefresh)
	app.Refresh()
	mustMark(t, tt, "second, waiting")
	before := f.count("attention")
	clock.Advance(desktop.FocusedRefresh)
	clock.Advance(desktop.AttentionRefresh)
	if f.count("attention") == before {
		t.Fatal("no attention read after Refresh")
	}
}

func TestAMarkLosesItsStatusWhileItsFleetLoads(t *testing.T) {
	f := markFixture(t)
	app, _, tt := markApp(t, f)
	app.Loading = true
	mustMark(t, tt, "golden")
	mustNotMark(t, tt, "golden, blocked")
}

func TestMarksShowInTheNarrowSheet(t *testing.T) {
	f := markFixture(t)
	app, clock, _ := markApp(t, f)
	tt := sized(t, app, 640, windowHeight, false)
	clock.Advance(desktop.FocusedRefresh)
	click(t, tt, "Menu")
	mustMark(t, tt, "second, waiting", "golden, blocked")
}

func TestTheAttentionReadRunsOffTheWindowThread(t *testing.T) {
	f := markFixture(t)
	app := newApp(t, f)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := newTester(app)
	clock.Advance(desktop.FocusedRefresh)
	deadline := time.After(5 * time.Second)
	for f.count("attention") == 0 || len(posted) > 0 || !hasMark(tt, "second, waiting") {
		select {
		case fn := <-posted:
			fn()
			tt.Frame()
		case <-deadline:
			t.Fatalf("the read never landed: %q", tt.Texts())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if got := f.count("attention"); got != 1 {
		t.Fatalf("%d reads, want 1", got)
	}
}

func sceneFixture(t *testing.T) *fixture {
	t.Helper()
	f := markFixture(t)
	for _, extra := range []desktop.Fleet{{ID: "f00000000e0b1", Name: "docs"}, {ID: "f00000000e0b2", Name: "research"}, {ID: "f00000000e0b3", Name: "api"}} {
		f.fleets = append(f.fleets, extra)
		f.status[extra.ID] = desktop.Status{AgentState: "idle"}
		f.timeline[extra.ID] = f.timeline[fleetID]
		f.tasks[extra.ID] = f.tasks[fleetID]
	}
	f.attnNeeds["f00000000e0b1"] = desktop.Needs{Waiting: 1, Worst: "report", WorstWord: "waiting"}
	f.attnGone = map[string]bool{"f00000000e0b3": true}
	return f
}

func TestScenesFleetMarks(t *testing.T) {
	for _, open := range []bool{true, false} {
		rail := "closed"
		if open {
			rail = "open"
		}
		for _, dark := range []bool{false, true} {
			t.Run(rail+"-"+theme(dark), func(t *testing.T) {
				app, clock, _ := markApp(t, sceneFixture(t))
				app.RailOpen = open
				tt := sized(t, app, windowWidth, windowHeight, dark)
				clock.Advance(desktop.FocusedRefresh)
				mustMark(t, tt, "golden, blocked", "second, waiting", "docs, waiting", "research, quiet", "api, unavailable")
				shot(t, tt, "81-fleet-marks-"+rail+"-1100x760-"+theme(dark))
			})
		}
	}
}

func TestAReadFromBeforeAResetIsDropped(t *testing.T) {
	f := markFixture(t)
	app := newApp(t, f)
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := newTester(app)
	quiet := func(what string) {
		t.Helper()
		select {
		case <-posted:
			t.Fatalf("%s", what)
		case <-time.After(50 * time.Millisecond):
		}
	}

	clock.Advance(desktop.FocusedRefresh)
	app.SetMinimized(true)
	app.SetMinimized(false)
	for len(posted) > 0 {
		(<-posted)()
	}
	quiet("a timer that a reset replaced started a read")
	if got := f.count("attention"); got != 0 {
		t.Fatalf("%d reads from a timer that a reset replaced", got)
	}

	f.attnBlock, f.attnEntered = make(chan struct{}), make(chan struct{}, 1)
	clock.Advance(desktop.FocusedRefresh)
	deadline := time.After(5 * time.Second)
	for entered := false; !entered; {
		select {
		case fn := <-posted:
			fn()
		case <-f.attnEntered:
			entered = true
		case <-deadline:
			t.Fatal("the read never started")
		}
	}
	app.SetMinimized(true)
	close(f.attnBlock)
	for landed := false; !landed; {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(200 * time.Millisecond):
			landed = true
		}
	}
	mustNotMark(t, tt, "second, waiting")
}
