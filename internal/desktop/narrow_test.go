package desktop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func dragDivider(t *testing.T, tt *ui.Tester, to float32) {
	t.Helper()
	div, ok := tt.Find("Divider")
	if !ok {
		t.Fatal("no divider")
	}
	x, y := div.X+div.W/2, float32(300)
	tt.Press(x, y)
	tt.Move(to, y)
	tt.Release(to, y)
	tt.Frame()
	tt.Frame()
}

func TestTheBoardBreakpointSitsAtNineHundred(t *testing.T) {
	for _, tc := range []struct {
		width int
		wide  bool
	}{{899, false}, {900, true}} {
		t.Run(fmt.Sprint(tc.width), func(t *testing.T) {
			tt := sized(t, newApp(t, goldenFixture(t)), tc.width, 760, false)
			_, rail := tt.Find("Pages and fleets")
			_, tabs := tt.Find("Tabs")
			_, divider := tt.Find("Divider")
			if rail != tc.wide || tabs == tc.wide || divider != tc.wide {
				t.Fatalf("at %d: rail %v, tabs %v, divider %v; want the rail %v, the tabs %v and the divider %v",
					tc.width, rail, tabs, divider, tc.wide, !tc.wide, tc.wide)
			}
			if tc.wide {
				mustText(t, tt, "Needs", "Tasks", "Supervisor", "Fix login")
				return
			}
			mustText(t, tt, "Needs", "Tasks", "Chat", "Failed")
			mustNotText(t, tt, "Supervisor")
			mustNotText(t, tt, "Fix login")
		})
	}
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, 900, 760, false)
	tt.SetSize(899, 760)
	if _, ok := tt.Find("Pages and fleets"); ok {
		t.Error("the rail survived 899")
	}
	if _, ok := tt.Find("Divider"); ok {
		t.Error("the divider survived 899")
	}
	if _, ok := tt.Find("Tabs"); !ok {
		t.Error("899 shows no tabs")
	}
	if tt.HasText("Supervisor") {
		t.Error("the chat sits beside the work column below 900")
	}
	tt.SetSize(900, 760)
	if _, ok := tt.Find("Pages and fleets"); !ok {
		t.Error("900 shows no rail")
	}
	if _, ok := tt.Find("Divider"); !ok {
		t.Error("900 shows no divider")
	}
	if _, ok := tt.Find("Tabs"); ok {
		t.Error("the tabs survived 900")
	}
}

func TestTheTabsHoldTheirPanelAcrossARepaintAndNeverSwitchThemselves(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	tt := sized(t, app, 880, 760, false)
	if app.Tab != "needs" {
		t.Fatalf("a fresh window with something waiting opens on %q, want needs", app.Tab)
	}
	click(t, tt, "Chat")
	mustText(t, tt, "Message the supervisor")
	click(t, tt, "Message the supervisor")
	tt.Type("a draft across the tick")
	st := f.status[fleetID]
	st.Needs.Waiting = 3
	st.Needs.Worst = "fail"
	f.status[fleetID] = st
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	if app.Tab != "chat" {
		t.Fatalf("a waiting item moved the tab to %q, want it left on chat", app.Tab)
	}
	mustText(t, tt, "Message the supervisor")
	mustNotText(t, tt, "Flaky test in the nightly pipeline")
	if app.Draft != "a draft across the tick" {
		t.Fatalf("the repaint changed the draft to %q", app.Draft)
	}
	click(t, tt, "Needs")
	mustText(t, tt, "Failed")
	mustNotText(t, tt, "Message the supervisor")
	click(t, tt, "Tasks")
	mustText(t, tt, "Fix login")
}

