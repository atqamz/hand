package desktop_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

func sized(t *testing.T, app *desktop.App, width, height int, dark bool) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(app.View, width, height)
	tt.SetDark(dark)
	return tt
}

func chatLeft(t *testing.T, tt *ui.Tester) float32 {
	t.Helper()
	r, ok := tt.Find("Supervisor")
	if !ok {
		t.Fatal("no pane head")
	}
	return r.X
}

func TestScenesTwoPanes(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		chat          float32
	}{{1100, 760, 418}, {1440, 900, 547.2}} {
		for _, dark := range []bool{false, true} {
			name := fmt.Sprintf("%dx%d-light", tc.width, tc.height)
			if dark {
				name = fmt.Sprintf("%dx%d-dark", tc.width, tc.height)
			}
			t.Run(name, func(t *testing.T) {
				tt := sized(t, newApp(t, goldenFixture(t)), tc.width, tc.height, dark)
				mustText(t, tt, "Needs", "Tasks", "Fix login", "s1", "sonnet · low", "Interrupt", "Stop", "All good.", "Message")
				if got := float32(tc.width) - chatLeft(t, tt); got < tc.chat-2 || got > tc.chat+2 {
					t.Errorf("the chat is %.0f wide, want the Board's default %.0f", got, tc.chat)
				}
				for _, s := range []string{"Message the supervisor", "Send", "More", "Stop"} {
					r, ok := tt.Find(s)
					if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(tc.width) || r.Y+r.H > float32(tc.height) {
						t.Errorf("%q is at %v, outside the %dx%d window", s, r, tc.width, tc.height)
					}
				}
				shotSized(t, tt, "20-panes-"+name, tc.width, tc.height)
			})
		}
	}
}

func TestTheChatKeepsTheBoardsLimits(t *testing.T) {
	for _, tc := range []struct {
		width      int
		chat, work float32
	}{{900, 400, 444}, {1100, 418, 626}, {1440, 547.2, 836.8}, {2400, 720, 1624}, {800, 360, 384}} {
		tt := sized(t, newApp(t, goldenFixture(t)), tc.width, 700, false)
		chat := float32(tc.width) - chatLeft(t, tt)
		if chat < tc.chat-2 || chat > tc.chat+2 {
			t.Errorf("%d wide: the chat is %.0f, want %.0f", tc.width, chat, tc.chat)
		}
		if work := float32(tc.width) - kit.RailWidth - chat - 1; work < tc.work-2 {
			t.Errorf("%d wide: the work pane is %.0f, want at least %.0f", tc.width, work, tc.work)
		}
	}
}

func TestDraggingTheDividerResizesTheChatWithinTheLimits(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, 1440, 900, false)
	div, ok := tt.Find("Divider")
	if !ok {
		t.Fatal("no divider")
	}
	x, y := div.X+div.W/2, float32(400)
	drag := func(to float32) float32 {
		tt.Press(x, y)
		tt.Move(to, y)
		tt.Release(to, y)
		tt.Frame()
		tt.Frame()
		return 1440 - chatLeft(t, tt)
	}
	if got := drag(x - 100); got < 640 || got > 650 {
		t.Fatalf("dragging 100 left made the chat %.0f, want about 647", got)
	}
	div, _ = tt.Find("Divider")
	x = div.X + div.W/2
	if got := drag(x - 1000); got != 1440-56-440-1 && (got < 942 || got > 945) {
		t.Fatalf("dragging far left made the chat %.0f, want the work pane held at 440", got)
	}
	div, _ = tt.Find("Divider")
	x = div.X + div.W/2
	if got := drag(x + 2000); got < 359 || got > 361 {
		t.Fatalf("dragging far right made the chat %.0f, want 360", got)
	}
	if app.Chat < 359 || app.Chat > 361 {
		t.Fatalf("the remembered width is %.0f, want 360", app.Chat)
	}
}

