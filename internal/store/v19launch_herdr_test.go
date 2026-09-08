package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/launch"
)

func TestReconcileCanonicalV19HerdrLaunchEstablishesExactExecutor(t *testing.T) {
	fixture, request := canonicalV19HerdrLaunchFixture(t, "operation-herdr-launch-success")
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request)
	client.requireSubmittedAtRun = true
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(sessionName string) canonicalV19HerdrLaunchClient {
			if sessionName != "hand-fleet-1" {
				t.Fatalf("Herdr session = %q, want hand-fleet-1", sessionName)
			}
			return client
		},
		now: func() time.Time { return time.Date(2026, 9, 8, 2, 30, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("reconcile Herdr Launch = %q, %v", state, err)
	}
	if client.runCalls != 1 {
		t.Fatalf("pane run calls = %d, want 1", client.runCalls)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var operationState, providerKey string
	if err := db.sql.QueryRow(`SELECT o.state,e.provider_executor_key
		FROM external_operation o JOIN executor_binding e ON e.launch_operation_id=o.id
		WHERE o.id=?`, request.OperationID).Scan(&operationState, &providerKey); err != nil {
		t.Fatal(err)
	}
	if operationState != "succeeded" {
		t.Fatalf("Launch state = %q, want succeeded", operationState)
	}
	key, err := parseCanonicalV19HerdrExecutorKey(providerKey)
	if err != nil {
		t.Fatal(err)
	}
	if key.SessionName != "hand-fleet-1" || key.WorkspaceID != "w-session" || key.TabID != "w-session:t1" ||
		key.PaneID != "w-session:p1" || key.ProcessGroupID != 22 || key.ProcessID != 22 {
		t.Fatalf("provider Executor key = %#v", key)
	}

	state, err = reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" || client.runCalls != 1 {
		t.Fatalf("terminal rerun = %q, %v, run calls %d", state, err, client.runCalls)
	}
}

func TestReconcileSubmittedCanonicalV19HerdrLaunchObservesExactExecutorWithoutReplay(t *testing.T) {
	fixture, request := canonicalV19HerdrLaunchFixture(t, "operation-herdr-launch-submitted-exact")
	if _, err := SubmitCanonicalV19Launch(context.Background(), fixture.Home, request.OperationID,
		"2026-09-08T02:29:00Z", "launch-submitted-before-crash"); err != nil {
		t.Fatal(err)
	}
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request)
	client.startExecutor()
	client.runCalls = 0
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(string) canonicalV19HerdrLaunchClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 2, 31, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("submitted exact recovery = %q, %v", state, err)
	}
	if client.runCalls != 0 {
		t.Fatalf("pane run calls = %d, want no replay", client.runCalls)
	}
}

func TestReconcileSubmittedCanonicalV19HerdrLaunchIdleBecomesUncertainWithoutReplay(t *testing.T) {
	fixture, request := canonicalV19HerdrLaunchFixture(t, "operation-herdr-launch-submitted-idle")
	if _, err := SubmitCanonicalV19Launch(context.Background(), fixture.Home, request.OperationID,
		"2026-09-08T02:29:00Z", "launch-submitted-before-crash"); err != nil {
		t.Fatal(err)
	}
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request)
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(string) canonicalV19HerdrLaunchClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 2, 32, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("submitted idle recovery = %q, %v, want uncertain diagnostic", state, err)
	}
	if client.runCalls != 0 {
		t.Fatalf("pane run calls = %d, want no replay", client.runCalls)
	}

	state, err = reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil || client.runCalls != 0 {
		t.Fatalf("uncertain idle recovery = %q, %v, run calls %d", state, err, client.runCalls)
	}
}

