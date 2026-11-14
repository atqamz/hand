package desktop_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func withAttemptTranscript(f *fixture) *fixture {
	ad := f.attempt[fleetID]
	ad.ModelNote = "the transcript is the worker's own record"
	ad.Attempt.EndedAt = "2026-11-01T05:00:00Z"
	ad.Attempt.Continues = "a0"
	ad.Transcript = desktop.Transcript{Shown: true, Model: "worker transcript", Answered: "operator (desktop)",
		Turns: []desktop.Turn{
			{Role: "supervisor", Text: "Please fix the cookie.", At: "2026-11-01T04:41:00Z"},
			{Role: "worker", Text: "Working on it.", At: "2026-11-01T04:42:00Z", Queued: true},
		}}
	f.attempt[fleetID] = ad
	return f
}

func withAnsweredDecision(f *fixture) *fixture {
	d := f.decision[fleetID]
	d.Body = "CI broke when luvus 0.15 changed the output of luvus session list.\n\n" + d.Body
	d.Decision.Status, d.Decision.Pill = "answered", "passing"
	d.Decision.Answer = "2. No, rename it\n\nthe old name is in the docs"
	d.Decision.AnsweredBy = "operator (desktop)"
	d.Decision.ClosedAt = "2026-11-01T05:00:00Z"
	f.decision[fleetID] = d
	return f
}

func attemptPageFixture(t *testing.T) *fixture {
	t.Helper()
	return withAttemptTranscript(viewFixture(t))
}

func answeredDecisionFixture(t *testing.T) *fixture {
	t.Helper()
	return withAnsweredDecision(choiceFixture(t))
}

func TestTheAttemptPageShowsItsFactsAndTranscript(t *testing.T) {
	f := attemptPageFixture(t)
	app := newApp(t, f)
	tt := newTesterAt(app, 1440, 900)
	started := time.Date(2026, 11, 1, 4, 40, 0, 0, time.UTC).Local().Format("Jan 2, 15:04")
	stopped := time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC).Local().Format("Jan 2, 15:04")
	app.OpenRef("a1")
	tt.Frame()
	mustText(t, tt, "Fleets / golden / t1 / a1", "claude sonnet low", "running",
		"the transcript is the worker's own record", "Reports", "Brief", "Transcript", "Timeline",
		"Details", "Harness", "Model", "sonnet", "Effort", "low", "Started", started,
		"Stopped", stopped, "Duration", "20m", "Answered by", "operator (desktop)",
		"Continues", "a0", "Branch", "hand/f00000000e0aa/t1-a1",
		"Please fix the cookie.", "Working on it.", "worker transcript", "queued", "Delivered")
	branch, ok := tt.Find("hand/f00000000e0aa/t1-a1")
	if !ok {
		t.Fatal("no branch in the facts")
	}
	details, ok := tt.Find("Details")
	if !ok {
		t.Fatal("no Details aside")
	}
	if branch.X <= details.X {
		t.Errorf("the branch is at %v, not in the aside at %v", branch, details)
	}
	role, ok := tt.Find("worker")
	if !ok {
		t.Fatal("no turn role")
	}
	queued, ok := tt.Find("queued")
	if !ok || queued.Y-role.Y > 24 {
		t.Fatalf("the queued badge is at %v, not on the worker's turn at %v", queued, role)
	}
}

func TestTheAttemptPageKeepsTheTasksTwoColumns(t *testing.T) {
	f := attemptPageFixture(t)
	app := newApp(t, f)
	tt := newTesterAt(app, 999, 900)
	app.OpenRef("a1")
	tt.Frame()
	reports, _ := tt.Find("Reports")
	details, _ := tt.Find("Details")
	if reports.X > 60 || details.X > 60 || details.Y >= reports.Y {
		t.Fatalf("reports at %v and details at %v, want one column with the aside after", reports, details)
	}
}

func TestTheDecisionPageShowsItsOptionsAndNote(t *testing.T) {
	f := choiceFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("d1")
	tt.Frame()
	mustText(t, tt, "Fleets / golden / t1 / d1", "Keep the old cookie name?", "open", "d1",
		"Yes, keep it", "No, rename it", "Recommended",
		"reproducible builds; we bump it by hand", "patch fixes arrive by themselves",
		"Note", "Send answer", "for Fix login")
	mustNotText(t, tt, "Your answer")
}

