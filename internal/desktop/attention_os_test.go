package desktop_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
)

type osSinks struct {
	flash  []bool
	badge  []int
	titles []string
}

func osApp(t *testing.T, f *fixture, setup ...func(*desktop.App)) (*desktop.App, *fakeClock, *osSinks) {
	t.Helper()
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	s := &osSinks{}
	app.OnFlash = func(on bool) { s.flash = append(s.flash, on) }
	app.OnBadge = func(n int) { s.badge = append(s.badge, n) }
	app.OnTitle = func(title string) { s.titles = append(s.titles, title) }
	for _, fn := range setup {
		fn(app)
	}
	app.StartRefresh()
	return app, clock, s
}

func quietPair(t *testing.T) *fixture {
	t.Helper()
	f := markFixture(t)
	f.status[fleetID] = desktop.Status{AgentState: "idle"}
	f.attnNeeds = map[string]desktop.Needs{}
	return f
}

func setNeeds(f *fixture, id string, n desktop.Needs) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.status[id]
	st.Needs = n
	f.status[id] = st
	if f.attnNeeds != nil {
		if _, ok := f.attnNeeds[id]; ok {
			f.attnNeeds[id] = n
		}
	}
}

func setAttn(f *fixture, id string, n desktop.Needs) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attnNeeds == nil {
		f.attnNeeds = map[string]desktop.Needs{}
	}
	f.attnNeeds[id] = n
}

var (
	decisions = func(n int) desktop.Needs {
		return desktop.Needs{Waiting: n, Worst: "decision", WorstWord: "waiting", Items: []desktop.Need{{Kind: "decision", Ref: "d1"}}}
	}
	blockedNeed = desktop.Needs{Waiting: 1, Worst: "blocked", WorstWord: "blocked", Items: []desktop.Need{{Kind: "blocked", Ref: "s1"}}}
	reports     = func(n int) desktop.Needs {
		return desktop.Needs{Waiting: n, Worst: "report", WorstWord: "waiting", Items: []desktop.Need{{Kind: "report", Ref: "r1"}}}
	}
)

func tickFor(clock *fakeClock, d time.Duration) {
	for elapsed := time.Duration(0); elapsed < d; elapsed += time.Second {
		clock.Advance(time.Second)
	}
}

func TestFlashOnceWhenTheSelectedFleetRisesWhileUnfocused(t *testing.T) {
	f := quietPair(t)
	app, clock, s := osApp(t, f)
	tickFor(clock, 10*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, decisions(1))
	tickFor(clock, 60*time.Second)
	if want := []bool{true}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v over 60 identical polls, want %v", s.flash, want)
	}
	setNeeds(f, fleetID, decisions(2))
	tickFor(clock, 10*time.Second)
	if want := []bool{true}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v after a second rise while one is pending, want %v", s.flash, want)
	}
}

func TestNoFlashWhileTheWindowHasFocus(t *testing.T) {
	f := quietPair(t)
	_, clock, s := osApp(t, f)
	tickFor(clock, 10*time.Second)
	setNeeds(f, fleetID, blockedNeed)
	setNeeds(f, secondID, decisions(2))
	tickFor(clock, 30*time.Second)
	if len(s.flash) != 0 {
		t.Fatalf("flash calls %v while focused", s.flash)
	}
}

func TestTheFirstReadIsABaselineNotARise(t *testing.T) {
	f := markFixture(t)
	f.attnNeeds = nil
	setNeeds(f, secondID, decisions(2))
	_, clock, s := osApp(t, f, func(a *desktop.App) { a.SetFocused(false) })
	tickFor(clock, 60*time.Second)
	if len(s.flash) != 0 {
		t.Fatalf("flash calls %v for a queue that was there when the window opened", s.flash)
	}
}

func TestAnotherFleetsRiseFlashesWithoutTouchingTitleOrBadge(t *testing.T) {
	f := quietPair(t)
	app, clock, s := osApp(t, f)
	tickFor(clock, 10*time.Second)
	app.SetFocused(false)
	before := len(s.titles)
	setAttn(f, secondID, blockedNeed)
	tickFor(clock, 10*time.Second)
	if want := []bool{true}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v, want %v", s.flash, want)
	}
	if len(s.badge) != 0 || len(s.titles) != before {
		t.Fatalf("another fleet changed the badge %v or the title %v", s.badge, s.titles[before:])
	}
	if got := app.Title(); got != "Hand · golden" {
		t.Fatalf("title = %q", got)
	}
}

