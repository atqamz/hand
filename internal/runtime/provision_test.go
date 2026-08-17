package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

func TestProvisionFailureAfterWorktreeRecordClearsOnlyReturnedEvidence(t *testing.T) {
	home, attempt := provisioningFixture(t)
	phaseErr := errors.New("stop after worktree phase")
	returned := false
	runtime := &Runtime{deps: dependencies{
		worktree: worktreeDependencies{
			get: func(string, string) (worktree.Lease, error) {
				return worktree.Lease{Path: "/tmp/hand-wt", ID: "lease-1"}, nil
			},
			returnWorktree: func(string, bool) error {
				returned = true
				return nil
			},
			checkCollision: func(string, worktree.Lease, string) (string, error) {
				return "", nil
			},
		},
		phase: func(phase lifecyclePhase) error {
			if phase == phaseWorktreeRecorded {
				return phaseErr
			}
			return nil
		},
	}}

	_, err := runtime.provision(context.Background(), provisioningRequest{
		home:        home,
		projectName: "demo",
		clonePath:   filepath.Join(home, "projects", "demo"),
		briefPath:   filepath.Join(home, "data", "task-1", "brief.md"),
		attempt:     attempt,
	})
	if !errors.Is(err, phaseErr) {
		t.Fatalf("provision() = %v, want %v", err, phaseErr)
	}
	if !returned {
		t.Fatal("provision() did not return the acquired worktree")
	}

	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := *history.ActiveAttempt
	if got.Worktree != "" || got.LeaseID != "" || got.Herdr.PaneID != "" || got.LaunchSubmittedAt != "" || got.LaunchConfirmedAt != "" {
		t.Fatalf("attempt after successful cleanup = %+v, want no resource or launch evidence", got)
	}
	if got.Lifecycle != state.AttemptProvisioning {
		t.Fatalf("attempt lifecycle = %q, want provisioning", got.Lifecycle)
	}
}

func TestProvisionResumeRefusesChangedLeaseBeforeHerdrCreation(t *testing.T) {
	home, attempt := provisioningFixture(t)
	attempt.Worktree = "/tmp/hand-wt"
	attempt.LeaseID = "lease-old"
	fake := &provisionHerdr{}
	runtime := testProvisionRuntime(fake, func(lifecyclePhase) error { return nil })
	runtime.deps.worktree.observeLease = func(path, leaseID string) worktree.LeaseObservation {
		if path != attempt.Worktree || leaseID != attempt.LeaseID {
			t.Fatalf("observeLease(%q, %q), want persisted lease", path, leaseID)
		}
		return worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "lease-new"}
	}

	_, err := runtime.provision(context.Background(), provisioningRequest{
		home: home, projectName: "demo", clonePath: filepath.Join(home, "projects", "demo"),
		briefPath: filepath.Join(home, "data", "task-1", "brief.md"), attempt: attempt, resumeExisting: true,
	})
	if err == nil {
		t.Fatal("provision() succeeded through changed resumed lease")
	}
	if fake.createdWorkspace {
		t.Fatal("provision() created a Herdr workspace after lease mismatch")
	}
}

func TestProvisionFailurePreservesWorktreeEvidenceWhenReturnFails(t *testing.T) {
	home, attempt := provisioningFixture(t)
	phaseErr := errors.New("stop after worktree phase")
	returnErr := errors.New("treehouse unavailable")
	runtime := &Runtime{deps: dependencies{
		worktree: worktreeDependencies{
			get: func(string, string) (worktree.Lease, error) {
				return worktree.Lease{Path: "/tmp/hand-wt", ID: "lease-1"}, nil
			},
			returnWorktree: func(string, bool) error { return returnErr },
			checkCollision: func(string, worktree.Lease, string) (string, error) { return "", nil },
		},
		phase: func(phase lifecyclePhase) error {
			if phase == phaseWorktreeRecorded {
				return phaseErr
			}
			return nil
		},
	}}

	_, err := runtime.provision(context.Background(), provisioningRequest{
		home:        home,
		projectName: "demo",
		clonePath:   filepath.Join(home, "projects", "demo"),
		briefPath:   filepath.Join(home, "data", "task-1", "brief.md"),
		attempt:     attempt,
	})
	if !errors.Is(err, phaseErr) || !errors.Is(err, returnErr) {
		t.Fatalf("provision() = %v, want phase and return errors", err)
	}

	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := *history.ActiveAttempt
	if got.Worktree != "/tmp/hand-wt" || got.LeaseID != "lease-1" {
		t.Fatalf("attempt after failed cleanup = %+v, want worktree ownership preserved", got)
	}
}

