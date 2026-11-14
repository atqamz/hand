package desktop_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

func composerBox(t *testing.T, tt *ui.Tester) ui.Rect {
	t.Helper()
	box, ok := tt.Find("Composer")
	if !ok || box.W <= 0 || box.H <= 0 {
		t.Fatal("the composer box is not drawn")
	}
	return box
}

func composerText(t *testing.T, tt *ui.Tester) ui.Rect {
	t.Helper()
	box, ok := tt.Find("Message the supervisor")
	if !ok || box.W <= 0 || box.H <= 0 {
		t.Fatal("the text box is not drawn")
	}
	return box
}

func TestScenesTheComposerAtRest(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme, func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
			tt := sized(t, app, 1100, 760, dark)
			mustText(t, tt, "Message the supervisor", "Send")
			mustNotText(t, tt, "Clear")
			mustNotText(t, tt, "Ctrl Enter")
			box := composerBox(t, tt)
			if box.W > 760 || box.H != 90 {
				t.Errorf("the empty box is %.0f by %.0f, want at most 760 by 90", box.W, box.H)
			}
			if bottom := box.Y + box.H; bottom > 760 || 760-bottom > 20 {
				t.Errorf("the box ends at %.0f in a 760 px window", bottom)
			}
			shotSized(t, tt, "27-composer-empty-"+theme, 1100, 760)
		})
	}
}

func TestScenesTheComposerFocused(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme, func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
			tt := sized(t, app, 1100, 760, dark)
			click(t, tt, "Message the supervisor")
			tt.Type("what next?")
			tt.Frame()
			shotSized(t, tt, "30-composer-focused-"+theme, 1100, 760)
		})
	}
}

func TestScenesTheComposerHint(t *testing.T) {
	const hint = "s1 is waiting on a screen in Needs; your message waits too."
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme, func(t *testing.T) {
			app := newApp(t, blockedComposerFixture(t))
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
			tt := sized(t, app, 1100, 760, dark)
			mustText(t, tt, hint, "Send")
			if box := composerBox(t, tt); box.H != 94 {
				t.Errorf("the box with a wrapped hint is %.0f high, want 94", box.H)
			}
			shotSized(t, tt, "28-composer-hint-"+theme, 1100, 760)
		})
	}
}

func TestScenesTheComposerDraftNote(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme, func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
			tt := sized(t, app, 1100, 760, dark)
			click(t, tt, "Message the supervisor")
			tt.Type("half a message")
			tt.Frame()
			mustText(t, tt, "Draft saved 14:32")
			if box := composerBox(t, tt); box.H != 90 {
				t.Errorf("the draft note grew the box to %.0f", box.H)
			}
			shotSized(t, tt, "29-composer-draft-"+theme, 1100, 760)
		})
	}
}

func TestScenesTheComposerSendDefault(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme, func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
			app.FormInHead = true
			tt := sized(t, app, 1100, 760, dark)
			click(t, tt, "Message the supervisor")
			tt.Type("a message while a form is open")
			tt.Frame()
			shotSized(t, tt, "31-composer-default-"+theme, 1100, 760)
		})
	}
}

func blockedComposerFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	st := f.status[fleetID]
	hint := "s1 is waiting on a screen in Needs; your message waits too."
	st.ComposerHint = hint
	sup := *st.Supervisor
	sup.Blocked = true
	sup.Ask = &desktop.Ask{Question: "Allow Bash?", Options: []desktop.AskOption{{Label: "1 Yes", Keys: []string{"1"}}}}
	st.Supervisor = &sup
	st.Needs.Items = append([]desktop.Need{{
		Kind: "blocked", Ref: "s1", Title: "Allow Bash?", Word: "BLOCKED", Tone: "fail",
	}}, st.Needs.Items...)
	f.status[fleetID] = st
	return f
}

func TestTheComposerHintSaysWhyAMessageWaits(t *testing.T) {
	cases := []struct {
		name, hint string
	}{
		{"none", "No supervisor is running; your message waits."},
		{"starting", "s1 is starting; your message waits."},
		{"stopped", "s1 stopped: test; your message waits."},
		{"luvus", "Luvus restarted; your message waits."},
		{"blocked", "s1 is waiting on a screen in Needs; your message waits too."},
		{"switching", "s1 switches to opus high after this turn; your message waits."},
		{"ready", ""},
		{"working", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := goldenFixture(t)
			st := f.status[fleetID]
			st.ComposerHint = c.hint
			f.status[fleetID] = st
			tt := sized(t, newApp(t, f), 1100, 760, false)
			if c.hint == "" {
				mustNotText(t, tt, "your message waits")
				if box := composerBox(t, tt); box.H != 90 {
					t.Errorf("the empty hint row made the box %.0f high, want the send group's 32 plus the box, 90", box.H)
				}
				return
			}
			mustText(t, tt, c.hint)
		})
	}
}