func TestTheSheetOpensFromTheMastheadAndClosesOnEscapeOrAPick(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, 880, 760, false)
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Menu") {
		t.Fatalf("the first tab stop is not the menu button: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if !desktop.SheetOpen(app) {
		t.Fatal("Enter on the menu button opened nothing")
	}
	if _, ok := tt.Find("Pages and fleets"); !ok {
		t.Fatal("the sheet is not drawn")
	}
	mustText(t, tt, "Board", "Screen", "Phone", "golden", "second")
	if _, ok := tt.Find("Show labels"); ok {
		t.Error("the rail is drawn beside the sheet")
	}
	if !tt.Focused("Board") {
		t.Errorf("the focus is not in the sheet: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Tasks") {
		t.Errorf("Tab did not walk the sheet's items: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Screen") {
		t.Errorf("Tab did not walk the sheet's items: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if desktop.SheetOpen(app) {
		t.Fatal("Escape left the sheet open")
	}
	if _, ok := tt.Find("Pages and fleets"); ok {
		t.Error("the sheet is still drawn after Escape")
	}
	click(t, tt, "Menu")
	click(t, tt, "second")
	if app.Fleet.ID != secondID {
		t.Fatalf("the pick opened %s, want %s", app.Fleet.ID, secondID)
	}
	if desktop.SheetOpen(app) {
		t.Fatal("a pick left the sheet open")
	}
	if _, ok := tt.Find("Pages and fleets"); ok {
		t.Error("the sheet is still drawn after a pick")
	}
}

func TestTheDividerKeepsTheClampsAtTheBreakpoint(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, 900, 760, false)
	dragDivider(t, tt, 1439)
	if app.Chat < 359 || app.Chat > 361 {
		t.Fatalf("dragging far right left the chat %.0f wide, want 360", app.Chat)
	}
	dragDivider(t, tt, 1)
	if app.Chat < 403 || app.Chat > 405 {
		t.Fatalf("dragging far left left the chat %.0f wide, want the room's widest 404", app.Chat)
	}
	if _, ok := tt.Find("Divider"); !ok {
		t.Fatal("the divider went away")
	}
	if got := desktop.ChatWidth(0, 700, 700); got != 360 {
		t.Fatalf("a window too narrow for both gives the chat %.0f, want the 360 floor", got)
	}
	if got := desktop.ChatWidth(5000, 700, 700); got != 360 {
		t.Fatalf("a stored width in a window too narrow for both gives %.0f, want the 360 floor", got)
	}
	app.Chat = 5000
	tt.SetSize(900, 760)
	tt.Frame()
	chat := float32(900) - chatLeft(t, tt)
	if chat < 403 || chat > 405 {
		t.Fatalf("a stored 5000 px chat renders %.0f wide at 900, want the room's widest 404", chat)
	}
}

func TestThePaneWidthSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	app := booted(t, dir)
	tt := sized(t, app, 1440, 900, false)
	div, ok := tt.Find("Divider")
	if !ok {
		t.Fatal("no divider")
	}
	dragDivider(t, tt, div.X-100)
	want := app.Chat
	if want < 639 || want > 649 {
		t.Fatalf("the dragged chat is %.0f wide, want about 647", want)
	}
	raw, err := os.ReadFile(desktop.PrefsPath(dir))
	if err != nil {
		t.Fatalf("no preferences file: %v", err)
	}
	var stored struct {
		Chat float32 `json:"chat"`
	}
	if json.Unmarshal(raw, &stored) != nil || stored.Chat != want {
		t.Fatalf("preferences hold %q, want the chat width %.0f", raw, want)
	}
	again := booted(t, dir)
	if again.Chat != want {
		t.Fatalf("the restarted window holds the chat at %.0f, want %.0f", again.Chat, want)
	}
	tt = sized(t, again, 1440, 900, false)
	if got := float32(1440) - chatLeft(t, tt); got < want-2 || got > want+2 {
		t.Fatalf("the restarted window's chat is %.0f wide, want %.0f", got, want)
	}
}