func TestTheDecisionPageAnswersWithItsNumberKeys(t *testing.T) {
	f := choiceFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("d1")
	tt.Frame()
	tt.Key(0, ui.Key2)
	tt.Frame()
	if app.AnswerChoice != "2. No, rename it" {
		t.Fatalf("2 picked %q", app.AnswerChoice)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if f.count("answer:d1") != 1 || f.choice != "2. No, rename it" {
		t.Fatalf("Enter sent %d answers, choice %q", f.count("answer:d1"), f.choice)
	}
	mustText(t, tt, "answered", "Your answer", "No, rename it", "Answered by operator (desktop)")
	mustNotText(t, tt, "Send answer")
}

func TestTheDecisionPagePicksItsLetterKeys(t *testing.T) {
	f := choiceFixture(t)
	d := f.decision[fleetID]
	d.Options[0].Key, d.Options[0].Value = "A", "A. Yes, keep it"
	d.Options[1].Key, d.Options[1].Value = "B", "B. No, rename it"
	f.decision[fleetID] = d
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("d1")
	tt.Frame()
	tt.Key(0, ui.KeyA)
	tt.Frame()
	if app.AnswerChoice != "A. Yes, keep it" {
		t.Fatalf("A picked %q", app.AnswerChoice)
	}
	tt.Key(ui.Shift, ui.KeyB)
	tt.Frame()
	if app.AnswerChoice != "B. No, rename it" {
		t.Fatalf("Shift+B picked %q", app.AnswerChoice)
	}
}

func TestAnAnsweredDecisionOffersNoActions(t *testing.T) {
	f := answeredDecisionFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("d1")
	tt.Frame()
	mustText(t, tt, "answered", "Your answer", "No, rename it", "the old name is in the docs",
		"note by operator (desktop)", "CI broke when luvus 0.15 changed the output")
	mustNotText(t, tt, "Send answer")
	mustNotText(t, tt, "Add a note (optional)")
	tt.Key(0, ui.Key1)
	tt.Frame()
	if app.AnswerChoice != "" {
		t.Fatalf("a number key picked %q on an answered decision", app.AnswerChoice)
	}
	click(t, tt, "Yes, keep it")
	tt.Frame()
	if app.AnswerChoice != "" || f.count("answer:d1") != 0 {
		t.Fatalf("clicking an answered option picked %q and sent %d answers",
			app.AnswerChoice, f.count("answer:d1"))
	}
}

func TestAnAnsweredDecisionWithNoNoteSaysWhoAndWhen(t *testing.T) {
	f := choiceFixture(t)
	d := f.decision[fleetID]
	d.Decision.Status, d.Decision.Pill = "answered", "passing"
	d.Decision.Answer = "1. Yes, keep it"
	d.Decision.AnsweredBy = "operator (desktop)"
	d.Decision.ClosedAt = "2026-11-01T05:00:00Z"
	f.decision[fleetID] = d
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("d1")
	tt.Frame()
	mustText(t, tt, "Your answer", "Yes, keep it", "Answered by operator (desktop)")
	mustNotText(t, tt, "Send answer")
}

func TestARefusedDecisionAnswerShowsTheToastAndKeepsTheForm(t *testing.T) {
	f := choiceFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("d1")
	tt.Frame()
	click(t, tt, "Yes, keep it")
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "decision d1 is answered"}
	click(t, tt, "Send answer")
	tt.Frame()
	mustText(t, tt, "The action did not go through", "decision d1 is answered", "Yes, keep it", "Send answer")
	if app.Screen != desktop.DecisionScreen || app.Ref != "d1" {
		t.Fatalf("the refusal left %v %q", app.Screen, app.Ref)
	}
	if app.AnswerChoice != "1. Yes, keep it" {
		t.Fatalf("the refusal dropped the pick %q", app.AnswerChoice)
	}
	if n := f.count("answer:d1"); n != 1 {
		t.Fatalf("the answer was posted %d times", n)
	}
	mustNotText(t, tt, "One answer")
}

func TestAnUnreadDecisionAnswerSaysSoAndThePageCatchesUp(t *testing.T) {
	f := choiceFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("d1")
	tt.Frame()
	click(t, tt, "Yes, keep it")
	f.action = &desktop.AppliedUnread{Err: errors.New("the board's /decision/d1/answer data: unexpected end of JSON input")}
	d := f.decision[fleetID]
	d.Decision.Status, d.Decision.Pill = "answered", "passing"
	d.Decision.Answer = "1. Yes, keep it"
	d.Decision.AnsweredBy = "operator (desktop)"
	d.Decision.ClosedAt = "2026-11-01T05:00:00Z"
	f.decision[fleetID] = d
	click(t, tt, "Send answer")
	tt.Frame()
	mustText(t, tt, "The answer could not be read", "the card refreshes itself", "Your answer", "Yes, keep it")
	mustNotText(t, tt, "The action did not go through")
	mustNotText(t, tt, "unexpected end of JSON input")
	mustNotText(t, tt, "Send answer")
}