func TestTheComposerHintWrapsAndGrowsTheBox(t *testing.T) {
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.ComposerHint = "No supervisor is running; your message waits."
	f.status[fleetID] = st
	tt := sized(t, newApp(t, f), 1100, 760, false)
	one := composerBox(t, tt)
	hint, ok := tt.Find(st.ComposerHint)
	if !ok || hint.H != 18 {
		t.Fatalf("the one-line hint is %v, want 18 high", hint)
	}
	if one.H != 90 {
		t.Fatalf("the one-line hint box is %.0f high, want 90", one.H)
	}

	f = goldenFixture(t)
	st = f.status[fleetID]
	st.ComposerHint = "s1 is waiting on a screen in Needs; your message waits too."
	f.status[fleetID] = st
	tt = sized(t, newApp(t, f), 1100, 760, false)
	two := composerBox(t, tt)
	hint, ok = tt.Find(st.ComposerHint)
	if !ok || hint.H != 36 {
		t.Fatalf("the two-line hint is %v, want 36 high", hint)
	}
	if two.H != 94 {
		t.Errorf("the two-line hint box is %.0f high, want 94", two.H)
	}
	if two.Y+two.H != one.Y+one.H {
		t.Errorf("the box moved: %0.f then %0.f", one.Y+one.H, two.Y+two.H)
	}
}

func TestTheBlockedHintOpensItsItem(t *testing.T) {
	f := blockedComposerFixture(t)
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	hint := f.status[fleetID].ComposerHint
	click(t, tt, hint)
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("screen = %v, want the Board", app.Screen)
	}
	if !desktop.NeedChosen(app, "blocked:s1") {
		t.Fatal("the blocked item was not opened")
	}
}

func TestTheSendKeyHintFollowsTheHardwareAndThePane(t *testing.T) {
	wide := func(apple, coarse bool) *ui.Tester {
		app := newApp(t, goldenFixture(t))
		app.Apple, app.Coarse = apple, coarse
		return sized(t, app, 1440, 900, false)
	}
	tt := wide(false, false)
	mustText(t, tt, "Ctrl Enter")
	mustNotText(t, tt, "⌘ Enter")

	tt = wide(true, false)
	mustText(t, tt, "⌘ Enter")
	mustNotText(t, tt, "Ctrl Enter")

	tt = wide(false, true)
	mustNotText(t, tt, "Ctrl Enter")
	mustNotText(t, tt, "⌘ Enter")

	narrow := sized(t, newApp(t, goldenFixture(t)), 1100, 760, false)
	mustNotText(t, narrow, "Ctrl Enter")
	mustNotText(t, narrow, "⌘ Enter")
	if chat := 1100 - chatLeft(t, narrow); chat >= 520 {
		t.Fatalf("the pane is %.0f wide, want under 520 for the hidden key", chat)
	}
}

func TestCtrlEnterSendsAndEnterBreaksTheLine(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "Message the supervisor")
	tt.Type("one")
	tt.Key(0, ui.KeyEnter)
	tt.Type("two")
	tt.Frame()
	if f.count("send:"+fleetID) != 0 {
		t.Fatalf("Enter sent the message %d times", f.count("send:"+fleetID))
	}
	if app.Draft != "one\ntwo" {
		t.Fatalf("the box holds %q, want a line break", app.Draft)
	}
	tt.Key(ui.Ctrl, ui.KeyEnter)
	tt.Frame()
	if f.count("send:"+fleetID) != 1 || f.sentText != "one\ntwo" {
		t.Fatalf("Ctrl+Enter sent %d times, %q", f.count("send:"+fleetID), f.sentText)
	}
}

func TestAFailedSendReportsInPlaceAndKeepsSend(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "Message the supervisor")
	tt.Type("keep this text")
	f.action = &desktop.APIError{Status: 500, Code: "internal", Message: "the supervisor is gone"}
	click(t, tt, "Send")
	tt.Frame()
	mustText(t, tt, "The action did not go through", "the supervisor is gone")
	if app.Draft != "keep this text" {
		t.Fatalf("draft = %q after a failed send", app.Draft)
	}
	box := composerBox(t, tt)
	err, ok := tt.Find("The action did not go through")
	if !ok || err.Y < box.Y || err.Y+err.H > box.Y+box.H {
		t.Fatalf("the error %v is not inside the composer %v", err, box)
	}
	f.action = nil
	click(t, tt, "Send")
	tt.Frame()
	if got := f.count("send:" + fleetID); got != 2 {
		t.Fatalf("Send stayed disabled: %d sends after the failure", got)
	}
}

