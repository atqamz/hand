package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
)

func TestReconcileCanonicalV19HerdrWorkerWakeFailsClosedForLegacyBindingBeforePrompt(t *testing.T) {
	fixture, request, _, client, _ := canonicalV19HerdrWorkerWakeFixture(t, "operation-herdr-worker-wake-unsupported")
	deps := canonicalV19HerdrWorkerWakeDeps{
		clientFor: func(string) canonicalV19HerdrWorkerWakeClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 9, 6, 9, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "no-effect" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("unqualified Herdr WorkerWake = %q, %v, want no-effect unsupported error", state, err)
	}
	state, err = reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "no-effect" || err != nil {
		t.Fatalf("replayed no-effect Herdr WorkerWake = %q, %v, want stable terminal state", state, err)
	}
	if client.promptCalls != 0 {
		t.Fatalf("agent prompt calls = %d, want 0", client.promptCalls)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var operationState, finalizedAt string
	if err := db.sql.QueryRow(`SELECT state,finalized_at FROM external_operation WHERE id=?`, request.OperationID).Scan(&operationState, &finalizedAt); err != nil {
		t.Fatal(err)
	}
	if operationState != "no-effect" {
		t.Fatalf("persisted WorkerWake state = %q, want no-effect", operationState)
	}
	if finalizedAt == "" {
		t.Fatal("no-effect WorkerWake lacks terminal timestamp")
	}
	second := CanonicalV19WorkerWakePrepareInput{
		OperationID:           "operation-herdr-worker-wake-after-no-effect",
		OperationKey:          "operation-key-operation-herdr-worker-wake-after-no-effect",
		ExecutorBindingID:     request.ExecutorBindingID,
		PendingThroughOrdinal: request.PendingThroughOrdinal,
		WakeReason:            request.WakeReason,
		DoorbellDigest:        request.DoorbellDigest,
		CreatedAt:             "2026-09-09T06:15:00Z",
	}
	if _, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, second); err != nil {
		t.Fatalf("WorkerWake after terminal no-effect remained blocked: %v", err)
	}
}

func TestReconcileCanonicalV19HerdrWorkerWakeSubmittedBecomesUncertainWithoutReplay(t *testing.T) {
	fixture, request, _, client, _ := canonicalV19HerdrWorkerWakeFixture(t, "operation-herdr-worker-wake-submitted-unsupported")
	if _, err := SubmitCanonicalV19WorkerWake(context.Background(), fixture.Home, request.OperationID,
		"2026-09-09T06:12:00Z", "submitted-worker-wake-unsupported"); err != nil {
		t.Fatal(err)
	}
	deps := canonicalV19HerdrWorkerWakeDeps{
		clientFor: func(string) canonicalV19HerdrWorkerWakeClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 9, 6, 13, 0, 0, time.UTC) },
	}
	state, err := reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("submitted Herdr WorkerWake = %q, %v, want uncertain unsupported error", state, err)
	}
	state, err = reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("replayed Herdr WorkerWake = %q, %v, want terminal uncertain unsupported error", state, err)
	}
	if client.promptCalls != 0 {
		t.Fatalf("provider WorkerWake calls = %d, want 0", client.promptCalls)
	}
	var operationState string
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, request.OperationID).Scan(&operationState); err != nil {
		t.Fatal(err)
	}
	if operationState != "uncertain" {
		t.Fatalf("persisted WorkerWake state = %q, want uncertain", operationState)
	}
}

