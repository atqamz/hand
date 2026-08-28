package steering

import (
	"errors"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/store"
)

type testPane struct {
	pane      herdr.Pane
	textErr   error
	keysErr   error
	textCalls []string
	keyCalls  [][]string
	// Answers successive PaneReadUnwrapped calls in order, repeating the last entry once exhausted - the
	// same "replay frames, hold the last one" idiom internal/runtime/launch_test.go uses for confirmLaunch.
	// Unset answers "" every time, which contains no send's message and so confirms on the first read.
	readResponses []string
	readErr       error
	// Makes readErr bite only from this call number onward (1-indexed); zero means every call fails,
	// matching the original behavior for a test that never sets it.
	readErrFrom int
	readCalls   int
}

func (p *testPane) PaneGet(string) (herdr.Pane, error) { return p.pane, nil }

func (p *testPane) PaneSendText(_ string, text string) error {
	p.textCalls = append(p.textCalls, text)
	return p.textErr
}

func (p *testPane) PaneSendKeys(_ string, keys ...string) error {
	p.keyCalls = append(p.keyCalls, keys)
	return p.keysErr
}

func (p *testPane) PaneReadUnwrapped(string, int) (string, error) {
	p.readCalls++
	if p.readErr != nil && (p.readErrFrom == 0 || p.readCalls >= p.readErrFrom) {
		return "", p.readErr
	}
	if len(p.readResponses) == 0 {
		return "", nil
	}
	index := p.readCalls - 1
	if index >= len(p.readResponses) {
		index = len(p.readResponses) - 1
	}
	return p.readResponses[index], nil
}

func newSteeringHome(t *testing.T) (string, state.Attempt) {
	t.Helper()
	home := t.TempDir()
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.CreateTask(state.Task{ID: "task-1", Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.CreateAttempt(state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning,
		Herdr: state.Herdr{Session: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1"}})
	if err != nil {
		t.Fatal(err)
	}
	return home, attempt
}

func TestExecuteSubmitsAndFinalizesExactAttempt(t *testing.T) {
	home, attempt := newSteeringHome(t)
	pane := &testPane{pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle}}
	result, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane,
		Now: func() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	if result.Send.State != state.SendSubmitted || result.Send.AttemptID != attempt.ID || result.Send.Message != "hello" {
		t.Fatalf("result = %+v, want submitted exact attempt", result.Send)
	}
	if len(pane.textCalls) != 1 || pane.textCalls[0] != "hello" || len(pane.keyCalls) != 1 || len(pane.keyCalls[0]) != 1 || pane.keyCalls[0][0] != "Enter" {
		t.Fatalf("external calls = text %v keys %v", pane.textCalls, pane.keyCalls)
	}
}

func TestExecuteClassifiesAmbiguousTextFailureWithoutRetry(t *testing.T) {
	home, attempt := newSteeringHome(t)
	pane := &testPane{pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle}, textErr: errors.New("transport lost")}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendUncertain || len(pane.textCalls) != 1 || len(pane.keyCalls) != 0 {
		t.Fatalf("err=%v calls=%v/%v, want uncertain after one text", err, pane.textCalls, pane.keyCalls)
	}
	if got, found, readErr := state.LatestSend(home, "task-1", attempt.ID); readErr != nil || !found || got.State != state.SendUncertain {
		t.Fatalf("latest send = %+v found=%v err=%v", got, found, readErr)
	}
}

func TestExecuteClassifiesHerdrProcessStartFailureAsRetrySafeNotSubmitted(t *testing.T) {
	home, _ := newSteeringHome(t)
	pane := &testPane{
		pane:    herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		textErr: &herdr.ExecError{Err: errors.New("executable missing")},
	}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendNotSubmitted || !sendErr.RetrySafe || sendErr.PartialComposer {
		t.Fatalf("err=%v, want retry-safe not-submitted result", err)
	}
	if len(pane.textCalls) != 1 || len(pane.keyCalls) != 0 {
		t.Fatalf("calls=%v/%v, want one Text attempt and no Enter", pane.textCalls, pane.keyCalls)
	}
}

func TestExecuteClassifiesEnterProcessStartFailureAsPartialNotSubmitted(t *testing.T) {
	home, _ := newSteeringHome(t)
	pane := &testPane{
		pane:    herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		keysErr: &herdr.ExecError{Err: errors.New("argument list too long")},
	}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendNotSubmitted || sendErr.RetrySafe || !sendErr.PartialComposer {
		t.Fatalf("err=%v, want partial not-submitted result after Enter process start failure", err)
	}
	if len(pane.textCalls) != 1 || len(pane.keyCalls) != 1 {
		t.Fatalf("calls=%v/%v, want one Text and one Enter attempt", pane.textCalls, pane.keyCalls)
	}
}

func TestExecuteRejectsStaleExpectedAttemptBeforePending(t *testing.T) {
	home, attempt := newSteeringHome(t)
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(attempt.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttempt(state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning,
		Herdr: state.Herdr{Session: "session-2", WorkspaceID: "workspace-2", TabID: "tab-2", PaneID: "pane-2"}}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	pane := &testPane{pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle}}
	_, err = Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginUsageLimitResume, Client: pane, Expected: &attempt})
	if err == nil || len(pane.textCalls) != 0 || len(pane.keyCalls) != 0 {
		t.Fatalf("err=%v calls=%v/%v, want stale refusal without mutation", err, pane.textCalls, pane.keyCalls)
	}
}

