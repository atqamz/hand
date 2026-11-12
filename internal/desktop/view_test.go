package desktop_test

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
	"github.com/egoist/mygo/ui"
)

const (
	windowWidth  = 1100
	windowHeight = 760
)

func newTester(app *desktop.App) *ui.Tester {
	return ui.NewTester(app.View, windowWidth, windowHeight)
}

func shot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	shotSized(t, tt, name, windowWidth, windowHeight)
}

func shotSized(t *testing.T, tt *ui.Tester, name string, width, height int) {
	t.Helper()
	img := tt.Image()
	if img == nil {
		t.Fatal("no frame")
	}
	if img.Bounds().Dx() != width || img.Bounds().Dy() != height {
		t.Fatalf("frame is %v, want %dx%d", img.Bounds(), width, height)
	}
	dir := os.Getenv("DESKTOP_SHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func mustText(t *testing.T, tt *ui.Tester, want ...string) {
	t.Helper()
	for _, s := range want {
		if !tt.HasText(s) {
			t.Errorf("missing %q in %q", s, tt.Texts())
		}
	}
}

func mustNotText(t *testing.T, tt *ui.Tester, want string) {
	t.Helper()
	if tt.HasText(want) {
		t.Errorf("unexpected %q in %q", want, tt.Texts())
	}
}

func click(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	if err := tt.Click(text); err != nil {
		t.Fatalf("click %q: %v", text, err)
	}
}

func menu(t *testing.T, tt *ui.Tester, item string) {
	t.Helper()
	click(t, tt, "More")
	click(t, tt, item)
}

func TestFleetSwitch(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	mustText(t, tt, "golden", "second")
	click(t, tt, "second")
	mustText(t, tt, "second", secondID)
	mustNotText(t, tt, "f00000000e0aa")
	if app.Fleet.ID != secondID {
		t.Fatalf("fleet = %s, want %s", app.Fleet.ID, secondID)
	}
}

func TestThePanesShowNeedsTasksAndTheSupervisor(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	mustText(t, tt, "Needs", "Tasks", "s1", "sonnet · low", "Interrupt", "Stop", "status?", "Fix login")
	mustNotText(t, tt, "Status")
	mustNotText(t, tt, "Controls")
}

func statusWith(t *testing.T, change func(*desktop.Supervisor)) *fixture {
	t.Helper()
	f := goldenFixture(t)
	st := f.status[fleetID]
	sup := *st.Supervisor
	change(&sup)
	st.Supervisor = &sup
	f.status[fleetID] = st
	return f
}

func TestThePaneHeadShowsOnlyTheControlsThatApply(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*desktop.Supervisor)
		agent  string
		want   []string
		not    []string
	}{
		{"working", func(s *desktop.Supervisor) { s.Status, s.Live = "running", true }, "working",
			[]string{"Interrupt", "Stop"}, []string{"Start new", "Resume", "Force", "starting…"}},
		{"idle", func(s *desktop.Supervisor) { s.Status, s.Live = "running", true }, "ready",
			[]string{"Stop"}, []string{"Interrupt", "Start new", "Resume", "Force"}},
		{"blocked", func(s *desktop.Supervisor) { s.Status, s.Live, s.Blocked = "running", true, true }, "working",
			[]string{"Force", "Stop"}, []string{"Interrupt", "Start new", "Resume"}},
		{"resumable", func(s *desktop.Supervisor) { s.Status, s.Live, s.Resumable = "interrupted", false, true }, "",
			[]string{"Resume"}, []string{"Start", "Stop", "Interrupt", "Force"}},
		{"stopped", func(s *desktop.Supervisor) { s.Status, s.Live = "stopped", false }, "",
			[]string{"Start new, same as s1"}, []string{"Resume", "Stop", "Interrupt", "Force"}},
		{"starting", func(s *desktop.Supervisor) { s.Status, s.Live = "starting", true }, "",
			[]string{"starting…"}, []string{"Start new", "Resume", "Stop", "Interrupt", "Force"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := statusWith(t, tc.change)
			st := f.status[fleetID]
			st.AgentState = tc.agent
			f.status[fleetID] = st
			tt := newTester(newApp(t, f))
			mustText(t, tt, tc.want...)
			for _, s := range tc.not {
				mustNotText(t, tt, s)
			}
		})
	}
}

func TestWithNoSupervisorTheHeadOnlyOpensTheBoard(t *testing.T) {
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.Supervisor = nil
	f.status[fleetID] = st
	tt := newTester(newApp(t, f))
	mustText(t, tt, "Open in browser", "More")
	for _, s := range []string{"Start new", "Resume", "Stop", "Interrupt", "Force"} {
		mustNotText(t, tt, s)
	}
}

func TestStopNeedsASecondPressWithinTheWindow(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	tt := newTester(app)
	click(t, tt, "Stop")
	mustText(t, tt, "Press again to stop")
	if f.count("control:stop") != 0 {
		t.Fatal("the first press stopped the supervisor")
	}
	clock.Advance(desktop.StopWindow - time.Millisecond)
	tt.Frame()
	mustText(t, tt, "Press again to stop")
	clock.Advance(time.Millisecond)
	tt.Frame()
	mustText(t, tt, "Stop")
	mustNotText(t, tt, "Press again to stop")
	click(t, tt, "Stop")
	click(t, tt, "Press again to stop")
	if f.count("control:stop") != 1 {
		t.Fatalf("Stop calls = %d after two presses", f.count("control:stop"))
	}
	mustNotText(t, tt, "Press again to stop")
}

func TestAResumeRunsOnOnePress(t *testing.T) {
	f := statusWith(t, func(sup *desktop.Supervisor) {
		sup.Status, sup.Live, sup.Resumable, sup.Blocked, sup.Keys = "interrupted", false, true, false, nil
	})
	tt := newTester(newApp(t, f))
	click(t, tt, "Resume")
	if f.count("control:resume") != 1 {
		t.Fatalf("Resume calls = %d", f.count("control:resume"))
	}
}

func blockedFixture(t *testing.T) *fixture {
	t.Helper()
	f := statusWith(t, func(sup *desktop.Supervisor) {
		sup.Status, sup.Live, sup.Resumable, sup.Blocked = "running", true, false, true
		sup.Keys = &desktop.Keys{Revision: 6, Digest: "0123456789abcdef0123456789abcdef", Pressed: 2}
	})
	st := f.status[fleetID]
	st.AgentState, st.Pill, st.Line = "blocked", "failing", "blocked"
	f.status[fleetID] = st
	return f
}

