package desktop_test

import (
	"testing"
	"time"

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

func TestScenesTheConversationAsTheBoardDrawsIt(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			app, _ := talk(t)
			tt := sized(t, app, 1100, 760, dark)
			mustText(t, tt, "Message from you", "Message from s1", "status?", "queued", "3 wakes", "13:30–13:34", "Sat, Oct 31",
				"s1 working", "· 4m · last output 2m ago · since the turn began")
			mustNotText(t, tt, "a1 started")
			mustNotText(t, tt, "Fri, Oct 30")
			shotSized(t, tt, "23-conversation-"+name, 1100, 760)

			click(t, tt, "3 wakes")
			mustText(t, tt, "a1 started", "a1 working", "a1 reported", "13:30")
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
			shotSized(t, tt, "25-conversation-scrolled-"+name, 1100, 760)
			click(t, tt, "Scroll to latest message")
			tt.Advance(2 * time.Second)
			tt.Frame()
			mustNotText(t, tt, "Scroll to latest message")
			if r, ok := tt.Find("Looking at t1 now."); !ok || r.Y+r.H > 760 || r.H == 0 {
				t.Fatalf("the newest entry is at %v after the button", r)
			}
		})
	}
}

func TestTheWorkingLineCountsOnTheWindowsClock(t *testing.T) {
	app, now := talk(t)
	tt := sized(t, app, 1100, 760, false)
	mustText(t, tt, "· 4m · last output 2m ago")
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
	for _, label := range []string{"Message from s1", "Message from you"} {
		r, ok := tt.Find(label)
		if !ok || r.W > kit.Measure+31 || r.W < 300 {
			t.Errorf("%q is %.0f wide, want the 72-character measure of at most %d", label, r.W, kit.Measure+30)
		}
	}
}

func TestMessageTextIs15OnA24Line(t *testing.T) {
	app, _ := talk(t)
	tt := sized(t, app, 1100, 760, false)
	r, ok := tt.Find("status?")
	if !ok || r.H < 23.5 || r.H > 24.5 {
		t.Fatalf("the message text box is %v, want a 24 px line", r)
	}
}

func TestADayDividerKeepsTimesShortAfterIt(t *testing.T) {
	app, _ := talk(t)
	tt := sized(t, app, 1100, 760, false)
	mustText(t, tt, "Oct 30, 11:35", "Sat, Oct 31", "11:00", "12:02")
	mustNotText(t, tt, "Oct 31, 11:00")
	mustNotText(t, tt, "Oct 31, 12:02")
}
