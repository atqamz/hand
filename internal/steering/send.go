package steering

import (
	"errors"
	"fmt"
	"strings"
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
	// PaneReadUnwrapped reads the pane's recent scrollback with terminal line-wrap reversed, so a long
	// sent message reads back as the same contiguous text it went in as.
	PaneReadUnwrapped(paneID string, lines int) (string, error)
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

// Bounds the read-back that decides whether a send actually left the composer, mirroring
// internal/runtime/launch.go's confirmLaunch: act, then poll-observe with a bounded timeout rather than
// declare success without evidence. The first read is immediate, so an idle composer costs one exec.
type confirmPoll struct {
	Interval  time.Duration
	Timeout   time.Duration
	ReadLines int
}

// ReadLines of 400 matches the value the operator's own retraction on atqamz/hand#420 (and this task's
// scout report) verified was not viewport-clipped against a several-KB composer; 10s/300ms is a fraction
// of confirmLaunch's minute-scale budget because this is one pane read settling, not a harness starting.
var sendConfirmPolling = confirmPoll{Interval: 300 * time.Millisecond, Timeout: 10 * time.Second, ReadLines: 400}

func ConfigureSendConfirmPollingForTest(interval, timeout time.Duration, readLines int) func() {
	previous := sendConfirmPolling
	sendConfirmPolling = confirmPoll{Interval: interval, Timeout: timeout, ReadLines: readLines}
	return func() { sendConfirmPolling = previous }
}

// Codex's own on-screen text for "Enter will not submit right now, Tab queues instead" - live-verified
// against a real busy codex pane (atqamz/hand#426). A codex UI fact, not a general one, so
// chooseSubmitKey only ever reads it for the codex harness.
const codexQueueDiscriminator = "tab to queue message"

// What codex prints above the composer once Tab has queued a message: the message itself, echoed back
// verbatim under this label (live-verified against a real busy codex pane). Without excluding it,
// composerRetains would misread a successful queue as the composer still stuck holding the message.
const codexQueuedMarker = "Queued follow-up inputs"

// Picks Enter, or Tab for a codex pane currently showing its own queue discriminator - conditioned on
// harness identity, never applied to another harness. A read failure falls back to Enter rather than
// turning an unrelated read problem into a submit failure.
func chooseSubmitKey(client Client, paneID, harnessName string) string {
	if harnessName != harness.Codex {
		return "Enter"
	}
	text, err := client.PaneReadUnwrapped(paneID, sendConfirmPolling.ReadLines)
	if err != nil {
		return "Enter"
	}
	if strings.Contains(strings.ToLower(text), codexQueueDiscriminator) {
		return "Tab"
	}
	return "Enter"
}

// The fixed-size fragment composerRetains looks for. A live reproduction of atqamz/hand#420 found a
// corrupted send surviving as a shifted *interior* slice, both ends missing, which only an any-intact-
// chunk search catches. Kept small: missing a surviving fragment means wrongly declaring submitted.
const confirmChunkSize = 24

func stripWhitespace(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// Reports whether text still shows a recognizable fragment of sent. When key is "Tab", a successful
// queue echoes sent verbatim under codexQueuedMarker - expected confirmation, not a stuck composer -
// so that marker and everything after it is excluded from the comparison first.
func composerRetains(text, sent, key string) bool {
	if key == "Tab" {
		if idx := strings.Index(text, codexQueuedMarker); idx >= 0 {
			text = text[:idx]
		}
	}
	haystack := stripWhitespace(text)
	needle := stripWhitespace(sent)
	if needle == "" {
		return false
	}
	runes := []rune(needle)
	if len(runes) <= confirmChunkSize {
		return strings.Contains(haystack, needle)
	}
	// Chunks overlap: a coarser, non-overlapping grid can miss a fragment straddling two chunk boundaries
	// without fully containing either one. A stride this much smaller than the chunk keeps that gap small
	// - any surviving span within confirmChunkSize of the full chunk size fully contains one probe.
	const stride = 8
	for i := 0; i < len(runes); i += stride {
		end := i + confirmChunkSize
		if end > len(runes) {
			end = len(runes)
		}
		if end-i < confirmChunkSize/2 {
			break
		}
		if strings.Contains(haystack, string(runes[i:end])) {
			return true
		}
	}
	return false
}

// Polls the pane until it no longer holds a recognizable fragment of sent, or the bounded timeout
// elapses.
func composerConfirms(client Client, paneID, sent, key string) (bool, error) {
	deadline := time.Now().Add(sendConfirmPolling.Timeout)
	for {
		text, err := client.PaneReadUnwrapped(paneID, sendConfirmPolling.ReadLines)
		if err != nil {
			return false, err
		}
		if !composerRetains(text, sent, key) {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(sendConfirmPolling.Interval)
	}
}

func submittedReason(key string, retried bool) string {
	reason := "text-and-enter-confirmed"
	if key == "Tab" {
		reason = "text-and-tab-queued"
	}
	if retried {
		reason += "-after-retry"
	}
	return reason
}

// Turns a PaneSendText/PaneSendKeys failure into the same NotSubmitted/Uncertain split the first
// attempt has always used, so a bounded retry that hits a structured rejection is reported exactly as
// informatively as an original attempt would have been.
func classifySubmitError(err error, notSubmittedReason string, ambiguousReason string, partial bool) (state.SendState, string, bool) {
	if herdr.IsPreSideEffectRejection(err) || herdr.IsProcessNotStarted(err) {
		return state.SendNotSubmitted, rejectionReason(err, notSubmittedReason), partial
	}
	return state.SendUncertain, ambiguousReason, false
}

// Runs once Text and the submit key both return success: reads the composer back and, if the message
// is still recognizably there, resends once before giving up - verification before retry, since an
// identical resend was observed recovering a send the first attempt silently lost.
func confirmSubmission(req Request, current state.Attempt, message, key string) (state.SendState, string, bool, bool) {
	confirmed, err := composerConfirms(req.Client, current.Herdr.PaneID, message, key)
	if err != nil {
		return state.SendUncertain, "composer-confirmation-read-failed", false, false
	}
	if confirmed {
		return state.SendSubmitted, submittedReason(key, false), true, false
	}

	retryKey := chooseSubmitKey(req.Client, current.Herdr.PaneID, current.Harness)
	if err := req.Client.PaneSendText(current.Herdr.PaneID, message); err != nil {
		next, reason, partial := classifySubmitError(err, state.SendReasonTextRejectedBeforeAcceptance, "retry-text-outcome-ambiguous", false)
		return next, reason, false, partial
	}
	if err := req.Client.PaneSendKeys(current.Herdr.PaneID, retryKey); err != nil {
		next, reason, partial := classifySubmitError(err, state.SendReasonEnterRejectedAfterTextStaged, "retry-enter-outcome-ambiguous", true)
		return next, reason, false, partial
	}

	confirmed, err = composerConfirms(req.Client, current.Herdr.PaneID, message, retryKey)
	if err != nil {
		return state.SendUncertain, "composer-confirmation-read-failed-after-retry", false, false
	}
	if confirmed {
		return state.SendSubmitted, submittedReason(retryKey, true), true, false
	}
	return state.SendNotSubmitted, state.SendReasonComposerRetainsMessage, false, true
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
	key := chooseSubmitKey(req.Client, current.Herdr.PaneID, current.Harness)
	if err := req.Client.PaneSendKeys(current.Herdr.PaneID, key); err != nil {
		if herdr.IsPreSideEffectRejection(err) || herdr.IsProcessNotStarted(err) {
			return finalize(req, send, state.SendNotSubmitted, rejectionReason(err, state.SendReasonEnterRejectedAfterTextStaged), false, true)
		}
		return finalize(req, send, state.SendUncertain, "enter-outcome-ambiguous", false, false)
	}
	if req.Faults.AfterEnter != nil {
		return Result{}, pendingError(send, req.Faults.AfterEnter, "enter-succeeded-before-finalization")
	}

	next, reason, submitted, partial := confirmSubmission(req, current, req.Message, key)

	if req.Faults.BeforePersist != nil {
		return Result{}, &Error{Cause: fmt.Errorf("terminal submission returned success, but durable send finalization failed: %w", req.Faults.BeforePersist), Send: &send, AttemptID: current.ID, State: state.SendPending, Reason: "durable-finalization-unresolved", FinalizationFault: true}
	}
	return finalize(req, send, next, reason, submitted, partial)
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