func TestABlockedSupervisorShowsTheKeys(t *testing.T) {
	f := blockedFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	mustText(t, tt, "Keys", "Enter", "Esc", "↑", "↓", "1", "2", "3")
	click(t, tt, "Enter")
	if f.count("keys:enter") != 1 {
		t.Fatalf("key calls = %d", f.count("keys:enter"))
	}
	f.mu.Lock()
	keys, revision, digest, after, ref := append([]string(nil), f.keys...), f.keyRevision, f.keyDigest, f.keyAfter, f.keyRef
	f.mu.Unlock()
	if len(keys) != 1 || keys[0] != "enter" || revision != 6 || digest != "0123456789abcdef0123456789abcdef" || after != 2 || ref != "s1" {
		t.Fatalf("pressed %q at revision %d digest %q after %d for %q", keys, revision, digest, after, ref)
	}
	click(t, tt, "Force")
	if f.count("control:force") != 1 {
		t.Fatalf("Force calls = %d", f.count("control:force"))
	}
}

func stop(t *testing.T, tt *ui.Tester) {
	t.Helper()
	click(t, tt, "Stop")
	click(t, tt, "Press again to stop")
}

func askFixture(t *testing.T) *fixture {
	t.Helper()
	f := blockedFixture(t)
	st := f.status[fleetID]
	sup := *st.Supervisor
	sup.Ask = &desktop.Ask{Question: "Do you want to proceed?", Options: []desktop.AskOption{
		{Label: "Yes", Keys: []string{"1"}},
		{Label: "Yes, and do not ask again", Keys: []string{"down", "enter"}},
		{Label: "No", Keys: []string{"esc"}},
	}}
	st.Supervisor = &sup
	st.Needs.Items = append([]desktop.Need{{Kind: "blocked", Ref: "s1", Word: "BLOCKED", Tone: "fail"}}, st.Needs.Items...)
	f.status[fleetID] = st
	return f
}

func TestABlockedSupervisorShowsItsQuestionAndOptions(t *testing.T) {
	f := askFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	mustText(t, tt, "Do you want to proceed?", "Yes", "Yes, and do not ask again", "No")
	mustNotText(t, tt, "Keys")
	click(t, tt, "Yes, and do not ask again")
	f.mu.Lock()
	keys, revision, digest, after, ref := append([]string(nil), f.keys...), f.keyRevision, f.keyDigest, f.keyAfter, f.keyRef
	f.mu.Unlock()
	if !slices.Equal(keys, []string{"down", "enter"}) || revision != 6 || digest != "0123456789abcdef0123456789abcdef" || after != 2 || ref != "s1" {
		t.Fatalf("pressed %q at revision %d digest %q after %d for %q", keys, revision, digest, after, ref)
	}
}

func TestAStaleAskPressIsRefusedAndTheAskStays(t *testing.T) {
	f := askFixture(t)
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "the screen changed since revision 6; nothing was sent"}
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Yes")
	tt.Frame()
	mustText(t, tt, "The action did not go through", "the screen changed since revision 6", "Do you want to proceed?", "Yes")
}

func TestTheBlockedPromptSitsBesideTheNeedsNotBehindThem(t *testing.T) {
	app := newApp(t, askFixture(t))
	tt := newTester(app)
	mustText(t, tt, "Blocked · s1", "Do you want to proceed?")
	click(t, tt, "Blocked · s1")
	mustText(t, tt, "Do you want to proceed?")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("screen = %v", app.Screen)
	}
}

func TestASupervisorControlIsBusyAndKeepsTheScreenOnError(t *testing.T) {
	f := statusWith(t, func(sup *desktop.Supervisor) {
		sup.Status, sup.Live, sup.Resumable, sup.Blocked, sup.Keys = "running", true, false, false, nil
	})
	app := newApp(t, f)
	tt := newTester(app)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 1)
	stop(t, tt)
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "Stopping…")
	select {
	case <-f.actionEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never stopped the supervisor")
	}
	close(f.actionBlock)
	waitPost(t, posted)()
	tt.Frame()
	mustNotText(t, tt, "Stopping…")

	app.Post = nil
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "supervisor s1 is not running"}
	stop(t, tt)
	tt.Frame()
	mustText(t, tt, "The action did not go through", "supervisor s1 is not running", "sonnet · low")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("screen = %v", app.Screen)
	}
}

func TestASupervisorControlAppliesTheReturnedStatus(t *testing.T) {
	f := statusWith(t, func(sup *desktop.Supervisor) {
		sup.Status, sup.Live, sup.Resumable, sup.Blocked, sup.Keys = "running", true, false, false, nil
	})
	stopped := f.status[fleetID]
	stopped.Line = "stopped by the test"
	stopped.Supervisor = &desktop.Supervisor{Ref: "s1", Harness: "claude", Status: "stopped"}
	f.controlStatus = &stopped
	app := newApp(t, f)
	before := f.count("status:" + fleetID)
	tt := newTester(app)
	stop(t, tt)
	tt.Frame()
	mustText(t, tt, "Start")
	if app.Status.Line != "stopped by the test" || app.Status.Supervisor.Status != "stopped" {
		t.Fatalf("status = %+v", app.Status)
	}
	if got := f.count("status:" + fleetID); got != before {
		t.Fatalf("the window re-read the status: %d reads, want %d", got, before)
	}
}

func TestASupervisorControlWarnsWhenTheStatusCannotBeRead(t *testing.T) {
	f := statusWith(t, func(sup *desktop.Supervisor) {
		sup.Status, sup.Live, sup.Resumable, sup.Blocked, sup.Keys = "running", true, false, false, nil
	})
	f.controlStale = true
	app := newApp(t, f)
	before := f.count("status:" + fleetID)
	tt := newTester(app)
	stop(t, tt)
	tt.Frame()
	mustText(t, tt, "The action went through", "the fleet status could not be read", "sonnet · low")
	if app.Status.Supervisor == nil || app.Status.Supervisor.Status != "running" || app.Busy {
		t.Fatalf("status = %+v, busy = %v", app.Status.Supervisor, app.Busy)
	}
	f.mu.Lock()
	ref := f.controlRef
	f.mu.Unlock()
	if ref != "s1" {
		t.Fatalf("the control sent ref %q", ref)
	}
	if got := f.count("status:" + fleetID); got != before {
		t.Fatalf("the window re-read the status: %d reads, want %d", got, before)
	}
}

func TestTheBoardScreenListsTheNeeds(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Board")
	mustText(t, tt, "Needs", "Failed", "Decision", "Report",
		"Keep the old cookie name?",
		"Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump",
		"Cookie fixed")
}