func TestExecuteDoesNotUseHistoricalPendingForCurrentAttempt(t *testing.T) {
	home, first := newSteeringHome(t)
	if _, err := state.BeginSend(home, "task-1", first.ID, first.Herdr, state.SendOriginOperator, "old", "2026-08-15T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(first.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateAttempt(state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning,
		Herdr: state.Herdr{Session: "session-2", WorkspaceID: "workspace-2", TabID: "tab-2", PaneID: "pane-2"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	pane := &testPane{pane: herdr.Pane{PaneID: second.Herdr.PaneID, TabID: second.Herdr.TabID, WorkspaceID: second.Herdr.WorkspaceID, AgentStatus: herdr.StatusIdle}}
	if _, err := Execute(Request{Home: home, TaskID: "task-1", Message: "new", Origin: state.SendOriginOperator, Client: pane}); err != nil {
		t.Fatal(err)
	}
	if len(pane.textCalls) != 1 || pane.textCalls[0] != "new" {
		t.Fatalf("text calls=%v, want current Attempt only", pane.textCalls)
	}
}

func TestExecuteLeavesPendingOnCrashAfterTextAndStaleRecoveryMakesItUncertain(t *testing.T) {
	home, attempt := newSteeringHome(t)
	pane := &testPane{pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle}}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane,
		Faults: Faults{AfterText: errors.New("crash")}})
	if err == nil || len(pane.textCalls) != 1 || len(pane.keyCalls) != 0 {
		t.Fatalf("err=%v calls=%v/%v, want pending crash boundary", err, pane.textCalls, pane.keyCalls)
	}
	sends, err := state.ListSends(home, "task-1")
	if err != nil || len(sends) != 1 || sends[0].State != state.SendPending {
		t.Fatalf("sends=%+v err=%v, want one pending send", sends, err)
	}
	if _, err := Execute(Request{Home: home, TaskID: "task-1", Message: "next", Origin: state.SendOriginOperator, Client: pane}); err == nil {
		t.Fatal("new send ignored stale pending")
	}
	sends, err = state.ListSends(home, "task-1")
	if err != nil || len(sends) != 1 || sends[0].State != state.SendUncertain || len(pane.textCalls) != 1 {
		t.Fatalf("sends=%+v err=%v calls=%v, want stale pending uncertain without resend", sends, err, pane.textCalls)
	}
	_ = attempt
}

func TestExecuteSurfacesConcurrentRecoveryWithoutStartingAnotherSend(t *testing.T) {
	home, attempt := newSteeringHome(t)
	const recoveredAt = "2026-08-15T12:00:00.123456789Z"
	pending, err := state.BeginSend(home, "task-1", attempt.ID, attempt.Herdr, state.SendOriginOperator, "hello", recoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	pane := &testPane{pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle}}
	_, err = Execute(Request{Home: home, TaskID: "task-1", Message: "next", Origin: state.SendOriginOperator, Client: pane,
		Faults: Faults{BeforeTaskLock: func() {
			if _, normalizeErr := state.NormalizePendingSends(home, "task-1", "reconcile-stale-pending", recoveredAt); normalizeErr != nil {
				t.Fatal(normalizeErr)
			}
		}}})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendUncertain {
		t.Fatalf("err=%v, want concurrent recovery surfaced as uncertain", err)
	}
	if len(pane.textCalls) != 0 || len(pane.keyCalls) != 0 {
		t.Fatalf("calls=%v/%v, want no external mutation", pane.textCalls, pane.keyCalls)
	}
	if got, found, err := state.ReadSend(home, pending.ID); err != nil || !found || got.State != state.SendUncertain {
		t.Fatalf("send=%+v found=%v err=%v, want unchanged uncertain row", got, found, err)
	}
}