func TestOnlyAFailOrWaitToneFlashes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		needs desktop.Needs
		want  []bool
	}{
		{"decision", decisions(1), []bool{true}},
		{"blocked", blockedNeed, []bool{true}},
		{"report alone", reports(2), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := quietPair(t)
			app, clock, s := osApp(t, f)
			tickFor(clock, 5*time.Second)
			app.SetFocused(false)
			setNeeds(f, fleetID, tc.needs)
			tickFor(clock, 10*time.Second)
			if !slices.Equal(s.flash, tc.want) {
				t.Fatalf("flash calls %v, want %v", s.flash, tc.want)
			}
		})
	}
}

func TestAWorseToneAtTheSameCountFlashes(t *testing.T) {
	f := quietPair(t)
	setNeeds(f, fleetID, decisions(1))
	app, clock, s := osApp(t, f)
	tickFor(clock, 5*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, blockedNeed)
	tickFor(clock, 5*time.Second)
	if want := []bool{true}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v, want %v", s.flash, want)
	}
}

func TestAFallNeverFlashes(t *testing.T) {
	f := quietPair(t)
	setNeeds(f, fleetID, decisions(3))
	app, clock, s := osApp(t, f)
	tickFor(clock, 5*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, decisions(1))
	tickFor(clock, 5*time.Second)
	if len(s.flash) != 0 {
		t.Fatalf("flash calls %v on a fall", s.flash)
	}
}

func TestFocusClearsTheFlash(t *testing.T) {
	f := quietPair(t)
	app, clock, s := osApp(t, f)
	tickFor(clock, 5*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, decisions(1))
	tickFor(clock, 5*time.Second)
	app.SetFocused(true)
	app.SetFocused(true)
	if want := []bool{true, false}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v, want %v", s.flash, want)
	}
	tickFor(clock, 30*time.Second)
	if len(s.flash) != 2 {
		t.Fatalf("flash calls %v after focus, want no more", s.flash)
	}
	app.SetFocused(false)
	setNeeds(f, fleetID, decisions(2))
	tickFor(clock, 5*time.Second)
	if want := []bool{true, false, true}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v after a new rise, want %v", s.flash, want)
	}
}

func TestRestoreClearsTheFlash(t *testing.T) {
	f := quietPair(t)
	app, clock, s := osApp(t, f)
	tickFor(clock, 5*time.Second)
	app.SetMinimized(true)
	setAttn(f, fleetID, decisions(1))
	setNeeds(f, fleetID, decisions(1))
	tickFor(clock, 15*time.Second)
	if want := []bool{true}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v, want %v", s.flash, want)
	}
	app.SetMinimized(false)
	if want := []bool{true, false}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v after restore, want %v", s.flash, want)
	}
}

func TestTheFlashEndsWhenTheFleetThatRaisedItIsQuiet(t *testing.T) {
	f := quietPair(t)
	app, clock, s := osApp(t, f)
	tickFor(clock, 5*time.Second)
	app.SetFocused(false)
	setNeeds(f, secondID, decisions(1))
	setAttn(f, secondID, decisions(1))
	tickFor(clock, 10*time.Second)
	tickFor(clock, 10*time.Second)
	if want := []bool{true}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v while the other fleet waits and the selected one is quiet, want %v", s.flash, want)
	}
	setAttn(f, secondID, desktop.Needs{})
	tickFor(clock, 10*time.Second)
	if want := []bool{true, false}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v after the queue emptied, want %v", s.flash, want)
	}
}

func TestAFailedReadNeverFlashesAndRecoveryIsANewBaseline(t *testing.T) {
	f := quietPair(t)
	app, clock, s := osApp(t, f)
	tickFor(clock, 5*time.Second)
	app.SetFocused(false)
	f.mu.Lock()
	f.attnErr = errors.New("boom")
	f.mu.Unlock()
	tickFor(clock, 3*time.Minute)
	if len(s.flash) != 0 {
		t.Fatalf("flash calls %v while reads fail", s.flash)
	}
	setAttn(f, secondID, decisions(2))
	f.mu.Lock()
	f.attnErr = nil
	f.mu.Unlock()
	tickFor(clock, 3*time.Minute)
	if len(s.flash) != 0 {
		t.Fatalf("flash calls %v on the first read after the board came back", s.flash)
	}
	setAttn(f, secondID, decisions(3))
	tickFor(clock, 10*time.Second)
	if want := []bool{true}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v, want %v after a real rise", s.flash, want)
	}
}