func TestAReportOpensItsTaskAtItsCard(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("t1")
	tt.Frame()
	if y, _ := desktop.ViewerScroll(app); y != 0 {
		t.Fatalf("the task page starts at %v", y)
	}
	tt.Scroll(400, 400, 0, 160)
	tt.Frame()
	click(t, tt, "open report r3")
	tt.Frame()
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" || desktop.PageReport(app) != "r3" {
		t.Fatalf("the report opened %v %q at %q, want the task t1 at r3",
			app.Screen, app.Ref, desktop.PageReport(app))
	}
	if got := f.count("report:r3"); got != 0 {
		t.Fatalf("the window read the report %d times, want the card the task holds", got)
	}
	if target := desktop.ScrollTarget(app); target != "" {
		t.Fatalf("the scroll target %q was not consumed by the card", target)
	}
	card, ok := tt.Find("report r3")
	if !ok {
		t.Fatal("no r3 card")
	}
	if card.Y < 70 || card.Y+card.H > windowHeight-24 {
		t.Fatalf("the r3 card is at %v, outside the viewer's body", card)
	}
	if y, _ := desktop.ViewerScroll(app); y <= 0 {
		t.Fatalf("the page did not scroll to the card: y=%v", y)
	}
	mustText(t, tt, "Halfway there")
}

func TestAReportInNeedsOpensItsTaskAtItsCard(t *testing.T) {
	f := viewFixture(t)
	delete(f.report, fleetID)
	app := newApp(t, f)
	app.Task, app.Attempt = desktop.TaskDetail{}, desktop.AttemptDetail{}
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Cookie fixed")
	click(t, tt, "Open r1")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" || desktop.PageReport(app) != "r1" {
		t.Fatalf("the report opened %v %q at %q, want the task t1 at r1",
			app.Screen, app.Ref, desktop.PageReport(app))
	}
	if got := f.count("report:r1"); got != 0 {
		t.Fatalf("the window read the report %d times, want the Needs item", got)
	}
	card, ok := tt.Find("report r1")
	if !ok {
		t.Fatal("no r1 card")
	}
	if card.Y < 70 || card.Y+card.H > windowHeight-24 {
		t.Fatalf("the r1 card is at %v, outside the viewer's body", card)
	}
}

func manyReportFixture(t *testing.T) *fixture {
	t.Helper()
	f := viewFixture(t)
	td := f.task[fleetID]
	reports := make([]desktop.Report, 0, 55)
	for i := 1; i <= 55; i++ {
		reports = append(reports, desktop.Report{
			Ref: fmt.Sprintf("r%d", i), Task: "t1", Attempt: "a1", Status: "done",
			Summary:   fmt.Sprintf("Report %d", i),
			Body:      fmt.Sprintf("Report %d\n\nbody %d", i, i),
			CreatedAt: "2026-11-01T04:40:00Z",
		})
	}
	td.Reports = reports
	td.Unread = nil
	td.MoreReports = true
	f.task[fleetID] = td
	return f
}

func TestAReportInUnreadOutsideTheWindowLandsOnItsCard(t *testing.T) {
	f := manyReportFixture(t)
	td := f.task[fleetID]
	td.Unread = []desktop.Report{{
		Ref: "r99", Task: "t1", Attempt: "a1", Status: "progress",
		Summary:   "Older than the window",
		Body:      "Older than the window\n\nstill unread",
		CreatedAt: "2026-10-01T04:40:00Z",
	}}
	f.task[fleetID] = td
	app := newApp(t, f)
	tt := newTesterAt(app, 1100, 900)
	app.OpenRef("t1")
	tt.Frame()
	app.OpenRef("r99")
	tt.Frame()
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" || desktop.PageReport(app) != "r99" {
		t.Fatalf("the report opened %v %q at %q, want the task t1 at r99",
			app.Screen, app.Ref, desktop.PageReport(app))
	}
	if got := f.count("report:r99"); got != 0 {
		t.Fatalf("the window read the report %d times, want the unread card", got)
	}
	card, ok := tt.Find("report r99")
	if !ok {
		t.Fatal("no r99 card")
	}
	if card.Y < 70 || card.Y+card.H > 900-24 {
		t.Fatalf("the r99 card is at %v, outside the viewer's body", card)
	}
	if y, _ := desktop.ViewerScroll(app); y <= 0 {
		t.Fatalf("the page did not scroll to the card: y=%v", y)
	}
}

