package desktop_test

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

func needsWith(t *testing.T, change func(*desktop.Status)) *fixture {
	t.Helper()
	f := goldenFixture(t)
	st := f.status[fleetID]
	change(&st)
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	return f
}

func darkTester(app *desktop.App, dark bool) *ui.Tester {
	tt := newTester(app)
	tt.SetDark(dark)
	tt.Frame()
	return tt
}

func yOf(t *testing.T, tt *ui.Tester, text string) float32 {
	t.Helper()
	r, ok := tt.Find(text)
	if !ok {
		t.Fatalf("no %q in %q", text, tt.Texts())
	}
	return r.Y
}

func TestScenesNeeds(t *testing.T) {
	const reason = "launch did not finish: the worktree already exists at a path that is not ours"
	cleared := func(st *desktop.Status) {
		st.Needs = desktop.Needs{Cleared: "2026-11-01T04:40:00Z", Summary: "3 reports read, 1 decision answered"}
	}
	scenes := []struct {
		name   string
		change func(*desktop.Status)
		play   func(*testing.T, *ui.Tester)
		want   []string
	}{
		{"bands-report-open", func(*desktop.Status) {}, func(t *testing.T, tt *ui.Tester) { click(t, tt, "Cookie fixed") },
			[]string{"Failed", "Decision", "Report", "Mark read", "Open r1", "All tests pass."}},
		{"decision-open", func(*desktop.Status) {}, func(t *testing.T, tt *ui.Tester) { click(t, tt, "Keep the old cookie name?") },
			[]string{"Yes, keep it", "No, rename it", "Send answer", "Open d1"}},
		{"failure-open", func(*desktop.Status) {}, nil, []string{"Failed", reason, "View a2"}},
		{"all-clear", cleared, nil, []string{"All clear", "since", "3 reports read, 1 decision answered"}},
	}
	for _, sc := range scenes {
		for _, dark := range []bool{false, true} {
			mode := map[bool]string{false: "light", true: "dark"}[dark]
			t.Run(sc.name+"-"+mode, func(t *testing.T) {
				tt := darkTester(newApp(t, needsWith(t, sc.change)), dark)
				if sc.play != nil {
					sc.play(t, tt)
				}
				mustText(t, tt, sc.want...)
				shot(t, tt, "needs-"+sc.name+"-"+mode)
			})
		}
	}
}

func TestTheOpenNeedHasTheTonedHeaderOfItsBand(t *testing.T) {
	for _, dark := range []bool{false, true} {
		k := kit.ColorsFor(dark)
		for _, c := range []struct {
			title string
			want  ui.Color
		}{
			{"Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump", k.FailBG},
			{"Keep the old cookie name?", k.WaitBG},
			{"Cookie fixed", k.NeutralBG},
		} {
			tt := darkTester(newApp(t, goldenFixture(t)), dark)
			if c.title != "Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump" {
				click(t, tt, c.title)
			}
			r, ok := tt.Find(c.title)
			if !ok {
				t.Fatalf("no %q", c.title)
			}
			got := tt.Image().RGBAAt(int(r.X)+2, int(r.Y+r.H/2))
			if got.R != c.want.R || got.G != c.want.G || got.B != c.want.B {
				t.Errorf("dark=%v %q: header fill %v, want %v", dark, c.title, got, c.want)
			}
		}
	}
}

func TestBandsRunInTheBoardsOrderWithOneHeadEach(t *testing.T) {
	f := needsWith(t, func(st *desktop.Status) {
		st.Needs.Items = []desktop.Need{
			{Kind: "failure", Ref: "a2", Title: "Failing run", Word: "FAILED", Tone: "fail", Task: "t2", Attempt: "a2"},
			{Kind: "decision", Ref: "d1", Title: "First decision", Word: "DECISION", Tone: "wait", Task: "t1", Decision: "d1", CreatedAt: "2026-11-01T04:40:00Z"},
			{Kind: "decision", Ref: "d2", Title: "Second decision", Word: "DECISION", Tone: "wait", Task: "t1", Decision: "d2", CreatedAt: "2026-11-01T05:40:00Z"},
			{Kind: "report", Ref: "r1", Title: "First report", Word: "REPORT", Tone: "neutral", Task: "t1", Report: "r1", CreatedAt: "2026-11-01T04:40:00Z"},
			{Kind: "report", Ref: "r2", Title: "Second report", Word: "REPORT", Tone: "neutral", Task: "t3", Report: "r2", CreatedAt: "2026-11-01T05:40:00Z"},
		}
	})
	tt := newTester(newApp(t, f))
	order := []string{"Failed", "Failing run", "Decision", "First decision", "Second decision", "Report", "First report", "Second report"}
	last := float32(-1)
	for _, text := range order {
		y := yOf(t, tt, text)
		if y <= last {
			t.Errorf("%q is at y=%v, not below the line before it (%v)", text, y, last)
		}
		last = y
	}
	for _, band := range []string{"Failed", "Decision", "Report"} {
		if n := countText(t, tt, band); n != 1 {
			t.Errorf("band %q is headed %d times, want once", band, n)
		}
	}
	mustNotText(t, tt, "DECISION")
}

