package desktop_test

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"slices"
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

func viewFixtureNoDecisions(t *testing.T) *fixture {
	t.Helper()
	f := viewFixture(t)
	td := f.task[fleetID]
	td.Decisions = nil
	f.task[fleetID] = td
	return f
}

func rgbIs(got color.RGBA, want ui.Color) bool {
	return got.R == want.R && got.G == want.G && got.B == want.B
}

func TestAnObjectOpensInTheWorkPaneBesideTheSupervisor(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	if app.Screen != desktop.TaskScreen {
		t.Fatalf("screen %v", app.Screen)
	}
	closeR, ok := tt.Find("Close the viewer")
	if !ok {
		t.Fatal("no close button")
	}
	sup, ok := tt.Find("Supervisor")
	if !ok {
		t.Fatal("the supervisor pane is gone while the page is open")
	}
	if closeR.X+closeR.W > sup.X {
		t.Fatalf("the page's strip ends at %v, past the supervisor pane at %v", closeR.X+closeR.W, sup.X)
	}
	if sup.X < 400 {
		t.Fatalf("the supervisor pane starts at %v, want it beside the work pane", sup.X)
	}
	mustText(t, tt, "Back", "Close the viewer", "Collapse the supervisor")
	backR, _ := tt.Find("Back")
	if backR.X < 56 || backR.Y != closeR.Y {
		t.Fatalf("Back at %v, Close at %v, want them on the work pane's strip", backR, closeR)
	}
	img := tt.Image()
	if got := img.RGBAAt(int(sup.X)+8, 740); got.R == 0 && got.G == 0 && got.B == 0 {
		t.Fatalf("the supervisor pane paints %v at its foot", got)
	}
	if _, ok := tt.Find("Open as a page"); ok {
		t.Fatal("a page in the work pane offers Open as a page")
	}
}

func TestTheViewerPageIsCappedAt1200InTheWorkPane(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTesterAt(app, 2400, 900)
	click(t, tt, "Fix login")
	closeR, ok := tt.Find("Close the viewer")
	if !ok {
		t.Fatal("no close button")
	}
	backR, _ := tt.Find("Back")
	if got := closeR.X + closeR.W - backR.X; got > 1200 {
		t.Fatalf("the strip is %v wide, want at most 1200", got)
	}
	if backR.X < 150 {
		t.Fatalf("the page starts at %v, want it centred in the pane", backR.X)
	}
}

func TestARailItemStaysLiveBesideAnOpenPage(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "Tasks")
	if app.Screen != desktop.TasksScreen {
		t.Fatalf("the rail's Tasks left %v", app.Screen)
	}
	if _, ok := tt.Find("Close the viewer"); ok {
		t.Fatal("the page stayed open over the Tasks page")
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
	tt.Key(ui.Shift, ui.KeyTab)
	if !tt.Focused("Close the viewer") {
		t.Fatal("Tab back from the body did not reach the strip before the page")
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

func TestBackStepsOnePageThenClosesToTheOriginalView(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "attempt a1")
	if app.Screen != desktop.AttemptScreen {
		t.Fatalf("the link left %v", app.Screen)
	}
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("the first Back left %v %q, want the task", app.Screen, app.Ref)
	}
	click(t, tt, "Back")
	if app.Screen != desktop.BoardScreen || app.Ref != "" {
		t.Fatalf("the second Back left %v %q, want the Board", app.Screen, app.Ref)
	}
	if _, ok := tt.Find("Close the viewer"); ok {
		t.Fatal("the page is still open after Back from the first page")
	}
	if !tt.Focused("task t1") {
		t.Fatal("the focus did not come back to the task row")
	}
}

