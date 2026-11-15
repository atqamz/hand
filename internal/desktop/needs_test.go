package desktop_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

func when(at string) string {
	parsed, _ := time.Parse(time.RFC3339, at)
	return parsed.Local().Format("Jan 2, 15:04")
}

func TestTheOpenItemsSlugLineCarriesTheAttemptsWords(t *testing.T) {
	const ended = "2026-11-01T04:40:00Z"
	f := needsWith(t, func(st *desktop.Status) {
		st.Projects = 2
		st.Needs.Items = []desktop.Need{
			{Kind: "failure", Ref: "a2", Title: "Failing run", Word: "FAILED", Tone: "fail",
				Task: "t2", Project: "hand", Attempt: "a2", Reason: "the launch found an old worktree",
				AttemptMeta: &desktop.NeedAttempt{Ref: "a2", Harness: "codex", Model: "gpt-6-luna", Status: "failed", EndedAt: ended}},
			{Kind: "report", Ref: "r1", Title: "Cookie fixed", Word: "REPORT", Tone: "neutral",
				Task: "t1", Project: "hand", Report: "r1", Attempt: "a1", Status: "done",
				CreatedAt: ended, Excerpt: "All tests pass."},
		}
	})
	tt := newTester(newApp(t, f))
	mustText(t, tt, "FAILED", "a2", "t2", "codex gpt-6-luna · failed", when(ended), "the launch found an old worktree")
	click(t, tt, "Cookie fixed")
	mustText(t, tt, "REPORT", "r1", "t1", "project hand", "a1 · done", when(ended))
}

func TestASlugLineLeavesNoGapForAFieldTheBoardOmitted(t *testing.T) {
	f := needsWith(t, func(st *desktop.Status) {
		st.Needs.Items = []desktop.Need{{
			Kind: "failure", Ref: "a2", Title: "Failing run", Word: "FAILED", Tone: "fail",
			Task: "t2", Attempt: "a2", Reason: "no model on this attempt",
			AttemptMeta: &desktop.NeedAttempt{Ref: "a2", Harness: "codex", Status: "failed"},
		}}
	})
	tt := newTester(newApp(t, f))
	mustText(t, tt, "codex · failed")
	mustNotText(t, tt, "codex  ·")
	mustNotText(t, tt, "failed ·")
}

func TestTheProjectBadgeFollowsTheBoardsMultiRule(t *testing.T) {
	item := func(kind string) desktop.Need {
		n := desktop.Need{Kind: kind, Ref: "r1", Title: "A thing", Word: "REPORT", Tone: "neutral",
			Task: "t1", Project: "hand", Report: "r1", Attempt: "a1", Status: "done",
			CreatedAt: "2026-11-01T04:40:00Z", Excerpt: "All tests pass."}
		switch kind {
		case "decision":
			n = desktop.Need{Kind: "decision", Ref: "d1", Title: "A question", Word: "DECISION", Tone: "wait",
				Task: "t1", Project: "hand", Decision: "d1", Headline: "A question", Status: "open",
				CreatedAt: "2026-11-01T04:40:00Z"}
		case "failure":
			n = desktop.Need{Kind: "failure", Ref: "a2", Title: "Failing run", Word: "FAILED", Tone: "fail",
				Task: "t2", Project: "hand", Attempt: "a2", Reason: "it failed",
				AttemptMeta: &desktop.NeedAttempt{Ref: "a2", Harness: "codex", Status: "failed"}}
		}
		return n
	}
	for _, c := range []struct {
		name, kind string
		projects   int
		want       bool
	}{
		{"one project", "decision", 1, false},
		{"decision", "decision", 2, true},
		{"report", "report", 2, true},
		{"failure", "failure", 2, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := needsWith(t, func(st *desktop.Status) {
				st.Projects = c.projects
				st.Needs.Items = []desktop.Need{item(c.kind)}
			})
			f.tasks[fleetID] = desktop.Tasks{}
			tt := newTester(newApp(t, f))
			if c.want {
				mustText(t, tt, "project hand")
			} else {
				mustNotText(t, tt, "project hand")
			}
		})
	}
}

