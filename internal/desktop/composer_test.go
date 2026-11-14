package desktop_test

import (
	"fmt"
	"image"
	"image/color"
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
			k := colorsFor(dark)
			app := newApp(t, goldenFixture(t))
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
			tt := sized(t, app, 1100, 760, dark)
			rest := composerBox(t, tt)
			if got := tt.Image().RGBAAt(int(rest.X+rest.W/2), int(rest.Y)); !sameInk(got, k.LineStrong) {
				t.Errorf("the resting box's top edge is %v, want the control edge %v", got, k.LineStrong)
			}
			click(t, tt, "Message the supervisor")
			tt.Type("what next?")
			tt.Frame()
			box := composerBox(t, tt)
			img := tt.Image()
			if got := img.RGBAAt(int(box.X+box.W/2), int(box.Y)); !sameInk(got, k.Accent) {
				t.Errorf("the focused box's border is %v, want the accent %v", got, k.Accent)
			}
			if got := img.RGBAAt(int(box.X+box.W/2), int(box.Y)-1); !sameInk(got, k.Accent) {
				t.Errorf("the ring above the focused box is %v, want the accent %v", got, k.Accent)
			}
			if box.H != 90 {
				t.Errorf("the focused box is %.0f high, want 90", box.H)
			}
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
			k := colorsFor(dark)
			app := newApp(t, resumableFixture(t))
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
			tt := sized(t, app, 1100, 760, dark)
			mustText(t, tt, "Resume")
			click(t, tt, "Message the supervisor")
			tt.Type("a message")
			tt.Frame()
			send, ok := tt.Find("Send")
			if !ok {
				t.Fatal("no Send")
			}
			img := tt.Image()
			if got := img.RGBAAt(int(send.X)+4, int(send.Y+send.H/2)); !sameInk(got, k.BG) {
				t.Errorf("Send's fill is %v, want the ground %v", got, k.BG)
			}
			if got := img.RGBAAt(int(send.X+send.W/2), int(send.Y)); !sameInk(got, k.LineStrong) {
				t.Errorf("Send's edge is %v, want the control edge %v", got, k.LineStrong)
			}
			if box := composerBox(t, tt); box.H != 90 {
				t.Errorf("the box with Send default is %.0f high, want 90", box.H)
			}
			shotSized(t, tt, "31-composer-default-"+theme, 1100, 760)
		})
	}
}

func TestScenesTheComposerKeyHint(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme, func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			app.Apple = false // the scene pins Ctrl Enter, which macOS's GOOS proxy would turn into ⌘
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
			tt := sized(t, app, 1440, 900, dark)
			if chat := 1440 - chatLeft(t, tt); chat < 520 {
				t.Fatalf("the pane is %.0f wide, want at least 520 for the key", chat)
			}
			click(t, tt, "Message the supervisor")
			tt.Type("look at the rename")
			tt.Frame()
			mustText(t, tt, "Ctrl Enter")
			key, ok := tt.Find("Ctrl Enter")
			if !ok {
				t.Fatal("no key hint")
			}
			send, _ := tt.Find("Send")
			if gap := send.X - (key.X + key.W); gap != 8 {
				t.Errorf("the key at %v and Send at %v are %.0f apart, want 8", key, send, gap)
			}
			shotSized(t, tt, "36-composer-key-1440x900-"+theme, 1440, 900)
		})
	}
}

