package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
)

func TestReconcileCanonicalV19HerdrWorkerWakeSucceedsOnlyFromAcceptedPromptAndReruns(t *testing.T) {
	fixture, request, key, client, workerInput := canonicalV19HerdrWorkerWakeFixture(t, "operation-herdr-worker-wake-success")
	client.requireSubmittedAtPrompt = true
	deps := canonicalV19HerdrWorkerWakeTestDeps(t, key, client, time.Date(2026, 9, 9, 6, 10, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("reconcile Herdr WorkerWake = %q, %v", state, err)
	}
	if client.promptCalls != 1 {
		t.Fatalf("agent prompt calls = %d, want 1", client.promptCalls)
	}
	if strings.Contains(client.lastPrompt, workerInput.Payload) {
		t.Fatalf("doorbell leaked WorkerInput payload: %q", client.lastPrompt)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var operationState string
	var acknowledgements int
	if err := db.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, request.OperationID).Scan(&operationState); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT count(*) FROM worker_input_acknowledgement WHERE worker_input_id=?`, workerInput.ID).Scan(&acknowledgements); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if operationState != "succeeded" || acknowledgements != 0 {
		t.Fatalf("WorkerWake persisted state/acknowledgements = %q/%d, want succeeded/0", operationState, acknowledgements)
	}

	state, err = reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("terminal rerun = %q, %v", state, err)
	}
	if client.promptCalls != 1 {
		t.Fatalf("agent prompt calls after terminal rerun = %d, want 1", client.promptCalls)
	}
}

func TestReconcileCanonicalV19HerdrWorkerWakePreSideEffectRejectionIsNoEffect(t *testing.T) {
	fixture, request, key, client, _ := canonicalV19HerdrWorkerWakeFixture(t, "operation-herdr-worker-wake-no-effect")
	client.promptErr = &herdr.APIError{
		Operation: "agent prompt " + key.PaneID + " wake", Code: "agent_blocked", Message: "agent blocked",
	}
	deps := canonicalV19HerdrWorkerWakeTestDeps(t, key, client, time.Date(2026, 9, 9, 6, 11, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "no-effect" || err == nil {
		t.Fatalf("pre-side-effect WorkerWake = %q, %v, want no-effect diagnostic", state, err)
	}
	if client.promptCalls != 1 {
		t.Fatalf("agent prompt calls = %d, want 1", client.promptCalls)
	}
}

func TestReconcileCanonicalV19HerdrWorkerWakeAmbiguousPromptBecomesUncertainWithoutReplay(t *testing.T) {
	fixture, request, key, client, _ := canonicalV19HerdrWorkerWakeFixture(t, "operation-herdr-worker-wake-ambiguous")
	client.promptErr = errors.New("provider response lost after agent prompt")
	deps := canonicalV19HerdrWorkerWakeTestDeps(t, key, client, time.Date(2026, 9, 9, 6, 12, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("ambiguous WorkerWake = %q, %v, want uncertain diagnostic", state, err)
	}
	if client.promptCalls != 1 {
		t.Fatalf("agent prompt calls = %d, want 1", client.promptCalls)
	}

	state, err = reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("uncertain rerun = %q, %v, want uncertain diagnostic", state, err)
	}
	if client.promptCalls != 1 {
		t.Fatalf("agent prompt calls after uncertain rerun = %d, want 1", client.promptCalls)
	}
}

func TestReconcileSubmittedCanonicalV19HerdrWorkerWakeDoesNotBlindReplay(t *testing.T) {
	fixture, request, key, client, _ := canonicalV19HerdrWorkerWakeFixture(t, "operation-herdr-worker-wake-submitted")
	if _, err := SubmitCanonicalV19WorkerWake(context.Background(), fixture.Home, request.OperationID,
		"2026-09-09T06:08:00Z", "worker-wake-submitted-before-crash"); err != nil {
		t.Fatal(err)
	}
	deps := canonicalV19HerdrWorkerWakeTestDeps(t, key, client, time.Date(2026, 9, 9, 6, 13, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("submitted recovery = %q, %v, want uncertain diagnostic", state, err)
	}
	if client.promptCalls != 0 {
		t.Fatalf("agent prompt calls = %d, want 0", client.promptCalls)
	}
}

func TestReconcilePreparedCanonicalV19HerdrWorkerWakeBlockedAgentIsNoEffectWithoutMutation(t *testing.T) {
	fixture, request, key, client, _ := canonicalV19HerdrWorkerWakeFixture(t, "operation-herdr-worker-wake-blocked")
	pane := client.panes[key.PaneID]
	pane.AgentStatus = herdr.StatusBlocked
	client.panes[key.PaneID] = pane
	deps := canonicalV19HerdrWorkerWakeTestDeps(t, key, client, time.Date(2026, 9, 9, 6, 14, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "no-effect" || err == nil {
		t.Fatalf("blocked WorkerWake = %q, %v, want no-effect diagnostic", state, err)
	}
	if client.promptCalls != 0 {
		t.Fatalf("agent prompt calls = %d, want 0", client.promptCalls)
	}
}

func TestReconcilePreparedCanonicalV19HerdrWorkerWakeRefusesProviderIdentityMismatch(t *testing.T) {
	fixture, request, key, client, _ := canonicalV19HerdrWorkerWakeFixture(t, "operation-herdr-worker-wake-mismatch")
	for i := range client.processInfo.ForegroundProcesses {
		if client.processInfo.ForegroundProcesses[i].PID == key.ProcessID {
			client.processInfo.ForegroundProcesses[i].Argv = []string{"different-worker", "--other"}
		}
	}
	deps := canonicalV19HerdrWorkerWakeTestDeps(t, key, client, time.Date(2026, 9, 9, 6, 15, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "prepared" || err == nil {
		t.Fatalf("mismatched WorkerWake = %q, %v, want prepared error", state, err)
	}
	if client.promptCalls != 0 {
		t.Fatalf("agent prompt calls = %d, want 0", client.promptCalls)
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
		ExecutorBindingID: current.Current.Request.ExecutorBindingID,
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
		wakeOperationID:                  operationID,
		targetPID:                        target.PID,
		targetAlive:                      true,
	}
	return fixture, request, executorKey, client, workerInput
}

func canonicalV19HerdrWorkerWakeTestDeps(
	t *testing.T,
	key canonicalV19HerdrExecutorProviderKey,
	client *canonicalV19HerdrWorkerWakeFakeClient,
	now time.Time,
) canonicalV19HerdrWorkerWakeDeps {
	t.Helper()
	return canonicalV19HerdrWorkerWakeDeps{
		clientFor: func(sessionName string) canonicalV19HerdrWorkerWakeClient {
			if sessionName != key.SessionName {
				t.Fatalf("Herdr session = %q, want %q", sessionName, key.SessionName)
			}
			return client
		},
		processAlive: func(pid int) (bool, error) {
			if pid != client.targetPID {
				return false, fmt.Errorf("unexpected PID %d", pid)
			}
			return client.targetAlive, client.livenessErr
		},
		now: func() time.Time { return now },
	}
}