func TestTheConversationOpensAtItsNewestEntryWithTheComposerPinned(t *testing.T) {
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	var entries []desktop.Entry
	for i := 60; i >= 1; i-- {
		entries = append(entries, desktop.Entry{Role: "supervisor", Ref: "s1", No: i, At: "2026-11-01T11:41:00Z", Text: fmt.Sprintf("entry number %d", i)})
	}
	tl.Entries = entries
	f.timeline[fleetID] = tl
	tt := sized(t, newApp(t, f), 1100, 760, false)
	newest, ok := tt.Find("entry number 60")
	if !ok || newest.H == 0 || newest.Y < 100 || newest.Y+newest.H > 760 {
		t.Fatalf("the newest entry is at %v, want it in view", newest)
	}
	if oldest, ok := tt.Find("entry number 1"); ok && oldest.H > 0 && oldest.Y >= 100 && oldest.Y+oldest.H <= 760 {
		t.Fatalf("the oldest entry is in view at %v", oldest)
	}
	message, ok := tt.Find("Message the supervisor")
	if !ok || message.Y+message.H > 760 || message.Y < 400 {
		t.Fatalf("the composer is at %v, want it pinned at the bottom", message)
	}
}

func TestTheOverflowMenuHoldsRefreshNowAndOpenInBrowser(t *testing.T) {
	tt := newTester(newApp(t, goldenFixture(t)))
	mustNotText(t, tt, "Refresh now")
	click(t, tt, "More")
	mustText(t, tt, "Refresh now", "Open in browser")
	mustNotText(t, tt, "Refresh models")
}

func TestPollingRunsAtTheBoardsTickAndSlowsWhenUnfocused(t *testing.T) {
	if desktop.FocusedRefresh != time.Second || desktop.UnfocusedRefresh != 5*time.Second {
		t.Fatalf("intervals are %v and %v, want 1s and 5s", desktop.FocusedRefresh, desktop.UnfocusedRefresh)
	}
	app, clock := newBoardApp(t, newFakeBoard())
	app.StartRefresh()
	if got := clock.delay(); got != time.Second {
		t.Fatalf("focused tick = %v", got)
	}
	app.SetFocused(false)
	if got := clock.delay(); got != 5*time.Second {
		t.Fatalf("unfocused tick = %v", got)
	}
}

func TestNoPollingWhileMinimised(t *testing.T) {
	app, clock := newBoardApp(t, newFakeBoard())
	app.StartRefresh()
	app.SetMinimized(true)
	if got := clock.delay(); got != 0 {
		t.Fatalf("a tick is armed %v while minimised", got)
	}
	app.SetMinimized(false)
	if got := clock.delay(); got != time.Second {
		t.Fatalf("restored tick = %v, want 1s", got)
	}
}

func inPane(t *testing.T, tt *ui.Tester, width, height int, names ...string) {
	t.Helper()
	left := chatLeft(t, tt)
	for _, s := range names {
		r, ok := tt.Find(s)
		if !ok || r.H == 0 || r.X < left || r.X+r.W > float32(width) || r.Y < 0 || r.Y+r.H > float32(height) {
			t.Errorf("%q is at %v, outside the pane at x=%.0f of the %dx%d window", s, r, left, width, height)
		}
	}
}

func TestTheBlockedKeysStayInsideTheChatPane(t *testing.T) {
	for _, width := range []int{780, 860, 1100} {
		f := blockedFixture(t)
		tasks := f.tasks[fleetID]
		for i := range tasks.Groups {
			tasks.Groups[i].Count = 7 + i
		}
		f.tasks[fleetID] = tasks
		tt := sized(t, newApp(t, f), width, 700, false)
		inPane(t, tt, width, 700, "Enter", "Esc", "↑", "↓", "1", "2", "3", "Message the supervisor", "Send")
		first, ok := tt.Find("status?")
		keys, _ := tt.Find("3")
		if ok && first.H > 0 && keys.Y+keys.H > first.Y {
			t.Errorf("%d wide: the key row ends at %.0f over the first entry at %.0f", width, keys.Y+keys.H, first.Y)
		}
	}
}

func TestALongAskCannotEatTheChatPane(t *testing.T) {
	f := askFixture(t)
	st := f.status[fleetID]
	sup := *st.Supervisor
	ask := *sup.Ask
	for i := 0; i < 14; i++ {
		ask.Options = append(ask.Options, desktop.AskOption{Label: fmt.Sprintf("Another option number %d", i), Keys: slices.Repeat([]string{"up"}, i+1)})
	}
	sup.Ask = &ask
	st.Supervisor = &sup
	f.status[fleetID] = st
	tt := sized(t, newApp(t, f), 780, 480, false)
	inPane(t, tt, 780, 480, "Message the supervisor", "Send")
	msg, _ := tt.Find("Message the supervisor")
	if msg.Y < 250 {
		t.Errorf("the composer is at y=%.0f, want it left at the bottom of the 480 px window", msg.Y)
	}
}

