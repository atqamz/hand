package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
)

func TestReconcileCanonicalV19HerdrInterruptSucceedsAfterExactCessationAndReruns(t *testing.T) {
	fixture, request, key, client := canonicalV19HerdrInterruptFixture(t, "operation-herdr-interrupt-success")
	client.requireSubmittedAtSend = true
	deps := canonicalV19HerdrInterruptTestDeps(t, key, client, time.Date(2026, 9, 8, 4, 10, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("reconcile Herdr Interrupt = %q, %v", state, err)
	}
	if client.sendCalls != 1 {
		t.Fatalf("pane send-keys calls = %d, want 1", client.sendCalls)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var operationState, terminalKind, interruptOperationID string
	if err := db.sql.QueryRow(`SELECT o.state,t.terminal_kind,t.interrupt_operation_id
		FROM external_operation o JOIN executor_binding_termination t ON t.interrupt_operation_id=o.id
		WHERE o.id=?`, request.OperationID).Scan(&operationState, &terminalKind, &interruptOperationID); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if operationState != "succeeded" || terminalKind != "interrupted" || interruptOperationID != request.OperationID {
		t.Fatalf("persisted Interrupt = %q/%q/%q", operationState, terminalKind, interruptOperationID)
	}

	state, err = reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("terminal rerun = %q, %v", state, err)
	}
	if client.sendCalls != 1 {
		t.Fatalf("pane send-keys calls after terminal rerun = %d, want 1", client.sendCalls)
	}
}

func TestReconcileCanonicalV19HerdrInterruptLostResponseConvergesFromPIDAbsence(t *testing.T) {
	fixture, request, key, client := canonicalV19HerdrInterruptFixture(t, "operation-herdr-interrupt-lost-response")
	client.sendErr = errors.New("provider response lost after ctrl+c")
	deps := canonicalV19HerdrInterruptTestDeps(t, key, client, time.Date(2026, 9, 8, 4, 11, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("lost-response Interrupt = %q, %v", state, err)
	}
	if client.sendCalls != 1 {
		t.Fatalf("pane send-keys calls = %d, want 1", client.sendCalls)
	}
}

func TestReconcileCanonicalV19HerdrInterruptPreSideEffectRejectionIsNoEffect(t *testing.T) {
	fixture, request, key, client := canonicalV19HerdrInterruptFixture(t, "operation-herdr-interrupt-no-effect")
	client.mutateOnSend = false
	client.sendErr = &herdr.APIError{Code: "pane_send_failed", Message: "queue full", PreSideEffectRejection: true}
	deps := canonicalV19HerdrInterruptTestDeps(t, key, client, time.Date(2026, 9, 8, 4, 12, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "no-effect" || err == nil {
		t.Fatalf("pre-side-effect Interrupt = %q, %v, want no-effect diagnostic", state, err)
	}
	if client.sendCalls != 1 || !client.targetAlive {
		t.Fatalf("provider state after no-effect = sends %d, alive %v, want 1/true", client.sendCalls, client.targetAlive)
	}
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var terminations int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM executor_binding_termination WHERE interrupt_operation_id=?`, request.OperationID).Scan(&terminations); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if terminations != 0 {
		t.Fatalf("ExecutorBinding termination rows = %d, want 0", terminations)
	}
}

func TestReconcileSubmittedCanonicalV19HerdrInterruptDoesNotBlindReplay(t *testing.T) {
	fixture, request, key, client := canonicalV19HerdrInterruptFixture(t, "operation-herdr-interrupt-submitted")
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-08T04:08:00Z", "interrupt-submitted-before-crash"); err != nil {
		t.Fatal(err)
	}
	deps := canonicalV19HerdrInterruptTestDeps(t, key, client, time.Date(2026, 9, 8, 4, 13, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("submitted recovery = %q, %v, want uncertain error", state, err)
	}
	if client.sendCalls != 0 {
		t.Fatalf("pane send-keys calls = %d, want 0", client.sendCalls)
	}
	state, err = reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("uncertain recovery = %q, %v, want uncertain error", state, err)
	}
	if client.sendCalls != 0 {
		t.Fatalf("pane send-keys calls after uncertain recovery = %d, want 0", client.sendCalls)
	}
}

func TestReconcilePreparedCanonicalV19HerdrInterruptCompletesAlreadyCeasedExecutorWithoutMutation(t *testing.T) {
	fixture, request, key, client := canonicalV19HerdrInterruptFixture(t, "operation-herdr-interrupt-already-ceased")
	client.stopTarget()
	deps := canonicalV19HerdrInterruptTestDeps(t, key, client, time.Date(2026, 9, 8, 4, 14, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("already-ceased Interrupt = %q, %v", state, err)
	}
	if client.sendCalls != 0 {
		t.Fatalf("pane send-keys calls = %d, want 0", client.sendCalls)
	}
}

func TestReconcilePreparedCanonicalV19HerdrInterruptRefusesProviderIdentityMismatch(t *testing.T) {
	fixture, request, key, client := canonicalV19HerdrInterruptFixture(t, "operation-herdr-interrupt-mismatch")
	for i := range client.processInfo.ForegroundProcesses {
		if client.processInfo.ForegroundProcesses[i].PID == key.ProcessID {
			client.processInfo.ForegroundProcesses[i].Argv = []string{"different-worker", "--other"}
		}
	}
	deps := canonicalV19HerdrInterruptTestDeps(t, key, client, time.Date(2026, 9, 8, 4, 15, 0, 0, time.UTC))

	state, err := reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "prepared" || err == nil {
		t.Fatalf("mismatched Interrupt = %q, %v, want prepared error", state, err)
	}
	if client.sendCalls != 0 {
		t.Fatalf("pane send-keys calls = %d, want 0", client.sendCalls)
	}
}

type canonicalV19HerdrInterruptFakeClient struct {
	*canonicalV19HerdrLaunchFakeClient
	interruptOperationID   string
	targetPID              int
	targetAlive            bool
	livenessErr            error
	sendCalls              int
	sendErr                error
	mutateOnSend           bool
	requireSubmittedAtSend bool
}

func (f *canonicalV19HerdrInterruptFakeClient) PaneSendKeys(paneID string, keys ...string) error {
	f.sendCalls++
	if paneID != f.processInfo.PaneID || len(keys) != 1 || keys[0] != "ctrl+c" {
		f.t.Fatalf("PaneSendKeys(%q, %v), want exact pane + ctrl+c", paneID, keys)
	}
	if f.requireSubmittedAtSend {
		db, err := openReadOnly(f.home)
		if err != nil {
			f.t.Fatal(err)
		}
		var state string
		if err := db.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, f.interruptOperationID).Scan(&state); err != nil {
			_ = db.Close()
			f.t.Fatal(err)
		}
		_ = db.Close()
		if state != "submitted" {
			f.t.Fatalf("state at first Interrupt provider mutation = %q, want submitted", state)
		}
	}
	if f.mutateOnSend {
		f.stopTarget()
	}
	return f.sendErr
}

func (f *canonicalV19HerdrInterruptFakeClient) stopTarget() {
	f.targetAlive = false
	f.processInfo.ForegroundProcessGroupID = f.processInfo.ShellPID
	f.processInfo.ForegroundProcesses = []herdr.Process{{
		PID: f.processInfo.ShellPID, Name: "shell", Argv: []string{"shell"}, Cwd: f.request.Spec.Cwd,
	}}
	pane := f.panes[f.processInfo.PaneID]
	pane.Agent = ""
	pane.AgentStatus = herdr.StatusUnknown
	f.panes[f.processInfo.PaneID] = pane
}

func canonicalV19HerdrInterruptFixture(
	t *testing.T,
	operationID string,
) (canonicalV19WorktreeCreateTestFixture, CanonicalV19InterruptRequest, canonicalV19HerdrExecutorProviderKey, *canonicalV19HerdrInterruptFakeClient) {
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
		EstablishedAt: "2026-09-08T04:06:00Z", EvidenceDigest: "executor-established-herdr-interrupt-fixture",
	}); err != nil {
		t.Fatal(err)
	}
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19InterruptPrepareInput{
		OperationID: operationID, OperationKey: "operation-key-" + operationID,
		ExecutorBindingID: launchRequest.BindingID, ReasonCode: "operator-request", CreatedAt: "2026-09-08T04:07:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &canonicalV19HerdrInterruptFakeClient{
		canonicalV19HerdrLaunchFakeClient: baseClient,
		interruptOperationID:             operationID,
		targetPID:                        target.PID,
		targetAlive:                      true,
		mutateOnSend:                     true,
	}
	return fixture, request, executorKey, client
}

func canonicalV19HerdrInterruptTestDeps(
	t *testing.T,
	key canonicalV19HerdrExecutorProviderKey,
	client *canonicalV19HerdrInterruptFakeClient,
	now time.Time,
) canonicalV19HerdrInterruptDeps {
	t.Helper()
	return canonicalV19HerdrInterruptDeps{
		clientFor: func(sessionName string) canonicalV19HerdrInterruptClient {
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