func resumableFixture(t *testing.T) *fixture {
	t.Helper()
	f := statusWith(t, func(s *desktop.Supervisor) { s.Status, s.Live, s.Resumable = "interrupted", false, true })
	st := f.status[fleetID]
	st.AgentState, st.Pill, st.Line = "idle", "idle", "interrupted"
	f.status[fleetID] = st
	tl := f.timeline[fleetID]
	tl.Live, tl.Working = false, nil
	f.timeline[fleetID] = tl
	return f
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
	wide := func(apple bool) (*ui.Tester, *desktop.App) {
		app := newApp(t, goldenFixture(t))
		app.Apple = apple
		return sized(t, app, 1440, 900, false), app
	}
	tt, app := wide(false)
	mustText(t, tt, "Ctrl Enter")
	mustNotText(t, tt, "⌘ Enter")

	tt, app = wide(true)
	mustText(t, tt, "⌘ Enter")
	mustNotText(t, tt, "Ctrl Enter")

	narrow := sized(t, newApp(t, goldenFixture(t)), 1100, 760, false)
	mustNotText(t, narrow, "Ctrl Enter")
	mustNotText(t, narrow, "⌘ Enter")
	if chat := 1100 - chatLeft(t, narrow); chat >= 520 {
		t.Fatalf("the pane is %.0f wide, want under 520 for the hidden key", chat)
	}

	tt, app = wide(false)
	drag := func(to float32) {
		div, ok := tt.Find("Divider")
		if !ok {
			t.Fatal("no divider")
		}
		x, y := div.X+div.W/2, float32(400)
		tt.Press(x, y)
		tt.Move(to, y)
		tt.Release(to, y)
		tt.Frame()
		tt.Frame()
	}
	drag(950)
	if chat := 1440 - chatLeft(t, tt); chat >= 520 {
		t.Fatalf("the dragged pane is %.0f wide, want under 520", chat)
	}
	mustNotText(t, tt, "Ctrl Enter")
	drag(900)
	if chat := 1440 - chatLeft(t, tt); chat < 520 {
		t.Fatalf("the dragged pane is %.0f wide, want the key back over 520", chat)
	}
	mustText(t, tt, "Ctrl Enter")
	if app.Chat < 520 {
		t.Fatalf("the remembered chat width is %.0f, want over 520", app.Chat)
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
	waitDraft(t, posted, dir, "abc")
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "abc" {
		t.Fatalf("the draft after 300ms = %q", got)
	}

	tt.Type("d")
	app.SetMinimized(true)
	waitDraft(t, posted, dir, "abcd")
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

// waitDraft pumps the window's posted work until the draft file holds want,
// so the trigger under test is what wrote it.
func waitDraft(t *testing.T, posted <-chan func(), dir, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for (desktop.Drafts{Dir: dir}).Load(fleetID) != want {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("the draft file never held %q", want)
		}
	}
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

func TestSendIsPrimaryUntilTheHeadShowsResume(t *testing.T) {
	cases := []struct {
		name    string
		fixture func(*testing.T) *fixture
		primary bool
	}{
		{"working", goldenFixture, true},
		{"running but unreachable", func(t *testing.T) *fixture {
			return statusWith(t, func(s *desktop.Supervisor) { s.Status, s.Live, s.Resumable = "running", false, true })
		}, true},
		{"resume in the head", resumableFixture, false},
	}
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		k := colorsFor(dark)
		for _, c := range cases {
			t.Run(c.name+"/"+theme, func(t *testing.T) {
				tt := sized(t, newApp(t, c.fixture(t)), 1100, 760, dark)
				click(t, tt, "Message the supervisor")
				tt.Type("x")
				tt.Frame()
				send, ok := tt.Find("Send")
				if !ok {
					t.Fatal("no Send")
				}
				bg, edge := k.BG, k.LineStrong
				if c.primary {
					bg, edge = k.Primary, k.Primary
				}
				img := tt.Image()
				if got := img.RGBAAt(int(send.X)+4, int(send.Y+send.H/2)); !sameInk(got, bg) {
					t.Errorf("Send's fill is %v, want %v", got, bg)
				}
				if got := img.RGBAAt(int(send.X+send.W/2), int(send.Y)); !sameInk(got, edge) {
					t.Errorf("Send's edge is %v, want %v", got, edge)
				}
				if c.primary {
					return
				}
				rest := sized(t, newApp(t, goldenFixture(t)), 1100, 760, dark)
				click(t, rest, "Message the supervisor")
				rest.Type("x")
				rest.Frame()
				primary, _ := rest.Find("Send")
				ink, want := inkCount(img, send, bg), inkCount(rest.Image(), primary, k.Primary)
				if ink*10 < want*9 {
					t.Errorf("the default Send's label has %d ink pixels against the primary's %d, want the same weight", ink, want)
				}
			})
		}
	}
}

func sameInk(got color.RGBA, want ui.Color) bool {
	return got.R == want.R && got.G == want.G && got.B == want.B
}

func inkCount(img *image.RGBA, r ui.Rect, bg ui.Color) int {
	ink := 0
	for y := int(r.Y) + 2; y < int(r.Y+r.H)-2; y++ {
		for x := int(r.X) + 2; x < int(r.X+r.W)-2; x++ {
			if !sameInk(img.RGBAAt(x, y), bg) {
				ink++
			}
		}
	}
	return ink
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
			box := composerBox(t, tt)
			if bottom := box.Y + box.H; bottom > float32(tc.height) {
				t.Errorf("the grown composer ends at %.0f in a %d px window", bottom, tc.height)
			}
			if gap := float32(tc.height) - (box.Y + box.H); gap < 15 || gap > 17 {
				t.Errorf("the grown composer ends %.0f px above the bottom, want 16", gap)
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
