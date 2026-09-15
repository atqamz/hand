package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
)

func TestReconcileCanonicalV19HerdrWorkerWakeFailsClosedForLegacyBindingBeforePrompt(t *testing.T) {
	fixture, request, _, client, _ := canonicalV19HerdrWorkerWakeFixture(t, "operation-herdr-worker-wake-unsupported")
	deps := canonicalV19HerdrWorkerWakeDeps{
		clientFor:    func(string) canonicalV19HerdrWorkerWakeClient { return client },
		processAlive: func(int) (bool, error) { return client.targetAlive, client.livenessErr },
		now:          func() time.Time { return time.Date(2026, 9, 9, 6, 9, 0, 0, time.UTC) },
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
		clientFor:    func(string) canonicalV19HerdrWorkerWakeClient { return client },
		processAlive: func(int) (bool, error) { return client.targetAlive, client.livenessErr },
		now:          func() time.Time { return time.Date(2026, 9, 9, 6, 13, 0, 0, time.UTC) },
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

func TestObserveCanonicalV19HerdrWorkerWakeTreatsPIDAbsenceAsUnknown(t *testing.T) {
	fixture, request, executorKey, client, _ := canonicalV19HerdrWorkerWakeFixture(t, "operation-herdr-worker-wake-pid-absence")
	client.targetAlive = false
	current, err := readCanonicalV19HerdrWorkerWakeCurrent(context.Background(), fixture.Home, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	sessionKey, err := parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey)
	if err != nil {
		t.Fatal(err)
	}

	observed := observeCanonicalV19HerdrWorkerWake(context.Background(), current, executorKey, sessionKey, client,
		func(int) (bool, error) { return client.targetAlive, client.livenessErr })
	if observed.State != canonicalV19HerdrWorkerWakeUnknown {
		t.Fatalf("PID absence observation = %q, want unknown", observed.State)
	}
}

func TestCanonicalV19HerdrWorkerWakeDoorbellIsDeterministicAndBounded(t *testing.T) {
	input := CanonicalV19HerdrWorkerWakeDoorbellInput{
		OperationID: "operation-worker-wake-doorbell", AttemptID: "attempt-1",
		ExecutorBindingID: "executor-binding-1", PendingThroughOrdinal: 3,
	}
	first, err := canonicalV19HerdrWorkerWakeDoorbellFor(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := canonicalV19HerdrWorkerWakeDoorbellFor(input)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first.Digest) != 64 || len(first.Text) > canonicalV19HerdrWorkerWakeDoorbellMaxBytes {
		t.Fatalf("doorbell = %#v / %#v", first, second)
	}
	if !strings.Contains(first.Text, `["runtime","worker-input","drain","attempt-1","executor-binding-1"]`) ||
		!strings.Contains(first.Text, `["runtime","worker-input","acknowledge","<worker-input-id>","executor-binding-1"]`) {
		t.Fatalf("doorbell protocol text = %q", first.Text)
	}
	input.PendingThroughOrdinal++
	third, err := canonicalV19HerdrWorkerWakeDoorbellFor(input)
	if err != nil {
		t.Fatal(err)
	}
	if third.Digest == first.Digest || third.Text == first.Text {
		t.Fatal("different pending boundary produced identical doorbell")
	}
}

type canonicalV19HerdrWorkerWakeFakeClient struct {
	*canonicalV19HerdrLaunchFakeClient
	wakeOperationID          string
	targetPID                int
	targetAlive              bool
	livenessErr              error
	promptCalls              int
	promptErr                error
	lastPrompt               string
	requireSubmittedAtPrompt bool
}

func (f *canonicalV19HerdrWorkerWakeFakeClient) AgentPromptContext(_ context.Context, target, text string) error {
	f.promptCalls++
	f.lastPrompt = text
	if target != f.processInfo.PaneID {
		f.t.Fatalf("AgentPromptContext target = %q, want %q", target, f.processInfo.PaneID)
	}
	current, err := readCanonicalV19HerdrWorkerWakeCurrent(context.Background(), f.home, f.wakeOperationID)
	if err != nil {
		f.t.Fatal(err)
	}
	want, err := canonicalV19HerdrWorkerWakeDoorbellFor(CanonicalV19HerdrWorkerWakeDoorbellInput{
		OperationID: current.Current.Request.OperationID, AttemptID: current.Current.Request.AttemptID,
		ExecutorBindingID:     current.Current.Request.ExecutorBindingID,
		PendingThroughOrdinal: current.Current.Request.PendingThroughOrdinal,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	if text != want.Text {
		f.t.Fatalf("AgentPromptContext text = %q, want %q", text, want.Text)
	}
	if f.requireSubmittedAtPrompt && current.Current.State != "submitted" {
		f.t.Fatalf("state at first WorkerWake provider mutation = %q, want submitted", current.Current.State)
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
	wakeInput.DoorbellDigest, err = CanonicalV19HerdrWorkerWakeDoorbellDigest(CanonicalV19HerdrWorkerWakeDoorbellInput{
		OperationID: wakeInput.OperationID, AttemptID: launchRequest.AttemptID,
		ExecutorBindingID: launchRequest.BindingID, PendingThroughOrdinal: wakeInput.PendingThroughOrdinal,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, wakeInput)
	if err != nil {
		t.Fatal(err)
	}
	client := &canonicalV19HerdrWorkerWakeFakeClient{
		canonicalV19HerdrLaunchFakeClient: baseClient,
		wakeOperationID:                   operationID,
		targetPID:                         target.PID,
		targetAlive:                       true,
	}
	return fixture, request, executorKey, client, workerInput
}
