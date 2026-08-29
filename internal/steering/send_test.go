package steering

import (
	"errors"
	"strings"
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
	pane := &testPane{
		pane:          herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		readResponses: []string{"› hello", "hello\n\n• new agent activity"},
	}
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
	// One read to choose the key (reused as the pre-key baseline) and one to confirm: decision 4's "one
	// more exec" cost for an idle composer that confirms on the first read.
	if pane.readCalls != 2 {
		t.Fatalf("readCalls = %d, want exactly 2", pane.readCalls)
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
	pane := &testPane{
		pane:          herdr.Pane{PaneID: second.Herdr.PaneID, TabID: second.Herdr.TabID, WorkspaceID: second.Herdr.WorkspaceID, AgentStatus: herdr.StatusIdle},
		readResponses: []string{"› new", "new\n\n• new agent activity"},
	}
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
	restoreSettle := ConfigureInterChunkSettleForTest(time.Millisecond)
	t.Cleanup(restoreSettle)
}

// Positive evidence, not absence: an accepted message stays visible forever as a history line
// indistinguishable from one still unsent, so an absence check races that redraw (atqamz/hand#420,
// atqamz/hand#459). Here new content also appeared and the tail is present, so Enter still confirms.
func TestExecuteConfirmsEnterDespiteAcceptedMessageRemainingVisibleAsHistory(t *testing.T) {
	home, attempt := newSteeringHome(t)
	pane := &testPane{
		pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		readResponses: []string{
			"› hello",
			"› hello\n\n› \n\n• Working (1s • esc to interrupt)",
		},
	}
	result, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane})
	if err != nil {
		t.Fatal(err)
	}
	if result.Send.State != state.SendSubmitted || result.Send.AttemptID != attempt.ID {
		t.Fatalf("result = %+v, want submitted despite the message remaining visible", result.Send)
	}
	if got, found, readErr := state.LatestSend(home, "task-1", attempt.ID); readErr != nil || !found || got.ReasonCode != "text-and-enter-confirmed" {
		t.Fatalf("latest send = %+v found=%v err=%v, want the confirmed reason", got, found, readErr)
	}
}

// atqamz/hand#426's shape generalized to Enter: the composer shows byte-identical content before and
// after the key, meaning Enter produced no observed reaction at all. Confirming here would be exactly
// the false "submitted" claim atqamz/hand#420 is about.
func TestExecuteDoesNotConfirmEnterWhenComposerShowsNoReaction(t *testing.T) {
	restore := ConfigureSendConfirmPollingForTest(time.Millisecond, 0, 400)
	t.Cleanup(restore)
	home, _ := newSteeringHome(t)
	pane := &testPane{
		pane:          herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		readResponses: []string{"› hello"},
	}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendNotSubmitted || sendErr.Reason != state.SendReasonEnterNotConfirmed {
		t.Fatalf("err=%v, want not-submitted enter-not-confirmed", err)
	}
}

// atqamz/hand#459's live-measured stall: a large PaneSendText can leave only an early fragment in the
// composer for many seconds, drifting further on Enter without ever completing. This lands on Uncertain,
// not not-submitted: the drift is positive evidence something landed, just never provably all of it.
func TestExecuteDoesNotConfirmEnterWhenOnlyAPartialFragmentArrived(t *testing.T) {
	restore := ConfigureSendConfirmPollingForTest(time.Millisecond, 5*time.Millisecond, 400)
	t.Cleanup(restore)
	home, _ := newSteeringHome(t)
	const sent = "please read the whole review before replying, thanks very much for the help"
	pane := &testPane{
		pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		readResponses: []string{
			"› please read the whole review before",
			"please read the whole review before\nreplying, thanks",
			"please read the whole review before\nreplying, thanks very",
		},
	}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: sent, Origin: state.SendOriginOperator, Client: pane})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendUncertain || sendErr.Reason != state.SendReasonEnterNotConfirmed {
		t.Fatalf("err=%v, want uncertain enter-not-confirmed: something landed, just never the tail", err)
	}
}

