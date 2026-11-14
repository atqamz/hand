package desktop_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

func inUTC(t *testing.T) {
	t.Helper()
	old := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = old })
}

func wake(at, text string) desktop.Entry { return desktop.Entry{Role: "hand", At: at, Text: text} }

func talk(t *testing.T) (*desktop.App, *time.Time) {
	t.Helper()
	inUTC(t)
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	tl.Entries = []desktop.Entry{
		{Role: "supervisor", Ref: "s1", Model: "sonnet low", No: 12, At: "2026-10-31T12:02:00Z", Text: "Looking at **t1** now."},
		wake("2026-10-31T12:01:00Z", "wake: a1 reported"),
		{Role: "operator", No: 10, At: "2026-10-31T11:00:00Z", Text: "status?", Queued: true},
		{Role: "wakes", No: 9, At: "2026-10-30T13:34:00Z", Wakes: []desktop.Entry{
			wake("2026-10-30T13:30:00Z", "a1 started"), wake("2026-10-30T13:32:00Z", "a1 working"), wake("2026-10-30T13:34:00Z", "a1 reported"),
		}},
		{Role: "supervisor", Ref: "s1", Model: "sonnet low", No: 8, At: "2026-10-30T12:31:00Z", Text: "Morning. Three tasks are in the inbox:\n\n- t4 Write docs\n- t5 Add dark mode toggle\n- t6 Rename the cookie\n\nWant me to start one?"},
		{Role: "operator", No: 7, At: "2026-10-30T11:50:00Z", Text: "start on the login bug first: people get logged out after the SSO redirect, mostly on Safari"},
		wake("2026-10-30T11:40:00Z", "s1 started · claude sonnet low"),
		{Role: "operator", No: 5, At: "2026-10-30T11:35:00Z", Text: "morning"},
	}
	tl.Working = &desktop.Working{Ref: "s1", Since: "2026-10-31T12:00:00Z", Last: "2026-10-31T12:02:00Z", On: "since the turn began"}
	f.timeline[fleetID] = tl
	now := time.Date(2026, 10, 31, 12, 4, 12, 0, time.UTC)
	app := newApp(t, f)
	app.Now = func() time.Time { return now }
	return app, &now
}

func states(t *testing.T) (*desktop.App, *time.Time) {
	t.Helper()
	inUTC(t)
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	tl.Entries = []desktop.Entry{
		{Role: "supervisor", Ref: "s1", Model: "sonnet low", No: 9, At: "2026-10-31T12:06:00Z", Text: "Pushed both. **t1** is green."},
		{Role: "operator", No: 8, At: "2026-10-31T12:05:00Z", Text: "run the tests", Delivery: "delivered", Delivered: "2026-10-31T12:05:00Z"},
		{Role: "operator", No: 7, At: "2026-10-31T12:04:00Z", Text: "and lint, despite the block", Delivery: "typed anyway", Delivered: "2026-10-31T12:04:00Z"},
		{Role: "operator", No: 6, At: "2026-10-31T12:03:00Z", Text: "status?", Queued: true, Delivery: "queued"},
		{Role: "wakes", No: 5, At: "2026-10-31T12:02:00Z", Wakes: []desktop.Entry{
			wake("2026-10-31T12:00:00Z", "a1 started"), wake("2026-10-31T12:01:00Z", "a1 working"), wake("2026-10-31T12:02:00Z", "a1 reported"),
		}},
		{Role: "operator", No: 4, At: "2026-10-30T11:00:00Z", Text: "yesterday", Delivery: "delivered", Delivered: "2026-10-30T11:00:00Z"},
	}
	tl.Working = &desktop.Working{Ref: "s1", Since: "2026-10-31T12:00:00Z", Last: "2026-10-31T12:06:00Z", On: "since the turn began"}
	f.timeline[fleetID] = tl
	now := time.Date(2026, 10, 31, 12, 8, 0, 0, time.UTC)
	app := newApp(t, f)
	app.Now = func() time.Time { return now }
	return app, &now
}

