package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
)

func TestReconcileCanonicalV19HerdrSessionAcquireEstablishesExactBinding(t *testing.T) {
	fixture, request := canonicalV19HerdrSessionAcquireFixture(t, "operation-herdr-session-success")
	client := newCanonicalV19HerdrSessionAcquireFakeClient(t, fixture.Home, request.OperationID)
	deps := canonicalV19HerdrSessionAcquireDeps{
		clientFor: func(sessionName string) canonicalV19HerdrSessionClient {
			if sessionName != "hand-fleet-1" {
				t.Fatalf("Herdr session = %q, want hand-fleet-1", sessionName)
			}
			client.sessionName = sessionName
			return client
		},
		now: func() time.Time { return time.Date(2026, 9, 6, 17, 0, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionAcquire(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("reconcile Herdr SessionAcquire = %q, %v", state, err)
	}
	if client.createCalls != 1 {
		t.Fatalf("workspace create calls = %d, want 1", client.createCalls)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var operationState, providerKey string
	if err := db.sql.QueryRow(`SELECT o.state,s.provider_session_key
		FROM external_operation o JOIN session_binding s ON s.acquire_operation_id=o.id
		WHERE o.id=?`, request.OperationID).Scan(&operationState, &providerKey); err != nil {
		t.Fatal(err)
	}
	if operationState != "succeeded" {
		t.Fatalf("operation state = %q, want succeeded", operationState)
	}
	key, err := parseCanonicalV19HerdrSessionProviderKey(providerKey)
	if err != nil {
		t.Fatal(err)
	}
	if key.SessionName != "hand-fleet-1" || key.WorkspaceID != "w-session" || key.TabID != "w-session:t1" || key.PaneID != "w-session:p1" {
		t.Fatalf("provider Session key = %#v", key)
	}
}

func TestReconcileCanonicalV19HerdrSessionAcquireSubmitsBeforeFirstMutation(t *testing.T) {
	fixture, request := canonicalV19HerdrSessionAcquireFixture(t, "operation-herdr-session-boundary")
	client := newCanonicalV19HerdrSessionAcquireFakeClient(t, fixture.Home, request.OperationID)
	client.requireSubmittedAtCreate = true
	deps := canonicalV19HerdrSessionAcquireDeps{
		clientFor: func(string) canonicalV19HerdrSessionClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 6, 17, 1, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionAcquire(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("reconcile Herdr SessionAcquire boundary = %q, %v", state, err)
	}
	if client.createCalls != 1 {
		t.Fatalf("workspace create calls = %d, want 1", client.createCalls)
	}
}

func TestReconcileCanonicalV19HerdrSessionAcquireLostResponseBecomesUncertain(t *testing.T) {
	fixture, request := canonicalV19HerdrSessionAcquireFixture(t, "operation-herdr-session-lost-response")
	client := newCanonicalV19HerdrSessionAcquireFakeClient(t, fixture.Home, request.OperationID)
	client.createErr = errors.New("provider response lost after workspace creation")
	client.mutateOnCreateError = true
	deps := canonicalV19HerdrSessionAcquireDeps{
		clientFor: func(string) canonicalV19HerdrSessionClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 6, 17, 2, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionAcquire(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("lost response reconcile = %q, %v, want uncertain error", state, err)
	}
	if client.createCalls != 1 {
		t.Fatalf("workspace create calls = %d, want 1", client.createCalls)
	}

	state, err = reconcileCanonicalV19HerdrSessionAcquire(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("uncertain recovery = %q, %v, want uncertain error", state, err)
	}
	if client.createCalls != 1 {
		t.Fatalf("workspace create calls after recovery = %d, want no blind retry", client.createCalls)
	}
}

func TestReconcileSubmittedCanonicalV19HerdrSessionAcquireDoesNotBlindRetryResidualWorkspace(t *testing.T) {
	fixture, request := canonicalV19HerdrSessionAcquireFixture(t, "operation-herdr-session-submitted")
	if _, err := SubmitCanonicalV19SessionAcquire(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T16:59:00Z", "submitted-before-crash"); err != nil {
		t.Fatal(err)
	}
	client := newCanonicalV19HerdrSessionAcquireFakeClient(t, fixture.Home, request.OperationID)
	client.addCreatedWorkspace(canonicalV19HerdrSessionWorkspaceLabel(request.BindingID), request.WorktreeBindingID)
	client.createCalls = 0
	deps := canonicalV19HerdrSessionAcquireDeps{
		clientFor: func(string) canonicalV19HerdrSessionClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 6, 17, 3, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionAcquire(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("submitted residual recovery = %q, %v, want uncertain error", state, err)
	}
	if client.createCalls != 0 {
		t.Fatalf("workspace create calls = %d, want 0", client.createCalls)
	}
}

func TestReconcileCanonicalV19HerdrSessionAcquirePositiveAbsenceIsNoEffect(t *testing.T) {
	fixture, request := canonicalV19HerdrSessionAcquireFixture(t, "operation-herdr-session-no-effect")
	client := newCanonicalV19HerdrSessionAcquireFakeClient(t, fixture.Home, request.OperationID)
	client.createErr = errors.New("provider rejected workspace create")
	deps := canonicalV19HerdrSessionAcquireDeps{
		clientFor: func(string) canonicalV19HerdrSessionClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 6, 17, 4, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionAcquire(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "no-effect" || err == nil {
		t.Fatalf("no-effect reconcile = %q, %v, want no-effect diagnostic", state, err)
	}
	if client.createCalls != 1 {
		t.Fatalf("workspace create calls = %d, want 1", client.createCalls)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var bindings int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM session_binding WHERE acquire_operation_id=?`, request.OperationID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if bindings != 0 {
		t.Fatalf("SessionBinding rows = %d, want 0", bindings)
	}
}

func TestCanonicalV19HerdrSessionProviderKeyRoundTripsEscapedIDs(t *testing.T) {
	want := canonicalV19HerdrSessionProviderKey{
		SessionName: "hand-fleet:one", WorkspaceID: "w/a", TabID: "w/a:t 1", PaneID: "w/a:p?1",
	}
	encoded, err := encodeCanonicalV19HerdrSessionProviderKey(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseCanonicalV19HerdrSessionProviderKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("provider key round trip = %#v, want %#v", got, want)
	}
}

func canonicalV19HerdrSessionAcquireFixture(
	t *testing.T,
	operationID string,
) (canonicalV19WorktreeCreateTestFixture, CanonicalV19SessionAcquireRequest) {
	t.Helper()
	base := canonicalV19AttemptWriterFixture(t)
	attempt := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	attempt.SessionAdapterRef = canonicalV19HerdrSessionAdapterRef
	if _, err := CreateCanonicalV19Attempt(context.Background(), base.Home, attempt); err != nil {
		t.Fatal(err)
	}
	fixture := canonicalV19WorktreeCreateTestFixture{Home: base.Home}
	createInput := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-create", "binding-1")
	worktree, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, createInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19WorktreeBinding(context.Background(), fixture.Home,
		canonicalV19WorktreeBindingEvidence(worktree, "worktree-physical-herdr")); err != nil {
		t.Fatal(err)
	}
	input := canonicalV19SessionAcquirePrepareInput(worktree, operationID, "session-binding-1")
	request, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, request
}

type canonicalV19HerdrSessionAcquireFakeClient struct {
	t                        *testing.T
	home                     string
	operationID              string
	sessionName              string
	workspaces               []herdr.Workspace
	tabs                     map[string][]herdr.Tab
	panes                    map[string]herdr.Pane
	createCalls              int
	createErr                error
	mutateOnCreateError      bool
	requireSubmittedAtCreate bool
}

func newCanonicalV19HerdrSessionAcquireFakeClient(
	t *testing.T,
	home string,
	operationID string,
) *canonicalV19HerdrSessionAcquireFakeClient {
	t.Helper()
	return &canonicalV19HerdrSessionAcquireFakeClient{
		t: t, home: home, operationID: operationID, sessionName: "hand-fleet-1",
		tabs: make(map[string][]herdr.Tab), panes: make(map[string]herdr.Pane),
	}
}

func (f *canonicalV19HerdrSessionAcquireFakeClient) ObserveSession(context.Context) herdr.SessionObservation {
	return herdr.SessionObservation{Name: f.sessionName, State: herdr.SessionRunningCompatible}
}

func (f *canonicalV19HerdrSessionAcquireFakeClient) WorkspaceListContext(context.Context) ([]herdr.Workspace, error) {
	return append([]herdr.Workspace(nil), f.workspaces...), nil
}

func (f *canonicalV19HerdrSessionAcquireFakeClient) WorkspaceCreate(
	cwd string,
	_ map[string]string,
	label string,
) (herdr.Workspace, herdr.Tab, herdr.Pane, error) {
	f.createCalls++
	if f.requireSubmittedAtCreate {
		db, err := openReadOnly(f.home)
		if err != nil {
			f.t.Fatal(err)
		}
		var state string
		if err := db.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, f.operationID).Scan(&state); err != nil {
			_ = db.Close()
			f.t.Fatal(err)
		}
		_ = db.Close()
		if state != "submitted" {
			f.t.Fatalf("state at first provider mutation = %q, want submitted", state)
		}
	}
	if f.createErr != nil {
		if f.mutateOnCreateError {
			f.addCreatedWorkspace(label, cwd)
		}
		return herdr.Workspace{}, herdr.Tab{}, herdr.Pane{}, f.createErr
	}
	return f.addCreatedWorkspace(label, cwd)
}

func (f *canonicalV19HerdrSessionAcquireFakeClient) addCreatedWorkspace(
	label string,
	cwd string,
) (herdr.Workspace, herdr.Tab, herdr.Pane) {
	workspace := herdr.Workspace{WorkspaceID: "w-session", Label: label, TabCount: 1}
	tab := herdr.Tab{TabID: "w-session:t1", WorkspaceID: workspace.WorkspaceID, Label: "1"}
	pane := herdr.Pane{PaneID: "w-session:p1", TabID: tab.TabID, WorkspaceID: workspace.WorkspaceID, Cwd: cwd}
	f.workspaces = []herdr.Workspace{workspace}
	f.tabs[workspace.WorkspaceID] = []herdr.Tab{tab}
	f.panes[pane.PaneID] = pane
	return workspace, tab, pane
}

func (f *canonicalV19HerdrSessionAcquireFakeClient) TabList(workspaceID string) ([]herdr.Tab, error) {
	tabs, ok := f.tabs[workspaceID]
	if !ok {
		return nil, fmt.Errorf("%w: workspace %s", herdr.ErrNotFound, workspaceID)
	}
	return append([]herdr.Tab(nil), tabs...), nil
}

func (f *canonicalV19HerdrSessionAcquireFakeClient) PaneGetContext(_ context.Context, paneID string) (herdr.Pane, error) {
	pane, ok := f.panes[paneID]
	if !ok {
		return herdr.Pane{}, fmt.Errorf("%w: pane %s", herdr.ErrNotFound, paneID)
	}
	return pane, nil
}