func TestAResumableHeadLeadsWithAPrimaryResumeAndKeepsStartInTheMenu(t *testing.T) {
	f := statusWith(t, func(s *desktop.Supervisor) { s.Status, s.Live, s.Resumable = "interrupted", false, true })
	tt := sized(t, newApp(t, f), 1100, 760, false)
	resume, ok := tt.Find("Resume")
	more, _ := tt.Find("More")
	if !ok || resume.X > more.X {
		t.Fatalf("Resume at %v, More at %v, want Resume first", resume, more)
	}
	primary := kit.ColorsFor(false).Primary
	got := tt.Image().RGBAAt(int(resume.X)+4, int(resume.Y+resume.H/2))
	if got.R != primary.R || got.G != primary.G || got.B != primary.B {
		t.Fatalf("Resume background is %v, want the primary %v", got, primary)
	}
	mustNotText(t, tt, "Start new")
	click(t, tt, "More")
	mustText(t, tt, "New session", "Refresh now", "Open in browser")
	click(t, tt, "New session")
	if f.count("control:start") != 1 {
		t.Fatalf("New session ran %d starts", f.count("control:start"))
	}
}

func TestTheHeadsButtonsFollowTheBoardsOrder(t *testing.T) {
	tt := sized(t, newApp(t, goldenFixture(t)), 1100, 760, false)
	interrupt, _ := tt.Find("Interrupt")
	stop, _ := tt.Find("Stop")
	more, _ := tt.Find("More")
	if !(interrupt.X < stop.X && stop.X < more.X) {
		t.Fatalf("Interrupt at %.0f, Stop at %.0f, More at %.0f", interrupt.X, stop.X, more.X)
	}
}

func TestStopArmedInOneFleetIsNotArmedInTheNext(t *testing.T) {
	if desktop.StopWindow != 4*time.Second {
		t.Fatalf("StopWindow = %v, want the Board's 4s", desktop.StopWindow)
	}
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Stop")
	if !app.StopArmed {
		t.Fatal("the first press did not arm Stop")
	}
	click(t, tt, "second")
	if app.StopArmed {
		t.Fatal("the next fleet opened with Stop armed")
	}
	click(t, tt, "golden")
	mustNotText(t, tt, "Press again to stop")
}

func TestALongDraftErrorKeepsTheComposerInsideThePane(t *testing.T) {
	for _, width := range []int{860, 1100} {
		app := newApp(t, goldenFixture(t))
		app.DraftErr = "the draft was not saved: " + strings.Repeat("x", 600)
		tt := sized(t, app, width, 700, false)
		inPane(t, tt, width, 700, "Send")
	}
}

func TestTheComposerHintFollowsTheSupervisorsState(t *testing.T) {
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.ComposerHint = "s1 is waiting on a screen in Needs; your message waits too."
	f.status[fleetID] = st
	app := newApp(t, f)
	tt := newTester(app)
	mustText(t, tt, st.ComposerHint)
	click(t, tt, "Message the supervisor")
	tt.Type("half a message")
	st.ComposerHint = ""
	f.status[fleetID] = st
	menu(t, tt, "Refresh now")
	tt.Frame()
	mustNotText(t, tt, "your message waits")
	if app.Draft != "half a message" {
		t.Fatalf("a status refresh changed the typed draft to %q", app.Draft)
	}
	click(t, tt, "second")
	if app.Draft != "" {
		t.Fatalf("the second fleet inherited the draft %q", app.Draft)
	}
}

func TestTheRailsInertItemsLeaveAnOpenDetailAlone(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("t1")
	tt.Frame()
	reads := f.count("status:" + fleetID)
	for _, item := range []string{"Tasks", "Conversation", "Screen", "Phone"} {
		click(t, tt, item)
	}
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" || f.count("status:"+fleetID) != reads {
		t.Fatalf("an inert rail item changed the screen to %v %q or reloaded it", app.Screen, app.Ref)
	}
	click(t, tt, "Board")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("Board left the window on %v", app.Screen)
	}
}