func TestScenesTheConversationAsTheBoardDrawsIt(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			app, _ := states(t)
			tt := sized(t, app, 1100, 760, dark)
			mustText(t, tt, "Message from you", "Message from s1", "run the tests", "delivered 12:05",
				"typed anyway 12:04", "status?", "queued", "3 wakes", "12:00–12:02", "Sat, Oct 31",
				"s1 working", "· 8m · last output 2m ago · since the turn began")
			if !hasTextNode(tt, "live") {
				t.Fatal("no live badge")
			}
			mustNotText(t, tt, "a1 started")
			mustNotText(t, tt, "Fri, Oct 30")
			if at, ok := tt.Find("12:06"); ok {
				if model, ok := tt.Find("sonnet low"); !ok || at.X > model.X {
					t.Errorf("the head puts the time at %.0f and the model at %.0f, want the author, the time, then the model", at.X, model.X)
				}
			}
			shotSized(t, tt, "23-conversation-"+name, 1100, 760)

			collapsed, _ := tt.Find("3 wakes")
			click(t, tt, "3 wakes")
			mustText(t, tt, "a1 started", "a1 working", "a1 reported", "12:00")
			wake, _ := tt.Find("a1 started")
			if wake.X < collapsed.X+15 {
				t.Errorf("the unfolded wake starts at %.0f, want it indented 16 from %.0f", wake.X, collapsed.X)
			}
			shotSized(t, tt, "24-conversation-unfolded-"+name, 1100, 760)
			click(t, tt, "3 wakes")
			mustNotText(t, tt, "a1 started")
		})
	}
}

func TestScenesScrolledUpTheLatestButtonReturnsToTheNewestEntry(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			app, _ := talk(t)
			tt := sized(t, app, 1100, 760, dark)
			mustNotText(t, tt, "Scroll to latest message")
			chat, _ := tt.Find("Supervisor")
			for range 4 {
				tt.Scroll(chat.X+100, 300, 0, -400)
				tt.Frame()
			}
			mustText(t, tt, "Scroll to latest message", "morning")
			desktop.SetLatestNew(app, 2)
			tt.Frame()
			mustText(t, tt, "Scroll to latest, 2 new messages", "2")
			tt.Advance(200 * time.Millisecond)
			tt.Frame()
			shotSized(t, tt, "25-conversation-scrolled-"+name, 1100, 760)
			click(t, tt, "Scroll to latest, 2 new messages")
			tt.Advance(2 * time.Second)
			tt.Frame()
			mustNotText(t, tt, "Scroll to latest")
			if got := desktop.LatestNew(app); got != 0 {
				t.Errorf("the count is %d after returning to the bottom", got)
			}
			if r, ok := tt.Find("Looking at t1 now."); !ok || r.Y+r.H > 760 || r.H == 0 {
				t.Fatalf("the newest entry is at %v after the button", r)
			}
		})
	}
}

func TestTheWorkingLineCountsOnTheWindowsClock(t *testing.T) {
	app, now := talk(t)
	tt := sized(t, app, 1100, 760, false)
	mustText(t, tt, "· 4m · last output 2m ago", "s1 working")
	if !hasTextNode(tt, "live") {
		t.Fatal("the working row has no live badge")
	}
	*now = now.Add(3 * time.Minute)
	tt.Frame()
	mustText(t, tt, "· 7m · last output 5m ago")
}

func TestOperatorBubblesSitRightAndTheSupervisorsLeft(t *testing.T) {
	app, _ := talk(t)
	tt := sized(t, app, 1100, 760, false)
	left := chatLeft(t, tt)
	you, _ := tt.Find("status?")
	sup, _ := tt.Find("Message from s1")
	if sup.X > left+20 {
		t.Errorf("the supervisor's bubble starts at %.0f, want it at the pane's left %.0f", sup.X, left)
	}
	if you.X+you.W < 1100-16-14-4 || you.X <= sup.X+40 {
		t.Errorf("the operator's text is at %v, want it at the right edge", you)
	}
}

func TestMessagesAreHeldToTheMeasure(t *testing.T) {
	app, _ := talk(t)
	long := ""
	for range 40 {
		long += "measure "
	}
	app.Timeline = desktop.Timeline{Entries: []desktop.Entry{
		{Role: "supervisor", Ref: "s1", No: 2, At: "2026-10-31T12:02:00Z", Text: long},
		{Role: "operator", No: 1, At: "2026-10-31T12:00:00Z", Text: long},
	}}
	tt := sized(t, app, 2400, 900, false)
	var measure float32
	ui.NewTester(func(c *ui.Context) { measure = kit.MeasureOf(c) }, 200, 200)
	for _, label := range []string{"Message from s1", "Message from you"} {
		r, ok := tt.Find(label)
		if !ok || r.W > measure+31 || r.W < 300 {
			t.Errorf("%q is %.0f wide, want the %d-character measure of at most %.0f", label, r.W, kit.MeasureCharacters, measure+30)
		}
	}
}