func TestExecuteCrashAfterEnterLeavesPendingAndNeverResubmits(t *testing.T) {
	home, _ := newSteeringHome(t)
	pane := &testPane{pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle}}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane,
		Faults: Faults{AfterEnter: errors.New("crash")}})
	if err == nil || len(pane.textCalls) != 1 || len(pane.keyCalls) != 1 {
		t.Fatalf("err=%v calls=%v/%v, want pending after both external calls", err, pane.textCalls, pane.keyCalls)
	}
	if _, err := Execute(Request{Home: home, TaskID: "task-1", Message: "next", Origin: state.SendOriginOperator, Client: pane}); err == nil {
		t.Fatal("new operation ignored stale pending")
	}
	if len(pane.textCalls) != 1 || len(pane.keyCalls) != 1 {
		t.Fatalf("calls=%v/%v, want no resend during stale recovery", pane.textCalls, pane.keyCalls)
	}
	sends, err := state.ListSends(home, "task-1")
	if err != nil || len(sends) != 1 || sends[0].State != state.SendUncertain {
		t.Fatalf("sends=%+v err=%v, want uncertain recovery", sends, err)
	}
}

func TestExecuteTypedEnterRejectionIsNotSubmittedButNotRetrySafe(t *testing.T) {
	home, _ := newSteeringHome(t)
	pane := &testPane{pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle}, keysErr: &herdr.APIError{Code: "pane_send_failed", Message: "queue full", PreSideEffectRejection: true}}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendNotSubmitted || sendErr.RetrySafe || !sendErr.PartialComposer {
		t.Fatalf("err=%v, want partial not-submitted non-retry-safe result", err)
	}
	if len(pane.textCalls) != 1 || len(pane.keyCalls) != 1 {
		t.Fatalf("calls=%v/%v, want one text and one Enter", pane.textCalls, pane.keyCalls)
	}
}

// Shrinks composerConfirms's poll window for the rest of the test, matching
// internal/runtime/launch_test.go's useFastLaunchPolling: milliseconds instead of the real settle
// window, with the interval-to-timeout ratio still generous enough that a genuine confirm never races it.
func useFastSendConfirmPolling(t *testing.T) {
	t.Helper()
	restore := ConfigureSendConfirmPollingForTest(time.Millisecond, 50*time.Millisecond, 400)
	t.Cleanup(restore)
}

// Live testing against a real codex worker found Enter has no working confirmation signal: an accepted
// message stays visible as a ">" history line, so a content check there would misreport ordinary sends
// as not-submitted (atqamz/hand#420). Enter stays claim-based and never reads the pane back to check.
func TestExecuteSubmitsOnEnterWithoutReadingTheComposerBack(t *testing.T) {
	home, attempt := newSteeringHome(t)
	pane := &testPane{
		pane:          herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		readResponses: []string{"hello still sitting in the composer, unmoved"},
	}
	result, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane})
	if err != nil {
		t.Fatal(err)
	}
	if result.Send.State != state.SendSubmitted || result.Send.AttemptID != attempt.ID {
		t.Fatalf("result = %+v, want submitted: Enter is claim-based", result.Send)
	}
	if got, found, readErr := state.LatestSend(home, "task-1", attempt.ID); readErr != nil || !found || got.ReasonCode != "text-and-enter-accepted" {
		t.Fatalf("latest send = %+v found=%v err=%v, want the plain accepted reason", got, found, readErr)
	}
	if pane.readCalls != 0 {
		t.Fatalf("readCalls = %d, want zero: Enter never reads the pane back", pane.readCalls)
	}
}

