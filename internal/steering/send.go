package steering

import (
	"errors"
	"fmt"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/state"
)

var ErrOwnershipConflict = errors.New("steering target changed")

var ErrPaneOwnershipMismatch = errors.New("pane ownership mismatch")

type Client interface {
	PaneGet(paneID string) (herdr.Pane, error)
	PaneSendText(paneID, text string) error
	PaneSendKeys(paneID string, keys ...string) error
}

type Request struct {
	Home              string
	TaskID            string
	Message           string
	Origin            state.SendOrigin
	Client            Client
	Wait              time.Duration
	WaitComposer      func(paneID string, timeout time.Duration) error
	TryLock           bool
	TryTaskLock       bool
	Force             bool
	Expected          *state.Attempt
	UsageLimitEpisode int64
	Now               func() time.Time
	Faults            Faults
}

type Faults struct {
	AfterPending   error
	BeforeTaskLock func()
	BeforeText     error
	AfterText      error
	BeforeEnter    error
	AfterEnter     error
	BeforePersist  error
}

type Result struct {
	Send state.SendAttempt
}

type Error struct {
	Cause             error
	Send              *state.SendAttempt
	AttemptID         int64
	State             state.SendState
	Reason            string
	RetrySafe         bool
	PartialComposer   bool
	Precondition      bool
	FinalizationFault bool
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return "steering failed"
	}
	return e.Cause.Error()
}

func (e *Error) Unwrap() error { return e.Cause }

func (e *Error) SendFields() (int64, int64, string, string, bool, bool) {
	var sendID int64
	if e.Send != nil {
		sendID = e.Send.ID
	}
	return sendID, e.AttemptID, string(e.State), e.Reason, e.RetrySafe, e.PartialComposer
}