func TestAResumeItemShowsTheSupervisorsLineAndEnd(t *testing.T) {
	const ended = "2026-11-01T04:41:00Z"
	f := needsWith(t, func(st *desktop.Status) {
		sup := *st.Supervisor
		sup.Status, sup.Live, sup.Resumable, sup.EndedAt = "interrupted", false, true, ended
		st.Supervisor = &sup
		st.Needs.Items = []desktop.Need{{
			Kind: "resume", Ref: "s1", Title: "The supervisor stopped unexpectedly", Word: "RESUME", Tone: "fail",
		}}
	})
	tt := newTester(newApp(t, f))
	mustText(t, tt, "claude sonnet · interrupted", when(ended), "Resume", "New session")
	next, ok := tt.Find("New session")
	if !ok {
		t.Fatal("no New session button")
	}
	tt.ClickAt(next.X-kit.Space8-40, next.Y+next.H/2)
	if f.count("control:resume") != 1 {
		t.Fatalf("resume ran %d times", f.count("control:resume"))
	}
	tt.Frame()
	mustText(t, tt, "Starting the supervisor…")
}

func TestAFailureShowsItsReasonWithoutReadingTheAttempt(t *testing.T) {
	f := goldenFixture(t)
	f.attemptRefs = nil
	delete(f.attempt, fleetID)
	tt := newTester(newApp(t, f))
	mustText(t, tt, "launch did not finish: the worktree already exists at a path that is not ours")
	mustNotText(t, tt, "Reading a2…")
	mustNotText(t, tt, "not found: attempt a2")
	if got := f.count("attempt:a2"); got != 0 {
		t.Errorf("the window read a2 %d times for its reason", got)
	}
}

func TestTheReceiptToastIsTheBoardsWords(t *testing.T) {
	const receipt = "d1 answered · no supervisor is running; it waits"
	f := goldenFixture(t)
	f.receipt = receipt
	tt := newTester(newApp(t, f))
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	tt.Frame()
	if _, ok := tt.Find(receipt); !ok {
		t.Fatalf("no toast whose title is exactly %q; the window shows %q", receipt, tt.Texts())
	}
	mustNotText(t, tt, "d1 answered · s1 reads it now")
}

func TestAPressedKeysToastIsTheBoardsReceiptInTheWarnTone(t *testing.T) {
	f := blockedScreen(t, true)
	const receipt = "Sent 1 to s1"
	const warn = "s1 is on a model it has not verified; refresh to see it"
	f.receipt, f.warn = receipt, warn
	tt := newTester(newApp(t, f))
	click(t, tt, "1 Yes")
	tt.Frame()
	mustText(t, tt, receipt, warn)
	r, ok := tt.Find(receipt)
	if !ok {
		t.Fatal("no receipt toast")
	}
	img, tone := tt.Image(), kit.ColorsFor(false).Wait
	edged := false
	for y := int(r.Y) - 40; y < int(r.Y) && !edged; y++ {
		run := 0
		for x := int(r.X); x < int(r.X+r.W) && run < 50; x++ {
			if sameInk(img.RGBAAt(x, y), tone) {
				run++
			} else {
				run = 0
			}
		}
		edged = run >= 50
	}
	if !edged {
		t.Errorf("the warn toast has no %v top border above %q", tone, receipt)
	}
}

func TestASupervisorControlSaysTheBoardsReceipt(t *testing.T) {
	f := statusWith(t, func(sup *desktop.Supervisor) {
		sup.Status, sup.Live, sup.Resumable, sup.Blocked, sup.Keys = "running", true, false, false, nil
	})
	f.receipt = "s1 stopping"
	app := newApp(t, f)
	tt := newTester(app)
	stop(t, tt)
	tt.Frame()
	mustText(t, tt, "s1 stopping")
}

func TestAFailureShowsTheAttemptsReason(t *testing.T) {
	const reason = "launch did not finish: the worktree already exists at a path that is not ours"
	mustText(t, newTester(newApp(t, goldenFixture(t))), reason, "View a2")
}

func TestAFailedAttemptReadOffersRetry(t *testing.T) {
	f := goldenFixture(t)
	saved := f.attemptRefs
	f.attemptRefs = nil
	noReason(t, f)
	tt := newTester(newApp(t, f))
	mustText(t, tt, "not found: attempt a2", "Retry")
	f.attemptRefs = saved
	click(t, tt, "Retry")
	mustText(t, tt, "launch did not finish: the worktree already exists at a path that is not ours")
}

