package desktop_test

import (
	"errors"
	"fmt"
	"image/color"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
	"github.com/egoist/mygo/ui"
)

func newTesterAt(app *desktop.App, width, height int) *ui.Tester {
	return ui.NewTester(app.View, width, height)
}

func viewFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	td := f.task[fleetID]
	td.Task.Goal = "Make the cookie name configurable."
	td.Task.UpdatedAt = "2026-11-01T04:45:00Z"
	td.Waits = &desktop.Waits{Ref: "w1", Due: "2026-11-03T00:00:00Z", Note: "the docs freeze", Status: "active", Waits: true}
	td.Children = []desktop.TaskRow{{
		Ref: "t3", Title: "Rotate the docs", Project: "hand", Status: "inbox", State: "waiting", Parent: "t1",
	}}
	td.Delivered = []desktop.Deliverable{
		{Ref: "https://github.com/example/hand/pull/42", Kind: "PR"},
		{Ref: "design.md", Kind: "file", Href: "/" + fleetID + "/doc/reports/t1/design.md"},
	}
	reports := []desktop.Report{
		{Ref: "r1", Task: "t1", Attempt: "a1", Status: "done", Summary: "Cookie fixed",
			Body: "Cookie fixed\nPR: https://github.com/example/hand/pull/42\n\nAll tests pass.", CreatedAt: "2026-11-01T04:40:00Z"},
		{Ref: "r2", Task: "t1", Attempt: "a1", Status: "stuck", Summary: "Blocked on the docs",
			Body: "Blocked on the docs\n\nThe docs freeze is late.", CreatedAt: "2026-11-01T04:41:00Z"},
		{Ref: "r3", Task: "t1", Attempt: "a1", Status: "progress", Summary: "Halfway there",
			Body: "Halfway there\n\nMore to come.", CreatedAt: "2026-11-01T04:42:00Z"},
	}
	td.Reports = reports
	td.Unread = []desktop.Report{reports[0], reports[2]}
	f.task[fleetID] = td
	return f
}

func rgbIs(got color.RGBA, want ui.Color) bool {
	return got.R == want.R && got.G == want.G && got.B == want.B
}

func TestTheViewerBoxAndItsBackdrop(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	img := tt.Image()
	backdrop := img.RGBAAt(26, 26)
	inside := img.RGBAAt(36, 36)
	if backdrop.R > 200 {
		t.Fatalf("the box's corner is %v, want the backdrop", backdrop)
	}
	if inside.R < 200 {
		t.Fatalf("the box's top strip is %v, want the panel", inside)
	}
	if got := img.RGBAAt(30, 725); got.R < 200 {
		t.Errorf("inside the box's bottom is %v, want the ground", got)
	}
	if got := img.RGBAAt(30, 745); got.R > 200 {
		t.Errorf("below the box is %v, want the backdrop", got)
	}
	closeR, ok := tt.Find("Close the viewer")
	if !ok || int(closeR.X+closeR.W) != 1067 {
		t.Fatalf("the box's right edge is %v, want 1076", closeR)
	}
	backR, _ := tt.Find("Back")
	if int(backR.X) != 33 || int(backR.Y) != 31 {
		t.Fatalf("the back button is %v, want 33,31", backR)
	}
}

func TestTheViewerBoxIsCappedAt1200(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTesterAt(app, 1440, 900)
	click(t, tt, "Fix login")
	closeR, ok := tt.Find("Close the viewer")
	if !ok || int(closeR.X+closeR.W) != 1311 {
		t.Fatalf("the box's right edge is %v, want 1320", closeR)
	}
	if got := tt.Image().RGBAAt(122, 24); got.R > 200 {
		t.Errorf("the box's left corner is %v, want the backdrop", got)
	}
}

func TestTheBackdropBlocksThePage(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	tt.Scroll(300, 420, 0, 160)
	before, max := desktop.BoardScroll(app)
	if max == 0 || before == 0 {
		t.Fatalf("the page behind does not scroll: y=%v max=%v", before, max)
	}
	click(t, tt, "Fix login")
	tt.Scroll(8, 300, 0, 300)
	if after, _ := desktop.BoardScroll(app); after != before {
		t.Errorf("a wheel over the backdrop moved the page behind from %v to %v", before, after)
	}
	for range 12 {
		tt.Key(0, ui.KeyTab)
		for _, behind := range []string{"Board", "Tasks", "Conversation", "Screen", "Phone"} {
			if tt.Focused(behind) {
				t.Fatalf("Tab left the viewer for %q", behind)
			}
		}
	}
	tt.ClickAt(8, 300)
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("a click on the page behind left the viewer on %v", app.Screen)
	}
}