func TestReconcileCanonicalV19HerdrWorkerWakePreparedSettlementRejectsConcurrentSubmit(t *testing.T) {
	fixture, request, _, client, _ := canonicalV19HerdrWorkerWakeFixture(t, "operation-herdr-worker-wake-prepared-submit-race")
	submitted := false
	deps := canonicalV19HerdrWorkerWakeDeps{
		clientFor: func(string) canonicalV19HerdrWorkerWakeClient { return client },
		now: func() time.Time {
			if !submitted {
				submitted = true
				if _, err := SubmitCanonicalV19WorkerWake(context.Background(), fixture.Home, request.OperationID,
					"2026-09-09T06:16:00Z", "submitted-worker-wake-race"); err != nil {
					t.Fatal(err)
				}
			}
			return time.Date(2026, 9, 9, 6, 17, 0, 0, time.UTC)
		},
	}
	state, err := reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("prepared WorkerWake after concurrent submit = %q, %v, want uncertain unsupported error", state, err)
	}
	state, err = reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("replayed raced WorkerWake = %q, %v, want stable uncertain unsupported error", state, err)
	}
	if client.promptCalls != 0 {
		t.Fatalf("raced WorkerWake provider calls = %d, want 0", client.promptCalls)
	}
	second := CanonicalV19WorkerWakePrepareInput{
		OperationID:           "operation-herdr-worker-wake-after-submit-race",
		OperationKey:          "operation-key-operation-herdr-worker-wake-after-submit-race",
		ExecutorBindingID:     request.ExecutorBindingID,
		PendingThroughOrdinal: request.PendingThroughOrdinal,
		WakeReason:            request.WakeReason,
		DoorbellDigest:        request.DoorbellDigest,
		CreatedAt:             "2026-09-09T06:18:00Z",
	}
	if _, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, second); !errors.Is(err, ErrCanonicalV19WorkerWakeConflict) {
		t.Fatalf("WorkerWake claim after raced submit error = %v, want unresolved claim conflict", err)
	}
}

func TestCanonicalV19HerdrWorkerWakeDoorbellMatchesTheGrammar(t *testing.T) {
	doorbell := canonicalV19HerdrWorkerWakeDoorbell
	if !regexp.MustCompile(`^\|[a-z |-]*$`).MatchString(doorbell) || strings.ContainsAny(doorbell, "0123456789") {
		t.Fatalf("doorbell %q must start with | and hold only lowercase letters, spaces, - and | (EG-14)", doorbell)
	}
	digest := sha256.Sum256([]byte(doorbell))
	if CanonicalV19HerdrWorkerWakeDoorbellDigest() != hex.EncodeToString(digest[:]) {
		t.Fatal("doorbell digest is not the SHA-256 of the one constant doorbell")
	}
}

type canonicalV19HerdrWorkerWakeFakeClient struct {
	*canonicalV19HerdrLaunchFakeClient
	wakeOperationID          string
	herdrCalls               int
	promptCalls              int
	promptErr                error
	onPrompt                 func()
	requireSubmittedAtPrompt bool
}

func (f *canonicalV19HerdrWorkerWakeFakeClient) bounded(ctx context.Context, call string) error {
	f.herdrCalls++
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
		f.t.Errorf("Herdr %s ran without a context bounded to 30s", call)
	}
	return ctx.Err()
}

func (f *canonicalV19HerdrWorkerWakeFakeClient) ObserveSession(ctx context.Context) herdr.SessionObservation {
	if err := f.bounded(ctx, "session observation"); err != nil {
		return herdr.SessionObservation{State: herdr.SessionUnknown, Reason: err.Error()}
	}
	return f.canonicalV19HerdrLaunchFakeClient.ObserveSession(ctx)
}

func (f *canonicalV19HerdrWorkerWakeFakeClient) WorkspaceListContext(ctx context.Context) ([]herdr.Workspace, error) {
	if err := f.bounded(ctx, "workspace list"); err != nil {
		return nil, err
	}
	return f.canonicalV19HerdrLaunchFakeClient.WorkspaceListContext(ctx)
}

func (f *canonicalV19HerdrWorkerWakeFakeClient) TabListContext(ctx context.Context, workspaceID string) ([]herdr.Tab, error) {
	if err := f.bounded(ctx, "tab list"); err != nil {
		return nil, err
	}
	return f.TabList(workspaceID)
}

func (f *canonicalV19HerdrWorkerWakeFakeClient) PaneGetContext(ctx context.Context, paneID string) (herdr.Pane, error) {
	if err := f.bounded(ctx, "pane get"); err != nil {
		return herdr.Pane{}, err
	}
	return f.canonicalV19HerdrLaunchFakeClient.PaneGetContext(ctx, paneID)
}

