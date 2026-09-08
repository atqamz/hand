package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/launch"
)

func TestReconcileCanonicalV19HerdrLaunchSucceedsWithExactStructuredSpecAndReruns(t *testing.T) {
	fixture, request, key := canonicalV19HerdrLaunchFixture(t, "operation-herdr-launch-success", canonicalV19HerdrLiteralEnvironment())
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request, key)
	client.requireSubmittedAtRun = true
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(sessionName string) canonicalV19HerdrLaunchClient {
			if sessionName != key.SessionName {
				t.Fatalf("Herdr session = %q, want %q", sessionName, key.SessionName)
			}
			return client
		},
		now: func() time.Time { return time.Date(2026, 9, 8, 3, 15, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("reconcile Herdr Launch = %q, %v", state, err)
	}
	if client.runCalls != 1 {
		t.Fatalf("pane run calls = %d, want 1", client.runCalls)
	}
	wantSpec := launch.LaunchSpec{
		Executable: request.Spec.Executable,
		Args:       append([]string(nil), request.Spec.Arguments...),
		Env:        map[string]string{"HAND_ROLE": "worker", "TOKEN": "literal-token"},
		Cwd:        request.Spec.Cwd,
	}
	if !reflect.DeepEqual(client.lastSpec, wantSpec) {
		t.Fatalf("provider LaunchSpec = %#v, want %#v", client.lastSpec, wantSpec)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var operationState, providerKey string
	if err := db.sql.QueryRow(`SELECT o.state,e.provider_executor_key
		FROM external_operation o JOIN executor_binding e ON e.launch_operation_id=o.id
		WHERE o.id=?`, request.OperationID).Scan(&operationState, &providerKey); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if operationState != "succeeded" {
		t.Fatalf("operation state = %q, want succeeded", operationState)
	}
	executorKey, err := parseCanonicalV19HerdrExecutorProviderKey(providerKey)
	if err != nil {
		t.Fatal(err)
	}
	if executorKey.SessionName != key.SessionName || executorKey.WorkspaceID != key.WorkspaceID ||
		executorKey.TabID != key.TabID || executorKey.PaneID != key.PaneID ||
		executorKey.ProcessGroup != canonicalV19HerdrLaunchTestProcessGroup ||
		executorKey.ProcessID != canonicalV19HerdrLaunchTestProcessID || len(executorKey.ProcessDigest) != 64 {
		t.Fatalf("provider Executor key = %#v", executorKey)
	}

	state, err = reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("terminal rerun = %q, %v", state, err)
	}
	if client.runCalls != 1 {
		t.Fatalf("pane run calls after terminal rerun = %d, want 1", client.runCalls)
	}
}

func TestReconcileSubmittedCanonicalV19HerdrLaunchObservesExistingTargetWithoutReplay(t *testing.T) {
	fixture, request, key := canonicalV19HerdrLaunchFixture(t, "operation-herdr-launch-submitted-running", canonicalV19HerdrLiteralEnvironment())
	if _, err := SubmitCanonicalV19Launch(context.Background(), fixture.Home, request.OperationID,
		"2026-09-08T03:10:00Z", "launch-submitted-before-crash"); err != nil {
		t.Fatal(err)
	}
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request, key)
	client.startTarget(request.Spec)
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(string) canonicalV19HerdrLaunchClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 3, 16, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("submitted running recovery = %q, %v", state, err)
	}
	if client.runCalls != 0 {
		t.Fatalf("pane run calls = %d, want 0", client.runCalls)
	}
}