func noReason(t *testing.T, f *fixture) {
	t.Helper()
	st := f.status[fleetID]
	st.Needs.Items = append([]desktop.Need(nil), st.Needs.Items...)
	for i := range st.Needs.Items {
		st.Needs.Items[i].Reason = ""
	}
	f.status[fleetID] = st
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
	mustText(t, tt, "r1", "t1", "a1 · done", at.Local().Format("Jan 2, 15:04"))
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
	tt.Advance(4*time.Second - time.Millisecond)
	mustText(t, tt, "r1 marked read")
	tt.Advance(2 * time.Millisecond)
	tt.Frame()
	mustNotText(t, tt, "r1 marked read")
}

func TestAnErrorToastStaysTheBoardsEightSeconds(t *testing.T) {
	f := goldenFixture(t)
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "decision d1 is answered"}
	tt := newTester(newApp(t, f))
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	tt.Frame()
	mustText(t, tt, "The action did not go through")
	tt.Advance(8*time.Second - time.Millisecond)
	mustText(t, tt, "The action did not go through")
	tt.Advance(2 * time.Millisecond)
	tt.Frame()
	mustNotText(t, tt, "The action did not go through")
}

func TestAWarnToastStaysTheBoardsTwelveSeconds(t *testing.T) {
	f := blockedScreen(t, true)
	f.receipt, f.warn = "Sent 1 to s1", "s1 is on a model it has not verified; refresh to see it"
	tt := newTester(newApp(t, f))
	click(t, tt, "1 Yes")
	tt.Frame()
	mustText(t, tt, "Sent 1 to s1")
	tt.Advance(12*time.Second - time.Millisecond)
	mustText(t, tt, "Sent 1 to s1")
	tt.Advance(2 * time.Millisecond)
	tt.Frame()
	mustNotText(t, tt, "Sent 1 to s1")
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
	if !hasTextNode(tt, "17") {
		t.Fatalf("the waiting count is not shown in %q", tt.Texts())
	}
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
	if hasTextNode(tt, "17") {
		t.Errorf("the stale waiting count is still shown in %q", tt.Texts())
	}
	if n := countNode(t, tt, "16"); n != 2 {
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
	noReason(t, f)
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

func TestAFreshTopNeedOpensOverALowerSelection(t *testing.T) {
	f := goldenFixture(t)
	detail := f.attemptRefs["a2"]
	detail.Attempt.Ref = "a9"
	f.attemptRefs["a9"] = detail
	tt := newTester(newApp(t, f))
	click(t, tt, "Cookie fixed")
	mustText(t, tt, "Mark read")
	st := f.status[fleetID]
	st.Needs.Items = append([]desktop.Need{{
		Kind: "failure", Ref: "a9", Title: "Fresh failure", Word: "FAILED", Tone: "fail", Task: "t9", Attempt: "a9",
	}}, st.Needs.Items...)
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	menu(t, tt, "Refresh now")
	mustText(t, tt, "Fresh failure", "View a9")
	mustNotText(t, tt, "Mark read")
}

func TestNoAllClearWhileMoreNeedsWait(t *testing.T) {
	f := needsWith(t, func(st *desktop.Status) {
		st.Needs.Items = []desktop.Need{{
			Kind: "report", Ref: "r1", Title: "Cookie fixed", Word: "REPORT", Tone: "neutral",
			Task: "t1", Report: "r1", Attempt: "a1", Status: "done", CreatedAt: "2026-11-01T04:40:00Z", Excerpt: "All tests pass.",
		}}
	})
	st := f.status[fleetID]
	st.Needs.More = 2
	st.Needs.Waiting = 3
	f.status[fleetID] = st
	f.keepNeeds = true
	app := newApp(t, f)
	tt := newTester(app)
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
	mustNotText(t, tt, "All clear")
	mustText(t, tt, "2 more waiting")
}

const (
	screenDigest = "0123456789abcdef0123456789abcdef"
	screenText   = "Bash command\n  rm -f $r/$f\nClaude Code will automatically deny this request in 1:59\nDo you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel · Tab to amend"
)

func meetNeed(kind string) desktop.Need {
	options := []desktop.AskOption{{Label: "2 No", Keys: []string{"2"}}, {Label: "1 Yes", Keys: []string{"1"}}}
	n := desktop.Need{
		Kind: kind, Ref: "s1", Title: "Bash: rm -f $r/$f · Do you want to proceed?", Word: "BLOCKED", Tone: "fail",
		Excerpt: screenText, DenyAt: "2026-11-09T12:01:30Z", Options: options,
		Keys: &desktop.NeedKeys{Options: options, Revision: 6, Digest: screenDigest, Pressed: 2},
	}
	if kind == "worker" {
		n.Ref, n.Attempt, n.Task = "a1", "a1", "t1"
		n.Keys.Revision, n.Keys.Pressed = 9, 4
	}
	return n
}

func blockedScreen(t *testing.T, ask bool) *fixture {
	t.Helper()
	f := goldenFixture(t)
	st := f.status[fleetID]
	sup := *st.Supervisor
	sup.Status, sup.Live, sup.Resumable, sup.Blocked = "running", true, false, true
	sup.Keys = &desktop.Keys{Revision: 6, Digest: screenDigest, Pressed: 2}
	if ask {
		sup.Ask = &desktop.Ask{Question: "Do you want to proceed?", Options: meetNeed("blocked").Options}
	}
	st.Supervisor = &sup
	st.AgentState, st.Pill, st.Line = "blocked", "failing", "blocked"
	st.Needs.Items = append([]desktop.Need{meetNeed("blocked")}, st.Needs.Items...)
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	return f
}

func workerScreen(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	st := f.status[fleetID]
	n := meetNeed("worker")
	st.Needs.Items = append([]desktop.Need{n}, st.Needs.Items...)
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	return f
}

func TestAPressSendsTheGuardAndAppliesTheReturnedStatus(t *testing.T) {
	f := blockedScreen(t, true)
	fresh := f.status[fleetID]
	sup := *fresh.Supervisor
	sup.Keys = &desktop.Keys{Revision: 7, Digest: screenDigest, Pressed: 3}
	fresh.Supervisor = &sup
	f.keyStatus = &fresh
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "1 Yes")
	if f.count("keys:1") != 1 {
		t.Fatalf("key presses = %d, want 1", f.count("keys:1"))
	}
	f.mu.Lock()
	keys, revision, digest, after, ref := append([]string(nil), f.keys...), f.keyRevision, f.keyDigest, f.keyAfter, f.keyRef
	f.mu.Unlock()
	if !slices.Equal(keys, []string{"1"}) || revision != 6 || digest != screenDigest || after != 2 || ref != "s1" {
		t.Fatalf("pressed %q at revision %d digest %q after %d for %q", keys, revision, digest, after, ref)
	}
	if app.Status.Supervisor == nil || app.Status.Supervisor.Keys == nil || app.Status.Supervisor.Keys.Revision != 7 {
		t.Fatalf("the returned status was not applied: %+v", app.Status.Supervisor)
	}
	tt.Frame()
	mustText(t, tt, "Sent 1 · waiting…", "Sent 1 to s1")
}

func TestAStalePressReopensOnTheFreshScreen(t *testing.T) {
	f := blockedScreen(t, true)
	app := newApp(t, f)
	tt := newTester(app)
	mustText(t, tt, screenText)
	fresh := f.status[fleetID]
	fresh.Needs.Items = append([]desktop.Need(nil), fresh.Needs.Items...)
	fresh.Needs.Items[0].Excerpt = "fresh screen after the move"
	sup := *fresh.Supervisor
	sup.Keys = &desktop.Keys{Revision: 8, Digest: screenDigest, Pressed: 5}
	fresh.Supervisor = &sup
	f.status[fleetID] = fresh
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "the screen changed since revision 6; nothing was sent"}
	click(t, tt, "1 Yes")
	tt.Frame()
	mustText(t, tt, "the screen moved; nothing was sent", "fresh screen after the move")
	if app.Status.Supervisor == nil || app.Status.Supervisor.Keys.Revision != 8 {
		t.Fatalf("the fresh status was not read: %+v", app.Status.Supervisor)
	}
}