func (f *canonicalV19HerdrWorkerWakeFakeClient) PaneProcessInfoContext(ctx context.Context, paneID string) (herdr.ProcessInfo, error) {
	if err := f.bounded(ctx, "pane process-info"); err != nil {
		return herdr.ProcessInfo{}, err
	}
	return f.PaneProcessInfo(paneID)
}

func (f *canonicalV19HerdrWorkerWakeFakeClient) AgentPromptContext(ctx context.Context, target, text string) error {
	f.promptCalls++
	if err := f.bounded(ctx, "agent prompt"); err != nil {
		return err
	}
	if target != f.processInfo.PaneID {
		f.t.Fatalf("AgentPromptContext target = %q, want %q", target, f.processInfo.PaneID)
	}
	current, err := readCanonicalV19HerdrWorkerWakeCurrent(context.Background(), f.home, f.wakeOperationID)
	if err != nil {
		f.t.Fatal(err)
	}
	if text != canonicalV19HerdrWorkerWakeDoorbell {
		f.t.Fatalf("AgentPromptContext text = %q, want the constant doorbell (EG-14)", text)
	}
	if f.requireSubmittedAtPrompt && current.Current.State != "submitted" {
		f.t.Fatalf("state at first WorkerWake provider mutation = %q, want submitted", current.Current.State)
	}
	if f.onPrompt != nil {
		f.onPrompt()
	}
	return f.promptErr
}

func canonicalV19HerdrWorkerWakeFixture(
	t *testing.T,
	operationID string,
) (canonicalV19WorktreeCreateTestFixture, CanonicalV19WorkerWakeRequest, canonicalV19HerdrExecutorProviderKey, *canonicalV19HerdrWorkerWakeFakeClient, CanonicalV19WorkerInput) {
	t.Helper()
	fixture, launchRequest, sessionKey := canonicalV19HerdrLaunchFixture(t, "operation-launch-for-"+operationID, canonicalV19HerdrLiteralEnvironment())
	baseClient := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, launchRequest, sessionKey)
	baseClient.startTarget(launchRequest.Spec)
	var target herdr.Process
	for _, process := range baseClient.processInfo.ForegroundProcesses {
		if process.PID == canonicalV19HerdrLaunchTestProcessID {
			target = process
			break
		}
	}
	if target.PID == 0 {
		t.Fatal("fixture target process is absent")
	}
	executorKey := canonicalV19HerdrExecutorProviderKey{
		SessionName: sessionKey.SessionName, WorkspaceID: sessionKey.WorkspaceID, TabID: sessionKey.TabID, PaneID: sessionKey.PaneID,
		ProcessGroup: canonicalV19HerdrLaunchTestProcessGroup, ProcessID: target.PID, ProcessDigest: canonicalV19HerdrProcessDigest(target),
	}
	providerExecutorKey, err := encodeCanonicalV19HerdrExecutorProviderKey(executorKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19ExecutorBinding(context.Background(), fixture.Home, CanonicalV19ExecutorBindingEvidence{
		OperationID: launchRequest.OperationID, ProviderExecutorKey: providerExecutorKey,
		EstablishedAt: "2026-09-09T06:05:00Z", EvidenceDigest: "executor-established-herdr-worker-wake-fixture",
	}); err != nil {
		t.Fatal(err)
	}
	workerInput, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home,
		canonicalV19WorkerInputCreateInput(launchRequest, "worker-input-for-"+operationID, "semantic payload must not enter doorbell", "digest-worker-input-for-"+operationID))
	if err != nil {
		t.Fatal(err)
	}
	wakeInput := canonicalV19WorkerWakePrepareInput(launchRequest, operationID, workerInput.Ordinal)
	wakeInput.CreatedAt = "2026-09-09T06:07:00Z"
	wakeInput.DoorbellDigest = CanonicalV19HerdrWorkerWakeDoorbellDigest()
	request, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, wakeInput)
	if err != nil {
		t.Fatal(err)
	}
	client := &canonicalV19HerdrWorkerWakeFakeClient{
		canonicalV19HerdrLaunchFakeClient: baseClient,
		wakeOperationID:                   operationID,
	}
	return fixture, request, executorKey, client, workerInput
}