func TestABlockedNeedShowsItsTitleNotTheExcerptsRuleLine(t *testing.T) {
	const rule = "────────────────────────"
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.Needs.Items = append([]desktop.Need{{Kind: "blocked", Ref: "s1", Word: "BLOCKED", Tone: "fail", Title: "Bash: rm -f x · Proceed?", Excerpt: rule + "\n Bash command\n  rm -f x"}}, st.Needs.Items...)
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	tt := newTester(newApp(t, f))
	click(t, tt, "Board")
	mustText(t, tt, "Bash: rm -f x · Proceed?")
	mustNotText(t, tt, rule)
}

func TestNeedsOpensADecisionInPlace(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	mustText(t, tt, "d1", "Yes, keep it", "No, rename it", "Send answer", "Open d1")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("a Needs row left the Board screen for %v", app.Screen)
	}
	click(t, tt, "Open d1")
	mustText(t, tt, "Options", "Open task")
}

func TestTheConversationShowsItsEntriesAndTheWorkingLine(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	mustText(t, tt, "Conversation", "s1 working", "All good.", "status?", "you", "s1 started")
}

func TestComposerSendsWithCtrlEnter(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("hello there")
	tt.Key(ui.Ctrl, ui.KeyEnter)
	tt.Frame()
	if f.count("send:"+fleetID) != 1 || f.sentText != "hello there" {
		t.Fatalf("send calls = %d, sent %q", f.count("send:"+fleetID), f.sentText)
	}
	mustText(t, tt, "hello there")
	if app.Draft != "" {
		t.Fatalf("draft = %q after a send", app.Draft)
	}
	click(t, tt, "Message the supervisor")
	tt.Type("again")
	tt.Key(ui.Super, ui.KeyEnter)
	tt.Frame()
	if f.sentText != "again" {
		t.Fatalf("the second send was %q", f.sentText)
	}
	mustNotText(t, tt, "Sending…")
}

func TestComposerShiftEnterBreaksTheLine(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("one")
	tt.Key(ui.Shift, ui.KeyEnter)
	tt.Type("two")
	tt.Frame()
	if got := f.count("send:" + fleetID); got != 0 {
		t.Fatalf("Shift+Enter sent the message %d times", got)
	}
	if app.Draft != "one\ntwo" {
		t.Fatalf("draft = %q, want a newline", app.Draft)
	}
	click(t, tt, "Send")
	if f.sentText != "one\ntwo" {
		t.Fatalf("sent %q", f.sentText)
	}
}

func TestAFailedSendKeepsTheDraft(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("keep this text")
	f.action = &desktop.APIError{Status: 500, Code: "internal", Message: "the supervisor is gone"}
	click(t, tt, "Send")
	tt.Frame()
	mustText(t, tt, "The action did not go through", "the supervisor is gone")
	if app.Draft != "keep this text" {
		t.Fatalf("draft = %q after a failed send", app.Draft)
	}
}

func TestTheComposerIsDisabledWhileSending(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("one moment")
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "Send")
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "Sending…")
	select {
	case <-f.actionEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never sent the message")
	}
	close(f.actionBlock)
	waitPost(t, posted)()
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "one moment")
	mustNotText(t, tt, "Sending…")
}

func TestTheDraftSurvivesAReopenAndFleetSwitch(t *testing.T) {
	dir := t.TempDir()
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("half a message")
	tt.Frame()
	app.FlushDrafts()
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "half a message" {
		t.Fatalf("saved draft = %q", got)
	}
	click(t, tt, "second")
	if app.Draft != "" {
		t.Fatalf("the second fleet inherited the draft %q", app.Draft)
	}
	click(t, tt, "golden")
	if app.Draft != "half a message" {
		t.Fatalf("the draft did not come back: %q", app.Draft)
	}

	again := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	again.DraftDir = dir
	again.Boot()
	if again.Draft != "half a message" {
		t.Fatalf("a reopened window loaded %q", again.Draft)
	}
	tt = newTester(again)
	if again.Draft != "half a message" {
		t.Fatalf("the reopened composer has %q", again.Draft)
	}
	mustText(t, tt, "Message", "Send")
}

func TestSendingClearsTheStoredDraft(t *testing.T) {
	dir := t.TempDir()
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("send me")
	tt.Frame()
	click(t, tt, "Send")
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "" {
		t.Fatalf("the stored draft = %q after a send", got)
	}
}

func TestTheComposerSendsTheDraftTextAsWritten(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("  indented\n\nand trailing\n")
	tt.Frame()
	click(t, tt, "Send")
	if want := "  indented\n\nand trailing\n"; f.sentText != want {
		t.Fatalf("sent %q, want %q", f.sentText, want)
	}
	if app.Draft != "" {
		t.Fatalf("draft = %q after a send", app.Draft)
	}
}

func TestASendWhoseTimelineCannotBeReadClearsTheDraft(t *testing.T) {
	f := goldenFixture(t)
	f.stale = true
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("sent once")
	tt.Frame()
	click(t, tt, "Send")
	tt.Frame()
	if got := f.count("send:" + fleetID); got != 1 {
		t.Fatalf("the message was sent %d times", got)
	}
	if app.Draft != "" {
		t.Fatalf("draft = %q after an accepted send", app.Draft)
	}
	mustText(t, tt, "The action went through", "the message was sent, but the conversation could not be read")
	mustNotText(t, tt, "The action did not go through")
}

func TestTheDraftWhoseSaveFailedSurvivesAFleetSwitch(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = blocked
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("keep this draft")
	tt.Frame()
	mustText(t, tt, "the draft was not saved")
	click(t, tt, "second")
	if app.Draft != "" {
		t.Fatalf("the second fleet inherited the draft %q", app.Draft)
	}
	click(t, tt, "golden")
	if app.Draft != "keep this draft" {
		t.Fatalf("the unsaved draft did not come back: %q", app.Draft)
	}
	mustText(t, tt, "the draft was not saved")
}

func TestADraftThatCannotBeRemovedAfterASendIsNotASendFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, fleetID+".txt", "kept"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("sent anyway")
	tt.Frame()
	click(t, tt, "Send")
	mustText(t, tt, "the message was sent, but its stored draft could not be removed")
	if app.ActionErr != "" {
		t.Fatalf("a failed draft cleanup became a failed send: %q", app.ActionErr)
	}
	if app.Draft != "" {
		t.Fatalf("draft = %q after a send", app.Draft)
	}
}

func pump(t *testing.T, posted <-chan func(), until func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !until() {
		select {
		case fn := <-posted:
			fn()
		case <-deadline:
			t.Fatal("the window never settled")
		}
	}
}