func TestReconcileCanonicalV19HerdrLaunchProcessNotStartedIsNoEffect(t *testing.T) {
	fixture, request := canonicalV19HerdrLaunchFixture(t, "operation-herdr-launch-no-effect")
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request)
	client.runErr = &herdr.ExecError{Started: false, Err: errors.New("Herdr executable could not start")}
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(string) canonicalV19HerdrLaunchClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 2, 33, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "no-effect" || err == nil {
		t.Fatalf("process-not-started reconcile = %q, %v, want no-effect diagnostic", state, err)
	}
	if client.runCalls != 1 {
		t.Fatalf("pane run calls = %d, want 1", client.runCalls)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var executors int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM executor_binding WHERE launch_operation_id=?`, request.OperationID).Scan(&executors); err != nil {
		t.Fatal(err)
	}
	if executors != 0 {
		t.Fatalf("ExecutorBinding rows = %d, want 0", executors)
	}
}

func TestReconcileCanonicalV19HerdrLaunchSecretRefRemainsPrepared(t *testing.T) {
	environment := map[string]CanonicalV19LaunchEnvironmentValue{
		"HAND_ROLE": {ValueKind: "literal", ValueMaterial: "worker", ValueDigest: "digest-role-worker"},
		"TOKEN":     {ValueKind: "secret-ref", ValueMaterial: "secret://worker/token", ValueDigest: "digest-secret"},
	}
	fixture, request := canonicalV19HerdrLaunchFixtureWithEnvironment(t, "operation-herdr-launch-secret", environment)
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request)
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(string) canonicalV19HerdrLaunchClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 2, 34, 0, 0, time.UTC) },
	}
	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "prepared" || err == nil {
		t.Fatalf("secret-ref reconcile = %q, %v, want prepared refusal", state, err)
	}
	if client.runCalls != 0 {
		t.Fatalf("pane run calls = %d, want 0", client.runCalls)
	}

	ro, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	var persistedState string
	if err := ro.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, request.OperationID).Scan(&persistedState); err != nil {
		t.Fatal(err)
	}
	if persistedState != "prepared" {
		t.Fatalf("persisted Launch state = %q, want prepared", persistedState)
	}
}

func TestCanonicalV19HerdrExecutorKeyRoundTripsEscapedIDs(t *testing.T) {
	want := canonicalV19HerdrExecutorKey{
		SessionName: "hand-fleet:one", WorkspaceID: "w/a", TabID: "w/a:t 1", PaneID: "w/a:p?1",
		ProcessGroupID: 77, ProcessID: 88,
	}
	encoded, err := encodeCanonicalV19HerdrExecutorKey(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseCanonicalV19HerdrExecutorKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("provider Executor key round trip = %#v, want %#v", got, want)
	}
}

func canonicalV19HerdrLaunchFixture(
	t *testing.T,
	operationID string,
) (canonicalV19WorktreeCreateTestFixture, CanonicalV19LaunchRequest) {
	t.Helper()
	return canonicalV19HerdrLaunchFixtureWithEnvironment(t, operationID, map[string]CanonicalV19LaunchEnvironmentValue{
		"HAND_ROLE": {ValueKind: "literal", ValueMaterial: "worker", ValueDigest: "digest-role-worker"},
	})
}

func canonicalV19HerdrLaunchFixtureWithEnvironment(
	t *testing.T,
	operationID string,
	environment map[string]CanonicalV19LaunchEnvironmentValue,
) (canonicalV19WorktreeCreateTestFixture, CanonicalV19LaunchRequest) {
	t.Helper()
	fixture, session := canonicalV19HerdrSessionAcquireFixture(t, "operation-session-for-"+operationID)
	providerKey, err := encodeCanonicalV19HerdrSessionProviderKey(canonicalV19HerdrSessionProviderKey{
		SessionName: "hand-fleet-1", WorkspaceID: "w-session", TabID: "w-session:t1", PaneID: "w-session:p1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19SessionBinding(context.Background(), fixture.Home, CanonicalV19SessionBindingEvidence{
		OperationID: session.OperationID, ProviderSessionKey: providerKey,
		EstablishedAt: "2026-09-08T02:25:00Z", EvidenceDigest: "session-established-launch-fixture",
	}); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var cwd string
	if err := db.sql.QueryRow(`SELECT path FROM attempt_worktree_binding WHERE id=?`, session.WorktreeBindingID).Scan(&cwd); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	request, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, CanonicalV19LaunchPrepareInput{
		OperationID: operationID, OperationKey: "operation-key-" + operationID,
		AttemptID: session.AttemptID, SessionBindingID: session.BindingID, BindingID: "executor-binding-1",
		Spec: CanonicalV19LaunchSpec{
			Executable: "worker-bin", Arguments: []string{"--mode", "execute"},
			Environment: environment,
			Cwd:         cwd,
		},
		CreatedAt: "2026-09-08T02:28:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture, request
}

type canonicalV19HerdrLaunchFakeClient struct {
	t                     *testing.T
	home                  string
	request               CanonicalV19LaunchRequest
	workspace             herdr.Workspace
	tab                   herdr.Tab
	pane                  herdr.Pane
	processInfo           herdr.ProcessInfo
	runCalls              int
	runErr                error
	requireSubmittedAtRun bool
}

func newCanonicalV19HerdrLaunchFakeClient(
	t *testing.T,
	home string,
	request CanonicalV19LaunchRequest,
) *canonicalV19HerdrLaunchFakeClient {
	t.Helper()
	return &canonicalV19HerdrLaunchFakeClient{
		t: t, home: home, request: request,
		workspace: herdr.Workspace{WorkspaceID: "w-session", Label: canonicalV19HerdrSessionWorkspaceLabel(request.SessionBindingID), TabCount: 1},
		tab:       herdr.Tab{TabID: "w-session:t1", WorkspaceID: "w-session", Label: "1"},
		pane:      herdr.Pane{PaneID: "w-session:p1", TabID: "w-session:t1", WorkspaceID: "w-session", Cwd: request.Spec.Cwd},
		processInfo: herdr.ProcessInfo{
			PaneID: "w-session:p1", ShellPID: 11, ForegroundProcessGroupID: 11,
			ForegroundProcesses: []herdr.Process{{PID: 11, Name: "bash", Argv: []string{"bash"}, Cwd: request.Spec.Cwd}},
		},
	}
}

func (f *canonicalV19HerdrLaunchFakeClient) ObserveSession(context.Context) herdr.SessionObservation {
	return herdr.SessionObservation{Name: "hand-fleet-1", State: herdr.SessionRunningCompatible}
}

func (f *canonicalV19HerdrLaunchFakeClient) WorkspaceListContext(context.Context) ([]herdr.Workspace, error) {
	return []herdr.Workspace{f.workspace}, nil
}

func (f *canonicalV19HerdrLaunchFakeClient) TabList(string) ([]herdr.Tab, error) {
	return []herdr.Tab{f.tab}, nil
}

func (f *canonicalV19HerdrLaunchFakeClient) PaneGetContext(context.Context, string) (herdr.Pane, error) {
	return f.pane, nil
}

func (f *canonicalV19HerdrLaunchFakeClient) PaneProcessInfo(string) (herdr.ProcessInfo, error) {
	return f.processInfo, nil
}

func (f *canonicalV19HerdrLaunchFakeClient) PaneRunCanonicalSpec(_ string, spec launch.LaunchSpec) error {
	f.runCalls++
	if f.requireSubmittedAtRun {
		db, err := openReadOnly(f.home)
		if err != nil {
			f.t.Fatal(err)
		}
		var state string
		if err := db.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, f.request.OperationID).Scan(&state); err != nil {
			_ = db.Close()
			f.t.Fatal(err)
		}
		_ = db.Close()
		if state != "submitted" {
			f.t.Fatalf("state at first provider mutation = %q, want submitted", state)
		}
	}
	if spec.Executable != f.request.Spec.Executable || len(spec.Args) != len(f.request.Spec.Arguments) ||
		spec.Cwd != f.request.Spec.Cwd || spec.Env["HAND_ROLE"] != "worker" {
		f.t.Fatalf("provider LaunchSpec = %#v", spec)
	}
	if f.runErr != nil {
		return f.runErr
	}
	f.startExecutor()
	return nil
}

func (f *canonicalV19HerdrLaunchFakeClient) startExecutor() {
	f.processInfo = herdr.ProcessInfo{
		PaneID: "w-session:p1", ShellPID: 11, ForegroundProcessGroupID: 22,
		ForegroundProcesses: []herdr.Process{
			{PID: 11, Name: "bash", Argv: []string{"bash"}, Cwd: f.request.Spec.Cwd},
			{PID: 22, Name: "worker-bin", Argv: []string{"worker-bin", "--mode", "execute"}, Cwd: f.request.Spec.Cwd},
		},
	}
}
