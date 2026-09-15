package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
)

func TestReconcileCanonicalV19HerdrInterruptFailsClosedWithoutExactExecutionProof(t *testing.T) {
	fixture, request, _, client := canonicalV19HerdrInterruptFixture(t, "operation-herdr-interrupt-unsupported")
	deps := canonicalV19HerdrInterruptDeps{
		clientFor:    func(string) canonicalV19HerdrInterruptClient { return client },
		processAlive: func(int) (bool, error) { return client.targetAlive, client.livenessErr },
		now:          func() time.Time { return time.Date(2026, 9, 8, 4, 9, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "no-effect" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("unqualified Herdr Interrupt = %q, %v, want no-effect unsupported error", state, err)
	}
	state, err = reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "no-effect" || err != nil {
		t.Fatalf("replayed no-effect Herdr Interrupt = %q, %v, want stable terminal state", state, err)
	}
	if client.sendCalls != 0 {
		t.Fatalf("pane send-keys calls = %d, want 0", client.sendCalls)
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
	var terminations int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM executor_binding_termination WHERE interrupt_operation_id=?`, request.OperationID).Scan(&terminations); err != nil {
		t.Fatal(err)
	}
	if operationState != "no-effect" || terminations != 0 {
		t.Fatalf("persisted Interrupt state/terminations = %q/%d, want no-effect/0", operationState, terminations)
	}
	if finalizedAt == "" {
		t.Fatal("no-effect Interrupt lacks terminal timestamp")
	}
	second := CanonicalV19InterruptPrepareInput{
		OperationID:       "operation-herdr-interrupt-after-no-effect",
		OperationKey:      "operation-key-operation-herdr-interrupt-after-no-effect",
		ExecutorBindingID: request.ExecutorBindingID,
		ReasonCode:        request.ReasonCode,
		CreatedAt:         "2026-09-08T04:15:00Z",
	}
	if _, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, second); err != nil {
		t.Fatalf("Interrupt after terminal no-effect remained blocked: %v", err)
	}
}

func TestReconcileCanonicalV19HerdrInterruptSubmittedBecomesUncertainWithoutReplay(t *testing.T) {
	fixture, request, _, client := canonicalV19HerdrInterruptFixture(t, "operation-herdr-interrupt-submitted-unsupported")
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-08T04:12:00Z", "submitted-interrupt-unsupported"); err != nil {
		t.Fatal(err)
	}
	deps := canonicalV19HerdrInterruptDeps{
		clientFor:    func(string) canonicalV19HerdrInterruptClient { return client },
		processAlive: func(int) (bool, error) { return client.targetAlive, client.livenessErr },
		now:          func() time.Time { return time.Date(2026, 9, 8, 4, 13, 0, 0, time.UTC) },
	}
	state, err := reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("submitted Herdr Interrupt = %q, %v, want uncertain unsupported error", state, err)
	}
	state, err = reconcileCanonicalV19HerdrInterrupt(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("replayed Herdr Interrupt = %q, %v, want terminal uncertain unsupported error", state, err)
	}
	if client.sendCalls != 0 {
		t.Fatalf("provider Interrupt calls = %d, want 0", client.sendCalls)
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
		t.Fatalf("persisted Interrupt state = %q, want uncertain", operationState)
	}
}

func TestObserveCanonicalV19HerdrInterruptTreatsPIDAbsenceAndInventoryFailureAsUnknown(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*canonicalV19HerdrInterruptFakeClient)
	}{
		{name: "PID absent", setup: func(client *canonicalV19HerdrInterruptFakeClient) { client.targetAlive = false }},
		{name: "provider inventory unavailable", setup: func(client *canonicalV19HerdrInterruptFakeClient) {
			client.workspaceErr = errors.New("provider inventory unavailable")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, request, executorKey, client := canonicalV19HerdrInterruptFixture(t, "operation-herdr-interrupt-pid-absence")
			test.setup(client)
			current, err := readCanonicalV19HerdrInterruptCurrent(context.Background(), fixture.Home, request.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			sessionKey, err := parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey)
			if err != nil {
				t.Fatal(err)
			}

			observed := observeCanonicalV19HerdrInterrupt(context.Background(), current, executorKey, sessionKey, client,
				func(int) (bool, error) { return client.targetAlive, client.livenessErr })
			if observed.State != canonicalV19HerdrInterruptUnknown {
				t.Fatalf("unqualified observation = %q, want unknown", observed.State)
			}
		})
	}
}

func TestObserveCanonicalV19HerdrInterruptCannotDistinguishRestartOrReplacement(t *testing.T) {
	fixture, request, executorKey, original := canonicalV19HerdrInterruptFixture(t, "operation-herdr-interrupt-replacement")
	if !strings.HasPrefix(request.ProviderExecutorKey, "herdr-executor:v1?") {
		t.Fatalf("fixture provider Executor key = %q, want legacy v1", request.ProviderExecutorKey)
	}
	current, err := readCanonicalV19HerdrInterruptCurrent(context.Background(), fixture.Home, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	sessionKey, err := parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey)
	if err != nil {
		t.Fatal(err)
	}
	restartedBase := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, original.request, sessionKey)
	restartedBase.startTarget(original.request.Spec)
	restarted := &canonicalV19HerdrInterruptFakeClient{
		canonicalV19HerdrLaunchFakeClient: restartedBase,
		targetPID:                         executorKey.ProcessID,
		targetAlive:                       true,
	}

	observe := func(client *canonicalV19HerdrInterruptFakeClient) canonicalV19HerdrInterruptObservation {
		return observeCanonicalV19HerdrInterrupt(context.Background(), current, executorKey, sessionKey, client,
			func(int) (bool, error) { return client.targetAlive, client.livenessErr })
	}
	before, after := observe(original), observe(restarted)
	if before.State != canonicalV19HerdrInterruptRunning || before.EvidenceDigest != after.EvidenceDigest {
		t.Fatalf("restart/replacement observations = %#v / %#v, want indistinguishable running evidence", before, after)
	}
}

func TestHerdrInterruptAcceptanceWithoutCessationIsOnlyDiagnostic(t *testing.T) {
	fixture, request, executorKey, client := canonicalV19HerdrInterruptFixture(t, "operation-herdr-interrupt-accepted-running")
	client.mutateOnSend = false
	if err := client.PaneSendKeys(executorKey.PaneID, "ctrl+c"); err != nil {
		t.Fatal(err)
	}
	current, err := readCanonicalV19HerdrInterruptCurrent(context.Background(), fixture.Home, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	sessionKey, err := parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey)
	if err != nil {
		t.Fatal(err)
	}
	observed := observeCanonicalV19HerdrInterrupt(context.Background(), current, executorKey, sessionKey, client,
		func(int) (bool, error) { return client.targetAlive, client.livenessErr })
	if observed.State != canonicalV19HerdrInterruptRunning {
		t.Fatalf("accepted interrupt without cessation = %q, want running diagnostic", observed.State)
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
		interruptOperationID:              operationID,
		targetPID:                         target.PID,
		targetAlive:                       true,
		mutateOnSend:                      true,
	}
	return fixture, request, executorKey, client
}