// A read failure choosing the key leaves no baseline to compare against - confirming blind would be
// worse than admitting uncertainty, so this never reaches enterConfirms at all.
func TestExecuteMarksEnterPreKeyReadFailureUncertain(t *testing.T) {
	home, _ := newSteeringHome(t)
	pane := &testPane{
		pane:    herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		readErr: errors.New("herdr pane read: transport lost"),
	}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendUncertain || sendErr.Reason != "composer-confirmation-read-failed" {
		t.Fatalf("err=%v, want uncertain composer-confirmation-read-failed", err)
	}
	if len(pane.keyCalls) != 1 || pane.keyCalls[0][0] != "Enter" {
		t.Fatalf("key calls = %v, want Enter still chosen despite the read failure", pane.keyCalls)
	}
}

// A read failure during the confirmation poll itself (as opposed to choosing the key) is the same
// honest uncertainty, reached through enterConfirms this time.
func TestExecuteMarksEnterConfirmationReadFailureUncertain(t *testing.T) {
	useFastSendConfirmPolling(t)
	home, _ := newSteeringHome(t)
	pane := &testPane{
		pane:          herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		readResponses: []string{"› hello"},
		readErr:       errors.New("herdr pane read: transport lost"),
		readErrFrom:   2,
	}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: "hello", Origin: state.SendOriginOperator, Client: pane})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendUncertain || sendErr.Reason != "composer-confirmation-read-failed" {
		t.Fatalf("err=%v, want uncertain composer-confirmation-read-failed", err)
	}
}

func TestComposerHasTail(t *testing.T) {
	const sent = "Please stop and wait for review before merging this pull request, thanks in advance."
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "short message must match in full, not just a fragment", text: "› Please stop and wait", want: false},
		{name: "full message present", text: "› " + sent, want: true},
		{name: "empty pane", text: "", want: false},
		{
			// The live-measured atqamz/hand#459 stall: an early fragment survives but the tail never
			// arrived. An any-chunk search would wrongly confirm this; the tail check must not.
			name: "early fragment survives, tail never arrived",
			text: "› Please stop and wait for review before merging this",
			want: false,
		},
		{
			name: "a terminal-wrapped line break lands inside the tail",
			text: "› Please stop and wait for review\nbefore merging this pull request, thanks in\nadvance.",
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := composerHasTail(tt.text, sent); got != tt.want {
				t.Fatalf("composerHasTail() = %t, want %t", got, tt.want)
			}
		})
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

