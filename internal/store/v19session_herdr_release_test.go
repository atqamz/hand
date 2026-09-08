package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
)

func TestReconcileCanonicalV19HerdrSessionReleaseSucceedsAndReruns(t *testing.T) {
	fixture, request, key := canonicalV19HerdrSessionReleaseFixture(t, "operation-herdr-session-release-success")
	client := newCanonicalV19HerdrSessionReleaseFakeClient(t, fixture.Home, request.OperationID, request.SessionBindingID, key)
	client.panes[key.PaneID] = herdr.Pane{
		PaneID: key.PaneID, TabID: key.TabID, WorkspaceID: key.WorkspaceID,
		Cwd: client.worktreePath, Agent: "codex", AgentStatus: "done",
	}
	deps := canonicalV19HerdrSessionReleaseDeps{
		clientFor: func(sessionName string) canonicalV19HerdrSessionReleaseClient {
			if sessionName != key.SessionName {
				t.Fatalf("Herdr session = %q, want %q", sessionName, key.SessionName)
			}
			return client
		},
		now: func() time.Time { return time.Date(2026, 9, 8, 1, 10, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionRelease(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("reconcile Herdr SessionRelease = %q, %v", state, err)
	}
	if client.closeCalls != 1 {
		t.Fatalf("workspace close calls = %d, want 1", client.closeCalls)
	}
	canonicalV19HerdrSessionReleaseAssertSucceeded(t, fixture.Home, request)

	state, err = reconcileCanonicalV19HerdrSessionRelease(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("terminal rerun = %q, %v", state, err)
	}
	if client.closeCalls != 1 {
		t.Fatalf("workspace close calls after terminal rerun = %d, want 1", client.closeCalls)
	}
}

func TestReconcileCanonicalV19HerdrSessionReleaseSubmitsBeforeFirstMutation(t *testing.T) {
	fixture, request, key := canonicalV19HerdrSessionReleaseFixture(t, "operation-herdr-session-release-boundary")
	client := newCanonicalV19HerdrSessionReleaseFakeClient(t, fixture.Home, request.OperationID, request.SessionBindingID, key)
	client.requireSubmittedAtClose = true
	deps := canonicalV19HerdrSessionReleaseDeps{
		clientFor: func(string) canonicalV19HerdrSessionReleaseClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 1, 11, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionRelease(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("reconcile Herdr SessionRelease boundary = %q, %v", state, err)
	}
	if client.closeCalls != 1 {
		t.Fatalf("workspace close calls = %d, want 1", client.closeCalls)
	}
}

func TestReconcileSubmittedCanonicalV19HerdrSessionReleaseDoesNotBlindRetry(t *testing.T) {
	fixture, request, key := canonicalV19HerdrSessionReleaseFixture(t, "operation-herdr-session-release-submitted")
	if _, err := SubmitCanonicalV19SessionRelease(context.Background(), fixture.Home, request.OperationID,
		"2026-09-08T01:05:00Z", "session-release-submitted-before-crash"); err != nil {
		t.Fatal(err)
	}
	client := newCanonicalV19HerdrSessionReleaseFakeClient(t, fixture.Home, request.OperationID, request.SessionBindingID, key)
	deps := canonicalV19HerdrSessionReleaseDeps{
		clientFor: func(string) canonicalV19HerdrSessionReleaseClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 1, 12, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionRelease(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("submitted recovery = %q, %v, want uncertain error", state, err)
	}
	if client.closeCalls != 0 {
		t.Fatalf("workspace close calls = %d, want 0", client.closeCalls)
	}
	state, err = reconcileCanonicalV19HerdrSessionRelease(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "uncertain" || err == nil {
		t.Fatalf("uncertain recovery = %q, %v, want uncertain error", state, err)
	}
	if client.closeCalls != 0 {
		t.Fatalf("workspace close calls after uncertain recovery = %d, want 0", client.closeCalls)
	}
}

func TestReconcileCanonicalV19HerdrSessionReleaseLostResponseAfterCloseSucceeds(t *testing.T) {
	fixture, request, key := canonicalV19HerdrSessionReleaseFixture(t, "operation-herdr-session-release-lost-response")
	client := newCanonicalV19HerdrSessionReleaseFakeClient(t, fixture.Home, request.OperationID, request.SessionBindingID, key)
	client.closeErr = errors.New("provider response lost after workspace close")
	client.mutateOnCloseError = true
	deps := canonicalV19HerdrSessionReleaseDeps{
		clientFor: func(string) canonicalV19HerdrSessionReleaseClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 1, 13, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionRelease(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("lost-response release = %q, %v", state, err)
	}
	if client.closeCalls != 1 {
		t.Fatalf("workspace close calls = %d, want 1", client.closeCalls)
	}
	canonicalV19HerdrSessionReleaseAssertSucceeded(t, fixture.Home, request)
}

func TestReconcileCanonicalV19HerdrSessionReleaseProcessNotStartedIsNoEffect(t *testing.T) {
	fixture, request, key := canonicalV19HerdrSessionReleaseFixture(t, "operation-herdr-session-release-no-effect")
	client := newCanonicalV19HerdrSessionReleaseFakeClient(t, fixture.Home, request.OperationID, request.SessionBindingID, key)
	client.closeErr = &herdr.ExecError{Started: false, Err: errors.New("herdr executable did not start")}
	deps := canonicalV19HerdrSessionReleaseDeps{
		clientFor: func(string) canonicalV19HerdrSessionReleaseClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 1, 14, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionRelease(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "no-effect" || err == nil {
		t.Fatalf("process-not-started release = %q, %v, want no-effect diagnostic", state, err)
	}
	if client.closeCalls != 1 {
		t.Fatalf("workspace close calls = %d, want 1", client.closeCalls)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var releases int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM session_binding_release WHERE session_binding_id=?`, request.SessionBindingID).Scan(&releases); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if releases != 0 {
		t.Fatalf("SessionBinding release rows = %d, want 0", releases)
	}

	replacement := CanonicalV19SessionReleasePrepareInput{
		OperationID: "operation-herdr-session-release-replacement", OperationKey: "operation-key-herdr-session-release-replacement",
		SessionBindingID: request.SessionBindingID, CreatedAt: "2026-09-08T01:15:00Z",
	}
	if _, err := PrepareCanonicalV19SessionRelease(context.Background(), fixture.Home, replacement); err != nil {
		t.Fatalf("replacement release after no-effect: %v", err)
	}
}

func TestReconcilePreparedCanonicalV19HerdrSessionReleaseRefusesProviderMismatch(t *testing.T) {
	fixture, request, key := canonicalV19HerdrSessionReleaseFixture(t, "operation-herdr-session-release-mismatch")
	client := newCanonicalV19HerdrSessionReleaseFakeClient(t, fixture.Home, request.OperationID, request.SessionBindingID, key)
	pane := client.panes[key.PaneID]
	pane.Cwd = t.TempDir()
	client.panes[key.PaneID] = pane
	deps := canonicalV19HerdrSessionReleaseDeps{
		clientFor: func(string) canonicalV19HerdrSessionReleaseClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 1, 16, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionRelease(context.Background(), fixture.Home, request.OperationID, deps)
	if state != "prepared" || err == nil {
		t.Fatalf("provider mismatch release = %q, %v, want prepared error", state, err)
	}
	if client.closeCalls != 0 {
		t.Fatalf("workspace close calls = %d, want 0", client.closeCalls)
	}
}

func TestReconcilePreparedCanonicalV19HerdrSessionReleasePositiveAbsenceSucceeds(t *testing.T) {
	fixture, request, key := canonicalV19HerdrSessionReleaseFixture(t, "operation-herdr-session-release-absent")
	client := newCanonicalV19HerdrSessionReleaseFakeClient(t, fixture.Home, request.OperationID, request.SessionBindingID, key)
	client.removeWorkspace(key)
	deps := canonicalV19HerdrSessionReleaseDeps{
		clientFor: func(string) canonicalV19HerdrSessionReleaseClient { return client },
		now:       func() time.Time { return time.Date(2026, 9, 8, 1, 17, 0, 0, time.UTC) },
	}

	state, err := reconcileCanonicalV19HerdrSessionRelease(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil || state != "succeeded" {
		t.Fatalf("positive-absence release = %q, %v", state, err)
	}
	if client.closeCalls != 0 {
		t.Fatalf("workspace close calls = %d, want 0", client.closeCalls)
	}
	canonicalV19HerdrSessionReleaseAssertSucceeded(t, fixture.Home, request)
}

func canonicalV19HerdrSessionReleaseFixture(
	t *testing.T,
	releaseOperationID string,
) (canonicalV19WorktreeCreateTestFixture, CanonicalV19SessionReleaseRequest, canonicalV19HerdrSessionProviderKey) {
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
		canonicalV19WorktreeBindingEvidence(worktree, "worktree-physical-herdr-release")); err != nil {
		t.Fatal(err)
	}
	key := canonicalV19HerdrSessionProviderKey{
		SessionName: herdr.SessionName("fleet-1"),
		WorkspaceID: "w-session-release", TabID: "w-session-release:t1", PaneID: "w-session-release:p1",
	}
	providerKey, err := encodeCanonicalV19HerdrSessionProviderKey(key)
	if err != nil {
		t.Fatal(err)
	}
	acquireInput := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-release-fixture", "session-binding-1")
	acquireInput.RequestedProviderSessionKey = providerKey
	acquire, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, acquireInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19SessionBinding(context.Background(), fixture.Home, CanonicalV19SessionBindingEvidence{
		OperationID: acquire.OperationID, ProviderSessionKey: providerKey,
		EstablishedAt: "2026-09-08T01:00:00Z", EvidenceDigest: "session-binding-herdr-release-fixture",
	}); err != nil {
		t.Fatal(err)
	}
	release, err := PrepareCanonicalV19SessionRelease(context.Background(), fixture.Home, CanonicalV19SessionReleasePrepareInput{
		OperationID: releaseOperationID, OperationKey: "operation-key-" + releaseOperationID,
		SessionBindingID: acquire.BindingID, CreatedAt: "2026-09-08T01:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture, release, key
}

type canonicalV19HerdrSessionReleaseFakeClient struct {
	t                       *testing.T
	home                    string
	operationID             string
	sessionName             string
	sessionBindingID        string
	worktreePath            string
	workspaces              []herdr.Workspace
	tabs                    map[string][]herdr.Tab
	panes                   map[string]herdr.Pane
	closeCalls              int
	closeErr                error
	mutateOnCloseError      bool
	requireSubmittedAtClose bool
}

func newCanonicalV19HerdrSessionReleaseFakeClient(
	t *testing.T,
	home string,
	operationID string,
	sessionBindingID string,
	key canonicalV19HerdrSessionProviderKey,
) *canonicalV19HerdrSessionReleaseFakeClient {
	t.Helper()
	client := &canonicalV19HerdrSessionReleaseFakeClient{
		t: t, home: home, operationID: operationID, sessionName: key.SessionName,
		sessionBindingID: sessionBindingID, tabs: make(map[string][]herdr.Tab), panes: make(map[string]herdr.Pane),
	}
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT b.path FROM session_binding s JOIN attempt_worktree_binding b ON b.id=s.worktree_binding_id WHERE s.id=?`,
		sessionBindingID).Scan(&client.worktreePath); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	workspace := herdr.Workspace{WorkspaceID: key.WorkspaceID, Label: canonicalV19HerdrSessionWorkspaceLabel(sessionBindingID), TabCount: 1}
	tab := herdr.Tab{TabID: key.TabID, WorkspaceID: key.WorkspaceID, Label: "1"}
	pane := herdr.Pane{PaneID: key.PaneID, TabID: key.TabID, WorkspaceID: key.WorkspaceID, Cwd: client.worktreePath}
	client.workspaces = []herdr.Workspace{workspace}
	client.tabs[key.WorkspaceID] = []herdr.Tab{tab}
	client.panes[key.PaneID] = pane
	return client
}

func (f *canonicalV19HerdrSessionReleaseFakeClient) ObserveSession(context.Context) herdr.SessionObservation {
	return herdr.SessionObservation{Name: f.sessionName, State: herdr.SessionRunningCompatible}
}

func (f *canonicalV19HerdrSessionReleaseFakeClient) WorkspaceListContext(context.Context) ([]herdr.Workspace, error) {
	return append([]herdr.Workspace(nil), f.workspaces...), nil
}

func (f *canonicalV19HerdrSessionReleaseFakeClient) WorkspaceClose(workspaceID string) error {
	f.closeCalls++
	if f.requireSubmittedAtClose {
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
	key := canonicalV19HerdrSessionProviderKey{WorkspaceID: workspaceID}
	for _, workspace := range f.workspaces {
		if workspace.WorkspaceID == workspaceID {
			key.TabID = f.tabs[workspaceID][0].TabID
			for paneID, pane := range f.panes {
				if pane.WorkspaceID == workspaceID {
					key.PaneID = paneID
					break
				}
			}
			break
		}
	}
	if f.closeErr != nil {
		if f.mutateOnCloseError {
			f.removeWorkspace(key)
		}
		return f.closeErr
	}
	f.removeWorkspace(key)
	return nil
}

func (f *canonicalV19HerdrSessionReleaseFakeClient) TabList(workspaceID string) ([]herdr.Tab, error) {
	tabs, ok := f.tabs[workspaceID]
	if !ok {
		return nil, fmt.Errorf("%w: workspace %s", herdr.ErrNotFound, workspaceID)
	}
	return append([]herdr.Tab(nil), tabs...), nil
}

func (f *canonicalV19HerdrSessionReleaseFakeClient) PaneGetContext(_ context.Context, paneID string) (herdr.Pane, error) {
	pane, ok := f.panes[paneID]
	if !ok {
		return herdr.Pane{}, fmt.Errorf("%w: pane %s", herdr.ErrNotFound, paneID)
	}
	return pane, nil
}

func (f *canonicalV19HerdrSessionReleaseFakeClient) removeWorkspace(key canonicalV19HerdrSessionProviderKey) {
	kept := f.workspaces[:0]
	for _, workspace := range f.workspaces {
		if workspace.WorkspaceID != key.WorkspaceID {
			kept = append(kept, workspace)
		}
	}
	f.workspaces = kept
	delete(f.tabs, key.WorkspaceID)
	if key.PaneID != "" {
		delete(f.panes, key.PaneID)
	}
}

func canonicalV19HerdrSessionReleaseAssertSucceeded(t *testing.T, home string, request CanonicalV19SessionReleaseRequest) {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state, releaseOperationID string
	if err := db.sql.QueryRow(`SELECT o.state,r.release_operation_id
		FROM external_operation o JOIN session_binding_release r ON r.release_operation_id=o.id
		WHERE r.session_binding_id=?`, request.SessionBindingID).Scan(&state, &releaseOperationID); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || releaseOperationID != request.OperationID {
		t.Fatalf("SessionRelease terminal state = %q/%q, want succeeded/%q", state, releaseOperationID, request.OperationID)
	}
}