func TestMessageTextIs15OnA24Line(t *testing.T) {
	app, _ := talk(t)
	run := strings.Repeat("W", 20)
	app.Timeline = desktop.Timeline{Entries: []desktop.Entry{
		{Role: "operator", No: 1, At: "2026-10-31T12:00:00Z", Text: run},
	}}
	tt := sized(t, app, 1100, 760, false)
	r, ok := tt.Find(run)
	if !ok || r.H < 23.5 || r.H > 24.5 {
		t.Fatalf("the message text box is %v, want a 24 px line", r)
	}
	var w15 float32
	ui.NewTester(func(c *ui.Context) {
		w15, _ = c.MeasureText(1e6, ui.Span{Text: run, Size: kit.Message.Size})
	}, 10, 10)
	if d := r.W - w15; d < -1 || d > 1 {
		t.Errorf("the run is %.0f wide, want %.0f at %v px", r.W, w15, kit.Message.Size)
	}
}

func TestTheOperatorsBubbleEndsOnTheComposersEdge(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
		chat float32
	}{{"pane", 1100, 760, 0}, {"column", 1440, 900, 900}} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := talk(t)
			app.Chat = tc.chat
			tt := sized(t, app, tc.w, tc.h, false)
			text, ok := tt.Find("status?")
			composer, ok2 := tt.Find("Composer")
			if !ok || !ok2 {
				t.Fatal("no operator bubble or composer")
			}
			if d := text.X + text.W + 14 - composer.X - composer.W; d < -1 || d > 1 {
				t.Errorf("the operator bubble ends at %.1f, the composer at %.1f", text.X+text.W+14, composer.X+composer.W)
			}
		})
	}
}

func TestTheConversationSitsInTheBoardsColumn(t *testing.T) {
	app, _ := talk(t)
	app.Chat = 900
	tt := sized(t, app, 1440, 900, false)
	composer, _ := tt.Find("Composer")
	sup, _ := tt.Find("Message from s1")
	if composer.W < 759 || composer.W > 761 {
		t.Errorf("the composer is %.0f wide, want the Board's 760 column", composer.W)
	}
	if d := sup.X - composer.X; d < -1 || d > 1 {
		t.Errorf("the supervisor bubble starts at %.1f, the column at %.1f", sup.X, composer.X)
	}
}

func TestLongRepliesHoldTheMeasureAndTheColumn(t *testing.T) {
	app, _ := talk(t)
	app.Chat = 900
	long := strings.Repeat("measure ", 60)
	app.Timeline = desktop.Timeline{Entries: []desktop.Entry{
		{Role: "supervisor", Ref: "s1", No: 2, At: "2026-10-31T12:02:00Z", Text: long},
		{Role: "operator", No: 1, At: "2026-10-31T12:00:00Z", Text: long},
	}}
	tt := sized(t, app, 1440, 900, false)
	var measure float32
	ui.NewTester(func(c *ui.Context) { measure = kit.MeasureOf(c) }, 200, 200)
	sup, _ := tt.Find("Message from s1")
	you, _ := tt.Find("Message from you")
	if limit := min(float32(760), measure+28); sup.W > limit+1 {
		t.Errorf("the supervisor bubble is %.0f wide, want at most %.0f", sup.W, limit)
	}
	if limit := min(float32(560), 760*.88, measure+28); you.W > limit+1 {
		t.Errorf("the operator bubble is %.0f wide, want at most %.0f", you.W, limit)
	}
}

func TestLiveShowsWithoutATurn(t *testing.T) {
	app, _ := talk(t)
	app.Timeline.Working = nil
	app.Timeline.Live = true
	tt := sized(t, app, 1100, 760, false)
	if !hasTextNode(tt, "live") {
		t.Fatalf("live is true with no turn, and the conversation has no live badge in %q", tt.Texts())
	}
}

