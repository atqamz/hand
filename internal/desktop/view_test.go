package desktop_test

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
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
	img := tt.Image()
	if img == nil {
		t.Fatal("no frame")
	}
	if img.Bounds().Dx() != windowWidth || img.Bounds().Dy() != windowHeight {
		t.Fatalf("frame is %v, want %dx%d", img.Bounds(), windowWidth, windowHeight)
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

func TestFleetSwitch(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	mustText(t, tt, "golden", "second")
	click(t, tt, "second")
	mustText(t, tt, "the second fleet's status", "second")
	if app.Fleet.ID != secondID {
		t.Fatalf("fleet = %s, want %s", app.Fleet.ID, secondID)
	}
}

func TestStatusScreen(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	mustText(t, tt, "Status", "Supervisor", "s1 claude sonnet low · running",
		"Active", "Inbox", "Done", "Abandoned", "Projects", "agent working")
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

func TestSupervisorControlsFollowTheState(t *testing.T) {
	f := statusWith(t, func(sup *desktop.Supervisor) {
		sup.Status, sup.Live, sup.Resumable, sup.Blocked, sup.Keys = "running", true, false, false, nil
	})
	app := newApp(t, f)
	tt := newTester(app)
	mustText(t, tt, "Controls", "Start", "Resume", "Stop", "Interrupt", "Force")
	mustNotText(t, tt, "Keys")
	click(t, tt, "Start")
	click(t, tt, "Resume")
	click(t, tt, "Force")
	if f.count("control:start")+f.count("control:resume")+f.count("control:force") != 0 {
		t.Fatalf("a disabled control ran: %v", []int{f.count("control:start"), f.count("control:resume"), f.count("control:force")})
	}
	click(t, tt, "Stop")
	if f.count("control:stop") != 1 {
		t.Fatalf("Stop calls = %d", f.count("control:stop"))
	}
}

func TestResumeIsEnabledOnlyWithASession(t *testing.T) {
	f := statusWith(t, func(sup *desktop.Supervisor) {
		sup.Status, sup.Live, sup.Resumable, sup.Blocked, sup.Keys = "interrupted", false, true, false, nil
	})
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Resume")
	if f.count("control:resume") != 1 {
		t.Fatalf("Resume calls = %d", f.count("control:resume"))
	}
	click(t, tt, "Stop")
	click(t, tt, "Interrupt")
	click(t, tt, "Force")
	if f.count("control:stop")+f.count("control:interrupt")+f.count("control:force") != 0 {
		t.Fatal("a control that needs a running supervisor ran")
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

func TestASupervisorControlIsBusyAndKeepsTheScreenOnError(t *testing.T) {
	f := statusWith(t, func(sup *desktop.Supervisor) {
		sup.Status, sup.Live, sup.Resumable, sup.Blocked, sup.Keys = "running", true, false, false, nil
	})
	app := newApp(t, f)
	tt := newTester(app)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "Stop")
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
	click(t, tt, "Stop")
	tt.Frame()
	mustText(t, tt, "The action did not go through", "supervisor s1 is not running", "s1 claude sonnet low · running")
	if app.Screen != desktop.StatusScreen {
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
	click(t, tt, "Stop")
	tt.Frame()
	mustText(t, tt, "stopped by the test")
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
	click(t, tt, "Stop")
	tt.Frame()
	mustText(t, tt, "The action went through", "the fleet status could not be read", "s1 claude sonnet low · running")
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

func TestNeedsScreen(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Needs")
	mustText(t, tt, "Needs", "FAILED", "DECISION", "REPORT",
		"Keep the old cookie name?",
		"Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump",
		"Cookie fixed")
}

func TestNeedsOpensADecision(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Needs")
	click(t, tt, "Keep the old cookie name?")
	mustText(t, tt, "d1", "Options", "Yes, keep it", "No, rename it", "Open task")
}

func TestTimelineScreen(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Timeline")
	mustText(t, tt, "Timeline", "s1 is working", "All good.", "status?", "you", "hand")
}

func TestComposerSendsWithEnter(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Timeline")
	click(t, tt, "Message")
	tt.Type("hello there")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if f.count("send:"+fleetID) != 1 || f.sentText != "hello there" {
		t.Fatalf("send calls = %d, sent %q", f.count("send:"+fleetID), f.sentText)
	}
	mustText(t, tt, "hello there")
	if app.Draft != "" {
		t.Fatalf("draft = %q after a send", app.Draft)
	}
	click(t, tt, "Message")
	tt.Type("again")
	tt.Key(0, ui.KeyEnter)
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
	click(t, tt, "Timeline")
	click(t, tt, "Message")
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
	click(t, tt, "Timeline")
	click(t, tt, "Message")
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
	click(t, tt, "Timeline")
	click(t, tt, "Message")
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
	click(t, tt, "Timeline")
	click(t, tt, "Message")
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
	click(t, tt, "Timeline")
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
	click(t, tt, "Timeline")
	click(t, tt, "Message")
	tt.Type("send me")
	tt.Frame()
	click(t, tt, "Send")
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "" {
		t.Fatalf("the stored draft = %q after a send", got)
	}
}

func TestTheDraftCanBeCleared(t *testing.T) {
	dir := t.TempDir()
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Timeline")
	click(t, tt, "Message")
	tt.Type("throw me away")
	tt.Frame()
	click(t, tt, "Clear")
	if app.Draft != "" || (desktop.Drafts{Dir: dir}).Load(fleetID) != "" {
		t.Fatalf("Clear left draft %q", app.Draft)
	}
}

func TestTheComposerSendsTheDraftTextAsWritten(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Timeline")
	click(t, tt, "Message")
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

func TestAWhitespaceDraftCanBeCleared(t *testing.T) {
	dir := t.TempDir()
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Timeline")
	click(t, tt, "Message")
	tt.Type("   ")
	tt.Frame()
	click(t, tt, "Clear")
	if app.Draft != "" {
		t.Fatalf("draft = %q after Clear", app.Draft)
	}
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "" {
		t.Fatalf("the stored draft = %q after Clear", got)
	}
}

func TestASendWhoseTimelineCannotBeReadClearsTheDraft(t *testing.T) {
	f := goldenFixture(t)
	f.stale = true
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Timeline")
	click(t, tt, "Message")
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
	click(t, tt, "Timeline")
	click(t, tt, "Message")
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
	click(t, tt, "Timeline")
	mustText(t, tt, "the draft was not saved")
}

func TestADraftThatCannotBeRemovedIsReported(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, fleetID+".txt", "kept"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Timeline")
	click(t, tt, "Message")
	tt.Type("clear me")
	tt.Frame()
	click(t, tt, "Clear")
	mustText(t, tt, "the stored draft could not be removed")
	if app.ActionErr != "" {
		t.Fatalf("a failed cleanup became a failed action: %q", app.ActionErr)
	}
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
	click(t, tt, "Timeline")
	click(t, tt, "Message")
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
	if err := os.RemoveAll(filepath.Join(dir, fleetID+".txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fleetID+".txt"), []byte("a newer draft"), 0o600); err != nil {
		t.Fatal(err)
	}
	click(t, tt, "Clear")
	if app.DraftErr != "" {
		t.Fatalf("the retry left %q", app.DraftErr)
	}
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "a newer draft" {
		t.Fatalf("the retry removed a newer draft: %q", got)
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
	click(t, tt, "Timeline")
	click(t, tt, "Message")
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
	app.Show(desktop.NeedsScreen)
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
	click(t, tt, "Timeline")
	pump(t, posted, func() bool { return !app.Loading })
	tt.Frame()
	click(t, tt, "Message")
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
	click(t, tt, "Timeline")
	click(t, tt, "Message")
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

func TestClearWinsOverAQueuedSave(t *testing.T) {
	dir := t.TempDir()
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Boot()
	posted := make(chan func(), 64)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	click(t, tt, "Timeline")
	pump(t, posted, func() bool { return !app.Loading })
	tt.Frame()
	click(t, tt, "Message")
	tt.Type("gone")
	click(t, tt, "Clear")
	if got := (desktop.Drafts{Dir: dir}).Load(fleetID); got != "" {
		t.Fatalf("the stored draft = %q after Clear", got)
	}
	if app.Draft != "" || app.DraftErr != "" {
		t.Fatalf("draft = %q, error = %q", app.Draft, app.DraftErr)
	}
}

func TestASendKeepsANewerDraft(t *testing.T) {
	dir := t.TempDir()
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = dir
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Timeline")
	click(t, tt, "Message")
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

func TestTasksScreenAndDetail(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Tasks")
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
	click(t, tt, "Tasks")
	click(t, tt, "Fix login")
	click(t, tt, "a1")
	mustText(t, tt, "Brief", "x", "20m", "Cookie fixed", "Reports")
}

func TestReportScreen(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Needs")
	click(t, tt, "Cookie fixed")
	mustText(t, tt, "r1", "Cookie fixed", "All tests pass.", "Open task", "Ack")
}

func TestAnswerDecision(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Needs")
	click(t, tt, "Keep the old cookie name?")
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
	click(t, tt, "Needs")
	click(t, tt, "Keep the old cookie name?")
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
	click(t, tt, "Needs")
	click(t, tt, "Keep the old cookie name?")
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "decision d1 is answered"}
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	mustText(t, tt, "The action did not go through", "decision d1 is answered",
		"Keep the old cookie name?", "Send answer")
	if app.Screen != desktop.DecisionScreen || app.Decision.Decision.Ref != "d1" {
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
	click(t, tt, "Needs")
	click(t, tt, "Cookie fixed")
	click(t, tt, "Ack")
	if f.count("ack:r1") != 1 {
		t.Fatalf("ack calls = %d, want 1", f.count("ack:r1"))
	}
	mustText(t, tt, "Acknowledged by operator (desktop)", "Acknowledged")
	if app.Report.Report.AckedAt == "" {
		t.Fatal("the report is not acknowledged in the window")
	}
}

func TestAFailedAckKeepsTheScreen(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Needs")
	click(t, tt, "Cookie fixed")
	f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "report r1 is already acknowledged"}
	click(t, tt, "Ack")
	mustText(t, tt, "The action did not go through", "report r1 is already acknowledged",
		"All tests pass.", "Ack")
	if app.Screen != desktop.ReportScreen {
		t.Fatalf("the error left screen %v", app.Screen)
	}
}

func TestTheAnswerButtonIsDisabledWhileSending(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Needs")
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
	tt.Frame()
	mustText(t, tt, "answered", "Answer")
	mustNotText(t, tt, "Sending…")
}

func TestBackFromAttemptRestoresTheTask(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Tasks")
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
	click(t, tt, "Needs")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Open task")
	mustText(t, tt, "Plan · p1")
	click(t, tt, "Back")
	if app.Screen != desktop.DecisionScreen || app.Ref != "d1" {
		t.Fatalf("Back left %v %q, want the decision d1", app.Screen, app.Ref)
	}
	mustText(t, tt, "Options", "d1")
	click(t, tt, "Back")
	if app.Screen != desktop.NeedsScreen {
		t.Fatalf("Back left %v, want Needs", app.Screen)
	}
	mustText(t, tt, "FAILED")
	mustNotText(t, tt, "The board could not be read")
}

func TestBackFromATaskReportReturnsToTheTask(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Tasks")
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
	click(t, tt, "Tasks")
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
	click(t, tt, "Tasks")
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
	click(t, tt, "Needs")
	click(t, tt, "Cookie fixed")
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
	st.Line = "the fleet changed"
	f.status[fleetID] = st
	click(t, tt, "Refresh")
	mustText(t, tt, "the fleet changed")
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
	click(t, tt, "Refresh")
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
	mustText(t, tt, "Status", "agent working")
	mustNotText(t, tt, "Reading the board")
}

func TestAFailedFetchShowsTheErrorOffTheRenderPath(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	click(t, tt, "Refresh")
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
	click(t, tt, "Needs")
	mustText(t, tt, "All clear", "Nothing waits on you.")
	click(t, tt, "Timeline")
	mustText(t, tt, "No messages")
	click(t, tt, "Tasks")
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
	blocked := blockedFixture(t)
	scenes := []scene{
		{"01-fleet-switch", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "second")
		}},
		{"02-status", newApp(t, goldenFixture(t)), nil},
		{"03-needs", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Needs")
		}},
		{"04-timeline", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Timeline")
		}},
		{"05-tasks", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Tasks")
		}},
		{"06-detail", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Tasks")
			click(t, tt, "Fix login")
		}},
		{"07-empty", newApp(t, emptyFixture()), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Needs")
		}},
		{"08-error", newApp(t, errorFixture()), nil},
		{"09-decision-answer", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Needs")
			click(t, tt, "Keep the old cookie name?")
			click(t, tt, "Yes, keep it")
		}},
		{"10-report-ack", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Needs")
			click(t, tt, "Cookie fixed")
		}},
		{"11-composer", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Timeline")
			click(t, tt, "Message")
			tt.Type("Please look at the cookie rename after the docs pass.")
		}},
		{"12-composer-error", errApp, func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Timeline")
			click(t, tt, "Message")
			tt.Type("This text stays when the send fails.")
			errFleet.action = &desktop.APIError{Status: 500, Code: "internal", Message: "the supervisor is gone"}
			click(t, tt, "Send")
		}},
		{"13-supervisor-controls", newApp(t, blocked), nil},
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
	click(t, tt, "Timeline")
	click(t, tt, "Refresh")
	if app.Model != desktop.ReadModel(second) {
		t.Fatal("Refresh did not discover the board again")
	}
	if app.Screen != desktop.TimelineScreen || app.Fleet.ID != fleetID {
		t.Fatalf("Refresh left %v %s, want the timeline of %s", app.Screen, app.Fleet.ID, fleetID)
	}
	mustText(t, tt, "third", "All good.")
}