func TestTheMisreadDisclosureTypesQueuedMessagesAnyway(t *testing.T) {
	f := blockedScreen(t, true)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Screen misread?")
	mustText(t, tt, "Types the queued messages, or else the wakes, into its prompt.")
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	click(t, tt, "Type queued messages anyway")
	if f.count("control:force") != 1 {
		t.Fatalf("force calls = %d, want 1", f.count("control:force"))
	}
	f.mu.Lock()
	ref := f.controlRef
	f.mu.Unlock()
	if ref != "s1" {
		t.Fatalf("force named %q, want s1", ref)
	}
}

func TestTheCountdownTicksLocallyAndThenRefreshes(t *testing.T) {
	f := blockedScreen(t, true)
	clock := newFakeClock()
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = t.TempDir()
	app.Clock = clock
	app.Now = func() time.Time {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		return clock.now
	}
	app.Boot()
	tt := newTester(app)
	tt.Frame()
	mustText(t, tt, "1:30")
	reads := f.count("status:" + fleetID)
	clock.Advance(30 * time.Second)
	tt.Frame()
	mustText(t, tt, "1:00")
	if got := f.count("status:" + fleetID); got != reads {
		t.Fatalf("the countdown read the board %d times, want %d", got, reads)
	}
	clock.Advance(60 * time.Second)
	tt.Frame()
	mustText(t, tt, "denying…", "s1 denied · refreshing")
	if got := f.count("status:" + fleetID); got <= reads {
		t.Fatalf("the denied screen did not refresh: %d reads, want more than %d", got, reads)
	}
}