func TestEveryBackKeyStepsBackOnePage(t *testing.T) {
	for _, tc := range []struct {
		name string
		mods ui.Modifiers
		key  ui.Key
	}{
		{"Alt+Left", ui.Alt, ui.KeyLeft},
		{"Cmd+[", ui.Super, ui.KeyBracketLeft},
		{"the Back key", 0, ui.KeyBack},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newApp(t, viewFixture(t))
			tt := newTester(app)
			click(t, tt, "Fix login")
			click(t, tt, "attempt a1")
			tt.Key(tc.mods, tc.key)
			tt.Frame()
			if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
				t.Fatalf("the key left %v %q, want the task", app.Screen, app.Ref)
			}
			tt.Key(tc.mods, tc.key)
			tt.Frame()
			if app.Screen != desktop.BoardScreen {
				t.Fatalf("the key from the first page left %v, want the Board", app.Screen)
			}
		})
	}
}

func TestBackKeysDoNotFireOutsideThePage(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "attempt a1")
	click(t, tt, "Message the supervisor")
	tt.Key(ui.Alt, ui.KeyLeft)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.Screen != desktop.AttemptScreen {
		t.Fatalf("a key typed in the composer left %v", app.Screen)
	}
}

func TestTheStackIsCappedAtTwentyAndDropsTheOldestPage(t *testing.T) {
	app := newApp(t, viewFixture(t))
	app.OpenRef("t1")
	for i := 2; i <= 21; i++ {
		app.OpenRef(fmt.Sprintf("t%d", i))
	}
	if app.Screen != desktop.TaskScreen || app.Ref != "t21" {
		t.Fatalf("the 21st link left %v %q, want t21", app.Screen, app.Ref)
	}
	for i := 20; i >= 2; i-- {
		app.Back()
		if want := fmt.Sprintf("t%d", i); app.Ref != want {
			t.Fatalf("Back left %q, want %s: the stack lost a page that is not the oldest", app.Ref, want)
		}
	}
	app.Back()
	if app.Screen != desktop.BoardScreen || app.Ref != "" {
		t.Fatalf("Back from the oldest kept page left %v %q, want the Board", app.Screen, app.Ref)
	}
}

func TestTheHistoryIsGoneAfterTheViewerCloses(t *testing.T) {
	app := newApp(t, viewFixture(t))
	app.OpenRef("t1")
	app.OpenRef("a1")
	app.CloseViewer()
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("Close left %v, want the Board", app.Screen)
	}
	app.OpenRef("t1")
	app.Back()
	if app.Screen != desktop.BoardScreen || app.Ref != "" {
		t.Fatalf("Back after a fresh open left %v %q, want the Board, not a page of the closed history", app.Screen, app.Ref)
	}
}

func TestCollapsingTheSupervisorWritesNoPreferences(t *testing.T) {
	dir := t.TempDir()
	app := booted(t, dir)
	clock := newFakeClock()
	app.Clock = clock
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	app.OpenRef("t1")
	tt.Frame()
	click(t, tt, "Collapse the supervisor")
	mustText(t, tt, "Expand the supervisor")
	click(t, tt, "Expand the supervisor")
	click(t, tt, "Collapse the supervisor")
	clock.Advance(5 * time.Second)
	for len(posted) > 0 {
		(<-posted)()
	}
	if _, err := os.Stat(desktop.PrefsPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("collapsing the supervisor wrote prefs.json: %v", err)
	}
	if err := os.WriteFile(desktop.PrefsPath(dir), []byte(`{"zoom":1.25}`), 0o600); err != nil {
		t.Fatal(err)
	}
	click(t, tt, "Expand the supervisor")
	click(t, tt, "Collapse the supervisor")
	clock.Advance(5 * time.Second)
	for len(posted) > 0 {
		(<-posted)()
	}
	raw, err := os.ReadFile(desktop.PrefsPath(dir))
	if err != nil || string(raw) != `{"zoom":1.25}` {
		t.Fatalf("collapsing changed prefs.json: %q, %v", raw, err)
	}
}

func TestTheStripNamesThePage(t *testing.T) {
	app := newApp(t, viewFixture(t))
	app.OpenRef("t1")
	if got := desktop.ViewerName(app); got != "Fix login" {
		t.Fatalf("the task page is named %q", got)
	}
	for ref, want := range map[string]string{"a1": "claude sonnet low", "r1": "Fix login", "d1": "Keep the old cookie name?"} {
		app.OpenRef(ref)
		if got := desktop.ViewerName(app); got != want {
			t.Fatalf("the %s page is named %q, want %q", ref, got, want)
		}
	}
	app.OpenRef("t9")
	if got := desktop.ViewerName(app); got != "t9" {
		t.Fatalf("a page not loaded is named %q, want its ref", got)
	}
}