func TestTheModelShowsOnlyWhenItChanged(t *testing.T) {
	app, _ := talk(t)
	app.Timeline = desktop.Timeline{Entries: []desktop.Entry{
		{Role: "supervisor", Ref: "s1", Model: "sonnet low", No: 4, At: "2026-10-31T12:04:00Z", Text: "fourth"},
		{Role: "supervisor", Ref: "s1", Model: "sonnet low", No: 3, At: "2026-10-31T12:03:00Z", Text: "third"},
		{Role: "supervisor", Ref: "s2", Model: "opus high", No: 2, At: "2026-10-31T12:02:00Z", Text: "second"},
	}}
	tt := sized(t, app, 1100, 760, false)
	if n := countText(t, tt, "sonnet low"); n != 1 {
		t.Errorf("sonnet low shows %d times, want once", n)
	}
	if n := countText(t, tt, "opus high"); n != 1 {
		t.Errorf("opus high shows %d times, want once", n)
	}
	mustText(t, tt, "fourth", "third", "second")
}

func TestTheOperatorHeadEndsWithItsDeliveryState(t *testing.T) {
	app, _ := talk(t)
	app.Timeline = desktop.Timeline{Entries: []desktop.Entry{
		{Role: "operator", No: 3, At: "2026-10-31T12:03:00Z", Text: "later", Delivery: "delivered", Delivered: "2026-10-31T12:07:00Z"},
		{Role: "operator", No: 2, At: "2026-10-31T12:02:00Z", Text: "anyway", Delivery: "typed anyway", Delivered: "2026-10-31T12:03:00Z"},
		{Role: "operator", No: 1, At: "2026-10-31T12:01:00Z", Text: "waiting", Delivery: "queued"},
	}}
	tt := sized(t, app, 1100, 760, false)
	mustText(t, tt, "delivered 12:07", "typed anyway 12:03", "queued")
	at, _ := tt.Find("12:03")
	delivery, _ := tt.Find("delivered 12:07")
	if at.X >= delivery.X {
		t.Errorf("the head puts the time at %.0f after the delivery state at %.0f", at.X, delivery.X)
	}
}

func TestTheLatestCountIsSpokenOnceItHolds(t *testing.T) {
	app, now := talk(t)
	tt := sized(t, app, 1100, 760, false)
	chat, _ := tt.Find("Supervisor")
	for range 4 {
		tt.Scroll(chat.X+100, 300, 0, -400)
		tt.Frame()
	}
	desktop.SetLatestNew(app, 2)
	tt.Frame()
	for _, said := range tt.Announcements() {
		if said == "2 new messages" {
			t.Fatal("the count was spoken before it had held")
		}
	}
	*now = now.Add(time.Second)
	tt.Frame()
	said := tt.Announcements()
	if len(said) != 1 || said[0] != "2 new messages" {
		t.Fatalf("announcements = %q, want the count once", said)
	}
	tt.Frame()
	for _, again := range tt.Announcements() {
		if again == "2 new messages" {
			t.Fatal("the count was spoken again on a later frame")
		}
	}
}