// Live reproduction of atqamz/hand#478, caught by rapid direct pane reads against a real codex worker:
// the instant a Tab-queued message's hosting turn ended, codex dropped "Queued follow-up inputs" and
// re-rendered the same text as an ordinary "›" history line, above a fresh empty composer.
func TestExecuteConfirmsCodexTabWhenItsHostingTurnEndedDuringThePoll(t *testing.T) {
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
			"• Working (8s • esc to interrupt)\n\ntab to queue message   92% context left",
			"• Ran sleep 8 && echo natural-done\n  └ natural-done\n\n───────────────────────────\n\n" +
				"› " + message + "\n\n\n› \n\ngpt-5.6-luna max",
		},
	}
	result, err := Execute(Request{Home: home, TaskID: "task-codex", Message: message, Origin: state.SendOriginOperator, Client: pane, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Send.State != state.SendSubmitted {
		t.Fatalf("result = %+v, want submitted: the turn ending mid-poll promoted the queued message to history, it did not lose it", result.Send)
	}
	if len(pane.textCalls) != 1 || len(pane.keyCalls) != 1 {
		t.Fatalf("calls=%v/%v, want no retry", pane.textCalls, pane.keyCalls)
	}
}

func TestLastComposerBlock(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "no composer line at all falls back to the full text", text: "• Ran go test ./...\n  ok", want: "• Ran go test ./...\n  ok"},
		{name: "a single composer line at the very start is already the whole text", text: "› hello", want: "› hello"},
		{
			name: "history above, live composer below: only the last block is kept",
			text: "› first turn, now history\n\n• done\n\n› second turn, still live",
			want: "› second turn, still live",
		},
		{
			// atqamz/hand#478: a dequeued message promoted to a "›" history line must not be mistaken
			// for the live composer just because it starts with the same glyph the composer uses.
			name: "a dequeued message promoted to history is not the final block",
			text: "• Ran sleep 8\n  └ done\n\n› promoted queued message\n\n› \n\ngpt-5.6-luna max",
			want: "› \n\ngpt-5.6-luna max",
		},
		{
			// Live-verified against a real composer: a sent message's own line starting with "›" is
			// rendered indented ("  › ..."), never flush at column 0, so it can never be mistaken for
			// the composer's own boundary. If it could, a stuck message would wrongly read as gone.
			name: "the message's own glyph-prefixed line is indented, not a false composer boundary",
			text: "› a genuinely stuck long unsent instruction line here\n  › ok",
			want: "› a genuinely stuck long unsent instruction line here\n  › ok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastComposerBlock(tt.text); got != tt.want {
				t.Fatalf("lastComposerBlock() = %q, want %q", got, tt.want)
			}
		})
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
			// atqamz/hand#478: live-reproduced by reading a real codex pane the instant a queued
			// message's hosting turn ended. The queue label is already gone, and the message now reads
			// as an ordinary "›" history line - but it precedes the true final block, an empty composer.
			name: "queue label gone, message promoted to history, fresh composer beneath it",
			text: "• Ran sleep 8\n  └ done\n\n› " + sent + "\n\n\n› \n\ngpt-5.6-luna max",
			key:  "Tab",
			want: false,
		},
		{
			// Live-reproduced under real CPU load against a long-lived worker: an earlier send of the
			// same text already promoted to history, with the label still on screen and intact below it.
			// A truncation keyed on the label's position alone would still see this older copy above it.
			name: "an earlier copy already sits in history above an intact, still-visible queue label",
			text: "› " + sent + "\n\n• Queued follow-up inputs\n  ↳ " + sent + "\n\n› \n",
			key:  "Tab",
			want: false,
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

func TestChunkMessage(t *testing.T) {
	tests := []struct {
		name    string
		message string
		size    int
		want    []string
	}{
		{name: "fits in one chunk, returned unchanged", message: "abc", size: 5, want: []string{"abc"}},
		{name: "exact multiple of size", message: "abcdefghij", size: 5, want: []string{"abcde", "fghij"}},
		{name: "remainder trails as a short final chunk", message: "abcdefghi", size: 4, want: []string{"abcd", "efgh", "i"}},
		{
			// Ranging over a string yields each rune's start offset, so a cut can only ever land there -
			// this multi-byte content would corrupt under a naive byte-offset slice.
			name:    "cuts only at rune boundaries, never inside a multi-byte rune",
			message: "ab日本語cd", size: 4,
			want: []string{"ab日", "本語", "cd"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chunkMessage(tt.message, tt.size)
			if len(got) != len(tt.want) {
				t.Fatalf("chunkMessage() = %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("chunkMessage() = %q, want %q", got, tt.want)
				}
			}
			if joined := strings.Join(got, ""); joined != tt.message {
				t.Fatalf("chunks rejoin to %q, want original %q", joined, tt.message)
			}
		})
	}
}

// The exact shape flagged in review: a chunk boundary landing mid-blank-line (routine in this fleet's
// Markdown) strips to an empty needle on its own, which composerHasTail always reports absent.
// chunkConfirms takes the cumulative text so far, so the tail reaches back into real content instead.
func TestChunkConfirmsReachesPastAWhitespaceOnlyChunkIntoEarlierContent(t *testing.T) {
	if composerHasTail("› first paragraph here", "\n\n") {
		t.Fatal("composerHasTail on a whitespace-only chunk in isolation should never confirm")
	}
	restore := ConfigureSendConfirmPollingForTest(time.Millisecond, 5*time.Millisecond, 400)
	t.Cleanup(restore)
	pane := &testPane{readResponses: []string{"› first paragraph here"}}
	confirmed, err := chunkConfirms(pane, "pane-1", "first paragraph here\n\n")
	if err != nil || !confirmed {
		t.Fatalf("chunkConfirms(cumulative) = %t, %v, want confirmed via the earlier real content", confirmed, err)
	}
}

// End-to-end version of the same fix: chunkMessage("AAAAAA\n\nBBBBBB", 2) really does produce a
// whitespace-only chunk ("\n\n") sandwiched between real content, and the full send still reaches and
// presses the submit key exactly once.
func TestExecuteSendsAWhitespaceOnlyChunkWithoutStallingConfirmation(t *testing.T) {
	restoreSize := ConfigureSendChunkSizeForTest(2)
	t.Cleanup(restoreSize)
	useFastSendConfirmPolling(t)
	home, attempt := newSteeringHome(t)
	const message = "AAAAAA\n\nBBBBBB"
	pane := &testPane{
		pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		readResponses: []string{
			"› AA", "› AAAA", "› AAAAAA", "› AAAAAA", "› AAAAAABB", "› AAAAAABBBB", "› AAAAAABBBBBB",
			"› AAAAAABBBBBB",
			"AAAAAABBBBBB\n\n• new agent activity",
		},
	}
	result, err := Execute(Request{Home: home, TaskID: "task-1", Message: message, Origin: state.SendOriginOperator, Client: pane})
	if err != nil {
		t.Fatal(err)
	}
	if result.Send.State != state.SendSubmitted || result.Send.AttemptID != attempt.ID {
		t.Fatalf("result = %+v, want submitted despite a whitespace-only chunk mid-send", result.Send)
	}
	wantChunks := []string{"AA", "AA", "AA", "\n\n", "BB", "BB", "BB"}
	if len(pane.textCalls) != len(wantChunks) {
		t.Fatalf("text calls = %q, want %q", pane.textCalls, wantChunks)
	}
	for i := range wantChunks {
		if pane.textCalls[i] != wantChunks[i] {
			t.Fatalf("text calls = %q, want %q", pane.textCalls, wantChunks)
		}
	}
	if len(pane.keyCalls) != 1 || pane.keyCalls[0][0] != "Enter" {
		t.Fatalf("key calls = %v, want a single Enter only after every chunk confirmed", pane.keyCalls)
	}
}

func TestExecuteSendsChunkedTextConfirmingEachChunkBeforeTheSubmitKey(t *testing.T) {
	restoreSize := ConfigureSendChunkSizeForTest(5)
	t.Cleanup(restoreSize)
	useFastSendConfirmPolling(t)
	home, attempt := newSteeringHome(t)
	const message = "abcdefghij" // chunkMessage(_, 5) -> "abcde", "fghij"
	pane := &testPane{
		pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		readResponses: []string{
			"› abcde",                            // confirms chunk 1
			"› abcdefghij",                       // confirms chunk 2 (the whole message, now fully present)
			"› abcdefghij",                       // chooseSubmitKey's pre-key baseline
			"abcdefghij\n\n• new agent activity", // enterConfirms: reacted and holds the tail
		},
	}
	result, err := Execute(Request{Home: home, TaskID: "task-1", Message: message, Origin: state.SendOriginOperator, Client: pane})
	if err != nil {
		t.Fatal(err)
	}
	if result.Send.State != state.SendSubmitted || result.Send.AttemptID != attempt.ID {
		t.Fatalf("result = %+v, want submitted", result.Send)
	}
	if len(pane.textCalls) != 2 || pane.textCalls[0] != "abcde" || pane.textCalls[1] != "fghij" {
		t.Fatalf("text calls = %q, want the two chunks in order", pane.textCalls)
	}
	if len(pane.keyCalls) != 1 || pane.keyCalls[0][0] != "Enter" {
		t.Fatalf("key calls = %v, want a single Enter chosen only after both chunks confirmed", pane.keyCalls)
	}
	if pane.readCalls != 4 {
		t.Fatalf("readCalls = %d, want exactly 4: one confirm per chunk, one pre-key baseline, one post-key confirm", pane.readCalls)
	}
}

// The hard constraint this issue is built around: a chunk that never confirms must not leave a half
// message that a later key press turns into a genuine partial submission. sendMessage must return before
// Execute ever reaches chooseSubmitKey or PaneSendKeys.
func TestExecuteFailsWithoutPressingSubmitKeyWhenAChunkNeverConfirms(t *testing.T) {
	restoreSize := ConfigureSendChunkSizeForTest(5)
	t.Cleanup(restoreSize)
	restorePoll := ConfigureSendConfirmPollingForTest(time.Millisecond, 5*time.Millisecond, 400)
	t.Cleanup(restorePoll)
	restoreSettle := ConfigureInterChunkSettleForTest(time.Millisecond)
	t.Cleanup(restoreSettle)
	home, _ := newSteeringHome(t)
	const message = "abcdefghij" // chunkMessage(_, 5) -> "abcde", "fghij"
	pane := &testPane{
		pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		// Confirms chunk 1, then never advances - chunk 2's tail ("fghij") never appears.
		readResponses: []string{"› abcde"},
	}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: message, Origin: state.SendOriginOperator, Client: pane})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendNotSubmitted || sendErr.Reason != state.SendReasonTextChunkNotConfirmed || !sendErr.PartialComposer {
		t.Fatalf("err=%v, want not-submitted text-chunk-not-confirmed partial", err)
	}
	if len(pane.textCalls) != 2 || pane.textCalls[0] != "abcde" || pane.textCalls[1] != "fghij" {
		t.Fatalf("text calls = %q, want both chunks written even though the second never confirmed", pane.textCalls)
	}
	if len(pane.keyCalls) != 0 {
		t.Fatalf("key calls = %v, want the submit key never chosen or sent", pane.keyCalls)
	}
}