func TestAStoredWidthOutsideTheClampsIsCorrected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(desktop.PrefsPath(dir), []byte(`{"chat":200}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if app := booted(t, dir); app.Chat != 360 {
		t.Fatalf("a stored chat of 200 loads as %.0f, want the 360 floor", app.Chat)
	}
	if err := os.WriteFile(desktop.PrefsPath(dir), []byte(`{"chat":5000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := booted(t, dir)
	if app.Chat != 5000 {
		t.Fatalf("a stored chat of 5000 loads as %.0f, want it kept for a wider window", app.Chat)
	}
	tt := sized(t, app, 900, 760, false)
	if chat := float32(900) - chatLeft(t, tt); chat < 403 || chat > 405 {
		t.Fatalf("the stored 5000 px chat renders %.0f wide at 900, want 404", chat)
	}
}

func TestTheWindowSizeSurvivesARestartAndFitsTheScreen(t *testing.T) {
	dir := t.TempDir()
	app := booted(t, dir)
	if w, h := app.WindowSize(1920, 1055); w != windowWidth || h != windowHeight {
		t.Fatalf("a fresh window opens %dx%d, want the default %dx%d", w, h, windowWidth, windowHeight)
	}
	app.SetWindowSize(1234, 800)
	again := booted(t, dir)
	if w, h := again.WindowSize(1920, 1055); w != 1234 || h != 800 {
		t.Fatalf("the restarted window opens %dx%d, want 1234x800", w, h)
	}
	again.SetWindowSize(4000, 3000)
	if w, h := again.WindowSize(1920, 1055); w != 1920 || h != 1055 {
		t.Fatalf("a 4000x3000 window on a 1920x1055 work area opens %dx%d", w, h)
	}
	if w, h := again.WindowSize(0, 0); w != 4000 || h != 3000 {
		t.Fatalf("with no work area the window opens %dx%d, want the stored 4000x3000", w, h)
	}
	again.SetWindowSize(200, 100)
	if w, h := again.WindowSize(1920, 1055); w != desktop.MinWindowWidth || h != desktop.MinWindowHeight {
		t.Fatalf("a 200x100 window opens %dx%d, want the window minimum", w, h)
	}
}

func TestAResizeBurstWritesThePreferencesOnce(t *testing.T) {
	dir := t.TempDir()
	app := booted(t, dir)
	clock := newFakeClock()
	app.Clock = clock
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	for _, size := range [][2]int{{1200, 800}, {1210, 810}, {1234, 812}} {
		app.SetWindowSize(size[0], size[1])
	}
	if _, err := os.Stat(desktop.PrefsPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("the burst wrote the preferences before it settled: %v", err)
	}
	clock.Advance(299 * time.Millisecond)
	if len(posted) != 0 {
		t.Fatalf("the burst posted a save %d ms into the delay", 299)
	}
	clock.Advance(time.Millisecond)
	if len(posted) != 1 {
		t.Fatalf("the burst posted %d saves, want one", len(posted))
	}
	(<-posted)()
	raw, err := os.ReadFile(desktop.PrefsPath(dir))
	if err != nil {
		t.Fatalf("no preferences file after the burst: %v", err)
	}
	var stored struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Width != 1234 || stored.Height != 812 {
		t.Fatalf("preferences hold %q, want 1234x812", raw)
	}
	if w, h := app.WindowSize(1920, 1055); w != 1234 || h != 812 {
		t.Fatalf("the window holds %dx%d, want 1234x812", w, h)
	}
}

func TestCloseFlushesThePendingResizeOnceAndLeavesNoTimer(t *testing.T) {
	dir := t.TempDir()
	app := booted(t, dir)
	clock := newFakeClock()
	app.Clock = clock
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	app.SetWindowSize(1200, 800)
	app.SetWindowSize(1300, 850)
	app.Close()
	if got := clock.delay(); got != 0 {
		t.Fatalf("closing left a timer with %v to run", got)
	}
	if len(posted) != 0 {
		t.Fatalf("closing posted %d saves, want the flush to write inline", len(posted))
	}
	raw, err := os.ReadFile(desktop.PrefsPath(dir))
	if err != nil {
		t.Fatalf("no preferences file after the close: %v", err)
	}
	var stored struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Width != 1300 || stored.Height != 850 {
		t.Fatalf("preferences hold %q, want 1300x850", raw)
	}
}

func TestASavePostedBeforeCloseDoesNotWriteAfterIt(t *testing.T) {
	dir := t.TempDir()
	app := booted(t, dir)
	clock := newFakeClock()
	app.Clock = clock
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	app.SetWindowSize(1300, 850)
	clock.Advance(300 * time.Millisecond)
	if len(posted) != 1 {
		t.Fatalf("the resize posted %d saves, want one", len(posted))
	}
	app.Close()
	if err := os.Remove(desktop.PrefsPath(dir)); err != nil {
		t.Fatal(err)
	}
	(<-posted)()
	if _, err := os.Stat(desktop.PrefsPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("a save posted before the close wrote after the window was gone: %v", err)
	}
}

func TestAPreferencesFileThatCannotBeReadLeavesTheDefaults(t *testing.T) {
	dir := t.TempDir()
	check := func(app *desktop.App) {
		t.Helper()
		if app.Zoom != 1 || app.Chat != 0 {
			t.Fatalf("zoom %v chat %v, want the defaults 1 and 0", app.Zoom, app.Chat)
		}
		if w, h := app.WindowSize(1920, 1055); w != windowWidth || h != windowHeight {
			t.Fatalf("the window opens %dx%d, want the default %dx%d", w, h, windowWidth, windowHeight)
		}
	}
	check(booted(t, dir))
	for _, raw := range []string{
		`{not json`,
		`{"zoom":1.5,"chat":700,`,
		`{"zoom":1.5,"chat":"x"}`,
		`{"zoom":1.5}` + strings.Repeat(" ", 1<<10),
	} {
		if err := os.WriteFile(desktop.PrefsPath(dir), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		check(booted(t, dir))
	}
	if _, err := os.Stat(desktop.PrefsPath(dir)); err != nil {
		t.Fatalf("the preferences fixture is missing: %v", err)
	}
}

func TestAnOpenedRefOpensTheTabThatHoldsIt(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, 880, 760, false)
	click(t, tt, "Chat")
	if err := app.OpenLink("handboard://open/" + fleetID + "/task/t4"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Tab != "tasks" {
		t.Fatalf("a task link left the tab on %q, want tasks", app.Tab)
	}
	if _, ok := tt.Find("Close the viewer"); !ok {
		t.Fatal("the link opened no viewer")
	}
	click(t, tt, "Close the viewer")
	mustText(t, tt, "Tasks", "Fix login")
	mustNotText(t, tt, "Message the supervisor")
	click(t, tt, "Chat")
	app.OpenRef("d1")
	tt.Frame()
	if app.Tab != "needs" {
		t.Fatalf("a decision from Needs left the tab on %q, want needs", app.Tab)
	}
	click(t, tt, "Close the viewer")
	click(t, tt, "Chat")
	app.OpenRef("r9")
	tt.Frame()
	if app.Tab != "chat" {
		t.Fatalf("a ref no panel holds left the tab on %q, want it unchanged", app.Tab)
	}
}

func TestADeepLinkIntoARunningWindowSelectsTheTabThatHoldsIt(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	tt := sized(t, app, 880, 760, false)
	click(t, tt, "Chat")
	mustText(t, tt, "Message the supervisor")
	settle := func() {
		t.Helper()
		for app.Loading {
			waitPost(t, posted)()
		}
	}
	if err := app.OpenLink("handboard://open/" + fleetID + "/task/t4"); err != nil {
		t.Fatal(err)
	}
	settle()
	tt.Frame()
	if app.Tab != "tasks" {
		t.Fatalf("a task deep link into the running window left the tab on %q, want tasks", app.Tab)
	}
	if _, ok := tt.Find("Close the viewer"); !ok {
		t.Fatal("the link opened no viewer")
	}
	click(t, tt, "Close the viewer")
	settle()
	tt.Frame()
	if app.Tab != "tasks" {
		t.Fatalf("closing the viewer left the tab on %q, want tasks", app.Tab)
	}
	mustText(t, tt, "Tasks", "Fix login")
	mustNotText(t, tt, "Message the supervisor")
}

func TestAClosedViewerDropsTheDeepLinksOwedTab(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before func(app *desktop.App)
		open   func(app *desktop.App) error
		undo   func(app *desktop.App)
		screen desktop.Screen
	}{
		{"close", func(app *desktop.App) {}, func(app *desktop.App) error {
			return app.OpenLink("handboard://open/" + fleetID + "/task/t4")
		}, func(app *desktop.App) { app.CloseViewer() }, desktop.BoardScreen},
		{"back", func(app *desktop.App) { app.OpenRef("d1") }, func(app *desktop.App) error {
			app.OpenRef("t4")
			return nil
		}, func(app *desktop.App) { app.Back() }, desktop.DecisionScreen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			posted := make(chan func(), 16)
			app.Post = func(fn func()) { posted <- fn }
			tt := sized(t, app, 880, 760, false)
			settle := func() {
				t.Helper()
				for app.Loading {
					waitPost(t, posted)()
				}
			}
			tc.before(app)
			settle()
			click(t, tt, "Chat")
			if err := tc.open(app); err != nil {
				t.Fatal(err)
			}
			tc.undo(app)
			settle()
			tt.Frame()
			if app.Screen != tc.screen {
				t.Fatalf("the undo left the screen on %v, want %v", app.Screen, tc.screen)
			}
			if app.Tab != "chat" {
				t.Fatalf("a deep link undone before its load left the tab on %q, want chat", app.Tab)
			}
			mustText(t, tt, "Message the supervisor")
		})
	}
}

func TestAFailedTasksReadLeavesTheOpenTaskPageAlone(t *testing.T) {
	f := goldenFixture(t)
	td := f.task[fleetID]
	td.Task.Ref = "t4"
	f.task[fleetID] = td
	app := newApp(t, f)
	tt := sized(t, app, 880, 760, false)
	click(t, tt, "Chat")
	f.tasksErr = errors.New("the tasks read failed")
	if err := app.OpenLink("handboard://open/" + fleetID + "/task/t4"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Screen != desktop.TaskScreen || app.Ref != "t4" {
		t.Fatalf("the link landed on %v %q", app.Screen, app.Ref)
	}
	if app.Err != "" {
		t.Fatalf("a failed Tasks read set the page error: %q", app.Err)
	}
	mustText(t, tt, "Fleets / golden / t4")
	mustNotText(t, tt, "The page could not be read")
}

func TestAPendingTabWaitsForTheTasksReadThatFailed(t *testing.T) {
	f := goldenFixture(t)
	td := f.task[fleetID]
	td.Task.Ref = "t4"
	f.task[fleetID] = td
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	tt := sized(t, app, 880, 760, false)
	click(t, tt, "Chat")
	f.tasksErr = errors.New("the tasks read failed")
	if err := app.OpenLink("handboard://open/" + fleetID + "/task/t4"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Tab != "chat" {
		t.Fatalf("a failed Tasks read moved the tab to %q, want chat", app.Tab)
	}
	mustText(t, tt, "Fleets / golden / t4")
	f.tasksErr = nil
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	if app.Tab != "tasks" {
		t.Fatalf("the poll that restored Tasks left the tab on %q, want tasks", app.Tab)
	}
}

type statusOnlyPoll struct {
	desktop.ReadModel
	status desktop.Status
}

func (p *statusOnlyPoll) Poll(context.Context, string, desktop.Screen, string, desktop.PollCursors) (desktop.Poll, error) {
	return desktop.Poll{StatusFresh: true, StatusRead: true, ScreenRead: true, TimelineRead: true, Status: p.status}, nil
}

func TestAPendingTabResolvesWhenStatusNamesTheNeed(t *testing.T) {
	f := goldenFixture(t)
	model := &statusOnlyPoll{ReadModel: f}
	app := desktop.New(func() (desktop.ReadModel, error) { return model, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	clock := newFakeClock()
	app.Clock = clock
	tt := sized(t, app, 880, 760, false)
	click(t, tt, "Chat")
	f.tasksErr = errors.New("the tasks read failed")
	if err := app.OpenLink("handboard://open/" + fleetID + "/task/t4"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Tab != "chat" || desktop.TabFor(app) != "t4" {
		t.Fatalf("a failed Tasks read left the tab on %q at %q, want chat at t4",
			app.Tab, desktop.TabFor(app))
	}
	st := f.status[fleetID]
	st.Needs.Items = append(st.Needs.Items, desktop.Need{Kind: "decision", Ref: "d9",
		Decision: "d9", Task: "t4", Title: "A need for t4", Word: "DECISION", Tone: "wait"})
	st.Needs.Waiting = len(st.Needs.Items)
	model.status = st
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	if app.Tab != "needs" {
		t.Fatalf("a fresh Status naming the need left the tab on %q, want needs", app.Tab)
	}
	if got := desktop.TabFor(app); got != "" {
		t.Fatalf("the resolved tab kept the owed ref %q", got)
	}
}

func TestTheBoardErrorCardShowsOnNarrowChat(t *testing.T) {
	f := goldenFixture(t)
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	app := newApp(t, f)
	tt := sized(t, app, 880, 760, false)
	tt.Frame()
	mustText(t, tt, "The board could not be read", "the board is restarting", "Message the supervisor")
	st := f.status[fleetID]
	st.Needs.Items, st.Needs.Waiting = nil, 0
	f.status[fleetID] = st
	f.fail = nil
	click(t, tt, "Retry")
	tt.Frame()
	mustNotText(t, tt, "The board could not be read")
	mustNotText(t, tt, "the board is restarting")
	mustText(t, tt, "Message the supervisor")
}

func TestAFleetSwitchDropsAnOwedTab(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := sized(t, app, 880, 760, false)
	click(t, tt, "Chat")
	f.tasksErr = errors.New("the tasks read failed")
	if err := app.OpenLink("handboard://open/" + fleetID + "/task/t4"); err != nil {
		t.Fatal(err)
	}
	if app.Tab != "chat" || desktop.TabFor(app) != "t4" {
		t.Fatalf("the link left the tab on %q at %q, want chat at t4",
			app.Tab, desktop.TabFor(app))
	}
	f.tasksErr = nil
	app.SelectFleet(secondID)
	tt.Frame()
	if app.Tab != "chat" {
		t.Fatalf("the fleet switch moved the tab to %q", app.Tab)
	}
	if got := desktop.TabFor(app); got != "" {
		t.Fatalf("the fleet switch kept the owed ref %q", got)
	}
}

func TestTheOpenCardKeepsTheChatColumnsMeasure(t *testing.T) {
	f := goldenFixture(t)
	f.fleets[0].Home = "/fleet"
	app := newApp(t, f)
	t.Setenv("PATH", t.TempDir())
	tt := sized(t, app, 880, 760, false)
	click(t, tt, "Chat")
	app.OpenInBrowser()
	tt.Frame()
	if app.OpenErr == "" {
		t.Fatal("the failed open left no card")
	}
	card, ok := tt.Find("Open feedback")
	if !ok {
		t.Fatal("no open card")
	}
	composer, ok := tt.Find("Composer")
	if !ok {
		t.Fatal("no composer")
	}
	if card.X != composer.X || card.W != composer.W {
		t.Fatalf("the open card runs %.0f…%.0f, the chat column %.0f…%.0f",
			card.X, card.X+card.W, composer.X, composer.X+composer.W)
	}
}

func TestOpeningATaskKeepsTheWorkPanesScrollAboveTheBreakpoint(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, 1440, 900, false)
	tt.Scroll(300, 400, 0, 400)
	before, max := desktop.BoardScroll(app)
	if before == 0 || max == 0 {
		t.Fatalf("the work pane does not scroll at 1440: y=%v max=%v", before, max)
	}
	click(t, tt, "task t4")
	tt.Frame()
	if app.Tab != "tasks" {
		t.Fatalf("opening t4 left the tab on %q, want tasks", app.Tab)
	}
	if got, _ := desktop.BoardScroll(app); got != before {
		t.Errorf("opening a task moved the work pane from %v to %v", before, got)
	}
	click(t, tt, "Close the viewer")
	tt.Frame()
	if got, _ := desktop.BoardScroll(app); got != before {
		t.Errorf("closing the task moved the work pane from %v to %v", before, got)
	}
}

func TestPickingATabResetsTheWorkPanesScroll(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, 880, 480, false)
	click(t, tt, "Tasks")
	tt.Scroll(400, 400, 0, 400)
	if y, m := desktop.BoardScroll(app); y == 0 {
		t.Fatalf("the narrow Tasks tab does not scroll at 880x480: max %v", m)
	}
	click(t, tt, "Needs")
	tt.Frame()
	if y, _ := desktop.BoardScroll(app); y != 0 {
		t.Fatalf("picking a tab left the work pane at %v, want the top", y)
	}
}

func TestScenesNarrow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		for _, tc := range []struct {
			name   string
			width  int
			height int
			tab    string
		}{
			{"39-narrow-needs-880x760", 880, 760, ""},
			{"40-narrow-tasks-880x760", 880, 760, "Tasks"},
			{"41-narrow-chat-820x700", 820, 700, "Chat"},
		} {
			tt := sized(t, newApp(t, goldenFixture(t)), tc.width, tc.height, dark)
			if tc.tab != "" {
				click(t, tt, tc.tab)
			}
			if _, ok := tt.Find("Tabs"); !ok {
				t.Fatalf("%s: the masthead draws no tabs at %d px", tc.name, tc.width)
			}
			if _, ok := tt.Find("Pages and fleets"); ok {
				t.Fatalf("%s: the rail is drawn at %d px", tc.name, tc.width)
			}
			if _, ok := tt.Find("Divider"); ok {
				t.Fatalf("%s: the divider is drawn at %d px", tc.name, tc.width)
			}
			shotSized(t, tt, tc.name+"-"+theme, tc.width, tc.height)
		}
	}
}