func TestAFailedTimelineReadStaysInTheChatPane(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	held := len(app.Timeline.Entries)
	app.Model = timelineFails{app.Model}
	app.Retry()
	if app.Stale != "" {
		t.Fatalf("a conversation-only failure put %q in the masthead's stale note", app.Stale)
	}
	if app.Err != "" || app.TimelineErr == "" || len(app.Timeline.Entries) != held {
		t.Fatalf("error %q, timeline error %q, %d of %d entries kept", app.Err, app.TimelineErr, len(app.Timeline.Entries), held)
	}
	tt := newTester(app)
	mustText(t, tt, "The conversation could not be read", "Needs", "Fix login", "status?", "Retry")
	mustNotText(t, tt, "The board could not be read")
}

type timelineFails struct{ desktop.ReadModel }

func (timelineFails) Timeline(context.Context, string, int) (desktop.Timeline, error) {
	return desktop.Timeline{}, errors.New("the timeline store failed")
}

func TestScenesBlockedAndResumableHeads(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		for _, size := range [][2]int{{860, 700}, {1100, 760}} {
			tt := sized(t, newApp(t, askFixture(t)), size[0], size[1], dark)
			shotSized(t, tt, fmt.Sprintf("21-blocked-%dx%d-%s", size[0], size[1], theme), size[0], size[1])
		}
		f := statusWith(t, func(s *desktop.Supervisor) { s.Status, s.Live, s.Resumable = "interrupted", false, true })
		st := f.status[fleetID]
		st.AgentState, st.Pill, st.Line = "idle", "idle", "interrupted"
		f.status[fleetID] = st
		tl := f.timeline[fleetID]
		tl.Live, tl.Working = false, nil
		f.timeline[fleetID] = tl
		tt := sized(t, newApp(t, f), 1100, 760, dark)
		shotSized(t, tt, "22-resumable-1100x760-"+theme, 1100, 760)
	}
}

func TestASecondStopPressDoesNotStopAReplacementSupervisor(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Stop")
	st := f.status[fleetID]
	sup := *st.Supervisor
	sup.Ref = "s2"
	st.Supervisor = &sup
	f.status[fleetID] = st
	menu(t, tt, "Refresh now")
	mustText(t, tt, "s2")
	mustNotText(t, tt, "Press again to stop")
	click(t, tt, "Stop")
	if f.count("control:stop") != 0 {
		t.Fatalf("Stop ran %d times for a supervisor the operator never confirmed", f.count("control:stop"))
	}
	mustText(t, tt, "Press again to stop")
	click(t, tt, "Press again to stop")
	if f.count("control:stop") != 1 {
		t.Fatalf("Stop ran %d times after the confirmation of s2", f.count("control:stop"))
	}
}

func TestOlderMessagesSurviveOpeningATask(t *testing.T) {
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	tl.Older = 42
	f.timeline[fleetID] = tl
	f.older = map[string]desktop.Timeline{fleetID: {
		Entries: []desktop.Entry{{Role: "operator", Text: "the earlier message", At: "2026-10-31T04:40:00Z"}},
	}}
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Older messages")
	mustText(t, tt, "the earlier message")
	click(t, tt, "Fix login")
	mustText(t, tt, "the earlier message")
}

func TestTheLabelledRailStaysClosedWhereTheWorkPaneWouldShrink(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, 860, 700, false)
	click(t, tt, "Show labels")
	if app.RailOpen {
		t.Fatal("the rail opened in an 860 px window")
	}
	wide := sized(t, newApp(t, goldenFixture(t)), 1100, 700, false)
	click(t, wide, "Show labels")
	mustText(t, wide, "Hide labels")
}

type olderFails struct{ desktop.ReadModel }

func (olderFails) Timeline(context.Context, string, int) (desktop.Timeline, error) {
	return desktop.Timeline{}, errors.New("the earlier page failed")
}

func olderFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	tl.Older = 42
	f.timeline[fleetID] = tl
	f.older = map[string]desktop.Timeline{fleetID: {
		Entries: []desktop.Entry{{Role: "operator", Text: "the earlier message", At: "2026-10-31T04:40:00Z"}},
	}}
	return f
}