func TestAWorkerPressSendsItsOwnGuard(t *testing.T) {
	f := workerScreen(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "2 No")
	f.mu.Lock()
	key, ref, revision, after, screen := f.attemptKey, f.attemptKeyRef, f.attemptKeyRev, f.attemptKeyAft, f.attemptScreen
	f.mu.Unlock()
	if key != "2" || ref != "a1" || revision != 9 || after != 4 || screen != screenDigest {
		t.Fatalf("worker key %q for %q at revision %d after %d screen %q", key, ref, revision, after, screen)
	}
	tt.Frame()
	mustText(t, tt, "Sent 2 · waiting…", "Sent 2 to a1")
}

func TestAWorkerPressWithNoDetailShowsTheWarn(t *testing.T) {
	f := workerScreen(t)
	app := newApp(t, f)
	tt := newTester(app)
	warn := "the press went through, but the attempt could not be read: the store went away; refresh to see it"
	f.accepted, f.warn = true, warn
	click(t, tt, "2 No")
	tt.Frame()
	if app.Busy {
		t.Fatal("the window is still sending")
	}
	mustText(t, tt, "Sent 2 to a1", warn)
}

func TestAStaleWorkerPressReopensOnTheFreshScreen(t *testing.T) {
	f := workerScreen(t)
	app := newApp(t, f)
	tt := newTester(app)
	fresh := f.status[fleetID]
	fresh.Needs.Items = append([]desktop.Need(nil), fresh.Needs.Items...)
	fresh.Needs.Items[0].Excerpt = "worker screen after the move"
	fresh.Needs.Items[0].Keys = &desktop.NeedKeys{Options: fresh.Needs.Items[0].Keys.Options, Revision: 10, Digest: screenDigest, Pressed: 4}
	f.status[fleetID] = fresh
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "the screen changed since revision 9; nothing was sent"}
	click(t, tt, "1 Yes")
	tt.Frame()
	mustText(t, tt, "the screen moved; nothing was sent", "worker screen after the move")
}

func TestTheBlockedCardSwitchesBetweenAskOptionsAndTheFixedKeys(t *testing.T) {
	ask := newTester(newApp(t, blockedScreen(t, true)))
	if n := countText(t, ask, "1 Yes"); n != 2 {
		t.Errorf("ask options shown %d times, want the card and the conversation", n)
	}
	mustNotText(t, ask, "Keys")
	fixed := newTester(newApp(t, blockedScreen(t, false)))
	enters := 0
	for _, text := range fixed.Texts() {
		if text == "Enter" {
			enters++
		}
	}
	if enters != 2 {
		t.Errorf("fixed keys shown %d times, want the card and the conversation", enters)
	}
	mustNotText(t, fixed, "1 Yes")
}