func TestASendInFlightKeepsTheTextBoxUntilTheAnswer(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "Message the supervisor")
	tt.Type("still here")
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "Send")
	waitPost(t, posted)()
	tt.Frame()
	select {
	case <-f.actionEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never sent the message")
	}
	if app.Draft != "still here" {
		t.Fatalf("the text left the box before the answer: %q", app.Draft)
	}
	mustText(t, tt, "Sending…")
	close(f.actionBlock)
	waitPost(t, posted)()
	waitPost(t, posted)()
	tt.Frame()
	if app.Draft != "" {
		t.Fatalf("draft = %q after the answer", app.Draft)
	}
}

func TestTheDraftWaitsForItsQuietMoment(t *testing.T) {
	dir := t.TempDir()
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Boot()
	clock := newFakeClock()
	posted := make(chan func(), 64)
	app.Clock = clock
	app.Post = func(fn func()) { posted <- fn }
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "Message the supervisor")
	tt.Type("abc")
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "" {
		t.Fatalf("the draft was written before the quiet moment: %q", got)
	}
	clock.Advance(299 * time.Millisecond)
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "" {
		t.Fatalf("the draft was written after 299ms: %q", got)
	}
	clock.Advance(time.Millisecond)
	app.FlushDrafts()
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "abc" {
		t.Fatalf("the draft after 300ms = %q", got)
	}
	pump(t, posted, func() bool { return desktop.DraftNote(app) != "" })

	tt.Type("d")
	app.SetMinimized(true)
	app.FlushDrafts()
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "abcd" {
		t.Fatalf("hiding the window left %q", got)
	}

	tt.Type("e")
	app.Close()
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "abcde" {
		t.Fatalf("closing the window left %q", got)
	}
	pump(t, posted, func() bool { return desktop.DraftNote(app) != "" })
}

func TestADraftIsRestoredOnlyIntoAnEmptyBox(t *testing.T) {
	dir := t.TempDir()
	if err := (desktop.Drafts{Dir: dir}).Save(fleetID, "from disk"); err != nil {
		t.Fatal(err)
	}
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Boot()
	if app.Draft != "from disk" {
		t.Fatalf("boot restored %q", app.Draft)
	}
	tt := sized(t, app, 1100, 760, false)
	mustText(t, tt, "Draft restored")
	click(t, tt, "Message the supervisor")
	tt.Type(" mine")
	tt.Frame()
	if app.Draft != "from disk mine" {
		t.Fatalf("the restored box holds %q", app.Draft)
	}
	app.SelectFleet(fleetID)
	if app.Draft != "from disk mine" {
		t.Fatalf("selecting the same fleet replaced the box with %q", app.Draft)
	}
	app.Draft = ""
	app.SelectFleet(fleetID)
	if app.Draft != "from disk mine" {
		t.Fatalf("an empty box restored %q, want the stored text", app.Draft)
	}
}

func TestTheDraftNoteSaysSavedOrRestoredAndHides(t *testing.T) {
	dir := t.TempDir()
	if err := (desktop.Drafts{Dir: dir}).Save(fleetID, "stored"); err != nil {
		t.Fatal(err)
	}
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Now = func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
	app.Boot()
	tt := sized(t, app, 1100, 760, false)
	mustText(t, tt, "Draft restored")
	mustNotText(t, tt, "Draft saved")
	if box := composerBox(t, tt); box.H != 90 {
		t.Fatalf("a restored note grew the box to %.0f", box.H)
	}
	click(t, tt, "Message the supervisor")
	tt.Type("!")
	tt.Frame()
	mustText(t, tt, "Draft saved 14:32")
	mustNotText(t, tt, "Draft restored")
	for range len("stored!") {
		tt.Key(0, ui.KeyBackspace)
	}
	tt.Frame()
	mustNotText(t, tt, "Draft saved")
	mustNotText(t, tt, "Draft restored")
	if note := desktop.DraftNote(app); note != "" {
		t.Fatalf("the empty box still carries %q", note)
	}
}

func TestALiveUpdateLeavesTheDraftAndTheCaretAlone(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "Message the supervisor")
	tt.Type("ab")
	tt.Key(0, ui.KeyLeft)
	tt.Frame()

	st := f.status[fleetID]
	st.ComposerHint = "s1 is starting; your message waits."
	st.Line = "changed"
	f.status[fleetID] = st
	tl := f.timeline[fleetID]
	tl.Entries = append(tl.Entries, desktop.Entry{Role: "supervisor", Ref: "s1", No: 99, At: "2026-11-01T11:59:00Z", Text: "a live message"})
	f.timeline[fleetID] = tl
	app.Reload()
	tt.Frame()
	mustText(t, tt, "a live message", "s1 is starting; your message waits.")
	tt.Type("X")
	if app.Draft != "aXb" {
		t.Fatalf("a live update moved the caret: %q", app.Draft)
	}

	tt.SetSize(1440, 900)
	tt.Frame()
	tt.Type("Y")
	if app.Draft != "aXYb" {
		t.Fatalf("a resize moved the caret: %q", app.Draft)
	}
}