// A read failure mid-chunk-confirmation is the same honest uncertainty the Enter and Tab paths already
// give a read failure - not a claim about what the composer holds either way.
func TestExecuteMarksChunkConfirmationReadFailureUncertain(t *testing.T) {
	restoreSize := ConfigureSendChunkSizeForTest(5)
	t.Cleanup(restoreSize)
	useFastSendConfirmPolling(t)
	home, _ := newSteeringHome(t)
	const message = "abcdefghij" // chunkMessage(_, 5) -> "abcde", "fghij"
	pane := &testPane{
		pane:          herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle},
		readResponses: []string{"› abcde"},
		readErr:       errors.New("herdr pane read: transport lost"),
		readErrFrom:   2, // chunk 1's confirmation read succeeds; chunk 2's fails
	}
	_, err := Execute(Request{Home: home, TaskID: "task-1", Message: message, Origin: state.SendOriginOperator, Client: pane})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || sendErr.State != state.SendUncertain || sendErr.Reason != "composer-confirmation-read-failed" {
		t.Fatalf("err=%v, want uncertain composer-confirmation-read-failed", err)
	}
	if len(pane.textCalls) != 2 || len(pane.keyCalls) != 0 {
		t.Fatalf("calls=%v/%v, want both chunks written and no submit key", pane.textCalls, pane.keyCalls)
	}
}

func TestExecuteReportsDurableFinalizationFailureWithoutRepeatingExternalCalls(t *testing.T) {
	useFastSendConfirmPolling(t)
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