func Execute(req Request) (Result, error) {
	if _, err := registry.Preflight(req.Home, false); err != nil {
		return Result{}, &Error{Cause: err, Precondition: true}
	}
	if req.Client == nil {
		return Result{}, &Error{Cause: errors.New("steering client is required"), Precondition: true}
	}
	if req.Home == "" || req.TaskID == "" {
		return Result{}, &Error{Cause: errors.New("steering home and task are required"), Precondition: true}
	}
	if req.Origin == "" {
		req.Origin = state.SendOriginOperator
	}
	if req.Now == nil {
		req.Now = time.Now
	}
	if req.WaitComposer == nil {
		req.WaitComposer = func(string, time.Duration) error { return nil }
	}
	unlockSend, err := lock(req, "send:"+req.TaskID)
	if err != nil {
		return Result{}, &Error{Cause: fmt.Errorf("lock send %q: %w", req.TaskID, err), Precondition: true}
	}
	defer unlockSend()

	active, err := state.ActiveAttempt(req.Home, req.TaskID)
	if err != nil {
		return Result{}, precondition(fmt.Errorf("read active attempt for task %q: %w", req.TaskID, err))
	}
	if req.Expected != nil && !sameAttempt(active, *req.Expected) {
		return Result{}, precondition(fmt.Errorf("%w: task %q attempt %d is no longer current", ErrOwnershipConflict, req.TaskID, req.Expected.ID))
	}
	if active.Lifecycle != state.AttemptRunning {
		return Result{}, precondition(fmt.Errorf("task %q has no confirmed running attempt", req.TaskID))
	}
	if harness.IsOneShot(active.Harness) {
		// A completed one-shot returns this exact pane to its shell. Reject before a send row or pane
		// mutation so operator prose can never become shell input merely because the Attempt is active.
		return Result{}, precondition(fmt.Errorf("harness %q uses one-shot worker execution and cannot accept hand send; wait for its report or start a new Attempt", active.Harness))
	}
	pending, found, err := pendingSend(req, active.ID)
	if err != nil {
		return Result{}, &Error{Cause: err, Precondition: true, AttemptID: active.ID}
	}
	if found {
		if req.Faults.BeforeTaskLock != nil {
			req.Faults.BeforeTaskLock()
		}
		unlockTask, err := taskLock(req, "task:"+req.TaskID)
		if err != nil {
			return Result{}, precondition(fmt.Errorf("lock task %q to recover pending send: %w", req.TaskID, err))
		}
		defer unlockTask()
		return Result{}, refusePending(req, active, &pending)
	}
	pane, err := verifyPane(req.Client, active)
	if err != nil {
		return Result{}, paneError(active, "observe pane", err)
	}
	if pane.AgentStatus == herdr.StatusWorking && !req.Force {
		if err := req.WaitComposer(active.Herdr.PaneID, req.Wait); err != nil {
			if errors.Is(err, herdr.ErrComposerBusyTimeout) {
				cause := fmt.Errorf("composer stayed busy for %s; no external message mutation occurred", req.Wait)
				var busyErr *herdr.ComposerBusyError
				if errors.As(err, &busyErr) {
					cause = fmt.Errorf("%s (%s)", cause, busyErr.Detail())
				}
				return Result{}, &Error{Cause: cause, AttemptID: active.ID, State: state.SendNotSubmitted, Reason: "composer-timeout", RetrySafe: true}
			}
			return Result{}, paneError(active, "wait for composer", err)
		}
	}

	if req.Faults.BeforeTaskLock != nil {
		req.Faults.BeforeTaskLock()
	}
	unlockTask, err := taskLock(req, "task:"+req.TaskID)
	if err != nil {
		if errors.Is(err, state.ErrLockBusy) {
			return Result{}, precondition(fmt.Errorf("%w: task %q changed while sending", ErrOwnershipConflict, req.TaskID))
		}
		return Result{}, precondition(fmt.Errorf("lock task %q before send: %w", req.TaskID, err))
	}
	defer unlockTask()
	current, err := state.ActiveAttempt(req.Home, req.TaskID)
	if err != nil {
		return Result{}, precondition(fmt.Errorf("re-read active attempt for task %q: %w", req.TaskID, err))
	}
	if !sameAttempt(active, current) || (req.Expected != nil && !sameAttempt(current, *req.Expected)) {
		return Result{}, precondition(fmt.Errorf("%w: task %q changed while waiting", ErrOwnershipConflict, req.TaskID))
	}
	if _, err := verifyPane(req.Client, current); err != nil {
		return Result{}, paneError(current, "verify pane ownership before send", err)
	}
	if err := refusePending(req, current, nil); err != nil {
		return Result{}, err
	}

	send, err := state.BeginSend(req.Home, req.TaskID, current.ID, current.Herdr, req.Origin, req.Message, req.Now().UTC().Format(time.RFC3339Nano), req.UsageLimitEpisode)
	if err != nil {
		return Result{}, precondition(fmt.Errorf("begin send: %w", err))
	}
	if req.Faults.AfterPending != nil {
		return Result{}, pendingError(send, req.Faults.AfterPending, "pending-before-text")
	}
	if req.Faults.BeforeText != nil {
		return Result{}, pendingError(send, req.Faults.BeforeText, "before-text")
	}
	if err := req.Client.PaneSendText(current.Herdr.PaneID, req.Message); err != nil {
		if herdr.IsPreSideEffectRejection(err) || herdr.IsProcessNotStarted(err) {
			return finalize(req, send, state.SendNotSubmitted, rejectionReason(err, state.SendReasonTextRejectedBeforeAcceptance), false, false)
		}
		return finalize(req, send, state.SendUncertain, "text-outcome-ambiguous", false, false)
	}
	if req.Faults.AfterText != nil {
		return Result{}, pendingError(send, req.Faults.AfterText, "text-succeeded-before-enter")
	}
	if req.Faults.BeforeEnter != nil {
		return Result{}, pendingError(send, req.Faults.BeforeEnter, "before-enter")
	}
	if err := req.Client.PaneSendKeys(current.Herdr.PaneID, "Enter"); err != nil {
		if herdr.IsPreSideEffectRejection(err) || herdr.IsProcessNotStarted(err) {
			return finalize(req, send, state.SendNotSubmitted, rejectionReason(err, state.SendReasonEnterRejectedAfterTextStaged), false, true)
		}
		return finalize(req, send, state.SendUncertain, "enter-outcome-ambiguous", false, false)
	}
	if req.Faults.AfterEnter != nil {
		return Result{}, pendingError(send, req.Faults.AfterEnter, "enter-succeeded-before-finalization")
	}
	if req.Faults.BeforePersist != nil {
		return Result{}, &Error{Cause: fmt.Errorf("terminal submission returned success, but durable send finalization failed: %w", req.Faults.BeforePersist), Send: &send, AttemptID: current.ID, State: state.SendPending, Reason: "durable-finalization-unresolved", FinalizationFault: true}
	}
	return finalize(req, send, state.SendSubmitted, "text-and-enter-accepted", true, false)
}

func lock(req Request, name string) (func(), error) {
	if req.TryLock {
		return state.TryLock(req.Home, name)
	}
	return state.Lock(req.Home, name)
}

func taskLock(req Request, name string) (func(), error) {
	if req.TryLock || req.TryTaskLock {
		return state.TryLock(req.Home, name)
	}
	return state.Lock(req.Home, name)
}