func TestAClosedNeedRowIs44High(t *testing.T) {
	f := needsWith(t, func(st *desktop.Status) {
		st.Needs.Items = []desktop.Need{
			{Kind: "failure", Ref: "a2", Title: "Failing run", Word: "FAILED", Tone: "fail", Task: "t2", Attempt: "a2"},
			{Kind: "decision", Ref: "d1", Title: "First decision", Word: "DECISION", Tone: "wait", Decision: "d1"},
			{Kind: "decision", Ref: "d2", Title: "Second decision", Word: "DECISION", Tone: "wait", Decision: "d2"},
		}
	})
	tt := newTester(newApp(t, f))
	row, ok := tt.Find("Second decision")
	if !ok || row.H != kit.Tap {
		t.Errorf("a closed row is %v high, want %d", row.H, kit.Tap)
	}
}

func TestOneNeedIsOpenAtATime(t *testing.T) {
	tt := newTester(newApp(t, goldenFixture(t)))
	mustText(t, tt, "View a2")
	mustNotText(t, tt, "Send answer")
	click(t, tt, "Keep the old cookie name?")
	mustText(t, tt, "Send answer")
	mustNotText(t, tt, "View a2")
	click(t, tt, "Cookie fixed")
	mustText(t, tt, "Mark read")
	mustNotText(t, tt, "Send answer")
	if n := countText(t, tt, "Mark read"); n != 1 {
		t.Errorf("%d Mark read buttons, want one", n)
	}
}

func TestTheTopNeedStartsOpenAndClosesAgain(t *testing.T) {
	tt := newTester(newApp(t, goldenFixture(t)))
	title := "Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump"
	mustText(t, tt, "View a2")
	click(t, tt, title)
	mustNotText(t, tt, "View a2")
	mustNotText(t, tt, "Send answer")
	click(t, tt, title)
	mustText(t, tt, "View a2")
}

func TestAFailureShowsTheAttemptsReason(t *testing.T) {
	const reason = "launch did not finish: the worktree already exists at a path that is not ours"
	mustText(t, newTester(newApp(t, goldenFixture(t))), reason, "View a2")
}

func TestAFailedAttemptReadOffersRetry(t *testing.T) {
	f := goldenFixture(t)
	refs := f.attemptRefs
	f.attemptRefs = nil
	tt := newTester(newApp(t, f))
	mustText(t, tt, "not found: attempt a2", "Retry")
	f.attemptRefs = refs
	click(t, tt, "Retry")
	mustText(t, tt, "launch did not finish: the worktree already exists at a path that is not ours")
}

func TestAFailedDecisionReadOffersRetry(t *testing.T) {
	f := goldenFixture(t)
	tt := newTester(newApp(t, f))
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	click(t, tt, "Keep the old cookie name?")
	mustText(t, tt, "the board is restarting", "Retry")
	f.fail = nil
	click(t, tt, "Retry")
	mustText(t, tt, "Yes, keep it", "Send answer")
}

func TestAFreshTopNeedOpensAfterTheOldOneCloses(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	title := "Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump"
	click(t, tt, title)
	mustNotText(t, tt, "View a2")
	st := f.status[fleetID]
	st.Needs.Items = append([]desktop.Need{{
		Kind: "report", Ref: "r9", Title: "Fresh report", Word: "REPORT", Tone: "neutral",
		Task: "t1", Report: "r9", Attempt: "a1", Status: "done",
		CreatedAt: "2026-11-01T06:00:00Z", Excerpt: "New.",
	}}, st.Needs.Items...)
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	menu(t, tt, "Refresh now")
	mustText(t, tt, "Fresh report", "Mark read")
}