func TestTheSentDraftIsClearedAfterANavigation(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("sent once")
	tt.Frame()
	posted := make(chan func(), 64)
	app.Post = func(fn func()) { posted <- fn }
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "Send")
	waitPost(t, posted)()
	select {
	case <-f.actionEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never sent the message")
	}
	app.Show(desktop.BoardScreen)
	close(f.actionBlock)
	deadline := time.After(5 * time.Second)
	for app.Draft != "" {
		select {
		case fn := <-posted:
			fn()
		case <-deadline:
			t.Fatalf("the sent draft stayed: %q", app.Draft)
		}
	}
}

func TestASaveFailureIsRestoredBeforeItsReport(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = blocked
	app.Boot()
	posted := make(chan func(), 64)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	pump(t, posted, func() bool { return !app.Loading })
	tt.Frame()
	click(t, tt, "Message the supervisor")
	tt.Type("keep this draft")
	click(t, tt, "second")
	click(t, tt, "golden")
	if app.Draft != "keep this draft" {
		t.Fatalf("the unsaved draft did not come back: %q", app.Draft)
	}
}

func TestAFailureSupersededByASaveIsForgotten(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "drafts")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = blocked
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("a")
	tt.Frame()
	mustText(t, tt, "the draft was not saved")
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	tt.Type("b")
	tt.Frame()
	mustNotText(t, tt, "the draft was not saved")
	click(t, tt, "second")
	click(t, tt, "golden")
	if app.Draft != "ab" {
		t.Fatalf("the restored draft = %q", app.Draft)
	}
}

func TestASendKeepsANewerDraft(t *testing.T) {
	dir := t.TempDir()
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("the older draft")
	tt.Frame()
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "Send")
	waitPost(t, posted)()
	select {
	case <-f.actionEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never sent the message")
	}
	if err := (desktop.Drafts{Dir: dir}).Save(fleetID, "a newer draft"); err != nil {
		t.Fatal(err)
	}
	close(f.actionBlock)
	waitPost(t, posted)()
	waitPost(t, posted)()
	tt.Frame()
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "a newer draft" {
		t.Fatalf("the send left %q", got)
	}
	if app.Draft != "" {
		t.Fatalf("draft = %q after a send", app.Draft)
	}
}

func TestTasksOpenTheirDetail(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	mustText(t, tt, "Tasks", "Fix login", "Write docs", "Active", "Inbox")
	click(t, tt, "Fix login")
	mustText(t, tt, "Fix login", "1.", "reproduce", "Plan · p1", "Cookie fixed",
		"Keep the old cookie name?", "a1")
	click(t, tt, "Back")
	mustText(t, tt, "Write docs")
}

func TestAttemptScreen(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "a1")
	mustText(t, tt, "Brief", "x", "20m", "Cookie fixed", "Reports")
}

func TestReportScreen(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Cookie fixed")
	click(t, tt, "Open r1")
	mustText(t, tt, "r1", "Cookie fixed", "All tests pass.", "Open task", "Mark read")
}

func TestAnswerDecision(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("t1")
	app.OpenRef("d1")
	tt.Frame()
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	if f.count("answer:d1") != 1 {
		t.Fatalf("answer calls = %d, want 1", f.count("answer:d1"))
	}
	if f.choice != "1. Yes, keep it" || f.answerNote != "" {
		t.Fatalf("sent choice %q note %q", f.choice, f.answerNote)
	}
	mustText(t, tt, "answered", "Answer", "1.", "Yes, keep it")
	mustNotText(t, tt, "Send answer")
}

func TestAnswerDecisionWithANote(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("t1")
	app.OpenRef("d1")
	tt.Frame()
	click(t, tt, "No, rename it")
	click(t, tt, "Note")
	tt.Type("the old name is in the docs")
	click(t, tt, "Send answer")
	if f.choice != "2. No, rename it" || f.answerNote != "the old name is in the docs" {
		t.Fatalf("sent choice %q note %q", f.choice, f.answerNote)
	}
	mustText(t, tt, "answered", "2.", "No, rename it", "the old name is in the docs")
}

func TestAFailedAnswerKeepsTheScreen(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "decision d1 is answered"}
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	mustText(t, tt, "The action did not go through", "decision d1 is answered",
		"Keep the old cookie name?", "Send answer")
	if app.Screen != desktop.BoardScreen || app.Decision.Decision.Ref != "d1" {
		t.Fatalf("the error left %v %q", app.Screen, app.Decision.Decision.Ref)
	}
	click(t, tt, "Dismiss")
	mustNotText(t, tt, "The action did not go through")
	mustText(t, tt, "Keep the old cookie name?")
}

func TestAckReport(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("t1")
	app.OpenRef("r1")
	tt.Frame()
	click(t, tt, "Mark read")
	if f.count("ack:r1") != 1 {
		t.Fatalf("ack calls = %d, want 1", f.count("ack:r1"))
	}
	mustText(t, tt, "Read by operator (desktop)", "Read")
	if app.Report.Report.AckedAt == "" {
		t.Fatal("the report is not acknowledged in the window")
	}
}

func TestAFailedAckKeepsTheScreen(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Cookie fixed")
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "report r1 is already acknowledged"}
	click(t, tt, "Mark read")
	mustText(t, tt, "The action did not go through", "report r1 is already acknowledged",
		"All tests pass.", "Mark read")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("the error left screen %v", app.Screen)
	}
}

func TestTheAnswerButtonIsDisabledWhileSending(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "Send answer")
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "Sending…")
	select {
	case <-f.actionEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never sent the answer")
	}
	close(f.actionBlock)
	waitPost(t, posted)()
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "d1 answered · s1 reads it now")
	mustNotText(t, tt, "Sending…")
}

func TestBackFromAttemptRestoresTheTask(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "a1")
	mustText(t, tt, "Brief")
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("Back left %v %q, want the task t1", app.Screen, app.Ref)
	}
	mustText(t, tt, "Plan · p1")
	mustNotText(t, tt, "The board could not be read")
	click(t, tt, "Back")
	mustText(t, tt, "Write docs")
}

func TestBackFromTheDecisionTaskReturnsToTheDecision(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Open d1")
	click(t, tt, "Open task")
	mustText(t, tt, "Plan · p1")
	click(t, tt, "Back")
	if app.Screen != desktop.DecisionScreen || app.Ref != "d1" {
		t.Fatalf("Back left %v %q, want the decision d1", app.Screen, app.Ref)
	}
	mustText(t, tt, "Options", "d1")
	click(t, tt, "Back")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("Back left %v, want the Board screen", app.Screen)
	}
	mustText(t, tt, "Failed")
	mustNotText(t, tt, "The board could not be read")
}