// A read failure while confirming a codex Tab/queue send is genuine uncertainty, distinguishable from a
// composer positively observed still holding the message (SendUncertain vs SendNotSubmitted). The first
// read (choosing Tab) succeeds; only the confirmation read after it fails.
func TestExecuteMarksCodexTabConfirmationReadFailureUncertain(t *testing.T) {
	useFastSendConfirmPolling(t)
	home, _ := newSteeringHome(t)
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(state.Task{ID: "task-codex", Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttempt(state.Attempt{TaskID: "task-codex", Lifecycle: state.AttemptRunning, Harness: "codex",
		Herdr: state.Herdr{Session: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1"}}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	pane := &testPane{
		pane:          herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusWorking},
		readResponses: []string{"tab to queue message                97% context left"},
		readErr:       errors.New("herdr pane read: transport lost"),
		readErrFrom:   2,
	}
	_, err = Execute(Request{Home: home, TaskID: "task-codex", Message: "please pause and wait for review", Origin: state.SendOriginOperator, Client: pane, Force: true})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendUncertain || sendErr.Reason != "composer-confirmation-read-failed" {
		t.Fatalf("err=%v, want uncertain confirmation-read-failed", err)
	}
	if len(pane.keyCalls) != 1 || len(pane.keyCalls[0]) != 1 || pane.keyCalls[0][0] != "Tab" {
		t.Fatalf("key calls = %v, want a single Tab chosen from the successful first read", pane.keyCalls)
	}
}

// codex's own composer refuses Enter mid-turn and advertises "tab to queue message" instead
// (atqamz/hand#426); decision 5 conditions the submit key on that discriminator, for codex only.
func TestExecuteSendsTabInsteadOfEnterWhenCodexAdvertisesQueueing(t *testing.T) {
	useFastSendConfirmPolling(t)
	home, _ := newSteeringHome(t)
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(state.Task{ID: "task-codex", Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttempt(state.Attempt{TaskID: "task-codex", Lifecycle: state.AttemptRunning, Harness: "codex",
		Herdr: state.Herdr{Session: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1"}}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	pane := &testPane{
		pane:          herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusWorking},
		readResponses: []string{"• Working (12s • esc to interrupt)\n\ntab to queue message                97% context left", ""},
	}
	result, err := Execute(Request{Home: home, TaskID: "task-codex", Message: "please pause and wait for review", Origin: state.SendOriginOperator, Client: pane, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Send.State != state.SendSubmitted {
		t.Fatalf("result = %+v, want submitted", result.Send)
	}
	if len(pane.keyCalls) != 1 || len(pane.keyCalls[0]) != 1 || pane.keyCalls[0][0] != "Tab" {
		t.Fatalf("key calls = %v, want a single Tab, never a hardcoded Enter", pane.keyCalls)
	}
	if got, found, readErr := state.LatestSend(home, "task-codex", result.Send.AttemptID); readErr != nil || !found || got.ReasonCode != "text-and-tab-queued" {
		t.Fatalf("latest send = %+v found=%v err=%v, want the tab-queued reason", got, found, readErr)
	}
}

// Live-verified against a real busy codex worker: once Tab queues a message, codex echoes it back
// verbatim under its own "Queued follow-up inputs" label, above the now-empty composer. Blind to that
// label, confirmation would misread the queue's own success echo as the composer still holding the message.
func TestExecuteConfirmsCodexQueuedMessageDespiteItsOwnEchoAboveTheComposer(t *testing.T) {
	useFastSendConfirmPolling(t)
	home, _ := newSteeringHome(t)
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(state.Task{ID: "task-codex", Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttempt(state.Attempt{TaskID: "task-codex", Lifecycle: state.AttemptRunning, Harness: "codex",
		Herdr: state.Herdr{Session: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1"}}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	const message = "please pause and wait for review"
	pane := &testPane{
		pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusWorking},
		readResponses: []string{
			"tab to queue message                97% context left",
			"• Working (14s • esc to interrupt)\n\nQueued follow-up inputs\n  ↳ " + message + "\n    alt + ↑ edit last queued message\n\n› \n\ngpt-5.6-luna max",
		},
	}
	result, err := Execute(Request{Home: home, TaskID: "task-codex", Message: message, Origin: state.SendOriginOperator, Client: pane, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Send.State != state.SendSubmitted {
		t.Fatalf("result = %+v, want submitted: the queue echo is confirmation, not a stuck composer", result.Send)
	}
	if len(pane.textCalls) != 1 || len(pane.keyCalls) != 1 {
		t.Fatalf("calls=%v/%v, want no retry: the echo is a successful queue, not a failure", pane.textCalls, pane.keyCalls)
	}
}

// The mirror of the queued-echo case above: Tab was sent but nothing was actually queued, so the
// message still sits at the live composer prompt with no "Queued follow-up inputs" label anywhere.
// That must not be confused with a successful queue.
func TestExecuteDoesNotConfirmCodexTabWhenMessageStaysInTheLiveComposer(t *testing.T) {
	restore := ConfigureSendConfirmPollingForTest(time.Millisecond, 0, 400)
	t.Cleanup(restore)
	home, _ := newSteeringHome(t)
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(state.Task{ID: "task-codex", Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttempt(state.Attempt{TaskID: "task-codex", Lifecycle: state.AttemptRunning, Harness: "codex",
		Herdr: state.Herdr{Session: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1"}}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	const message = "please pause and wait for review"
	pane := &testPane{
		pane:          herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusWorking},
		readResponses: []string{"tab to queue message  97% context left", "› " + message + "\n\ntab to queue message  95% context left"},
	}
	_, err = Execute(Request{Home: home, TaskID: "task-codex", Message: message, Origin: state.SendOriginOperator, Client: pane, Force: true})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendNotSubmitted || sendErr.Reason != state.SendReasonComposerRetainsMessage {
		t.Fatalf("err=%v, want confirmed-not-submitted: no queue label ever appeared", err)
	}
}

func TestComposerRetains(t *testing.T) {
	const sent = "Please stop and wait for review before merging this pull request, thanks in advance."
	tests := []struct {
		name string
		text string
		key  string
		want bool
	}{
		{name: "exact match still sitting in the composer", text: "› " + sent, key: "Enter", want: true},
		{name: "unrelated transcript with no trace of it", text: "• Ran go test ./...\n  ok\n\n❯ ", key: "Enter", want: false},
		{name: "empty pane", text: "", key: "Enter", want: false},
		{
			// The scout's live reproduction of atqamz/hand#420: both the start and end of the message
			// are gone, only a shifted interior slice survives. A prefix or suffix match would miss
			// this; any one intact chunk anywhere must not.
			name: "corrupted interior slice with both ends missing",
			text: "wait for review before merging this pull",
			key:  "Enter",
			want: true,
		},
		{
			name: "a terminal-wrapped line break lands inside an otherwise intact chunk",
			text: "› Please stop and wait for review\nbefore merging this pull request, thanks in\nadvance.",
			key:  "Enter",
			want: true,
		},
		{
			// A successful Tab-queue echoes the message back verbatim under codex's own label - that is
			// confirmation, not evidence the composer still holds it.
			name: "queued verbatim under codex's own label after a successful Tab",
			text: "• Working (3s)\n\nQueued follow-up inputs\n  ↳ " + sent + "\n\n› \n",
			key:  "Tab",
			want: false,
		},
		{
			name: "still at the live composer prompt with no queued label anywhere - Tab did not queue it",
			text: "› " + sent + "\n\ntab to queue message   90% context left",
			key:  "Tab",
			want: true,
		},
		{
			// The exclusion is specific to a Tab attempt; an Enter attempt gets no special-casing even
			// if the literal label string appears somewhere unrelated.
			name: "the queued label text is irrelevant when the key was Enter, not Tab",
			text: "Queued follow-up inputs\n" + sent,
			key:  "Enter",
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := composerRetains(tt.text, sent, tt.key); got != tt.want {
				t.Fatalf("composerRetains() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestExecuteReportsDurableFinalizationFailureWithoutRepeatingExternalCalls(t *testing.T) {
	home, _ := newSteeringHome(t)
	pane := &testPane{pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle}}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane,
		Faults: Faults{BeforePersist: errors.New("database unavailable")}})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || !sendErr.FinalizationFault || sendErr.State != state.SendPending {
		t.Fatalf("err=%v, want pending durable finalization failure", err)
	}
	if len(pane.textCalls) != 1 || len(pane.keyCalls) != 1 {
		t.Fatalf("calls=%v/%v, want exactly one external submission", pane.textCalls, pane.keyCalls)
	}
	sends, err := state.ListSends(home, "task-1")
	if err != nil || len(sends) != 1 || sends[0].State != state.SendPending {
		t.Fatalf("sends=%+v err=%v, want pending durable fact", sends, err)
	}
}