func TestTheLatestButtonMovesTheFocusToTheMessageBoxOnAKeyboardClick(t *testing.T) {
	app, _ := talk(t)
	tt := sized(t, app, 1100, 760, false)
	chat, _ := tt.Find("Supervisor")
	for range 4 {
		tt.Scroll(chat.X+100, 300, 0, -400)
		tt.Frame()
	}
	found := false
	for range 40 {
		tt.Key(0, ui.KeyTab)
		tt.Frame()
		if tt.Focused("Scroll to latest message") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Tab never reached the latest button")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Advance(2 * time.Second)
	tt.Frame()
	if !tt.Focused("Message the supervisor") {
		t.Fatal("a keyboard activation left the focus on the button, not the message box")
	}
}

func TestAGapEventReadsThePageOnce(t *testing.T) {
	f := goldenFixture(t)
	m := &timedReader{ReadModel: f}
	app := desktop.New(func() (desktop.ReadModel, error) { return m, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	reads := m.reads
	before := append([]desktop.Entry(nil), app.Timeline.Entries...)
	app.ApplyTimelineEvent(desktop.TimelineEvent{From: 99, To: 100, Entries: []desktop.Entry{
		{Role: "hand", At: "2026-11-02T09:00:00Z", Text: "not applied"},
	}})
	if m.reads != reads+1 {
		t.Fatalf("%d page reads for a gap, want one", m.reads-reads)
	}
	if !reflect.DeepEqual(app.Timeline.Entries, before) {
		t.Fatal("a gap event was applied before the page was read")
	}
	app.ApplyTimelineEvent(desktop.TimelineEvent{From: 3, To: 4, Entries: []desktop.Entry{
		{Role: "hand", At: "2026-11-02T09:00:00Z", Text: "applied"},
	}})
	if m.reads != reads+1 {
		t.Fatalf("%d page reads for a matched event, want none more", m.reads-reads)
	}
	if len(app.Timeline.Entries) != 4 || app.Timeline.Entries[0].Text != "applied" {
		t.Fatalf("matched event left %+v", app.Timeline.Entries)
	}
}

type timedReader struct {
	desktop.ReadModel
	reads int
}

func (m *timedReader) Timeline(ctx context.Context, id string, before int) (desktop.Timeline, error) {
	m.reads++
	return m.ReadModel.Timeline(ctx, id, before)
}

func TestTheLatestButtonKeepsTheKeyboardFocusOnAPointerClick(t *testing.T) {
	app, _ := talk(t)
	tt := sized(t, app, 1100, 760, false)
	chat, _ := tt.Find("Supervisor")
	for range 4 {
		tt.Scroll(chat.X+100, 300, 0, -400)
		tt.Frame()
	}
	click(t, tt, "Message the supervisor")
	if !tt.Focused("Message the supervisor") {
		t.Fatal("the message box did not take the focus")
	}
	click(t, tt, "Scroll to latest message")
	if !tt.Focused("Message the supervisor") {
		t.Fatal("the pointer click on the latest button moved the focus")
	}
}

func TestADayDividerKeepsTimesShortAfterIt(t *testing.T) {
	app, _ := talk(t)
	tt := sized(t, app, 1100, 760, false)
	mustText(t, tt, "Oct 30, 11:35", "Sat, Oct 31", "11:00", "12:02")
	if n := countText(t, tt, "Sat, Oct 31"); n != 1 {
		t.Errorf("the day divider shows %d times, want once", n)
	}
	mustNotText(t, tt, "Oct 31, 11:00")
	mustNotText(t, tt, "Oct 31, 12:02")
}

func filled(t *testing.T, n int) (*desktop.App, *ui.Tester) {
	t.Helper()
	app, _ := talk(t)
	entries := make([]desktop.Entry, 0, n)
	for i := range n {
		entries = append(entries, desktop.Entry{Role: "hand", At: "2026-10-31T12:00:00Z", Text: fmt.Sprintf("filler %02d", n-1-i)})
	}
	app.Timeline = desktop.Timeline{Entries: entries}
	return app, sized(t, app, 1100, 760, false)
}

func TestANewEntryArrivingWhilePinnedStaysInView(t *testing.T) {
	app, tt := filled(t, 40)
	mustNotText(t, tt, "Scroll to latest message")
	app.Timeline.Entries = append([]desktop.Entry{
		{Role: "supervisor", Ref: "s1", No: 99, At: "2026-11-01T09:00:00Z", Text: "arrived while pinned"},
	}, app.Timeline.Entries...)
	tt.Frame()
	view, ok := tt.Find("The conversation")
	if !ok {
		t.Fatal("no conversation viewport")
	}
	r, ok := tt.Find("arrived while pinned")
	if !ok || r.H == 0 || r.Y < view.Y || r.Y+r.H > view.Y+view.H {
		t.Fatalf("the new entry is at %v, outside the viewport %v", r, view)
	}
	mustNotText(t, tt, "Scroll to latest message")
}

func TestAnEntryArrivingWhileScrolledUpMovesNothing(t *testing.T) {
	app, tt := filled(t, 40)
	chat, _ := tt.Find("Supervisor")
	for range 4 {
		tt.Scroll(chat.X+100, 300, 0, -400)
		tt.Frame()
	}
	mustText(t, tt, "Scroll to latest message")
	before, ok := tt.Find("filler 10")
	if !ok || before.H == 0 {
		t.Fatalf("filler 10 is at %v, want it in view", before)
	}
	app.Timeline.Entries = append([]desktop.Entry{
		{Role: "supervisor", Ref: "s1", No: 100, At: "2026-11-01T10:00:00Z", Text: "arrived while scrolled up"},
	}, app.Timeline.Entries...)
	tt.Frame()
	after, _ := tt.Find("filler 10")
	if after != before {
		t.Errorf("filler 10 moved from %v to %v as an entry arrived", before, after)
	}
	mustText(t, tt, "Scroll to latest message")
}