func TestBackFromATaskReportReturnsToTheTask(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "Cookie fixed")
	mustText(t, tt, "All tests pass.")
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("Back left %v %q, want the task t1", app.Screen, app.Ref)
	}
	mustText(t, tt, "Plan · p1")
	mustNotText(t, tt, "The board could not be read")
}

func TestBackFromATaskDecisionReturnsToTheTask(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "Keep the old cookie name?")
	mustText(t, tt, "Options")
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("Back left %v %q, want the task t1", app.Screen, app.Ref)
	}
	mustText(t, tt, "Plan · p1")
	mustNotText(t, tt, "The board could not be read")
}

func TestBackFromAnAttemptReportReturnsToTheAttempt(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "a1")
	click(t, tt, "Cookie fixed")
	mustText(t, tt, "All tests pass.")
	click(t, tt, "Back")
	if app.Screen != desktop.AttemptScreen || app.Ref != "a1" {
		t.Fatalf("Back left %v %q, want the attempt a1", app.Screen, app.Ref)
	}
	mustText(t, tt, "Brief", "20m")
	mustNotText(t, tt, "The board could not be read")
}

func TestBackFromAReportsTaskReturnsToTheReport(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Cookie fixed")
	click(t, tt, "Open r1")
	click(t, tt, "Open task")
	mustText(t, tt, "Plan · p1")
	click(t, tt, "Back")
	if app.Screen != desktop.ReportScreen || app.Ref != "r1" {
		t.Fatalf("Back left %v %q, want the report r1", app.Screen, app.Ref)
	}
	mustText(t, tt, "All tests pass.")
	mustNotText(t, tt, "The board could not be read")
}

func TestRefreshReadsTheScreenAgain(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	st := f.status[fleetID]
	st.Needs.Items = append(st.Needs.Items, desktop.Need{Kind: "decision", Ref: "d9", Decision: "d9", Title: "A brand new need", Word: "DECISION", Tone: "wait"})
	f.status[fleetID] = st
	menu(t, tt, "Refresh now")
	mustText(t, tt, "A brand new need")
}

func waitPost(t *testing.T, posted <-chan func()) func() {
	t.Helper()
	select {
	case fn := <-posted:
		return fn
	case <-time.After(5 * time.Second):
		t.Fatal("the window never posted the load")
		return nil
	}
}

func TestAHungBoardDoesNotFreezeTheWindow(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	f.block, f.entered = make(chan struct{}), make(chan struct{}, 1)
	menu(t, tt, "Refresh now")
	waitPost(t, posted)()
	mustText(t, tt, "Reading the board")
	waitPost(t, posted)()
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never read the board")
	}
	close(f.block)
	waitPost(t, posted)()
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "Needs", "s1 working")
	mustNotText(t, tt, "Reading the board")
}

func TestAFailedFetchShowsTheErrorOffTheRenderPath(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	menu(t, tt, "Refresh now")
	waitPost(t, posted)()
	mustText(t, tt, "Reading the board")
	waitPost(t, posted)()
	waitPost(t, posted)()
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "the board is restarting")
	mustNotText(t, tt, "Reading the board")
}

func TestFleetSwitchUpdatesTheTitle(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	var titles []string
	app.OnFleet = func(f desktop.Fleet) { titles = append(titles, f.ID) }
	tt := newTester(app)
	click(t, tt, "second")
	if len(titles) == 0 || titles[len(titles)-1] != secondID {
		t.Fatalf("titles = %v, want the second fleet", titles)
	}
}

func TestEmptyStates(t *testing.T) {
	app := newApp(t, emptyFixture())
	tt := newTester(app)
	click(t, tt, "Board")
	mustText(t, tt, "All clear")
	mustText(t, tt, "No messages")
	mustText(t, tt, "No tasks")
}

func TestErrorState(t *testing.T) {
	app := newApp(t, errorFixture())
	tt := newTester(app)
	mustText(t, tt, "The board could not be read",
		"the board did not accept this fleet's token; open the fleet once with `hand open`")
	mustNotText(t, tt, "Back")
	click(t, tt, "Retry")
	mustText(t, tt, "The board could not be read")
}

func TestScenes(t *testing.T) {
	type scene struct {
		name string
		app  *desktop.App
		play func(*testing.T, *ui.Tester)
	}
	errFleet := goldenFixture(t)
	errApp := newApp(t, errFleet)
	sceneNow := func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
	errApp.Now = sceneNow
	composer := newApp(t, goldenFixture(t))
	composer.Now = sceneNow
	blocked := blockedFixture(t)
	delivered := goldenFixture(t)
	td := delivered.task[fleetID]
	td.Delivered = []desktop.Deliverable{
		{Ref: "https://github.com/example/hand/pull/42", Kind: "PR"},
		{Ref: "https://docs.example.com/q3-review", Kind: "link"},
		{Ref: "design.md", Kind: "file", Href: "/" + fleetID + "/doc/reports/t1/design.md"},
		{Ref: "export.csv", Kind: "file"},
	}
	delivered.task[fleetID] = td
	opened := newApp(t, goldenFixture(t))
	if err := opened.OpenLink("handboard://open/" + secondID + "/decision/d1"); err != nil {
		t.Fatal(err)
	}
	scenes := []scene{
		{"01-fleet-switch", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "second")
		}},
		{"02-panes", newApp(t, goldenFixture(t)), nil},
		{"06-detail", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Fix login")
		}},
		{"07-empty", newApp(t, emptyFixture()), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Board")
		}},
		{"08-error", newApp(t, errorFixture()), nil},
		{"09-decision-answer", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Board")
			click(t, tt, "Keep the old cookie name?")
			click(t, tt, "Yes, keep it")
		}},
		{"10-report-ack", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Board")
			click(t, tt, "Cookie fixed")
		}},
		{"11-composer", composer, func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Message the supervisor")
			tt.Type("Please look at the cookie rename after the docs pass.")
		}},
		{"12-composer-error", errApp, func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Message the supervisor")
			tt.Type("This text stays when the send fails.")
			errFleet.action = &desktop.APIError{Status: 500, Code: "internal", Message: "the supervisor is gone"}
			click(t, tt, "Send")
		}},
		{"13-supervisor-controls", newApp(t, blocked), nil},
		{"16-blocked-ask", newApp(t, askFixture(t)), nil},
		{"14-open-error", homedApp(t, "/very/long/path/to/a-fleet"), func(t *testing.T, tt *ui.Tester) {
			t.Setenv("PATH", t.TempDir())
			menu(t, tt, "Open in browser")
		}},
		{"15-delivered", newApp(t, delivered), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Fix login")
		}},
		{"17-open-link", opened, nil},
		{"18-overflow-menu", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "More")
		}},
		{"19-stop-armed", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Stop")
		}},
	}
	for _, sc := range scenes {
		t.Run(sc.name, func(t *testing.T) {
			tt := newTester(sc.app)
			if sc.play != nil {
				sc.play(t, tt)
			}
			shot(t, tt, sc.name)
		})
	}
}