func TestAFailingStatusReadRebaselinesTheSelectedFleet(t *testing.T) {
	f := quietPair(t)
	app, clock, s := osApp(t, f)
	tickFor(clock, 5*time.Second)
	app.SetFocused(false)
	f.mu.Lock()
	f.pollErr = errors.New("boom")
	f.mu.Unlock()
	tickFor(clock, 30*time.Second)
	setNeeds(f, fleetID, decisions(1))
	f.mu.Lock()
	f.pollErr = nil
	f.mu.Unlock()
	tickFor(clock, 120*time.Second)
	if len(s.flash) != 0 {
		t.Fatalf("flash calls %v for the first status after an outage", s.flash)
	}
}

func TestTheBadgeIsTheSelectedFleetsOwnCount(t *testing.T) {
	f := quietPair(t)
	app, clock, s := osApp(t, f)
	tickFor(clock, 10*time.Second)
	setAttn(f, secondID, decisions(4))
	tickFor(clock, 10*time.Second)
	if len(s.badge) != 0 {
		t.Fatalf("badge %v for a count that is another fleet's", s.badge)
	}
	setNeeds(f, fleetID, decisions(3))
	tickFor(clock, 2*time.Second)
	setNeeds(f, fleetID, decisions(2))
	tickFor(clock, 2*time.Second)
	tickFor(clock, 20*time.Second)
	if want := []int{3, 2}; !slices.Equal(s.badge, want) {
		t.Fatalf("badge %v, want %v: one push per change, never a sum", s.badge, want)
	}
	setNeeds(f, fleetID, desktop.Needs{})
	tickFor(clock, 2*time.Second)
	if want := []int{3, 2, 0}; !slices.Equal(s.badge, want) {
		t.Fatalf("badge %v, want %v", s.badge, want)
	}
	_ = app
}

func TestTheBadgeClearsWhileTheBoardFails(t *testing.T) {
	f := quietPair(t)
	setNeeds(f, fleetID, decisions(2))
	_, clock, s := osApp(t, f)
	tickFor(clock, 3*time.Second)
	f.mu.Lock()
	f.pollErr = errors.New("boom")
	f.mu.Unlock()
	tickFor(clock, 3*time.Minute)
	if want := []int{2, 0}; !slices.Equal(s.badge, want) {
		t.Fatalf("badge %v, want %v", s.badge, want)
	}
	f.mu.Lock()
	f.pollErr = nil
	f.mu.Unlock()
	tickFor(clock, 3*time.Minute)
	if want := []int{2, 0, 2}; !slices.Equal(s.badge, want) {
		t.Fatalf("badge %v, want %v after recovery", s.badge, want)
	}
}

func TestAMinimizedWindowReadsAttentionEveryFifteenSeconds(t *testing.T) {
	f := markFixture(t)
	app, clock, _ := osApp(t, f)
	clock.Advance(desktop.FocusedRefresh)
	app.SetMinimized(true)
	polls := f.count("poll:" + fleetID)
	reads := f.count("attention")
	clock.Advance(15*time.Second - time.Millisecond)
	if got := f.count("attention") - reads; got != 0 {
		t.Fatalf("%d reads before 15 s", got)
	}
	clock.Advance(time.Millisecond)
	if got := f.count("attention") - reads; got != 1 {
		t.Fatalf("%d reads at 15 s, want 1", got)
	}
	clock.Advance(15 * time.Second)
	if got := f.count("attention") - reads; got != 2 {
		t.Fatalf("%d reads at 30 s, want 2", got)
	}
	if got := f.count("poll:"+fleetID) - polls; got != 0 {
		t.Fatalf("%d status polls while minimized", got)
	}
}

func TestAMinimizedWindowOfOneFleetStillReadsAttention(t *testing.T) {
	f := emptyFixture()
	app, clock, _ := osApp(t, f)
	tickFor(clock, time.Minute)
	if got := f.count("attention"); got != 0 {
		t.Fatalf("%d reads with one visible fleet", got)
	}
	app.SetMinimized(true)
	tickFor(clock, time.Minute)
	if got := f.count("attention"); got != 4 {
		t.Fatalf("%d reads in a minute while minimized, want one per 15 s", got)
	}
	got := f.count("attention")
	app.SetMinimized(false)
	tickFor(clock, time.Minute)
	if f.count("attention") != got {
		t.Fatalf("reads went on after restore with one fleet")
	}
}