func TestAKindWithNothingToShowStaysInert(t *testing.T) {
	const title = "Supervisor idle for 1h with 2 active tasks"
	f := needsWith(t, func(st *desktop.Status) {
		st.Needs.Items = []desktop.Need{
			{Kind: "stalled", Ref: "s1", Title: title, Word: "STALLED", Tone: "wait"},
			{Kind: "report", Ref: "r1", Title: "Cookie fixed", Word: "REPORT", Tone: "neutral", Task: "t1", Report: "r1", Attempt: "a1", Status: "done", Excerpt: "All tests pass."},
		}
	})
	tt := newTester(newApp(t, f))
	row, ok := tt.Find(title)
	if !ok || row.H != kit.Tap {
		t.Fatalf("a closed inert row is %v high, want %d", row.H, kit.Tap)
	}
	click(t, tt, title)
	if row, _ = tt.Find(title); row.H != kit.Tap {
		t.Errorf("an inert row opened to %v high, want %d", row.H, kit.Tap)
	}
	mustNotText(t, tt, "Mark read")
	mustNotText(t, tt, "View a1")
}

func TestAReportOpenCardShowsItsAttemptAndStatus(t *testing.T) {
	tt := newTester(newApp(t, goldenFixture(t)))
	click(t, tt, "Cookie fixed")
	at, _ := time.Parse(time.RFC3339, "2026-11-01T04:40:00Z")
	mustText(t, tt, "r1 t1 a1 · done", at.Local().Format("Jan 2, 15:04"))
}

func TestACutExcerptReadsMore(t *testing.T) {
	f := needsWith(t, func(st *desktop.Status) { st.Needs.Items[2].Cut = true })
	tt := newTester(newApp(t, f))
	click(t, tt, "Cookie fixed")
	mustText(t, tt, "read more")
	mustNotText(t, tt, "Open r1")
}

func TestAReportOpensToItsExcerptAndMarksRead(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Cookie fixed")
	mustText(t, tt, "All tests pass.", "Mark read", "Open r1")
	click(t, tt, "Mark read")
	if f.count("ack:r1") != 1 {
		t.Fatalf("ack calls = %d, want 1", f.count("ack:r1"))
	}
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("Mark read left the Board screen for %v", app.Screen)
	}
}

func TestMarkReadOpensTheNextNeed(t *testing.T) {
	f := goldenFixture(t)
	f.keepNeeds = true
	tt := newTester(newApp(t, f))
	click(t, tt, "Cookie fixed")
	mustNotText(t, tt, "View a2")
	click(t, tt, "Mark read")
	mustText(t, tt, "View a2")
	mustNotText(t, tt, "Mark read")
}

func TestAnAnswerOpensTheNextNeed(t *testing.T) {
	f := goldenFixture(t)
	f.keepNeeds = true
	tt := newTester(newApp(t, f))
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	mustText(t, tt, "View a2")
	mustNotText(t, tt, "Send answer")
}

func TestTheReceiptToastAppearsAndGoes(t *testing.T) {
	tt := newTester(newApp(t, goldenFixture(t)))
	mustNotText(t, tt, "r1 marked read")
	click(t, tt, "Cookie fixed")
	click(t, tt, "Mark read")
	tt.Frame()
	mustText(t, tt, "r1 marked read")
	tt.Advance(3 * time.Second)
	mustText(t, tt, "r1 marked read")
	tt.Advance(2 * time.Second)
	tt.Frame()
	mustNotText(t, tt, "r1 marked read")
}

func TestAnAnswersReceiptNamesTheSupervisorOrSaysItWaits(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(*desktop.Supervisor)
		want   string
	}{
		{"running", func(*desktop.Supervisor) {}, "d1 answered · s1 reads it now"},
		{"stopped", func(s *desktop.Supervisor) { s.Status, s.Live = "stopped", false }, "d1 answered · no supervisor is running; it waits"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tt := newTester(newApp(t, statusWith(t, c.change)))
			click(t, tt, "Keep the old cookie name?")
			click(t, tt, "Yes, keep it")
			click(t, tt, "Send answer")
			tt.Frame()
			mustText(t, tt, c.want)
		})
	}
}

func TestAFailedMarkReadShowsNoReceipt(t *testing.T) {
	f := goldenFixture(t)
	tt := newTester(newApp(t, f))
	click(t, tt, "Cookie fixed")
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "report r1 is already acknowledged"}
	click(t, tt, "Mark read")
	tt.Frame()
	mustNotText(t, tt, "r1 marked read")
	mustText(t, tt, "Mark read", "All tests pass.")
}