func TestReconcileSubmittedCanonicalV19HerdrLaunchDoesNotBlindReplay(t *testing.T) {
	fixture, request, key := canonicalV19HerdrLaunchFixture(t, "operation-herdr-launch-submitted-ready", canonicalV19HerdrLiteralEnvironment())
	if _, err := SubmitCanonicalV19Launch(context.Background(), fixture.Home, request.OperationID,
		"2026-09-08T03:10:00Z", "launch-submitted-before-crash"); err != nil {
		t.Fatal(err)
	}
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request, key)
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(string) canonicalV19HerdrLaunchClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 3, 17, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("submitted recovery = %q, %v, want uncertain error", state, err)
	}
	if client.runCalls != 0 {
		t.Fatalf("pane run calls = %d, want 0", client.runCalls)
	}
	state, err = reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("uncertain recovery = %q, %v, want uncertain error", state, err)
	}
	if client.runCalls != 0 {
		t.Fatalf("pane run calls after uncertain recovery = %d, want 0", client.runCalls)
	}
}

func TestReconcileCanonicalV19HerdrLaunchLostResponseWithExactProcessSucceeds(t *testing.T) {
	fixture, request, key := canonicalV19HerdrLaunchFixture(t, "operation-herdr-launch-lost-response", canonicalV19HerdrLiteralEnvironment())
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request, key)
	client.runErr = errors.New("provider response lost after pane run")
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(string) canonicalV19HerdrLaunchClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 3, 18, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("lost-response Launch = %q, %v", state, err)
	}
	if client.runCalls != 1 {
		t.Fatalf("pane run calls = %d, want 1", client.runCalls)
	}
}