func TestFrameIsNotBlank(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	img := tt.Image()
	bg := img.RGBAAt(0, 0)
	varied := 0
	for y := 0; y < img.Bounds().Dy(); y += 4 {
		for x := 0; x < img.Bounds().Dx(); x += 4 {
			if img.RGBAAt(x, y) != bg {
				varied++
			}
		}
	}
	if varied < 100 {
		t.Fatalf("the frame is blank: %d sampled pixels differ from %v", varied, bg)
	}
}

func TestBootWithoutABoard(t *testing.T) {
	app := desktop.New(func() (desktop.ReadModel, error) { return nil, desktop.ErrNoBoard })
	app.Boot()
	tt := newTester(app)
	mustText(t, tt, "The board could not be read", "no board is running; start it with `hand open` in a fleet")
	click(t, tt, "Retry")
	mustText(t, tt, "The board could not be read")
}

func countText(t *testing.T, tt *ui.Tester, want string) int {
	t.Helper()
	n := 0
	for _, text := range tt.Texts() {
		n += strings.Count(text, want)
	}
	return n
}

func TestEachFleetGetsItsOwnNameLookup(t *testing.T) {
	f := goldenFixture(t)
	f.stall = fleetID
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.NameTimeout = 40 * time.Millisecond
	app.Boot()
	app.OpenFleet("second")
	if app.Fleet.ID != secondID {
		t.Fatalf("fleet = %q (%s), want the second fleet by name", app.Fleet.Name, app.Fleet.ID)
	}
}

func TestRetryDoesNotBlockTheWindow(t *testing.T) {
	first := true
	block := make(chan struct{})
	app := desktop.New(func() (desktop.ReadModel, error) {
		if first {
			first = false
			return nil, desktop.ErrNoBoard
		}
		select {
		case <-block:
		case <-time.After(3 * time.Second):
		}
		return nil, desktop.ErrNoBoard
	})
	app.Boot()
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	click(t, tt, "Retry")
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "Reading the board")
	close(block)
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "no board is running")
	mustNotText(t, tt, "Reading the board")
}

func TestSelectingAFleetClearsTheStatus(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	f.block, f.entered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "second")
	if app.Status.Tasks.Active != 0 || app.Status.Line != "" || app.Status.Supervisor != nil {
		t.Fatalf("status = %+v, want it cleared while the fleet loads", app.Status)
	}
	close(f.block)
	waitPost(t, posted)()
	waitPost(t, posted)()
	if app.Fleet.ID != secondID {
		t.Fatalf("fleet = %s, want %s", app.Fleet.ID, secondID)
	}
}

func TestRefreshDiscoversAgainAndKeepsTheScreen(t *testing.T) {
	first := goldenFixture(t)
	second := goldenFixture(t)
	second.fleets = append(second.fleets, desktop.Fleet{ID: "f00000000e0ac", Name: "third"})
	calls := 0
	app := desktop.New(func() (desktop.ReadModel, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return second, nil
	})
	app.Boot()
	tt := newTester(app)
	menu(t, tt, "Refresh now")
	if app.Model != desktop.ReadModel(second) {
		t.Fatal("Refresh did not discover the board again")
	}
	if app.Screen != desktop.BoardScreen || app.Fleet.ID != fleetID {
		t.Fatalf("Refresh left %v %s, want the Board screen of %s", app.Screen, app.Fleet.ID, fleetID)
	}
	mustText(t, tt, "third", "All good.")
}

func TestASupersededReloadStartsNoDetailRequest(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	before := f.count("tasks")
	f.block, f.entered = make(chan struct{}), make(chan struct{}, 2)
	click(t, tt, "Board")
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never read the board")
	}
	click(t, tt, "Board")
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the second read never started")
	}
	close(f.block)
	deadline := time.After(5 * time.Second)
	for f.count("tasks") == before {
		select {
		case fn := <-posted:
			fn()
		case <-deadline:
			t.Fatal("no reload read the task list")
		}
	}
	if got := f.count("tasks") - before; got != 1 {
		t.Fatalf("the two reloads read the task list %d times, want only the last", got)
	}
	tt.Frame()
	mustText(t, tt, "Conversation")
}

func TestBackStaysOnTheErrorCard(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Fix login")
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	menu(t, tt, "Refresh now")
	mustText(t, tt, "The board could not be read", "the board is restarting")
	click(t, tt, "Back")
	if app.Screen != desktop.BoardScreen || app.Ref != "" {
		t.Fatalf("Back left %v %q, want the tasks list", app.Screen, app.Ref)
	}
}

func TestOlderMessagesReadTheEarlierPage(t *testing.T) {
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	tl.Older = 42
	f.timeline[fleetID] = tl
	f.older = map[string]desktop.Timeline{fleetID: {
		Entries: []desktop.Entry{{Role: "operator", Text: "the earlier message", At: "2026-10-31T04:40:00Z"}},
	}}
	app := newApp(t, f)
	tt := newTester(app)
	mustText(t, tt, "Older messages")
	click(t, tt, "Older messages")
	mustText(t, tt, "the earlier message", "All good.")
	mustNotText(t, tt, "Older messages")
}

func TestTaskShowsDecisionHistory(t *testing.T) {
	f := goldenFixture(t)
	td := f.task[fleetID]
	f.extra = map[string]desktop.DecisionDetail{"d0": {Decision: td.Decisions[0], Task: td.Task, Body: td.Decisions[0].Question}}
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Fix login")
	mustText(t, tt, "Rename the helper?", "Yes")
	if n := countText(t, tt, "Keep the old cookie name?"); n != 1 {
		t.Fatalf("the open decision is shown %d times, want its one open row", n)
	}
	click(t, tt, "Rename the helper?")
	if app.Screen != desktop.DecisionScreen || app.Ref != "d0" {
		t.Fatalf("the history row opened %v %q, want d0", app.Screen, app.Ref)
	}
}