func TestABlockedNeedOpensToTheSupervisorsKeys(t *testing.T) {
	f := blockedFixture(t)
	st := f.status[fleetID]
	st.Needs.Items = append([]desktop.Need{{Kind: "blocked", Ref: "s1", Title: "Bash: rm -f x · Proceed?", Word: "BLOCKED", Tone: "fail"}}, st.Needs.Items...)
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	app := newApp(t, f)
	tt := newTester(app)
	mustText(t, tt, "Blocked", "Keys")
	if n := countText(t, tt, "Esc"); n != 2 {
		t.Fatalf("Esc shown %d times, want in the card and beside the conversation", n)
	}
	click(t, tt, "Bash: rm -f x · Proceed?")
	if n := countText(t, tt, "Esc"); n != 1 {
		t.Fatalf("Esc shown %d times after the card closed, want once", n)
	}
}

func TestAllClearShowsWhenItClearedAndTheSummary(t *testing.T) {
	f := needsWith(t, func(st *desktop.Status) {
		st.Needs = desktop.Needs{Cleared: "2026-11-01T04:40:00Z", Summary: "3 reports read"}
	})
	tt := newTester(newApp(t, f))
	at, _ := time.Parse(time.RFC3339, "2026-11-01T04:40:00Z")
	mustText(t, tt, "All clear", "since "+at.Local().Format("Jan 2, 15:04")+" · 3 reports read")
	mustNotText(t, tt, "Nothing waits on you.")
}

func TestAnAnswerDraftBelongsToItsDecision(t *testing.T) {
	f := needsWith(t, func(st *desktop.Status) {
		st.Needs.Items = []desktop.Need{
			{Kind: "decision", Ref: "d1", Title: "Keep the old cookie name?", Word: "DECISION", Tone: "wait", Task: "t1", Decision: "d1", CreatedAt: "2026-11-01T04:40:00Z"},
			{Kind: "decision", Ref: "d2", Title: "Rename the cookie?", Word: "DECISION", Tone: "wait", Task: "t1", Decision: "d2", CreatedAt: "2026-11-01T05:40:00Z"},
		}
	})
	second := f.decision[fleetID]
	second.Decision.Ref, second.Decision.Headline = "d2", "Rename the cookie?"
	second.Decision.Question = "Rename the cookie?\n\n1. Yes, keep it\n2. No, rename it"
	f.extra = map[string]desktop.DecisionDetail{"d2": second}
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Yes, keep it")
	if app.AnswerChoice == "" {
		t.Fatal("the option was not selected")
	}

	st := f.status[fleetID]
	st.Needs.Items = st.Needs.Items[1:]
	st.Needs.Waiting = 1
	f.status[fleetID] = st
	menu(t, tt, "Refresh now")
	if app.AnswerChoice != "" {
		t.Errorf("the next decision opens with %q still selected", app.AnswerChoice)
	}
	mustText(t, tt, "Rename the cookie?", "Send answer")
}

func TestAFailedStatusAfterMarkReadKeepsTheReportRead(t *testing.T) {
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.Needs.Waiting = 17
	f.status[fleetID] = st
	app := newApp(t, f)
	tt := newTester(app)
	mustText(t, tt, "17")
	click(t, tt, "Cookie fixed")
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "Mark read")
	select {
	case <-f.actionEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the acknowledgement never started")
	}
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	close(f.actionBlock)
	waitPost(t, posted)()
	waitPost(t, posted)()
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "r1 marked read", "View a2", "16")
	mustNotText(t, tt, "Mark read")
	mustNotText(t, tt, "17")
	if n := countText(t, tt, "16"); n != 2 {
		t.Errorf("the waiting count is shown %d times, want once in Needs and once in the rail", n)
	}
}

func borderRows(tt *ui.Tester, r ui.Rect, k kit.Colors) int {
	img := tt.Image()
	x := int(r.X) + 20
	n := 0
	for y := int(r.Y) - 14; y <= int(r.Y)+2; y++ {
		if c := img.RGBAAt(x, y); c.R == k.Line.R && c.G == k.Line.G && c.B == k.Line.B {
			n++
		}
	}
	return n
}