func TestFocusMovesIntoTheViewerAndComesBack(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	if !tt.Focused("The page") {
		t.Fatal("the focus is not in the viewer's body")
	}
	if tt.Focused("task t1") {
		t.Fatal("the focus stayed on the task row behind the viewer")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("Escape left the viewer on %v", app.Screen)
	}
	if !tt.Focused("task t1") {
		t.Fatal("the focus did not come back to the task row")
	}
}

func TestEveryCloseClosesTheWholeStack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close func(*testing.T, *ui.Tester)
	}{
		{"escape", func(t *testing.T, tt *ui.Tester) { tt.Key(0, ui.KeyEscape) }},
		{"close button", func(t *testing.T, tt *ui.Tester) { click(t, tt, "Close the viewer") }},
		{"backdrop", func(t *testing.T, tt *ui.Tester) { tt.ClickAt(4, 4) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newApp(t, viewFixture(t))
			tt := newTester(app)
			click(t, tt, "Fix login")
			click(t, tt, "attempt a1")
			if app.Screen != desktop.AttemptScreen {
				t.Fatalf("the link left the viewer on %v", app.Screen)
			}
			tc.close(t, tt)
			tt.Frame()
			if app.Screen != desktop.BoardScreen || app.Ref != "" {
				t.Fatalf("the close left %v %q, want the Board", app.Screen, app.Ref)
			}
			if _, ok := tt.Find("Close the viewer"); ok {
				t.Fatal("the viewer is still open")
			}
			if !tt.Focused("task t1") {
				t.Fatal("the focus did not come back to the task row")
			}
		})
	}
}

func TestBackStepsOnePageAndStopsAtTheFirst(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("Back left the first page for %v %q", app.Screen, app.Ref)
	}
	click(t, tt, "attempt a1")
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("Back left %v %q, want the task", app.Screen, app.Ref)
	}
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("Back went past the first page to %v %q", app.Screen, app.Ref)
	}
}

func TestTheStackIsCappedAtTwenty(t *testing.T) {
	app := newApp(t, viewFixture(t))
	app.OpenRef("t1")
	for i := 2; i <= 21; i++ {
		app.OpenRef(fmt.Sprintf("t%d", i))
	}
	if app.Screen != desktop.TaskScreen || app.Ref != "t21" {
		t.Fatalf("the 21st link left %v %q, want t21", app.Screen, app.Ref)
	}
	for range 19 {
		app.Back()
	}
	if app.Ref != "t1" {
		t.Fatalf("19 Backs left %q, want the first page t1", app.Ref)
	}
	app.Back()
	if app.Ref != "t1" {
		t.Fatalf("Back went past the first page to %q", app.Ref)
	}
}

func TestTheStripNamesThePage(t *testing.T) {
	app := newApp(t, viewFixture(t))
	app.OpenRef("t1")
	if got := desktop.ViewerName(app); got != "Fix login" {
		t.Fatalf("the task page is named %q", got)
	}
	for _, ref := range []string{"a1", "r1", "d1"} {
		app.OpenRef(ref)
		if got := desktop.ViewerName(app); got != ref {
			t.Fatalf("the %s page is named %q", ref, got)
		}
	}
	app.OpenRef("t9")
	if got := desktop.ViewerName(app); got != "t9" {
		t.Fatalf("a page not loaded is named %q, want its ref", got)
	}
}

func TestOpenAsAPageRunsHandOpen(t *testing.T) {
	hand := installFakeHand(t)
	f := viewFixture(t)
	f.fleets[0].Home = hand.home
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "Open as a page")
	hand.want(t, "open", "t1")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("the page link left the viewer on %v %q", app.Screen, app.Ref)
	}
}

func TestTheTaskPageKeepsTwoColumnsFrom1000(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTesterAt(app, 1000, 900)
	click(t, tt, "Fix login")
	main, ok := tt.Find("Reports")
	if !ok {
		t.Fatal("no Reports head")
	}
	side, ok := tt.Find("Delivered")
	if !ok {
		t.Fatal("no Delivered aside")
	}
	if main.X > 60 || side.X < 600 || side.X-main.X < 500 {
		t.Fatalf("main at %v, aside at %v, want two columns", main, side)
	}
	app2 := newApp(t, viewFixture(t))
	tt2 := newTesterAt(app2, 999, 900)
	click(t, tt2, "Fix login")
	main2, _ := tt2.Find("Reports")
	side2, _ := tt2.Find("Delivered")
	if side2.X > 60 || main2.X > 60 || side2.Y >= main2.Y {
		t.Fatalf("main at %v, aside at %v, want one column with the aside first", main2, side2)
	}
}