func TestAnOmittedTranscriptIsNoticed(t *testing.T) {
	f := goldenFixture(t)
	ad := f.attempt[fleetID]
	ad.Transcript = desktop.Transcript{Shown: true, More: true, Turns: []desktop.Turn{
		{Role: "supervisor", Text: "the last turn", At: "2026-11-01T04:41:00Z"},
	}}
	f.attempt[fleetID] = ad
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Fix login")
	click(t, tt, "a1")
	mustText(t, tt, "the last turn", "Only the latest 50 entries are shown.")
}

func TestDecisionOptionsAreShownOnce(t *testing.T) {
	f := goldenFixture(t)
	dd := f.decision[fleetID]
	dd.Decision.Question = "Keep the old cookie name?\n\nWe made this earlier.\n\n1. Yes, keep it\n2. No, rename it\n\n1. Then update the docs."
	f.decision[fleetID] = dd
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	mustText(t, tt, "We made this earlier.", "Then update the docs.")
	for _, want := range []string{"Yes, keep it", "No, rename it"} {
		if n := countText(t, tt, want); n != 1 {
			t.Fatalf("%q is shown %d times, want once", want, n)
		}
	}
}

func TestARefreshErrorSurvivesAStaleLoad(t *testing.T) {
	f := goldenFixture(t)
	first := true
	app := desktop.New(func() (desktop.ReadModel, error) {
		if first {
			first = false
			return f, nil
		}
		return nil, desktop.ErrNoBoard
	})
	app.Boot()
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	f.block, f.entered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "Board")
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never read the board")
	}
	menu(t, tt, "Refresh now")
	deadline := time.After(5 * time.Second)
	for app.Err != desktop.ErrNoBoard.Error() {
		select {
		case fn := <-posted:
			fn()
		case <-deadline:
			t.Fatalf("the refresh error never arrived; the window shows %q", tt.Texts())
		}
	}
	tt.Frame()
	mustText(t, tt, "no board is running")
	close(f.block)
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "no board is running")
	mustNotText(t, tt, "Nothing to show")
}

func TestRefreshCancelsTheEarlierDiscovery(t *testing.T) {
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.Boot()
	f.stall, f.stallOnce = fleetID, true
	f.nameIn = make(chan struct{}, 1)
	f.nameOut = make(chan error, 1)
	app.NameTimeout = time.Hour
	base := f.count("name:" + secondID)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	menu(t, tt, "Refresh now")
	select {
	case <-f.nameIn:
	case <-time.After(5 * time.Second):
		t.Fatal("the first discovery never started")
	}
	menu(t, tt, "Refresh now")
	select {
	case err := <-f.nameOut:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the earlier discovery ended with %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the earlier discovery was never canceled")
	}
	for i := 0; i < 6; i++ {
		waitPost(t, posted)()
	}
	if got := f.count("name:" + secondID); got != base+1 {
		t.Fatalf("the canceled discovery looked up %d later fleets, want 1", got-base)
	}
}

func TestRefreshUpdatesTheWindowTitle(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	var names []string
	app.OnFleet = func(fl desktop.Fleet) { names = append(names, fl.Name) }
	f.fleets[0].Name = "renamed"
	tt := newTester(app)
	menu(t, tt, "Refresh now")
	if len(names) == 0 || names[len(names)-1] != "renamed" {
		t.Fatalf("titles = %v, want the renamed fleet", names)
	}
	mustText(t, tt, "renamed")
}

func TestACSRFRefusalShowsTheBoardMessage(t *testing.T) {
	f := goldenFixture(t)
	f.action = &desktop.APIError{Status: 403, Code: "csrf_required",
		Message: "the X-Hand-CSRF header is missing or wrong; read the session's token first"}
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	mustText(t, tt, "the X-Hand-CSRF header is missing or wrong")
	mustNotText(t, tt, "the board's API answers only on its loopback address")
}

func TestAStaleStatusDoesNotHideTheAnswer(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	click(t, tt, "Send answer")
	mustText(t, tt, "Answer", "Yes, keep it", "the answer was saved, but the fleet status could not be read", "the board is restarting")
	mustNotText(t, tt, "Send answer")
	if app.Busy {
		t.Fatal("the window is still busy after the answer was saved")
	}
	mustText(t, tt, "Refresh")
}

func TestAnOtherOptionAsksForItsNote(t *testing.T) {
	f := goldenFixture(t)
	d := f.decision[fleetID]
	d.Options = append(d.Options, desktop.Option{Key: "3", Label: "Other", Value: "3. Other", Other: true})
	f.decision[fleetID] = d
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Other")
	mustText(t, tt, "That option needs a note.")
	click(t, tt, "Note")
	tt.Type("my own answer")
	mustNotText(t, tt, "That option needs a note.")
}

func TestBackClearsTheAnswerDraft(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Note")
	tt.Type("first draft")
	if app.AnswerNote != "first draft" {
		t.Fatalf("draft = %q", app.AnswerNote)
	}
	click(t, tt, "Open d1")
	if app.AnswerNote != "" {
		t.Fatalf("opening the decision kept the draft %q", app.AnswerNote)
	}
	click(t, tt, "Open task")
	click(t, tt, "Keep the old cookie name?")
	if app.AnswerNote != "" {
		t.Fatalf("opening a decision kept the draft %q", app.AnswerNote)
	}
	click(t, tt, "Note")
	tt.Type("second draft")
	click(t, tt, "Back")
	click(t, tt, "Back")
	if app.AnswerNote != "" {
		t.Fatalf("Back kept the second decision's draft %q", app.AnswerNote)
	}
}

func TestAnAckLeavesTheOtherFleetsNeedsAlone(t *testing.T) {
	f := goldenFixture(t)
	before := len(f.status[secondID].Needs.Items)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Cookie fixed")
	click(t, tt, "Mark read")
	if got := len(f.status[secondID].Needs.Items); got != before {
		t.Fatalf("the second fleet lost a need: %d, want %d", got, before)
	}
}

func TestComposerEnterBreaksTheLineAndShowsNoHint(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Message the supervisor")
	tt.Type("one")
	tt.Key(0, ui.KeyEnter)
	tt.Type("two")
	tt.Frame()
	if got := f.count("send:" + fleetID); got != 0 {
		t.Fatalf("Enter sent the message %d times", got)
	}
	if app.Draft != "one\ntwo" {
		t.Fatalf("draft = %q, want a newline", app.Draft)
	}
	mustNotText(t, tt, "Enter sends · Shift+Enter starts a new line.")
}