func TestAReportThatArrivesInAPollScrollsToItsCard(t *testing.T) {
	f := manyReportFixture(t)
	st := f.status[fleetID]
	st.Needs.Items = append(st.Needs.Items, desktop.Need{
		Kind: "report", Ref: "r99", Word: "REPORT", Tone: "neutral", Task: "t1",
		Report: "r99", Title: "Late report", Status: "done", CreatedAt: "2026-10-01T04:40:00Z",
	})
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := newTesterAt(app, 1100, 900)
	app.OpenRef("r99")
	tt.Frame()
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" || desktop.PageReport(app) != "r99" {
		t.Fatalf("the report opened %v %q at %q, want the task t1 at r99",
			app.Screen, app.Ref, desktop.PageReport(app))
	}
	if _, ok := tt.Find("report r99"); ok {
		t.Fatal("the card is drawn before the report arrived")
	}
	if target := desktop.ScrollTarget(app); target != "r99" {
		t.Fatalf("the scroll target is %q, want r99 held for the card", target)
	}
	td := f.task[fleetID]
	td.Unread = append(td.Unread, desktop.Report{
		Ref: "r99", Task: "t1", Attempt: "a1", Status: "done",
		Summary:   "Late report",
		Body:      "Late report\n\narrived in the poll",
		CreatedAt: "2026-10-01T04:40:00Z",
	})
	f.task[fleetID] = td
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	card, ok := tt.Find("report r99")
	if !ok {
		t.Fatal("no r99 card after the poll")
	}
	if card.Y < 70 || card.Y+card.H > 900-24 {
		t.Fatalf("the r99 card is at %v, outside the viewer's body", card)
	}
	if y, _ := desktop.ViewerScroll(app); y <= 0 {
		t.Fatalf("the poll did not scroll to the card: y=%v", y)
	}
	if target := desktop.ScrollTarget(app); target != "" {
		t.Fatalf("the scroll target %q was not consumed by the card", target)
	}
}

func TestAReportLinkReadsItsTaskWhenNoPageHoldsIt(t *testing.T) {
	f := goldenFixture(t)
	st := f.status[fleetID]
	kept := st.Needs.Items[:0:0]
	for _, n := range st.Needs.Items {
		if n.Report == "" {
			kept = append(kept, n)
		}
	}
	st.Needs.Items = kept
	st.Needs.Waiting = len(kept)
	f.status[fleetID] = st
	app := newApp(t, f)
	if err := app.OpenLink("handboard://open/" + fleetID + "/task/t1#r1"); err != nil {
		t.Fatal(err)
	}
	if got := f.count("report:r1"); got != 1 {
		t.Fatalf("the window read the report %d times, want 1", got)
	}
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("the link opened %v %q, want the task at r1's card", app.Screen, app.Ref)
	}
}

func TestThePollStaysAtItsIntervalWhileAPageIsOpen(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	app.OpenRef("a1")
	if got := clock.delay(); got != desktop.FocusedRefresh {
		t.Fatalf("the open attempt polls every %v, want %v", got, desktop.FocusedRefresh)
	}
	clock.Advance(desktop.FocusedRefresh)
	if f.pollScreen != desktop.AttemptScreen || f.pollRef != "a1" {
		t.Fatalf("the poll asked for %v %q, want the attempt", f.pollScreen, f.pollRef)
	}
	app.OpenRef("d1")
	if got := clock.delay(); got != desktop.FocusedRefresh {
		t.Fatalf("the open decision polls every %v, want %v", got, desktop.FocusedRefresh)
	}
	clock.Advance(desktop.FocusedRefresh)
	if f.pollScreen != desktop.DecisionScreen || f.pollRef != "d1" {
		t.Fatalf("the poll asked for %v %q, want the decision", f.pollScreen, f.pollRef)
	}
}

func TestAnOpenAttemptPageFollowsItsTranscript(t *testing.T) {
	f := attemptPageFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := newTester(app)
	app.OpenRef("a1")
	tt.Frame()
	mustText(t, tt, "Please fix the cookie.")
	button, ok := tt.Find("open report r1")
	if !ok {
		t.Fatal("no report link")
	}
	tt.Press(button.X+button.W/2, button.Y+button.H/2)
	tt.Release(button.X+button.W/2+60, button.Y+button.H/2)
	tt.Frame()
	if !tt.Focused("open report r1") {
		t.Fatal("the report link did not take the focus")
	}
	tt.Scroll(400, 300, 0, 240)
	tt.Frame()
	before, max := desktop.ViewerScroll(app)
	if max == 0 || before == 0 {
		t.Fatalf("the attempt page does not scroll: y=%v max=%v", before, max)
	}
	ad := f.attempt[fleetID]
	ad.Transcript.Turns = append(append([]desktop.Turn(nil), ad.Transcript.Turns...),
		desktop.Turn{Role: "worker", Text: "A new line from the worker.", At: "2026-11-01T04:43:00Z"})
	f.attempt[fleetID] = ad
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "A new line from the worker.")
	if after, _ := desktop.ViewerScroll(app); after != before {
		t.Errorf("the live repaint moved the page from %v to %v", before, after)
	}
	if !tt.Focused("open report r1") {
		t.Fatal("the live repaint dropped the focus")
	}
}