func TestTheWaitingRowsCarryTheirCodes(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	mustText(t, tt, "DECISION", "d1", "Keep the old cookie name?", "Answer d1",
		"REPORT", "r1", "Cookie fixed")
	img := tt.Image()
	for word, want := range map[string]ui.Color{
		"DECISION": kit.ColorsFor(false).WaitBG,
		"REPORT":   kit.ColorsFor(false).NeutralBG,
	} {
		r, ok := tt.Find(word)
		if !ok {
			t.Fatalf("no %q code", word)
		}
		if got := img.RGBAAt(int(r.X)+4, int(r.Y+r.H/2)); !rgbIs(got, want) {
			t.Errorf("the %s code's fill is %v, want %v", word, got, want)
		}
	}
}

func TestReportCardsCarryTheirTone(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTesterAt(app, 1100, 1600)
	click(t, tt, "Fix login")
	k := kit.ColorsFor(false)
	for ref, want := range map[string]ui.Color{"r1": k.PassBG, "r2": k.FailBG, "r3": k.NeutralBG} {
		r, ok := tt.Find("report " + ref)
		if !ok {
			t.Fatalf("no card for %s", ref)
		}
		if got := tt.Image().RGBAAt(int(r.X)+4, int(r.Y)+8); !rgbIs(got, want) {
			t.Errorf("the %s card's header is %v, want %v", ref, got, want)
		}
	}
}

func TestMarkReadFromTheCardLeavesItsReceipt(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "Mark read r1")
	mustText(t, tt, "read by operator (desktop)", "r1 marked read")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("the ack left the viewer on %v %q", app.Screen, app.Ref)
	}
	if _, ok := tt.Find("Mark read r1"); ok {
		t.Fatal("the Mark read button stayed after it succeeded")
	}
	if ref := app.Task.Reports[0]; ref.Ref != "r1" || ref.AckedAt == "" {
		t.Fatalf("the task's report is %+v", ref)
	}
}

func TestARefusedMarkReadSaysWhyAndKeepsTheButton(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Fix login")
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "report r1 is already acknowledged"}
	click(t, tt, "Mark read r1")
	mustText(t, tt, "The action did not go through", "report r1 is already acknowledged")
	if _, ok := tt.Find("Mark read r1"); !ok {
		t.Fatal("the refusal removed the Mark read button")
	}
	mustNotText(t, tt, "r1 marked read")
}

func TestAnUnreadAnswerKeepsTheButton(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Fix login")
	f.action = &desktop.AppliedUnread{Err: errors.New("the board's /report/r1/ack data: unexpected end of JSON input")}
	click(t, tt, "Mark read r1")
	mustText(t, tt, "The action did not go through", "unexpected end of JSON input")
	if _, ok := tt.Find("Mark read r1"); !ok {
		t.Fatal("an unread answer removed the Mark read button")
	}
	mustNotText(t, tt, "r1 marked read")
}

func TestMarkReadIsDisabledWhileInFlight(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Fix login")
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "Mark read r1")
	mustText(t, tt, "Marking read…")
	click(t, tt, "Mark read r1")
	if got := f.count("ack:r1"); got != 1 {
		t.Fatalf("%d acknowledgements while in flight, want 1", got)
	}
	select {
	case <-f.actionEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the acknowledgement never started")
	}
	close(f.actionBlock)
	waitPost(t, posted)()
	waitPost(t, posted)()
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "read by operator (desktop)")
}

func TestTheAsideHoldsItsFourSections(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTesterAt(app, 1440, 900)
	click(t, tt, "Fix login")
	mustText(t, tt, "Delivered", "Sub-tasks", "Attempts", "Details")
	mustText(t, tt, "Rotate the docs", "claude sonnet low", "Project", "Plan", "Created")
}