func TestTheConversationReadsOldestFirstWithTheComposerBelow(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	texts := tt.Texts()
	at := func(s string) int {
		for i, x := range texts {
			if x == s {
				return i
			}
		}
		t.Fatalf("%q not on screen in %q", s, texts)
		return -1
	}
	e := app.Timeline.Entries
	if len(e) < 2 {
		t.Fatalf("fixture has %d entries", len(e))
	}
	if at(e[len(e)-1].Text) > at(e[0].Text) {
		t.Fatalf("the oldest message is below the newest in %q", texts)
	}
	if at("Message the supervisor") < at(e[0].Text) {
		t.Fatalf("the composer is above the messages in %q", texts)
	}
}

func TestTheWindowOpensOnTheTwoPanesAndAFleetSwitchKeepsThem(t *testing.T) {
	app := newLandingApp(t, goldenFixture(t))
	tt := newTester(app)
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("screen = %v, want the Board screen", app.Screen)
	}
	mustText(t, tt, "Keep the old cookie name?")
	click(t, tt, "second")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("a fleet switch landed on %v, want the Board screen", app.Screen)
	}
}

func TestAQuietFleetOpensOnTheTwoPanesToo(t *testing.T) {
	f := goldenFixture(t)
	for id, st := range f.status {
		st.Needs = desktop.Needs{}
		f.status[id] = st
	}
	app := newLandingApp(t, f)
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("screen = %v, want the Board screen", app.Screen)
	}
	mustText(t, newTester(app), "All clear")
}

func TestAnAnswerMovesToTheNextNeed(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	mustText(t, tt, "View a2")
	mustNotText(t, tt, "Send answer")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("an answer left the Board screen for %v", app.Screen)
	}
}

func TestTheLastReadReturnsToAnEmptyNeeds(t *testing.T) {
	f := goldenFixture(t)
	for id, st := range f.status {
		var keep []desktop.Need
		for _, n := range st.Needs.Items {
			if n.Report == "r1" {
				keep = append(keep, n)
			}
		}
		st.Needs.Items, st.Needs.Waiting = keep, len(keep)
		f.status[id] = st
	}
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Mark read")
	mustText(t, tt, "All clear")
}

func TestAnAnswerBeforeABlockedSupervisorRowReturnsToNeeds(t *testing.T) {
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.Needs.Items = append([]desktop.Need{{Kind: "blocked", Ref: "s1", Word: "BLOCKED", Tone: "fail"}}, st.Needs.Items...)
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	if app.Screen != desktop.BoardScreen || app.Ref != "" {
		t.Fatalf("moved to %v %q, want the Board screen", app.Screen, app.Ref)
	}
}

func TestAFailedAnswerOrReadDoesNotMove(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	f.action = errors.New("boom")
	click(t, tt, "Send answer")
	mustText(t, tt, "Send answer")
	click(t, tt, "Dismiss")
	click(t, tt, "Cookie fixed")
	click(t, tt, "Mark read")
	mustText(t, tt, "Mark read")
	mustNotText(t, tt, "View a2")
}

func TestAStatusStillListingTheAnsweredItemIsSkipped(t *testing.T) {
	f := goldenFixture(t)
	f.keepNeeds = true
	st := f.status[fleetID]
	for i, n := range st.Needs.Items {
		if n.Decision == "d1" {
			st.Needs.Items[0], st.Needs.Items[i] = st.Needs.Items[i], st.Needs.Items[0]
		}
	}
	f.status[fleetID] = st
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	mustText(t, tt, "View a2")
	mustNotText(t, tt, "Send answer")
}

func TestTheWorkingBannerSitsBelowTheLastMessage(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	texts := tt.Texts()
	idx := func(s string) int {
		for i, x := range texts {
			if x == s {
				return i
			}
		}
		t.Fatalf("%q not on screen in %q", s, texts)
		return -1
	}
	e := app.Timeline.Entries
	if idx("s1 working") < idx(e[0].Text) || idx("s1 working") > idx("Message the supervisor") {
		t.Fatalf("banner is not between the last message and the composer in %q", texts)
	}
}

func TestTheRailCountsTheWaitingNeeds(t *testing.T) {
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.Needs.Waiting = 13
	f.status[fleetID] = st
	tt := newTester(newApp(t, f))
	mustText(t, tt, "13")
}

func TestTheConversationDotFollowsTheAgent(t *testing.T) {
	if _, ok := newTester(newApp(t, goldenFixture(t))).Find("Working"); !ok {
		t.Error("no working dot while the agent works")
	}
	if _, ok := newTester(newApp(t, emptyFixture())).Find("Working"); ok {
		t.Error("working dot while the agent is idle")
	}
}

func TestTheLabelButtonOpensAndClosesTheRail(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Show labels")
	if !app.RailOpen {
		t.Fatal("rail did not open")
	}
	if _, ok := tt.Find("Hide labels"); !ok {
		t.Fatalf("no Hide labels in %q", tt.Texts())
	}
	click(t, tt, "Hide labels")
	if app.RailOpen {
		t.Error("rail did not close")
	}
}

func TestTheMastheadContextFigureIsMutedMono(t *testing.T) {
	tt := newTester(newApp(t, goldenFixture(t)))
	r, ok := tt.Find("610K / 1M")
	if !ok {
		t.Fatalf("no figure in %q", tt.Texts())
	}
	if r.W < 60 {
		t.Errorf("figure is %v wide, a proportional face", r.W)
	}
	img := tt.Image()
	darkest := uint32(0xffff)
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			if red, _, _, _ := img.At(x, y).RGBA(); red < darkest {
				darkest = red
			}
		}
	}
	if got := darkest >> 8; got < 0x50 {
		t.Errorf("figure ink is %#x, darker than the muted tone", got)
	}
}

func railBadgeFill(t *testing.T, worst string) [3]uint8 {
	t.Helper()
	f := goldenFixture(t)
	st := f.status[fleetID]
	st.Needs.Waiting, st.Needs.Worst = 13, worst
	f.status[fleetID] = st
	tt := newTester(newApp(t, f))
	r, ok := tt.Find("13")
	if !ok {
		t.Fatalf("no badge in %q", tt.Texts())
	}
	red, green, blue, _ := tt.Image().At(int(r.X)-2, int(r.Y+r.H/2)).RGBA()
	return [3]uint8{uint8(red >> 8), uint8(green >> 8), uint8(blue >> 8)}
}

func TestTheRailBadgeIsTonedByTheWorstNeed(t *testing.T) {
	k := kit.ColorsFor(false)
	for worst, want := range map[string]ui.Color{"failure": k.FailBG, "decision": k.WaitBG, "": k.NeutralBG} {
		if got := railBadgeFill(t, worst); got != [3]uint8{want.R, want.G, want.B} {
			t.Errorf("worst=%q: badge fill %v, want %v", worst, got, want)
		}
	}
}