func TestAScreensExcerptIsPlainText(t *testing.T) {
	f := blockedScreen(t, true)
	st := f.status[fleetID]
	st.Needs.Items[0].Excerpt = "**not bold**\n- not a list"
	f.status[fleetID] = st
	tt := newTester(newApp(t, f))
	mustText(t, tt, "**not bold**\n- not a list")
}

func TestAClippedExcerptMarksTheClip(t *testing.T) {
	f := blockedScreen(t, true)
	st := f.status[fleetID]
	st.Needs.Items[0].Cut = true
	f.status[fleetID] = st
	tt := newTester(newApp(t, f))
	mustText(t, tt, screenText+"\n…")
}

func TestScenesBlockedStates(t *testing.T) {
	for _, dark := range []bool{false, true} {
		mode := map[bool]string{false: "light", true: "dark"}[dark]
		open := func(t *testing.T, f *fixture, now time.Time) (*desktop.App, *ui.Tester) {
			app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
			app.DraftDir = t.TempDir()
			app.Now = func() time.Time { return now }
			app.Boot()
			return app, darkTester(app, dark)
		}
		now, _ := time.Parse(time.RFC3339, "2026-11-09T12:00:00Z")
		_, ask := open(t, blockedScreen(t, true), now)
		shot(t, ask, "needs-blocked-ask-"+mode)

		_, fixed := open(t, blockedScreen(t, false), now)
		shot(t, fixed, "needs-blocked-fixed-"+mode)

		clipped := blockedScreen(t, true)
		st := clipped.status[fleetID]
		st.Needs.Items[0].Cut = true
		clipped.status[fleetID] = st
		_, clip := open(t, clipped, now)
		shot(t, clip, "needs-blocked-clipped-"+mode)

		late, _ := time.Parse(time.RFC3339, "2026-11-09T12:01:00Z")
		_, count := open(t, blockedScreen(t, true), late)
		shot(t, count, "needs-blocked-countdown-"+mode)

		stale := blockedScreen(t, true)
		fresh := stale.status[fleetID]
		fresh.Needs.Items = append([]desktop.Need(nil), fresh.Needs.Items...)
		fresh.Needs.Items[0].Excerpt = "Bash command\n  git push --force\nDo you want to proceed?"
		sup := *fresh.Supervisor
		sup.Keys = &desktop.Keys{Revision: 8, Digest: screenDigest, Pressed: 5}
		fresh.Supervisor = &sup
		stale.status[fleetID] = fresh
		stale.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "the screen changed since revision 6; nothing was sent"}
		_, refused := open(t, stale, now)
		click(t, refused, "1 Yes")
		refused.Advance(300 * time.Millisecond)
		refused.Frame()
		shot(t, refused, "needs-blocked-stale-"+mode)

		_, worker := open(t, workerScreen(t), now)
		shot(t, worker, "needs-blocked-worker-"+mode)
	}
}

type pollUnchanged struct{ desktop.ReadModel }

func (pollUnchanged) Poll(context.Context, string, desktop.Screen, string, desktop.PollCursors) (desktop.Poll, error) {
	return desktop.Poll{StatusRead: true, ScreenRead: true, TimelineRead: true}, nil
}