func TestAnOpenViewerFollowsItsTask(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := newTester(app)
	click(t, tt, "Fix login")
	button, ok := tt.Find("Mark read r1")
	if !ok {
		t.Fatal("no Mark read button")
	}
	tt.Press(button.X+button.W/2, button.Y+button.H/2)
	tt.Release(button.X+button.W/2+60, button.Y+button.H/2)
	tt.Frame()
	if !tt.Focused("Mark read r1") {
		t.Fatal("the button did not take the focus")
	}
	app.Draft = "half a message"
	tt.Scroll(400, 400, 0, 220)
	before, max := desktop.ViewerScroll(app)
	if max == 0 || before == 0 {
		t.Fatalf("the viewer's page does not scroll: y=%v max=%v", before, max)
	}
	td := f.task[fleetID]
	td.Reports = append([]desktop.Report(nil), td.Reports...)
	td.Reports[0].Summary = "Cookie fixed for good"
	td.Attempts = append([]desktop.AttemptRow(nil), td.Attempts...)
	td.Attempts[0].Status = "passing"
	f.task[fleetID] = td
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "Cookie fixed for good")
	if after, _ := desktop.ViewerScroll(app); after != before {
		t.Errorf("the live repaint moved the page from %v to %v", before, after)
	}
	if !tt.Focused("Mark read r1") {
		t.Fatal("the live repaint dropped the focus")
	}
	if app.Draft != "half a message" {
		t.Fatalf("the live repaint changed the typed draft to %q", app.Draft)
	}
	td = f.task[fleetID]
	td.Reports = append([]desktop.Report(nil), td.Reports...)
	td.Reports[0].AckedAt = "2026-11-01T05:00:00Z"
	td.Reports[0].AckedBy = "you"
	f.task[fleetID] = td
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	if !tt.Focused("The page") {
		t.Fatal("a vanished control did not give the focus to the viewer's body")
	}
	for _, behind := range []string{"Board", "Tasks", "Conversation"} {
		if tt.Focused(behind) {
			t.Fatalf("the focus left the viewer for %q", behind)
		}
	}
}

func TestThePollFollowsTheTaskAndReturnsToTheBoard(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := newTester(app)
	click(t, tt, "Fix login")
	if got := clock.delay(); got != desktop.FocusedRefresh {
		t.Fatalf("the open viewer polls every %v, want %v", got, desktop.FocusedRefresh)
	}
	clock.Advance(desktop.FocusedRefresh)
	if f.pollScreen != desktop.TaskScreen || f.pollRef != "t1" {
		t.Fatalf("the poll asked for %v %q, want the task", f.pollScreen, f.pollRef)
	}
	click(t, tt, "Close the viewer")
	clock.Advance(desktop.FocusedRefresh)
	if f.pollScreen != desktop.BoardScreen || f.pollRef != "" {
		t.Fatalf("the closed viewer still polls %v %q", f.pollScreen, f.pollRef)
	}
}

func TestScenesViewer(t *testing.T) {
	type scene struct {
		name string
		play func(*testing.T, *fixture, *desktop.App, *ui.Tester) func()
	}
	scenes := []scene{
		{"task", func(t *testing.T, f *fixture, app *desktop.App, tt *ui.Tester) func() {
			click(t, tt, "Fix login")
			return nil
		}},
		{"cards", func(t *testing.T, f *fixture, app *desktop.App, tt *ui.Tester) func() {
			click(t, tt, "Fix login")
			tt.Scroll(400, 400, 0, 200)
			tt.Frame()
			posted := make(chan func(), 8)
			app.Post = func(fn func()) { posted <- fn }
			f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 1)
			click(t, tt, "Mark read r1")
			select {
			case <-f.actionEntered:
			case <-time.After(5 * time.Second):
				t.Fatal("the acknowledgement never started")
			}
			tt.Frame()
			return func() {
				close(f.actionBlock)
				waitPost(t, posted)()
				waitPost(t, posted)()
				waitPost(t, posted)()
			}
		}},
		{"stack", func(t *testing.T, f *fixture, app *desktop.App, tt *ui.Tester) func() {
			click(t, tt, "Fix login")
			click(t, tt, "attempt a1")
			return nil
		}},
	}
	for _, size := range []struct{ w, h int }{{1100, 760}, {1440, 900}} {
		for _, dark := range []bool{false, true} {
			for _, sc := range scenes {
				t.Run(fmt.Sprintf("%s-%dx%d-dark=%v", sc.name, size.w, size.h, dark), func(t *testing.T) {
					f := viewFixture(t)
					app := newApp(t, f)
					tt := ui.NewTester(app.View, size.w, size.h)
					tt.SetDark(dark)
					cleanup := sc.play(t, f, app, tt)
					tt.Frame()
					theme := "light"
					if dark {
						theme = "dark"
					}
					shotSized(t, tt, fmt.Sprintf("viewer-%s-%dx%d-%s", sc.name, size.w, size.h, theme), size.w, size.h)
					if cleanup != nil {
						cleanup()
					}
				})
			}
		}
	}
}

func TestBoardTextShowsControlCharacters(t *testing.T) {
	f := viewFixture(t)
	td := f.task[fleetID]
	td.Task.Title = "Fix \u202Elogin\u202C"
	td.Task.Goal = "a\x00b"
	td.Task.Project = "han\u200Ed"
	td.Events = append([]desktop.Event(nil), td.Events...)
	td.Events[0].Detail = "x\u2066y"
	f.task[fleetID] = td
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Fix login")
	mustText(t, tt, "Fix U+202EloginU+202C", "aU+0000b", "hanU+200Ed", "xU+2066y")
	mustNotText(t, tt, "Fix \u202Elogin")
}