func TestTheOpenCardIsInTheTreeOnce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
	}{{"wide", 1100}, {"narrow", 880}} {
		t.Run(tc.name, func(t *testing.T) {
			f := goldenFixture(t)
			f.fleets[0].Home = "/fleet"
			app := newApp(t, f)
			tt := sized(t, app, tc.width, 760, false)
			count := func() int {
				n := 0
				for _, s := range tt.Texts() {
					if s == "Open feedback" {
						n++
					}
				}
				return n
			}
			app.OpenRef("t1")
			tt.Frame()
			app.OpenBoardLink("/" + fleetID + "/task/d3")
			tt.Frame()
			if app.OpenErr == "" || app.Screen != desktop.TaskScreen {
				t.Fatalf("OpenErr = %q on screen %v", app.OpenErr, app.Screen)
			}
			if got := count(); got != 1 {
				t.Fatalf("the frame holds %d open cards with the viewer open, want 1", got)
			}
			mustText(t, tt, "This page did not open")
			click(t, tt, "Close the viewer")
			tt.Frame()
			if app.Screen != desktop.BoardScreen || app.OpenErr != "" {
				t.Fatalf("closing the viewer left %v with OpenErr %q", app.Screen, app.OpenErr)
			}
			app.OpenBoardLink("/" + fleetID + "/task/d3")
			tt.Frame()
			if app.OpenErr == "" {
				t.Fatal("the Board's failed open left no card")
			}
			if got := count(); got != 1 {
				t.Fatalf("the frame holds %d open cards with the viewer closed, want 1", got)
			}
			mustText(t, tt, "This page did not open")
		})
	}
}

func TestABoardDeliverableOpensInTheViewer(t *testing.T) {
	f := viewFixture(t)
	td := f.task[fleetID]
	td.Delivered = append(append([]desktop.Deliverable(nil), td.Delivered...),
		desktop.Deliverable{Ref: "t3", Kind: "link", Href: "/" + fleetID + "/task/t3"})
	f.task[fleetID] = td
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "open t3")
	if app.Screen != desktop.TaskScreen || app.Ref != "t3" {
		t.Fatalf("the Board deliverable left the viewer on %v %q", app.Screen, app.Ref)
	}
	if app.OpenErr != "" {
		t.Fatalf("the Board deliverable ran hand: %q", app.OpenErr)
	}
	if got := tt.OpenedURLs(); len(got) != 0 {
		t.Fatalf("the Board deliverable reached the browser: %q", got)
	}
}

func TestTheTaskPageKeepsTwoColumnsFromAWorkPaneOf1000(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTesterAt(app, 1800, 900)
	click(t, tt, "Fix login")
	main, ok := tt.Find("Reports")
	if !ok {
		t.Fatal("no Reports head")
	}
	side, ok := tt.Find("Delivered")
	if !ok {
		t.Fatal("no Delivered aside")
	}
	if side.X-main.X < 500 {
		t.Fatalf("main at %v, aside at %v, want two columns", main, side)
	}
	app2 := newApp(t, viewFixture(t))
	tt2 := newTesterAt(app2, 1690, 900)
	click(t, tt2, "Fix login")
	main2, _ := tt2.Find("Reports")
	side2, _ := tt2.Find("Delivered")
	if side2.X != main2.X || side2.Y >= main2.Y {
		t.Fatalf("main at %v, aside at %v, want one column with the aside first", main2, side2)
	}
}

func TestTheTaskHeadUsesTheBoardsSlugType(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	for _, text := range []string{"hand", "plan p1"} {
		r, ok := tt.Find(text)
		if !ok {
			t.Fatalf("no %q in the task head", text)
		}
		if r.H != kit.Meta.Line {
			t.Errorf("%q is %.0f high, want the Board's 12/18 slug", text, r.H)
		}
	}
}