func TestTheCountdownArmsWhenTheWindowInstallsPost(t *testing.T) {
	f := blockedScreen(t, true)
	clock := newFakeClock()
	app := desktop.New(func() (desktop.ReadModel, error) { return pollUnchanged{f}, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	reads := f.count("status:" + fleetID)
	app.Clock = clock
	app.Now = func() time.Time {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		return clock.now
	}
	app.Post = func(fn func()) { fn() }
	app.StartRefresh()
	app.Post = nil
	tt := newTester(app)
	tt.Frame()
	clock.Advance(2 * time.Second)
	tt.Frame()
	if got := f.count("status:" + fleetID); got != reads {
		t.Fatalf("the first poll read the status again: %d reads, want %d", got, reads)
	}
	clock.Advance(2 * time.Minute)
	tt.Frame()
	mustText(t, tt, "s1 denied · refreshing")
	if got := f.count("status:" + fleetID); got != reads+1 {
		t.Fatalf("the denied screen re-read the status %d times, want %d", got, reads+1)
	}
	clock.Advance(10 * time.Second)
	tt.Frame()
	if got := f.count("status:" + fleetID); got != reads+1 {
		t.Fatalf("the stale status re-read again: %d reads, want %d", got, reads+1)
	}
}

func TestARefusedSecondPressClearsTheSentRow(t *testing.T) {
	f := blockedScreen(t, true)
	sent := f.status[fleetID]
	sup := *sent.Supervisor
	sup.Keys = &desktop.Keys{Revision: 7, Digest: screenDigest, Pressed: 3}
	sent.Supervisor = &sup
	f.keyStatus = &sent
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "1 Yes")
	tt.Frame()
	mustText(t, tt, "Sent 1 · waiting…")

	fresh := f.status[fleetID]
	fresh.Needs.Items = append([]desktop.Need(nil), fresh.Needs.Items...)
	fresh.Needs.Items[0].Excerpt = "screen after the second press"
	next := *fresh.Supervisor
	next.Keys = &desktop.Keys{Revision: 8, Digest: screenDigest, Pressed: 5}
	fresh.Supervisor = &next
	f.status[fleetID] = fresh
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "the screen changed since revision 7; nothing was sent"}
	click(t, tt, "1 Yes")
	tt.Frame()
	mustText(t, tt, "the screen moved; nothing was sent", "screen after the second press")
	mustNotText(t, tt, "Sent 1 · waiting…")
}

func TestScenesTheOpenSlugAndTheReceipts(t *testing.T) {
	const ended = "2026-11-09T12:00:00Z"
	report := func(t *testing.T) *fixture {
		t.Helper()
		return needsWith(t, func(st *desktop.Status) {
			st.Projects = 2
			st.Needs.Items = []desktop.Need{{
				Kind: "report", Ref: "r1", Title: "Cookie fixed", Word: "REPORT", Tone: "neutral",
				Task: "t1", Project: "hand", Report: "r1", Attempt: "a1", Status: "done",
				CreatedAt: ended, Excerpt: "PR: https://github.com/example/hand/pull/42\n\nAll tests pass.",
			}}
		})
	}
	resume := func(t *testing.T) *fixture {
		t.Helper()
		return needsWith(t, func(st *desktop.Status) {
			sup := *st.Supervisor
			sup.Status, sup.Live, sup.Resumable, sup.EndedAt = "interrupted", false, true, ended
			st.Supervisor = &sup
			st.AgentState, st.Pill, st.Line = "idle", "idle", "interrupted"
			st.Needs.Items = []desktop.Need{{
				Kind: "resume", Ref: "s1", Title: "The supervisor stopped unexpectedly", Word: "RESUME", Tone: "fail",
			}}
		})
	}
	warn := func(t *testing.T) *fixture {
		t.Helper()
		f := goldenFixture(t)
		f.warn = "the answer was saved, but its decision could not be read: the store went away; refresh to see it"
		f.accepted = true
		return f
	}
	for _, dark := range []bool{false, true} {
		mode := map[bool]string{false: "light", true: "dark"}[dark]
		for _, size := range [][2]int{{1100, 760}, {1440, 900}} {
			size := size
			scene := func(name string) string {
				return fmt.Sprintf("needs-%s-%dx%d-%s", name, size[0], size[1], mode)
			}
			tt := sized(t, newApp(t, report(t)), size[0], size[1], dark)
			mustText(t, tt, "project hand")
			shotSized(t, tt, scene("slug-report"), size[0], size[1])

			failure := sized(t, newApp(t, goldenFixture(t)), size[0], size[1], dark)
			mustText(t, failure, "launch did not finish: the worktree already exists at a path that is not ours")
			shotSized(t, failure, scene("slug-failure"), size[0], size[1])

			resumed := sized(t, newApp(t, resume(t)), size[0], size[1], dark)
			mustText(t, resumed, "claude sonnet · interrupted")
			shotSized(t, resumed, scene("slug-resume"), size[0], size[1])

			warned := sized(t, newApp(t, warn(t)), size[0], size[1], dark)
			click(t, warned, "Keep the old cookie name?")
			click(t, warned, "Yes, keep it")
			click(t, warned, "Send answer")
			warned.Frame()
			mustText(t, warned, "d1 answered · s1 reads it now")
			shotSized(t, warned, scene("slug-warn"), size[0], size[1])
		}
	}
}