func TestAnOlderFailureStaysUntilAnOlderReadAnswers(t *testing.T) {
	f := olderFixture(t)
	app := newApp(t, f)
	good := app.Model
	app.Model = olderFails{good}
	app.Older()
	if app.OlderErr == "" || app.TimelineErr != "" {
		t.Fatalf("older error %q, timeline error %q", app.OlderErr, app.TimelineErr)
	}
	clock := newFakeClock()
	app.Clock = clock
	app.Model = good
	app.StartRefresh()
	for range 3 {
		clock.Advance(desktop.FocusedRefresh)
	}
	if app.OlderErr == "" {
		t.Fatal("a poll answering 304 cleared the earlier-messages error")
	}
	tt := newTester(app)
	mustText(t, tt, "The earlier messages could not be read", "Retry")
	click(t, tt, "Retry")
	if app.OlderErr != "" {
		t.Fatalf("Retry left older error %q", app.OlderErr)
	}
	mustText(t, tt, "the earlier message")
}

func TestSendingKeepsTheOlderMessagesLoaded(t *testing.T) {
	f := olderFixture(t)
	app := newApp(t, f)
	app.Older()
	app.Draft = "a new message"
	app.Send()
	found := slices.ContainsFunc(app.Timeline.Entries, func(e desktop.Entry) bool { return e.Text == "the earlier message" })
	sent := slices.ContainsFunc(app.Timeline.Entries, func(e desktop.Entry) bool { return e.Text == "a new message" })
	if !found || !sent {
		t.Fatalf("after a send, earlier message kept=%v, sent message shown=%v", found, sent)
	}
}

func TestARailLeftOpenInATooNarrowWindowCollapses(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	app.RailOpen = true
	tt := sized(t, app, 860, 700, false)
	if app.RailOpen {
		t.Fatal("the rail stayed open in an 860 px window")
	}
	mustText(t, tt, "Show labels")
	inPane(t, tt, 860, 700, "Send")
	left := chatLeft(t, tt)
	if work := left - kit.RailWidth; work < 440 {
		t.Errorf("the work pane is %.0f wide after the collapse, want at least 440", work)
	}
}

func TestAFailingStatusDoesNotStopTheConversation(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	b.setFail(true)
	b.setTimeline(func(tl *desktop.Timeline) {
		tl.Entries = append([]desktop.Entry{{Role: "supervisor", Text: "while status failed", No: 2}}, tl.Entries...)
	})
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	if app.Stale == "" || app.Timeline.Entries[0].Text != "while status failed" {
		t.Fatalf("stale %q, newest entry %q", app.Stale, app.Timeline.Entries[0].Text)
	}
}

func TestTheConversationErrorsStayInViewOverALongHistory(t *testing.T) {
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	var entries []desktop.Entry
	for i := 60; i >= 1; i-- {
		entries = append(entries, desktop.Entry{Role: "supervisor", Ref: "s1", No: i, At: "2026-11-01T11:41:00Z", Text: fmt.Sprintf("entry number %d", i)})
	}
	tl.Entries, tl.Older = entries, 42
	f.timeline[fleetID] = tl
	app := newApp(t, f)
	app.TimelineErr, app.OlderErr = "the timeline store failed", "the earlier page failed"
	tt := sized(t, app, 1100, 760, false)
	inPane(t, tt, 1100, 760, "Retry")
	r, _ := tt.Find("Retry")
	if r.Y > 400 {
		t.Errorf("Retry is at y=%.0f, want it above the history", r.Y)
	}
}

func TestAShortConversationSitsNextToTheComposer(t *testing.T) {
	app, _ := talk(t)
	app.Timeline = desktop.Timeline{Entries: []desktop.Entry{
		{Role: "supervisor", Ref: "s1", No: 2, At: "2026-10-31T12:02:00Z", Text: "Only one."},
	}}
	tt := sized(t, app, 1100, 760, false)
	view, ok := tt.Find("The conversation")
	if !ok || view.H == 0 {
		t.Fatalf("the conversation viewport is %v", view)
	}
	one, ok := tt.Find("Only one.")
	if !ok || one.H == 0 {
		t.Fatalf("the only entry is %v", one)
	}
	if bottom := view.Y + view.H; one.Y+one.H > bottom || bottom-(one.Y+one.H) > 40 {
		t.Errorf("the only entry is at %v in the viewport %v, want it against the composer", one, view)
	}
}