func TestReconcileCanonicalV19HerdrLaunchProcessNotStartedIsNoEffect(t *testing.T) {
	fixture, request, key := canonicalV19HerdrLaunchFixture(t, "operation-herdr-launch-no-effect", canonicalV19HerdrLiteralEnvironment())
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request, key)
	client.mutateOnRun = false
	client.runErr = &herdr.ExecError{Started: false, Err: errors.New("Herdr executable did not start")}
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(string) canonicalV19HerdrLaunchClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 3, 19, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "no-effect" || err == nil {
		t.Fatalf("process-not-started Launch = %q, %v, want no-effect diagnostic", state, err)
	}
	if client.runCalls != 1 {
		t.Fatalf("pane run calls = %d, want 1", client.runCalls)
	}
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var bindings int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM executor_binding WHERE launch_operation_id=?`, request.OperationID).Scan(&bindings); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if bindings != 0 {
		t.Fatalf("ExecutorBinding rows = %d, want 0", bindings)
	}
}

func TestReconcilePreparedCanonicalV19HerdrLaunchRejectsSecretRefBeforeMutation(t *testing.T) {
	environment := canonicalV19HerdrLiteralEnvironment()
	environment["TOKEN"] = CanonicalV19LaunchEnvironmentValue{
		ValueKind: "secret-ref", ValueMaterial: "secret://worker/token", ValueDigest: "digest-secret-value",
	}
	fixture, request, key := canonicalV19HerdrLaunchFixture(t, "operation-herdr-launch-secret-ref", environment)
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request, key)
	clientForCalls := 0
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(string) canonicalV19HerdrLaunchClient {
			clientForCalls++
			return client
		},
		now: func() time.Time { return time.Date(2026, 9, 8, 3, 20, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "prepared" || err == nil {
		t.Fatalf("secret-ref Launch = %q, %v, want prepared error", state, err)
	}
	if clientForCalls != 0 || client.runCalls != 0 {
		t.Fatalf("provider calls before secret resolution = client %d, run %d, want 0/0", clientForCalls, client.runCalls)
	}
}

func TestReconcilePreparedCanonicalV19HerdrLaunchRefusesProviderMismatch(t *testing.T) {
	fixture, request, key := canonicalV19HerdrLaunchFixture(t, "operation-herdr-launch-mismatch", canonicalV19HerdrLiteralEnvironment())
	client := newCanonicalV19HerdrLaunchFakeClient(t, fixture.Home, request, key)
	pane := client.panes[key.PaneID]
	pane.Cwd = t.TempDir()
	client.panes[key.PaneID] = pane
	deps := canonicalV19HerdrLaunchDeps{
		clientFor: func(string) canonicalV19HerdrLaunchClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 3, 21, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "prepared" || err == nil {
		t.Fatalf("provider mismatch Launch = %q, %v, want prepared error", state, err)
	}
	if client.runCalls != 0 {
		t.Fatalf("pane run calls = %d, want 0", client.runCalls)
	}
}

func canonicalV19HerdrLiteralEnvironment() map[string]CanonicalV19LaunchEnvironmentValue {
	return map[string]CanonicalV19LaunchEnvironmentValue{
		"HAND_ROLE": {ValueKind: "literal", ValueMaterial: "worker", ValueDigest: "digest-role-worker"},
		"TOKEN":     {ValueKind: "literal", ValueMaterial: "literal-token", ValueDigest: "digest-literal-token"},
	}
}

func canonicalV19HerdrLaunchFixture(
	t *testing.T,
	launchOperationID string,
	environment map[string]CanonicalV19LaunchEnvironmentValue,
) (canonicalV19WorktreeCreateTestFixture, CanonicalV19LaunchRequest, canonicalV19HerdrSessionProviderKey) {
	t.Helper()
	base := canonicalV19AttemptWriterFixture(t)
	attempt := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	attempt.SessionAdapterRef = canonicalV19HerdrSessionAdapterRef
	if _, err := CreateCanonicalV19Attempt(context.Background(), base.Home, attempt); err != nil {
		t.Fatal(err)
	}
	fixture := canonicalV19WorktreeCreateTestFixture(base)
	createInput := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-create", "binding-1")
	worktree, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, createInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19WorktreeBinding(context.Background(), fixture.Home,
		canonicalV19WorktreeBindingEvidence(worktree, "worktree-physical-herdr-launch")); err != nil {
		t.Fatal(err)
	}
	key := canonicalV19HerdrSessionProviderKey{
		SessionName: herdr.SessionName("fleet-1"),
		WorkspaceID: "w-launch", TabID: "w-launch:t1", PaneID: "w-launch:p1",
	}
	providerSessionKey, err := encodeCanonicalV19HerdrSessionProviderKey(key)
	if err != nil {
		t.Fatal(err)
	}
	acquireInput := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-launch-fixture", "session-binding-1")
	acquireInput.RequestedProviderSessionKey = providerSessionKey
	session, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, acquireInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19SessionBinding(context.Background(), fixture.Home, CanonicalV19SessionBindingEvidence{
		OperationID: session.OperationID, ProviderSessionKey: providerSessionKey,
		EstablishedAt: "2026-09-08T03:00:00Z", EvidenceDigest: "session-binding-herdr-launch-fixture",
	}); err != nil {
		t.Fatal(err)
	}
	launchInput := canonicalV19LaunchPrepareInput(worktree, session, launchOperationID, "executor-binding-1")
	launchInput.Spec.Environment = environment
	launchInput.CreatedAt = "2026-09-08T03:05:00Z"
	request, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, launchInput)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, request, key
}

const (
	canonicalV19HerdrLaunchTestShellPID     = 4100
	canonicalV19HerdrLaunchTestProcessGroup = 4200
	canonicalV19HerdrLaunchTestProcessID    = 4300
)

type canonicalV19HerdrLaunchFakeClient struct {
	t                     *testing.T
	home                  string
	request               CanonicalV19LaunchRequest
	sessionName           string
	workspaces            []herdr.Workspace
	tabs                  map[string][]herdr.Tab
	panes                 map[string]herdr.Pane
	processInfo           herdr.ProcessInfo
	runCalls              int
	runErr                error
	mutateOnRun           bool
	requireSubmittedAtRun bool
	lastSpec              launch.LaunchSpec
}

func newCanonicalV19HerdrLaunchFakeClient(
	t *testing.T,
	home string,
	request CanonicalV19LaunchRequest,
	key canonicalV19HerdrSessionProviderKey,
) *canonicalV19HerdrLaunchFakeClient {
	t.Helper()
	workspace := herdr.Workspace{
		WorkspaceID: key.WorkspaceID,
		Label:       canonicalV19HerdrSessionWorkspaceLabel(request.SessionBindingID),
		TabCount:    1,
	}
	tab := herdr.Tab{TabID: key.TabID, WorkspaceID: key.WorkspaceID, Label: "1"}
	pane := herdr.Pane{PaneID: key.PaneID, TabID: key.TabID, WorkspaceID: key.WorkspaceID, Cwd: request.Spec.Cwd}
	return &canonicalV19HerdrLaunchFakeClient{
		t: t, home: home, request: request, sessionName: key.SessionName,
		workspaces: []herdr.Workspace{workspace},
		tabs:       map[string][]herdr.Tab{key.WorkspaceID: {tab}},
		panes:      map[string]herdr.Pane{key.PaneID: pane},
		processInfo: herdr.ProcessInfo{
			PaneID: key.PaneID, ShellPID: canonicalV19HerdrLaunchTestShellPID,
			ForegroundProcessGroupID: canonicalV19HerdrLaunchTestShellPID,
			ForegroundProcesses: []herdr.Process{{
				PID: canonicalV19HerdrLaunchTestShellPID, Name: "shell", Argv: []string{"shell"}, Cwd: request.Spec.Cwd,
			}},
		},
		mutateOnRun: true,
	}
}

func (f *canonicalV19HerdrLaunchFakeClient) ObserveSession(context.Context) herdr.SessionObservation {
	return herdr.SessionObservation{Name: f.sessionName, State: herdr.SessionRunningCompatible}
}

func (f *canonicalV19HerdrLaunchFakeClient) WorkspaceListContext(context.Context) ([]herdr.Workspace, error) {
	return append([]herdr.Workspace(nil), f.workspaces...), nil
}

func (f *canonicalV19HerdrLaunchFakeClient) TabList(workspaceID string) ([]herdr.Tab, error) {
	tabs, ok := f.tabs[workspaceID]
	if !ok {
		return nil, fmt.Errorf("%w: workspace %s", herdr.ErrNotFound, workspaceID)
	}
	return append([]herdr.Tab(nil), tabs...), nil
}

func (f *canonicalV19HerdrLaunchFakeClient) PaneGetContext(_ context.Context, paneID string) (herdr.Pane, error) {
	pane, ok := f.panes[paneID]
	if !ok {
		return herdr.Pane{}, fmt.Errorf("%w: pane %s", herdr.ErrNotFound, paneID)
	}
	return pane, nil
}

func (f *canonicalV19HerdrLaunchFakeClient) PaneProcessInfo(paneID string) (herdr.ProcessInfo, error) {
	if paneID != f.processInfo.PaneID {
		return herdr.ProcessInfo{}, fmt.Errorf("%w: pane %s", herdr.ErrNotFound, paneID)
	}
	return f.processInfo, nil
}

func (f *canonicalV19HerdrLaunchFakeClient) PaneRunExactSpec(paneID string, spec launch.LaunchSpec) error {
	f.runCalls++
	f.lastSpec = spec.Clone()
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
	if f.mutateOnRun {
		f.startTarget(f.request.Spec)
	}
	return f.runErr
}

func (f *canonicalV19HerdrLaunchFakeClient) startTarget(spec CanonicalV19LaunchSpec) {
	argv := append([]string{spec.Executable}, spec.Arguments...)
	f.processInfo.ForegroundProcessGroupID = canonicalV19HerdrLaunchTestProcessGroup
	f.processInfo.ForegroundProcesses = []herdr.Process{
		{PID: canonicalV19HerdrLaunchTestShellPID, Name: "shell", Argv: []string{"shell"}, Cwd: spec.Cwd},
		{PID: canonicalV19HerdrLaunchTestProcessID, Name: spec.Executable, Argv: argv, Cwd: spec.Cwd},
	}
	pane := f.panes[f.processInfo.PaneID]
	pane.Agent = "worker"
	pane.AgentStatus = herdr.StatusWorking
	f.panes[f.processInfo.PaneID] = pane
}
