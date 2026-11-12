package desktop_test

import (
	"fmt"
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

func TestTwoPanes(t *testing.T) {
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
				for _, s := range []string{"Message", "Send", "More", "Stop"} {
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
	message, ok := tt.Find("Message")
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