func TestTheListsCarryAHairlineAboveTheirFirstRow(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTesterAt(app, 1800, 1600)
	click(t, tt, "Fix login")
	img := tt.Image()
	line := kit.ColorsFor(false).Line
	for _, text := range []string{"Rename the helper?", "decision asked"} {
		r, ok := tt.Find(text)
		if !ok {
			t.Fatalf("no %q", text)
		}
		found := false
		for y := int(r.Y) - 1; y >= int(r.Y)-14; y-- {
			if rgbIs(img.RGBAAt(int(r.X)+2, y), line) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no hairline above the first row at %q", text)
		}
	}
}

func TestTheWaitingListSharesTheHeadsRule(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTesterAt(app, 1100, 1600)
	click(t, tt, "Fix login")
	badge, ok := tt.Find("DECISION")
	if !ok {
		t.Fatal("no waiting row")
	}
	img := tt.Image()
	line := kit.ColorsFor(false).Line
	var at []int
	for y := int(badge.Y) - 40; y < int(badge.Y); y++ {
		if rgbIs(img.RGBAAt(int(badge.X)+4, y), line) {
			at = append(at, y)
		}
	}
	if len(at) != 1 {
		t.Fatalf("the band above the waiting list has hairlines at %v, want the head's rule alone", at)
	}
}