func TestASupersededReloadStartsNoDetailRequest(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	f.block, f.entered = make(chan struct{}), make(chan struct{}, 2)
	click(t, tt, "Tasks")
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never read the board")
	}
	click(t, tt, "Timeline")
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the second read never started")
	}
	close(f.block)
	for i := 0; i < 4; i++ {
		waitPost(t, posted)()
	}
	if got := f.count("tasks"); got != 0 {
		t.Fatalf("the superseded reload read the task list %d times", got)
	}
	tt.Frame()
	mustText(t, tt, "Timeline")
}

func TestBackStaysOnTheErrorCard(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Tasks")
	click(t, tt, "Fix login")
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	click(t, tt, "Refresh")
	mustText(t, tt, "The board could not be read", "the board is restarting")
	click(t, tt, "Back")
	if app.Screen != desktop.TasksScreen || app.Ref != "" {
		t.Fatalf("Back left %v %q, want the tasks list", app.Screen, app.Ref)
	}
}

func TestTimelineOlderReadsTheEarlierPage(t *testing.T) {
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	tl.Older = 42
	f.timeline[fleetID] = tl
	f.older = map[string]desktop.Timeline{fleetID: {
		Entries: []desktop.Entry{{Role: "operator", Text: "the earlier message", At: "2026-10-31T04:40:00Z"}},
	}}
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Timeline")
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
	click(t, tt, "Tasks")
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
	click(t, tt, "Tasks")
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
	click(t, tt, "Needs")
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
	click(t, tt, "Timeline")
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never read the board")
	}
	click(t, tt, "Refresh")
	waitPost(t, posted)()
	waitPost(t, posted)()
	waitPost(t, posted)()
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
	f.stall = fleetID
	f.nameIn = make(chan struct{}, 2)
	app.NameTimeout = 40 * time.Millisecond
	base := f.count("name:" + secondID)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	click(t, tt, "Refresh")
	select {
	case <-f.nameIn:
	case <-time.After(5 * time.Second):
		t.Fatal("the first discovery never started")
	}
	click(t, tt, "Refresh")
	select {
	case <-f.nameIn:
	case <-time.After(5 * time.Second):
		t.Fatal("the second discovery never started")
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
	click(t, tt, "Refresh")
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
	click(t, tt, "Needs")
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
	click(t, tt, "Needs")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	click(t, tt, "Send answer")
	mustText(t, tt, "answered", "Answer", "1.", "Yes, keep it",
		"the answer was saved, but the fleet status could not be read", "the board is restarting")
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
	click(t, tt, "Needs")
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
	click(t, tt, "Needs")
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Note")
	tt.Type("first draft")
	if app.AnswerNote != "first draft" {
		t.Fatalf("draft = %q", app.AnswerNote)
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
	click(t, tt, "Needs")
	click(t, tt, "Cookie fixed")
	click(t, tt, "Ack")
	if got := len(f.status[secondID].Needs.Items); got != before {
		t.Fatalf("the second fleet lost a need: %d, want %d", got, before)
	}
}