func TestAMinimizedWindowShowsTheAttentionReadForTheSelectedFleet(t *testing.T) {
	f := markFixture(t)
	app, clock, s := osApp(t, f)
	clock.Advance(desktop.FocusedRefresh)
	if got := app.Title(); got != "(!) Hand · golden · s1 blocked" {
		t.Fatalf("title = %q", got)
	}
	app.SetMinimized(true)
	if got := app.Title(); got != "(!) Hand · golden · s1 blocked" {
		t.Fatalf("title = %q before the first minimized read", got)
	}
	clock.Advance(15 * time.Second)
	if got, want := app.Title(), "(3) Hand · golden"; got != want {
		t.Fatalf("title = %q, want %q: the list has no ref", got, want)
	}
	if got := s.badge[len(s.badge)-1]; got != 3 {
		t.Fatalf("badge = %d, want 3", got)
	}
	app.SetMinimized(false)
	if got := app.Title(); got != "(3) Hand · golden" {
		t.Fatalf("title = %q before the first poll after restore", got)
	}
	clock.Advance(desktop.FocusedRefresh)
	if got := app.Title(); got != "(!) Hand · golden · s1 blocked" {
		t.Fatalf("title = %q after the first poll after restore", got)
	}
}

func TestARiseWhileMinimizedFlashesAndRestoreAfterwardsDoesNot(t *testing.T) {
	f := quietPair(t)
	app, clock, s := osApp(t, f)
	tickFor(clock, 5*time.Second)
	app.SetMinimized(true)
	tickFor(clock, 15*time.Second)
	if len(s.flash) != 0 {
		t.Fatalf("flash calls %v with nothing new", s.flash)
	}
	setNeeds(f, fleetID, blockedNeed)
	setAttn(f, fleetID, blockedNeed)
	clock.Advance(14 * time.Second)
	if len(s.flash) != 0 {
		t.Fatalf("flash calls %v before the read", s.flash)
	}
	clock.Advance(time.Second)
	if want := []bool{true}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v, want %v", s.flash, want)
	}
	app.SetMinimized(false)
	app.SetFocused(false)
	tickFor(clock, 10*time.Second)
	if want := []bool{true, false}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v after restore, want %v: the poll that follows is not a rise", s.flash, want)
	}
}

func TestFramesMakeNoOSCalls(t *testing.T) {
	f := markFixture(t)
	app, clock, s := osApp(t, f)
	tt := sized(t, app, windowWidth, windowHeight, false)
	clock.Advance(desktop.FocusedRefresh)
	calls := len(s.flash) + len(s.badge) + len(s.titles)
	reads := f.count("attention")
	for range 200 {
		tt.Frame()
	}
	if got := len(s.flash) + len(s.badge) + len(s.titles); got != calls {
		t.Fatalf("200 frames made %d OS calls", got-calls)
	}
	if got := f.count("attention"); got != reads {
		t.Fatalf("200 frames made %d attention reads", got-reads)
	}
}

func TestTheListNeverSpeaksForTheOpenFleetWhileTheWindowIsVisible(t *testing.T) {
	f := markFixture(t)
	_, clock, s := osApp(t, f, func(a *desktop.App) { a.SetFocused(false) })
	tickFor(clock, 60*time.Second)
	if len(s.flash) != 0 {
		t.Fatalf("flash calls %v: the list says 3 for the open fleet whose own status says 1", s.flash)
	}
}

func TestAFleetThatWasUnavailableIsABaselineWhenItReturns(t *testing.T) {
	f := quietPair(t)
	app, clock, s := osApp(t, f)
	tickFor(clock, 10*time.Second)
	app.SetFocused(false)
	f.mu.Lock()
	f.attnGone = map[string]bool{secondID: true}
	f.mu.Unlock()
	tickFor(clock, 10*time.Second)
	setAttn(f, secondID, decisions(2))
	tickFor(clock, 10*time.Second)
	f.mu.Lock()
	f.attnGone = nil
	f.mu.Unlock()
	tickFor(clock, 30*time.Second)
	if len(s.flash) != 0 {
		t.Fatalf("flash calls %v for a fleet that came back with a queue", s.flash)
	}
	setAttn(f, secondID, decisions(3))
	tickFor(clock, 10*time.Second)
	if want := []bool{true}; !slices.Equal(s.flash, want) {
		t.Fatalf("flash calls %v after a real rise, want %v", s.flash, want)
	}
}
