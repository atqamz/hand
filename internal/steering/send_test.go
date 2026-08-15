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