func TestAnOpenDecisionPageFollowsItsAnswer(t *testing.T) {
	f := choiceFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := newTesterAt(app, 1100, 420)
	app.OpenRef("d1")
	tt.Frame()
	tt.Key(0, ui.Key1)
	tt.Frame()
	click(t, tt, "Note")
	tt.Type("half a note")
	if app.AnswerNote != "half a note" || app.AnswerChoice != "1. Yes, keep it" {
		t.Fatalf("note %q pick %q", app.AnswerNote, app.AnswerChoice)
	}
	tt.Scroll(400, 250, 0, 120)
	tt.Frame()
	before, max := desktop.ViewerScroll(app)
	if max == 0 || before == 0 {
		t.Fatalf("the decision page does not scroll: y=%v max=%v", before, max)
	}
	d := f.decision[fleetID]
	d.Options[1].Note = "a newer consequence"
	d.Body = "The docs keep the old cookie name.\n\n1. Yes, keep it\n2. No, rename it"
	f.decision[fleetID] = d
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "a newer consequence", "The docs keep the old cookie name.")
	if app.AnswerNote != "half a note" {
		t.Fatalf("the live repaint changed the typed note to %q", app.AnswerNote)
	}
	if !tt.Focused("Note") {
		t.Fatal("the live repaint dropped the focus")
	}
	if after, _ := desktop.ViewerScroll(app); after != before {
		t.Errorf("the live repaint moved the page from %v to %v", before, after)
	}
}

func TestAnOpenDecisionPageTurnsAnsweredOnAPoll(t *testing.T) {
	f := choiceFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := newTester(app)
	app.OpenRef("d1")
	tt.Frame()
	mustText(t, tt, "Send answer")
	d := f.decision[fleetID]
	d.Decision.Status, d.Decision.Pill = "answered", "passing"
	d.Decision.Answer = "2. No, rename it\n\nthe old name is in the docs"
	d.Decision.AnsweredBy = "operator (desktop)"
	d.Decision.ClosedAt = "2026-11-01T05:00:00Z"
	f.decision[fleetID] = d
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "Your answer", "No, rename it", "the old name is in the docs",
		"note by operator (desktop)")
	mustNotText(t, tt, "Send answer")
}

func TestTheAttemptAndDecisionPagesShowControlCharacters(t *testing.T) {
	f := attemptPageFixture(t)
	ad := f.attempt[fleetID]
	ad.ModelNote = "note\u202Ehere"
	ad.Attempt.Reason = "reason\u202Ehere"
	ad.Attempt.EndedAt = "not\u202Ea time"
	ad.Transcript.Turns[0].Text = "turn\u202Etext"
	ad.Events[0].Detail = "event\u202Etext"
	f.attempt[fleetID] = ad
	d := f.decision[fleetID]
	d.Decision.Headline = "decide\u202Enow"
	d.Options[0].Label = "label\u202Eone"
	d.Options[0].Note = "note\u202Eone"
	d.Decision.Status, d.Decision.Pill = "answered", "passing"
	d.Decision.Answer = "2. No, rename it\n\nreply\u202Ehere"
	d.Decision.AnsweredBy = "operator\u202Eby"
	d.Decision.ClosedAt = "2026-11-01T05:00:00Z"
	f.decision[fleetID] = d
	app := newApp(t, f)
	tt := newTesterAt(app, 1440, 900)
	app.OpenRef("a1")
	tt.Frame()
	mustText(t, tt, "noteU+202Ehere", "reasonU+202Ehere", "notU+202Ea time",
		"turnU+202Etext", "eventU+202Etext")
	mustNotText(t, tt, "turn\u202Etext")
	app.OpenRef("d1")
	tt.Frame()
	mustText(t, tt, "decideU+202Enow", "labelU+202Eone", "noteU+202Eone",
		"replyU+202Ehere", "note by operatorU+202Eby")
	mustNotText(t, tt, "label\u202Eone")
	mustNotText(t, tt, "reply\u202Ehere")
}