func TestTheHintFollowsTheStatusOnThePollTick(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	app.Show(desktop.BoardScreen)
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "Message the supervisor")
	tt.Type("my draft")
	app.StartRefresh()
	b.setStatus(func(st *desktop.Status) { st.ComposerHint = "s1 is starting; your message waits." })
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "s1 is starting; your message waits.")
	if app.Draft != "my draft" {
		t.Fatalf("the poll touched the typed text: %q", app.Draft)
	}
}

func TestSendIsPrimaryUntilAFormIsInTheHead(t *testing.T) {
	for _, dark := range []bool{false, true} {
		k := colorsFor(dark)
		app := newApp(t, goldenFixture(t))
		bg, fg, edge, bold := desktop.SendStyle(app, dark)
		if bg != k.Primary || fg != k.OnPrimary || edge.A != 0 || !bold {
			t.Errorf("dark=%v: Send is %v on %v with edge %v bold=%v, want primary", dark, fg, bg, edge, bold)
		}
		app.FormInHead = true
		bg, fg, edge, bold = desktop.SendStyle(app, dark)
		if bg != k.BG || fg != k.FG || edge != k.LineStrong || bold {
			t.Errorf("dark=%v: Send with a form is %v on %v with edge %v bold=%v, want default", dark, fg, bg, edge, bold)
		}
	}
}

func colorsFor(dark bool) kit.Colors { return kit.ColorsFor(dark) }

func TestTheTextBoxGrowsToFortyPercentThenScrolls(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		max           float32
	}{{1100, 760, 304}, {1440, 900, 360}} {
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			tt := sized(t, app, tc.width, tc.height, false)
			click(t, tt, "Message the supervisor")
			rest := composerText(t, tt)
			if rest.H != 44 {
				t.Fatalf("the empty text box is %.0f high, want 44", rest.H)
			}
			tt.Type(strings.Repeat("line\n", 40))
			tt.Frame()
			grown := composerText(t, tt)
			if grown.H > tc.max+.5 {
				t.Errorf("the text box is %.0f high, past 40%% of %d", grown.H, tc.height)
			}
			if grown.H < tc.max-20 {
				t.Errorf("the text box is %.0f high, want about %.0f", grown.H, tc.max)
			}
		})
	}
}

func TestTheComposerHoldsTheBoardsWidth(t *testing.T) {
	for _, width := range []int{800, 900, 1100, 1440, 2400} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			app.Chat = 1200
			tt := sized(t, app, width, 760, false)
			drawn := drawnZoom(1, width)
			box := composerBox(t, tt)
			if box.W/drawn > 760.5 {
				t.Errorf("%d wide: the box is %.0f logical, want at most 760", width, box.W/drawn)
			}
			if bottom := (760 - (box.Y + box.H)) / drawn; box.Y+box.H > 760 || bottom < 15 {
				t.Errorf("%d wide: the box ends %.0f logical px above the bottom, want 16", width, bottom)
			}
			head, ok := tt.Find("Supervisor")
			if !ok {
				t.Fatal("no pane head")
			}
			left, right := (box.X-head.X)/drawn, (head.X+head.W-(box.X+box.W))/drawn
			if left < 15 {
				t.Errorf("%d wide: the box starts %.0f logical px into the pane, want 16", width, left)
			}
			if diff := right - left; diff > 1 || diff < -1 {
				t.Errorf("%d wide: the box has %.0f logical px on its left and %.0f on its right, want it centred", width, left, right)
			}
		})
	}
}

func TestTheComposerEndsInsideTheWindow(t *testing.T) {
	for _, tc := range []struct{ width, height int }{{1100, 760}, {1440, 900}} {
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			tt := sized(t, newApp(t, goldenFixture(t)), tc.width, tc.height, false)
			box := composerBox(t, tt)
			if box.Y < 0 || box.Y+box.H > float32(tc.height) {
				t.Errorf("the composer %v is outside the %dx%d window", box, tc.width, tc.height)
			}
			if gap := float32(tc.height) - (box.Y + box.H); gap < 15 || gap > 17 {
				t.Errorf("the composer ends %.0f px above the bottom, want 16", gap)
			}
		})
	}
}