func TestProvisionFailureAfterHerdrRecordPreservesAttemptAttribution(t *testing.T) {
	home, attempt := provisioningFixture(t)
	phaseErr := errors.New("stop after Herdr phase")
	fake := &provisionHerdr{}
	returned := false
	runtime := testProvisionRuntime(fake, func(phase lifecyclePhase) error {
		if phase == phaseHerdrRecorded {
			return phaseErr
		}
		return nil
	})
	runtime.deps.worktree.returnWorktree = func(string, bool) error { returned = true; return nil }

	_, err := runtime.provision(context.Background(), provisioningRequest{
		home: home, projectName: "demo", clonePath: filepath.Join(home, "projects", "demo"),
		briefPath: filepath.Join(home, "data", "task-1", "brief.md"), attempt: attempt,
	})
	if !errors.Is(err, phaseErr) || !returned {
		t.Fatalf("provision() = %v, returned=%v, want phase failure and worktree cleanup", err, returned)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := *history.ActiveAttempt
	if got.Herdr.PaneID != "" || got.Worktree != "" {
		t.Fatalf("attempt after Herdr cleanup = %+v, want no stale ownership", got)
	}
	if fake.closedWorkspace != "ws-1" {
		t.Fatalf("closed workspace = %q, want the owned workspace", fake.closedWorkspace)
	}
}

func TestProvisionFailureAfterLaunchSubmissionKeepsSubmissionEvidence(t *testing.T) {
	home, attempt := provisioningFixture(t)
	phaseErr := errors.New("stop after launch submission")
	fake := &provisionHerdr{}
	runtime := testProvisionRuntime(fake, func(phase lifecyclePhase) error {
		if phase == phaseLaunchSubmitted {
			return phaseErr
		}
		return nil
	})

	_, err := runtime.provision(context.Background(), provisioningRequest{
		home: home, projectName: "demo", clonePath: filepath.Join(home, "projects", "demo"),
		briefPath: filepath.Join(home, "data", "task-1", "brief.md"), attempt: attempt,
	})
	if !errors.Is(err, phaseErr) {
		t.Fatalf("provision() = %v, want %v", err, phaseErr)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := *history.ActiveAttempt
	if got.LaunchSubmittedAt == "" || got.LaunchConfirmedAt != "" || got.Lifecycle != state.AttemptProvisioning {
		t.Fatalf("attempt after submission failure = %+v, want submitted-only provisioning evidence", got)
	}
}

func TestProvisionFailureAfterLaunchConfirmationKeepsConfirmationEvidence(t *testing.T) {
	home, attempt := provisioningFixture(t)
	phaseErr := errors.New("stop after launch confirmation")
	fake := &provisionHerdr{}
	runtime := testProvisionRuntime(fake, func(phase lifecyclePhase) error {
		if phase == phaseLaunchConfirmed {
			return phaseErr
		}
		return nil
	})

	_, err := runtime.provision(context.Background(), provisioningRequest{
		home: home, projectName: "demo", clonePath: filepath.Join(home, "projects", "demo"),
		briefPath: filepath.Join(home, "data", "task-1", "brief.md"), attempt: attempt,
	})
	if !errors.Is(err, phaseErr) {
		t.Fatalf("provision() = %v, want %v", err, phaseErr)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := *history.ActiveAttempt
	if got.LaunchSubmittedAt == "" || got.LaunchConfirmedAt == "" || got.Lifecycle != state.AttemptProvisioning {
		t.Fatalf("attempt after confirmation failure = %+v, want confirmed provisioning evidence", got)
	}
}

type provisionHerdr struct {
	closedWorkspace  string
	createdWorkspace bool
	closedTab        string
	tabCloseErr      error
	tabs             []herdr.Tab
	paneStatus       herdr.Status
}

func (f *provisionHerdr) FindWorkspaceByLabel(string) (herdr.Workspace, bool, error) {
	return herdr.Workspace{WorkspaceID: "ws-1"}, true, nil
}

func (f *provisionHerdr) WorkspaceList() ([]herdr.Workspace, error) { return nil, nil }

func (f *provisionHerdr) WorkspaceCreate(string, string) (herdr.Workspace, herdr.Tab, herdr.Pane, error) {
	f.createdWorkspace = true
	return herdr.Workspace{WorkspaceID: "ws-1"}, herdr.Tab{TabID: "tab-1"}, herdr.Pane{PaneID: "pane-1"}, nil
}

func (f *provisionHerdr) WorkspaceClose(id string) error { f.closedWorkspace = id; return nil }

func (f *provisionHerdr) TabList(string) ([]herdr.Tab, error) {
	if f.tabs != nil {
		return f.tabs, nil
	}
	return []herdr.Tab{{TabID: "tab-1"}}, nil
}

func (f *provisionHerdr) TabCreate(string, string, string) (herdr.Tab, herdr.Pane, error) {
	return herdr.Tab{TabID: "tab-1"}, herdr.Pane{PaneID: "pane-1"}, nil
}

func (f *provisionHerdr) TabRename(string, string) error { return nil }

func (f *provisionHerdr) TabClose(id string) error {
	f.closedTab = id
	return f.tabCloseErr
}

func (f *provisionHerdr) PaneGet(string) (herdr.Pane, error) {
	status := f.paneStatus
	if status == "" {
		status = herdr.StatusDone
	}
	return herdr.Pane{PaneID: "pane-1", Agent: "claude", AgentStatus: status}, nil
}

func (f *provisionHerdr) PaneRun(string, string) error { return nil }

func (f *provisionHerdr) PaneSendKeys(string, ...string) error { return nil }

func (f *provisionHerdr) PaneRead(string, int) (string, error) { return "ready", nil }

func testProvisionRuntime(client herdrClient, phase func(lifecyclePhase) error) *Runtime {
	return &Runtime{deps: dependencies{
		now:   func() time.Time { return time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC) },
		herdr: func() herdrClient { return client },
		worktree: worktreeDependencies{
			get: func(path, holder string) (worktree.Lease, error) {
				return worktree.Lease{Path: filepath.Join(path, "leased"), ID: "lease-1"}, nil
			},
			observeLease: func(string, string) worktree.LeaseObservation {
				return worktree.LeaseObservation{State: worktree.LeaseExact}
			},
			returnWorktree: func(string, bool) error { return nil },
			checkCollision: func(string, worktree.Lease, string) (string, error) { return "", nil },
		},
		buildHarness:     func(string, harness.Options) (string, error) { return "launch", nil },
		confirmLaunch:    func(herdrClient, string, string) error { return nil },
		phase:            phase,
		appendCompletion: completion.Append,
	}}
}

func provisioningFixture(t *testing.T) (string, state.Attempt) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "brief.md"), []byte("brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://example.com/demo.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{
		ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md", Lifecycle: state.TaskOpen,
	}, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	return home, attempt
}