func TestTheRowAfterTheOpenNeedKeepsItsTopBorder(t *testing.T) {
	for _, dark := range []bool{false, true} {
		k := kit.ColorsFor(dark)
		tt := darkTester(newApp(t, goldenFixture(t)), dark)
		click(t, tt, "Cookie fixed")
		r, ok := tt.Find("Halfway there")
		if !ok {
			t.Fatal("no row below the open need")
		}
		if n := borderRows(tt, r, k); n < 2 {
			t.Errorf("dark=%v: %d border rows above the row after the open need, want the card's and the row's", dark, n)
		}
		click(t, tt, "Cookie fixed")
		r, _ = tt.Find("Halfway there")
		if n := borderRows(tt, r, k); n > 1 {
			t.Errorf("dark=%v: %d border rows above the row with no need open, want only the row before it", dark, n)
		}
	}
}

func TestClosingANeedDoesNotReopenTheTopItem(t *testing.T) {
	tt := newTester(newApp(t, goldenFixture(t)))
	mustText(t, tt, "View a2")
	click(t, tt, "Cookie fixed")
	mustText(t, tt, "Mark read")
	click(t, tt, "Cookie fixed")
	mustNotText(t, tt, "View a2")
	mustNotText(t, tt, "Mark read")
	mustNotText(t, tt, "Send answer")
}

func TestACompletedNeedDoesNotReopenAsTheTopItem(t *testing.T) {
	f := needsWith(t, func(st *desktop.Status) {
		st.Needs.Items = []desktop.Need{{
			Kind: "report", Ref: "r1", Title: "Cookie fixed", Word: "REPORT", Tone: "neutral",
			Task: "t1", Report: "r1", Attempt: "a1", Status: "done", CreatedAt: "2026-11-01T04:40:00Z", Excerpt: "All tests pass.",
		}}
	})
	f.keepNeeds = true
	tt := newTester(newApp(t, f))
	click(t, tt, "Mark read")
	mustNotText(t, tt, "Mark read")
	mustText(t, tt, "All clear")
}

func answerReads(t *testing.T, first chan error, ch chan chan error, posted <-chan func(), tt *ui.Tester, done func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !done() {
		select {
		case c := <-ch:
			c <- nil
			waitPost(t, posted)()
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("the need never settled on a read")
		}
		tt.Frame()
	}
	first <- errors.New("the earlier read failed")
	waitPost(t, posted)()
	for {
		select {
		case c := <-ch:
			c <- errors.New("the earlier read failed")
			waitPost(t, posted)()
		case <-time.After(100 * time.Millisecond):
			tt.Frame()
			return
		}
	}
}

func TestAnEarlierAttemptReadCannotReplaceTheNewerOne(t *testing.T) {
	const (
		title  = "Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump"
		reason = "launch did not finish: the worktree already exists at a path that is not ours"
	)
	f := goldenFixture(t)
	f.attemptIn = make(chan chan error, 8)
	app := newApp(t, f)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	tt.Frame()
	first := <-f.attemptIn
	click(t, tt, title)
	click(t, tt, title)
	answerReads(t, first, f.attemptIn, posted, tt, func() bool { return tt.HasText(reason) })
	mustNotText(t, tt, "the earlier read failed")
	mustText(t, tt, reason)
}

func TestAnEarlierDecisionReadCannotReplaceTheNewerOne(t *testing.T) {
	f := goldenFixture(t)
	f.decisionIn = make(chan chan error, 8)
	app := newApp(t, f)
	tt := newTester(app)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	const title = "Keep the old cookie name?"
	click(t, tt, title)
	first := <-f.decisionIn
	click(t, tt, title)
	click(t, tt, title)
	answerReads(t, first, f.decisionIn, posted, tt, func() bool { return tt.HasText("Send answer") })
	mustNotText(t, tt, "the earlier read failed")
	mustText(t, tt, "Yes, keep it", "Send answer")
}

func TestAFailedDecisionRereadShowsTheErrorNotTheCachedForm(t *testing.T) {
	f := goldenFixture(t)
	tt := newTester(newApp(t, f))
	const title = "Keep the old cookie name?"
	click(t, tt, title)
	mustText(t, tt, "Yes, keep it", "Send answer")
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	click(t, tt, title)
	click(t, tt, title)
	mustText(t, tt, "the board is restarting", "Retry")
	mustNotText(t, tt, "Yes, keep it")
	mustNotText(t, tt, "Send answer")
}

func TestTwoActionReceiptsStayForTheirFullInterval(t *testing.T) {
	f := goldenFixture(t)
	f.keepNeeds = true
	tt := newTester(newApp(t, f))
	click(t, tt, "Cookie fixed")
	click(t, tt, "Mark read")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	tt.Frame()
	mustText(t, tt, "r1 marked read", "d1 answered · s1 reads it now")
}