func TestATaskWithNoDecisionsDrawsNoList(t *testing.T) {
	app := newApp(t, viewFixtureNoDecisions(t))
	tt := newTesterAt(app, 1100, 1600)
	click(t, tt, "Fix login")
	mustText(t, tt, "No decisions yet.")
	quiet, ok := tt.Find("No decisions yet.")
	if !ok {
		t.Fatal("no No decisions yet.")
	}
	events, ok := tt.Find("Events")
	if !ok {
		t.Fatal("no Events head")
	}
	img := tt.Image()
	for y := int(quiet.Y+quiet.H) + 1; y < int(events.Y); y++ {
		for _, x := range []int{60, 300, 600} {
			if rgbIs(img.RGBAAt(x, y), kit.ColorsFor(false).Line) {
				t.Fatalf("a list's hairline at %d,%d under No decisions yet.", x, y)
			}
		}
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
	for status, want := range map[string]ui.Color{"done": k.PassBG, "stuck": k.FailBG, "progress": k.WaitBG} {
		r, ok := tt.Find(status)
		if !ok {
			t.Fatalf("no %s badge", status)
		}
		if got := tt.Image().RGBAAt(int(r.X)+2, int(r.Y)+2); !rgbIs(got, want) {
			t.Errorf("the %s badge is %v, want %v", status, got, want)
		}
	}
}

func TestMarkReadFromTheCardLeavesItsReceipt(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTesterAt(app, 1800, 900)
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

func TestARefusedMarkReadShowsInTheToastAtTheScrollItActedAt(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	tt := newTesterAt(app, 1800, 900)
	click(t, tt, "Fix login")
	tt.Scroll(400, 400, 0, 240)
	tt.Frame()
	button, ok := tt.Find("Mark read r2")
	if !ok || button.Y < 0 || button.Y+button.H > 900 {
		t.Fatalf("the r2 button is at %v, not on screen after the scroll", button)
	}
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "report r2 is already acknowledged"}
	tt.ClickAt(button.X+button.W/2, button.Y+button.H/2)
	tt.Frame()
	refusal, ok := tt.Find("The action did not go through")
	if !ok {
		t.Fatal("the refusal is not in the toast")
	}
	if refusal.Y < 0 || refusal.Y+refusal.H > 900 {
		t.Fatalf("the refusal is at %v, outside the scroll position the click acted at", refusal)
	}
	mustText(t, tt, "report r2 is already acknowledged", "Blocked on the docs")
	if _, ok := tt.Find("Mark read r2"); !ok {
		t.Fatal("the refusal removed the Mark read button")
	}
	mustNotText(t, tt, "r2 marked read")
}

func TestAnUnreadAnswerSaysSoAndTheCardCatchesUp(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	tt := newTesterAt(app, 1800, 900)
	click(t, tt, "Fix login")
	f.action = &desktop.AppliedUnread{Err: errors.New("the board's /report/r1/ack data: unexpected end of JSON input")}
	td := f.task[fleetID]
	td.Reports = append([]desktop.Report(nil), td.Reports...)
	td.Reports[0].AckedAt, td.Reports[0].AckedBy = "2026-11-01T05:00:00Z", "operator (desktop)"
	td.Unread = nil
	f.task[fleetID] = td
	click(t, tt, "Mark read r1")
	tt.Frame()
	mustText(t, tt, "The answer could not be read", "the card refreshes itself", "read by operator (desktop)")
	mustNotText(t, tt, "The action did not go through")
	mustNotText(t, tt, "unexpected end of JSON input")
	if _, ok := tt.Find("Mark read r1"); ok {
		t.Fatal("the refreshed card still shows the Mark read button")
	}
}

func TestMarkReadIsDisabledWhileInFlight(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	tt := newTesterAt(app, 1800, 900)
	click(t, tt, "Fix login")
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 1)
	tt.Scroll(400, 400, 0, 200)
	tt.Frame()
	if r, ok := tt.Find("Mark read r2"); !ok || r.H == 0 {
		t.Fatalf("the second card is not on screen: %v", r)
	}
	click(t, tt, "Mark read r1")
	mustText(t, tt, "Marking read…")
	if !desktop.AckDisabled(app, "r1") {
		t.Fatal("the card in flight is not disabled")
	}
	if desktop.AckDisabled(app, "r2") || desktop.AckDisabled(app, "r3") {
		t.Fatal("a card besides the one in flight is disabled")
	}
	click(t, tt, "Mark read r2")
	click(t, tt, "Mark read r1")
	select {
	case <-f.actionEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the acknowledgement never started")
	}
	if got := f.count("ack:r1"); got != 1 {
		t.Fatalf("%d acknowledgements of r1 while in flight, want 1", got)
	}
	if got := f.count("ack:r2"); got != 0 {
		t.Fatalf("%d acknowledgements of r2 while r1 is in flight, want 0", got)
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
	tt := newTesterAt(app, 1800, 900)
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

func TestTheViewerKeepsTheTasksRegionLive(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := newTester(app)
	click(t, tt, "Fix login")
	ts := f.tasks[fleetID]
	ts.Groups = append([]desktop.TaskGroup(nil), ts.Groups...)
	ts.Groups[0].Tasks = append([]desktop.TaskRow(nil), ts.Groups[0].Tasks...)
	ts.Groups[0].Tasks[0].Title = "Fix login, finished elsewhere"
	f.tasks[fleetID] = ts
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	if f.pollScreen != desktop.TaskScreen || f.pollRef != "t1" {
		t.Fatalf("the open viewer stopped asking for its task: %v %q", f.pollScreen, f.pollRef)
	}
	if got := app.Tasks.Groups[0].Tasks[0].Title; got != "Fix login, finished elsewhere" {
		t.Fatalf("the poll did not hold the new task while the viewer is open: %q", got)
	}
	click(t, tt, "Close the viewer")
	tt.Frame()
	mustText(t, tt, "Fix login, finished elsewhere")
}

func TestAPushedPageAndTheCloseStartAtTheTopAndBackRestoresTheScroll(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	tt := newTesterAt(app, 1800, 420)
	tt.Scroll(300, 300, 0, 120)
	boardY, boardMax := desktop.BoardScroll(app)
	if boardMax == 0 || boardY == 0 {
		t.Fatalf("the Board does not scroll: y=%v max=%v", boardY, boardMax)
	}
	app.OpenRef("t1")
	scrollBoth := func() (float32, float32) {
		tt.Frame()
		tt.Scroll(400, 300, 0, 320)
		tt.Frame()
		y, max := desktop.ViewerScroll(app)
		if max == 0 || y == 0 {
			t.Fatalf("the page does not scroll: y=%v max=%v", y, max)
		}
		tt.Scroll(900, 300, 0, 320)
		tt.Frame()
		ay, amax := desktop.AsideScroll(app)
		if amax == 0 || ay == 0 {
			t.Fatalf("the aside does not scroll: y=%v max=%v", ay, amax)
		}
		return y, ay
	}
	assertAt := func(what string, page, aside float32) {
		t.Helper()
		tt.Frame()
		if y, _ := desktop.ViewerScroll(app); y != page {
			t.Errorf("%s puts the page scroll at %v, want %v", what, y, page)
		}
		if y, _ := desktop.AsideScroll(app); y != aside {
			t.Errorf("%s puts the aside at %v, want %v", what, y, aside)
		}
	}
	page, aside := scrollBoth()
	app.OpenRef("a1")
	if app.Screen != desktop.AttemptScreen {
		t.Fatalf("a1 opened %v, want the attempt page", app.Screen)
	}
	assertAt("a pushed page", 0, 0)
	app.Back()
	assertAt("Back", page, aside)
	app.Back()
	tt.Frame()
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("the second Back left %v, want the Board", app.Screen)
	}
	if y, _ := desktop.BoardScroll(app); y != boardY {
		t.Fatalf("the Board came back at %v, want the scroll it left at %v", y, boardY)
	}
	app.OpenRef("t1")
	assertAt("a page opened again", 0, 0)
	scrollBoth()
	app.CloseViewer()
	assertAt("the close", 0, 0)
}

func TestTheSupervisorCollapsesToAStripAndExpandsAgain(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	before, _ := tt.Find("Close the viewer")
	mustText(t, tt, "Message the supervisor")
	click(t, tt, "Collapse the supervisor")
	mustNotText(t, tt, "Message the supervisor")
	mustText(t, tt, "Expand the supervisor")
	after, _ := tt.Find("Close the viewer")
	if after.X+after.W < before.X+before.W+300 {
		t.Fatalf("the page's strip ends at %v after collapsing, was %v: the work pane did not take the room", after.X+after.W, before.X+before.W)
	}
	toggle, _ := tt.Find("Expand the supervisor")
	if toggle.W > float32(kit.SupStrip) || toggle.X+toggle.W < 1090 {
		t.Fatalf("the toggle is %v, want it inside the 40px strip at the right edge", toggle)
	}
	if got := tt.Image().RGBAAt(1080, 400); !rgbIs(got, kit.ColorsFor(false).Panel) {
		t.Fatalf("the strip paints %v, want the panel", got)
	}
	click(t, tt, "Expand the supervisor")
	mustText(t, tt, "Message the supervisor", "Collapse the supervisor")
	back, _ := tt.Find("Close the viewer")
	if back.X != before.X {
		t.Fatalf("the page's strip ends at %v after expanding, want %v", back.X, before.X)
	}
}

func TestTheCollapsedSupervisorIsRememberedAcrossPagesAndShowsItsState(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "Collapse the supervisor")
	click(t, tt, "attempt a1")
	mustText(t, tt, "Expand the supervisor")
	mustNotText(t, tt, "Message the supervisor")
	click(t, tt, "Close the viewer")
	mustText(t, tt, "Message the supervisor")
	mustNotText(t, tt, "Expand the supervisor")
	click(t, tt, "Fix login")
	mustText(t, tt, "Expand the supervisor")
	mustNotText(t, tt, "Message the supervisor")
	strip, ok := tt.Find("Supervisor " + app.Status.Supervisor.Ref + " working")
	if !ok || strip.W != float32(kit.SupStrip) {
		t.Fatalf("the strip is %v, want a named 40px strip with the supervisor's state", strip)
	}
	mustText(t, tt, app.Status.Supervisor.Ref)
}
func TestClosingAfterTheOpenerVanishesFocusesTheBoard(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := newTester(app)
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Open d1")
	st := f.status[fleetID]
	st.Needs.Items = slices.DeleteFunc(append([]desktop.Need(nil), st.Needs.Items...),
		func(n desktop.Need) bool { return n.Ref == "d1" })
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustNotText(t, tt, "Open d1")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("Escape left the viewer on %v", app.Screen)
	}
	if !tt.Focused("The board") {
		t.Fatal("the focus went nowhere when the opener was gone")
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
			tt.Scroll(400, 400, 0, 420)
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
		{"collapsed", func(t *testing.T, f *fixture, app *desktop.App, tt *ui.Tester) func() {
			click(t, tt, "Fix login")
			click(t, tt, "Collapse the supervisor")
			return nil
		}},
		{"stack", func(t *testing.T, f *fixture, app *desktop.App, tt *ui.Tester) func() {
			click(t, tt, "Fix login")
			click(t, tt, "attempt a1")
			return nil
		}},
		{"attempt", func(t *testing.T, f *fixture, app *desktop.App, tt *ui.Tester) func() {
			withAttemptTranscript(f)
			app.OpenRef("a1")
			return nil
		}},
		{"decision", func(t *testing.T, f *fixture, app *desktop.App, tt *ui.Tester) func() {
			d := f.decision[fleetID]
			d.Body = "CI broke when luvus 0.15 changed the output of luvus session list.\n\n" + d.Body
			d.Options[0].Recommended = true
			d.Options[0].Note = "the output keeps its shape"
			d.Options[1].Note = "the release notes change with it"
			f.decision[fleetID] = d
			app.OpenRef("d1")
			return nil
		}},
		{"answered", func(t *testing.T, f *fixture, app *desktop.App, tt *ui.Tester) func() {
			withAnsweredDecision(f)
			app.OpenRef("d1")
			return nil
		}},
		{"report", func(t *testing.T, f *fixture, app *desktop.App, tt *ui.Tester) func() {
			click(t, tt, "Fix login")
			tt.Scroll(400, 400, 0, 760)
			tt.Frame()
			click(t, tt, "open report r3")
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
	td.Reports = append([]desktop.Report(nil), td.Reports...)
	td.Reports[0].Summary = "sum\u202Emary"
	if td.Plan != nil {
		plan := *td.Plan
		plan.Body = "plan\u202Ebody"
		td.Plan = &plan
	}
	td.Delivered = append([]desktop.Deliverable(nil), td.Delivered...)
	td.Delivered[0].Ref = "https://example.com/\u202Epath"
	td.Events = append([]desktop.Event(nil), td.Events...)
	td.Events[0].Detail = "x\u2066y"
	f.task[fleetID] = td
	app := newApp(t, f)
	tt := newTesterAt(app, 1100, 1600)
	click(t, tt, "Fix login")
	mustText(t, tt, "Fix U+202EloginU+202C", "aU+0000b", "hanU+200Ed", "sumU+202Emary",
		"planU+202Ebody", "https://example.com/U+202Epath", "xU+2066y")
	mustNotText(t, tt, "Fix \u202Elogin")
	mustNotText(t, tt, "sum\u202Emary")
}

func TestTheStripNamesTheObjectOnce(t *testing.T) {
	app := newApp(t, viewFixture(t))
	tt := newTesterAt(app, 1800, 900)
	click(t, tt, "Fix login")
	n := 0
	for _, s := range tt.Texts() {
		if s == "t1" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the header names t1 %d times, want once", n)
	}
}

func TestBackWaitsForAPageThatIsStillLoadingBeforeItRestoresTheScroll(t *testing.T) {
	f := viewFixture(t)
	sub := f.task[fleetID]
	sub.Task.Ref, sub.Task.Title = "t3", "Rotate the docs"
	f.taskExtra = map[string]desktop.TaskDetail{"t3": sub}
	app := newApp(t, f)
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	settle := func() {
		t.Helper()
		for app.Loading {
			waitPost(t, posted)()
		}
	}
	tt := newTesterAt(app, 1800, 420)
	app.OpenRef("t1")
	settle()
	tt.Frame()
	tt.Scroll(400, 300, 0, 320)
	tt.Frame()
	want, max := desktop.ViewerScroll(app)
	if max == 0 || want == 0 {
		t.Fatalf("the page does not scroll: y=%v max=%v", want, max)
	}
	app.OpenRef("t3")
	settle()
	tt.Frame()
	app.Back()
	tt.Frame()
	if app.Ref != "t1" || !app.Loading {
		t.Fatalf("Back left %q with Loading=%v, want t1 still loading", app.Ref, app.Loading)
	}
	settle()
	tt.Frame()
	if y, _ := desktop.ViewerScroll(app); y != want {
		t.Fatalf("the page came back at %v, want the scroll it left at %v", y, want)
	}
}

func TestAFailedSupervisorControlShowsOnADocumentPage(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	tt := newTesterAt(app, 1800, 900)
	app.OpenDoc("reports/t1/design.md")
	tt.Frame()
	if app.Screen != desktop.DocScreen {
		t.Fatalf("OpenDoc opened %v, want the document page", app.Screen)
	}
	f.action = errors.New("the board did not answer")
	app.Supervise("interrupt")
	tt.Frame()
	if app.ActionErr == "" {
		t.Fatalf("the failed control left no error")
	}
	mustText(t, tt, app.ActionErr)
}

func TestBackKeepsTheScrollWhenTheReportRevealWasPending(t *testing.T) {
	f := viewFixture(t)
	app := newApp(t, f)
	tt := newTesterAt(app, 1800, 900)
	app.OpenRef("t1")
	tt.Frame()
	tt.Scroll(400, 400, 0, 320)
	tt.Frame()
	click(t, tt, "open report r3")
	tt.Frame()
	tt.Frame()
	if desktop.PageReport(app) != "r3" {
		t.Fatalf("the report did not open at r3: %q", desktop.PageReport(app))
	}
	revealed, max := desktop.ViewerScroll(app)
	if max == 0 || revealed == 0 {
		t.Fatalf("the report did not reveal itself: y=%v max=%v", revealed, max)
	}
	tt.Scroll(400, 400, 0, -60)
	tt.Frame()
	want, _ := desktop.ViewerScroll(app)
	if want == revealed {
		t.Fatalf("the scroll did not move off the reveal: y=%v", want)
	}
	app.OpenRef("a1")
	tt.Frame()
	app.Back()
	tt.Frame()
	tt.Frame()
	if y, _ := desktop.ViewerScroll(app); y != want {
		t.Fatalf("Back put the page at %v, want %v where it left", y, want)
	}
}

func TestAPageOpenedWhileBackIsStillLoadingKeepsTheScrollBackWillRestore(t *testing.T) {
	f := viewFixture(t)
	sub := f.task[fleetID]
	sub.Task.Ref, sub.Task.Title = "t3", "Rotate the docs"
	f.taskExtra = map[string]desktop.TaskDetail{"t3": sub}
	app := newApp(t, f)
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	settle := func() {
		t.Helper()
		for app.Loading {
			waitPost(t, posted)()
		}
	}
	tt := newTesterAt(app, 1800, 420)
	app.OpenRef("t1")
	settle()
	tt.Frame()
	tt.Scroll(400, 300, 0, 320)
	tt.Frame()
	want, max := desktop.ViewerScroll(app)
	if max == 0 || want == 0 {
		t.Fatalf("the page does not scroll: y=%v max=%v", want, max)
	}
	app.OpenRef("t3")
	settle()
	tt.Frame()
	app.Back()
	tt.Frame()
	if app.Ref != "t1" || !app.Loading {
		t.Fatalf("Back left %q with Loading=%v, want t1 still loading", app.Ref, app.Loading)
	}
	app.OpenDoc("reports/t1/design.md")
	for app.Loading {
		waitPost(t, posted)()
	}
	tt.Frame()
	app.Back()
	settle()
	tt.Frame()
	tt.Frame()
	if app.Ref != "t1" {
		t.Fatalf("Back left %q, want t1", app.Ref)
	}
	if y, _ := desktop.ViewerScroll(app); y != want {
		t.Fatalf("the page came back at %v, want the scroll it left at %v", y, want)
	}
}