func sameAttempt(a, b state.Attempt) bool {
	return a.ID == b.ID && a.TaskID == b.TaskID && a.Lifecycle == b.Lifecycle && a.Herdr == b.Herdr
}

func verifyPane(client Client, attempt state.Attempt) (herdr.Pane, error) {
	pane, err := client.PaneGet(attempt.Herdr.PaneID)
	if err != nil {
		return herdr.Pane{}, err
	}
	if pane.PaneID != attempt.Herdr.PaneID ||
		(attempt.Herdr.TabID != "" && pane.TabID != attempt.Herdr.TabID) ||
		(attempt.Herdr.WorkspaceID != "" && pane.WorkspaceID != attempt.Herdr.WorkspaceID) {
		return herdr.Pane{}, fmt.Errorf("%w: pane %q does not match persisted session/workspace/tab ownership", ErrPaneOwnershipMismatch, pane.PaneID)
	}
	return pane, nil
}

func paneError(attempt state.Attempt, operation string, err error) error {
	message := fmt.Errorf("%s for pane %s: %w", operation, attempt.Herdr.PaneID, err)
	if errors.Is(err, herdr.ErrNotFound) {
		message = fmt.Errorf("herdr pane %s not found: %w", attempt.Herdr.PaneID, err)
	}
	if errors.Is(err, ErrPaneOwnershipMismatch) {
		return precondition(message)
	}
	return &Error{Cause: message, AttemptID: attempt.ID}
}

func refusePending(req Request, active state.Attempt, observed *state.SendAttempt) error {
	if observed != nil {
		send, found, err := state.ReadSendMetadata(req.Home, observed.ID)
		if err != nil {
			return &Error{Cause: fmt.Errorf("read send %d: %w", observed.ID, err), Precondition: true, AttemptID: active.ID}
		}
		if !found {
			return precondition(fmt.Errorf("send %d disappeared while recovering pending state", observed.ID))
		}
		if send.State != state.SendPending {
			return unresolved(send)
		}
	}
	send, found, err := state.PendingSend(req.Home, req.TaskID, active.ID)
	if err != nil {
		return &Error{Cause: fmt.Errorf("read pending send for task %q: %w", req.TaskID, err), Precondition: true, AttemptID: active.ID}
	}
	if !found {
		return nil
	}
	recovered, changed, err := state.NormalizePendingSend(req.Home, send.ID, req.TaskID, active.ID, "stale-pending-recovered", req.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return &Error{Cause: fmt.Errorf("recover pending send %d: %w", send.ID, err), Send: &send, AttemptID: send.AttemptID, State: state.SendPending, Reason: "stale-pending-recovery-failed"}
	}
	if !changed {
		return unresolved(recovered)
	}
	return unresolved(recovered)
}

func pendingSend(req Request, attemptID int64) (state.SendAttempt, bool, error) {
	return state.PendingSend(req.Home, req.TaskID, attemptID)
}

func unresolved(send state.SendAttempt) error {
	return &Error{Cause: fmt.Errorf("send %d is %s; do not blindly retry", send.ID, send.State), Send: &send, AttemptID: send.AttemptID, State: send.State, Reason: send.ReasonCode}
}

func rejectionReason(err error, fallback string) string {
	var apiErr *herdr.APIError
	if errors.As(err, &apiErr) && apiErr.Code != "" {
		return fallback + ":" + apiErr.Code
	}
	return fallback
}

func finalize(req Request, send state.SendAttempt, next state.SendState, reason string, submitted, partial bool) (Result, error) {
	finalized, err := state.FinalizeSend(req.Home, send.ID, send.TaskID, send.AttemptID, next, reason, req.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		message := fmt.Sprintf("send %d external submission is unresolved: %v", send.ID, err)
		if submitted {
			message = fmt.Sprintf("terminal submission returned success, but durable send finalization failed: %v", err)
		}
		return Result{}, &Error{Cause: errors.New(message), Send: &send, AttemptID: send.AttemptID, State: state.SendPending, Reason: "durable-finalization-unresolved", FinalizationFault: true}
	}
	if next == state.SendSubmitted {
		return Result{Send: finalized}, nil
	}
	return Result{}, &Error{Cause: fmt.Errorf("send %d is %s: %s", finalized.ID, next, reason), Send: &finalized, AttemptID: finalized.AttemptID, State: next, Reason: reason, RetrySafe: next == state.SendNotSubmitted && !partial, PartialComposer: partial}
}

func pendingError(send state.SendAttempt, cause error, reason string) error {
	return &Error{Cause: cause, Send: &send, AttemptID: send.AttemptID, State: state.SendPending, Reason: reason}
}

func precondition(err error) error {
	return &Error{Cause: err, Precondition: true}
}
