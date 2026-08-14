package runtime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

func TestPreflightBriefAllowsLegacyStandardAndDeep(t *testing.T) {
	called := false
	r := &Runtime{deps: dependencies{
		projectBaseCommit: func(string) (string, error) {
			called = true
			return strings.Repeat("a", 40), nil
		},
	}}

	for _, class := range []brief.ExecutionClass{"", brief.ExecutionClassStandard, brief.ExecutionClassDeep} {
		if err := r.preflightBrief(brief.Declaration{ExecutionClass: class, PlannedAgainst: "HEAD"}, "/clone"); err != nil {
			t.Fatalf("preflightBrief(%q) = %v, want nil", class, err)
		}
	}
	if called {
		t.Fatal("preflightBrief resolved a project base for a non-mechanical brief")
	}
}

func TestClassifyTierErrorOnlyClassifiesBriefValidation(t *testing.T) {
	internalErr := errors.New("brief storage unavailable")
	if got := classifyTierError(internalErr); got != internalErr {
		t.Fatalf("classifyTierError(internal) = %v, want original error", got)
	}
	validationErr := &brief.ValidationError{Field: "execution_class", Value: "cheap", Want: "mechanical, standard, or deep"}
	classified := classifyTierError(validationErr)
	var runtimeErr *Error
	if !errors.As(classified, &runtimeErr) || runtimeErr.Kind != ErrorPrecondition {
		t.Fatalf("classifyTierError(validation) = %v, want precondition", classified)
	}
}

func TestPreflightBriefAllowsExactMechanicalPlan(t *testing.T) {
	commit := strings.Repeat("a", 40)
	r := &Runtime{deps: dependencies{
		projectBaseCommit: func(clonePath string) (string, error) {
			if clonePath != "/clone" {
				t.Fatalf("clone path = %q, want /clone", clonePath)
			}
			return commit, nil
		},
	}}

	if err := r.preflightBrief(brief.Declaration{
		ExecutionClass: brief.ExecutionClassMechanical,
		PlannedAgainst: commit,
	}, "/clone"); err != nil {
		t.Fatalf("preflightBrief() = %v, want nil", err)
	}
}

func TestPreflightBriefRejectsMechanicalPlanWithoutProvenance(t *testing.T) {
	called := false
	r := &Runtime{deps: dependencies{
		projectBaseCommit: func(string) (string, error) {
			called = true
			return strings.Repeat("a", 40), nil
		},
	}}

	err := r.preflightBrief(brief.Declaration{ExecutionClass: brief.ExecutionClassMechanical}, "/clone")
	assertPreconditionError(t, err)
	if !strings.Contains(err.Error(), "mechanical") || !strings.Contains(err.Error(), "planned_against") {
		t.Fatalf("error = %q, want mechanical planned_against guidance", err)
	}
	if called {
		t.Fatal("preflightBrief resolved a base before checking mechanical provenance")
	}
}

func TestPreflightBriefRejectsStaleMechanicalPlan(t *testing.T) {
	planned := strings.Repeat("a", 40)
	current := strings.Repeat("b", 40)
	r := &Runtime{deps: dependencies{
		projectBaseCommit: func(string) (string, error) { return current, nil },
	}}

	err := r.preflightBrief(brief.Declaration{
		ExecutionClass: brief.ExecutionClassMechanical,
		PlannedAgainst: planned,
	}, "/clone")
	assertPreconditionError(t, err)
	for _, want := range []string{"mechanical", planned, current, "re-check", "rewrite"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
}

func TestPreflightBriefClassifiesBaseResolutionFailureAsPrecondition(t *testing.T) {
	lookupErr := errors.New("default branch unavailable")
	r := &Runtime{deps: dependencies{
		projectBaseCommit: func(string) (string, error) { return "", lookupErr },
	}}

	err := r.preflightBrief(brief.Declaration{
		ExecutionClass: brief.ExecutionClassMechanical,
		PlannedAgainst: strings.Repeat("a", 40),
	}, "/clone")
	assertPreconditionError(t, err)
	if !errors.Is(err, lookupErr) || !strings.Contains(err.Error(), "project base") {
		t.Fatalf("error = %q, want project-base lookup context and cause", err)
	}
}

func TestProjectBaseCommitResolvesLocalDefaultBranchCommit(t *testing.T) {
	clonePath := filepath.Join(t.TempDir(), "clone")
	initRuntimeGitRepo(t, clonePath)
	initial := gitOutput(t, clonePath, "rev-parse", "refs/heads/main")

	if err := os.WriteFile(filepath.Join(clonePath, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRuntimeGit(t, clonePath, "add", "second.txt")
	runRuntimeGit(t, clonePath, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "second")
	current := gitOutput(t, clonePath, "rev-parse", "refs/heads/main")
	runRuntimeGit(t, clonePath, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	got, err := projectBaseCommit(clonePath)
	if err != nil {
		t.Fatal(err)
	}
	if got != current || got == initial || len(got) != 40 {
		t.Fatalf("projectBaseCommit() = %q, want current local main commit %q", got, current)
	}
}

func TestProjectBaseCommitDoesNotQueryRemoteToResolveBranch(t *testing.T) {
	clonePath := filepath.Join(t.TempDir(), "clone")
	initRuntimeGitRepo(t, clonePath)
	runRuntimeGit(t, clonePath, "remote", "add", "origin", "ssh://example.invalid/review/repo.git")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "remote-show-called")
	fakeDir := t.TempDir()
	fakeGit := filepath.Join(fakeDir, "git")
	script := "#!/bin/sh\nif [ \"$1\" = remote ] && [ \"$2\" = show ] && [ \"$3\" = origin ]; then\n  echo queried > \"$HAND_GIT_QUERY_MARKER\"\n  exit 1\nfi\nexec \"$HAND_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_GIT_QUERY_MARKER", marker)
	t.Setenv("HAND_REAL_GIT", realGit)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	want := gitOutput(t, clonePath, "rev-parse", "refs/heads/main")
	got, err := projectBaseCommit(clonePath)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("projectBaseCommit() = %q, want %q", got, want)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("remote show marker exists with err %v, want no remote query", err)
	}
}

func TestSpawnMechanicalPlanStopsBeforeAttemptAndExternalProvisioning(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: mechanical\nplanned_against: "+strings.Repeat("a", 40)+"\n---\nbrief\n")
	calls := &executionPlanCalls{}
	r := executionPlanRuntime(t, calls, func(string) (string, error) { return strings.Repeat("b", 40), nil })

	_, err := r.Spawn(context.Background(), SpawnRequest{Home: home, ID: "task-1", Project: "demo", Harness: "claude"})
	assertPreconditionError(t, err)
	if !strings.Contains(err.Error(), "mechanical plan is stale") {
		t.Fatalf("error = %q, want stale mechanical plan", err)
	}
	assertNoProvisioningSideEffects(t, home, calls)
	if _, err := state.ReadHistory(home, "task-1"); !errors.Is(err, state.ErrTaskNotFound) {
		t.Fatalf("history after refused Spawn = %v, want no Task/Attempt", err)
	}
}

func TestSpawnMechanicalPlanRejectsWorktreeHeadDriftBeforeHerdr(t *testing.T) {
	planned := strings.Repeat("a", 40)
	acquired := strings.Repeat("b", 40)
	home := executionPlanHome(t, "---\nexecution_class: mechanical\nplanned_against: "+planned+"\n---\nbrief\n")
	calls := &executionPlanCalls{}
	r := executionPlanRuntime(t, calls, func(string) (string, error) { return planned, nil })
	r.deps.worktree.headCommit = func(string) (string, error) { return acquired, nil }

	_, err := r.Spawn(context.Background(), SpawnRequest{Home: home, ID: "task-1", Project: "demo", Harness: "claude"})
	assertPreconditionError(t, err)
	for _, want := range []string{planned, acquired, "returned without launching"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
	if calls.worktreeReturns != 1 || calls.herdrGets != 0 || calls.harnessBuilds != 0 {
		t.Fatalf("provisioning calls = %+v, want one safe return and no Herdr or harness", calls)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Worktree != "" {
		t.Fatalf("attempt after rejected worktree = %+v, want no worktree evidence", history.ActiveAttempt)
	}
}

func TestSpawnMechanicalPlanRequiresProvenanceBeforeAttempt(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: mechanical\n---\nbrief\n")
	calls := &executionPlanCalls{}
	r := executionPlanRuntime(t, calls, func(string) (string, error) {
		calls.baseLookups++
		return strings.Repeat("a", 40), nil
	})

	_, err := r.Spawn(context.Background(), SpawnRequest{Home: home, ID: "task-1", Project: "demo", Harness: "claude"})
	assertPreconditionError(t, err)
	if calls.baseLookups != 0 {
		t.Fatalf("base lookups = %d, want no lookup without planned_against", calls.baseLookups)
	}
	assertNoProvisioningSideEffects(t, home, calls)
	if _, err := state.ReadHistory(home, "task-1"); !errors.Is(err, state.ErrTaskNotFound) {
		t.Fatalf("history after refused Spawn = %v, want no Task/Attempt", err)
	}
}

func TestSpawnStandardAndDeepDoNotApplyMechanicalExactMatch(t *testing.T) {
	for _, class := range []brief.ExecutionClass{brief.ExecutionClassStandard, brief.ExecutionClassDeep} {
		t.Run(string(class), func(t *testing.T) {
			home := executionPlanHome(t, "---\nexecution_class: "+string(class)+"\nplanned_against: "+strings.Repeat("a", 40)+"\n---\nbrief\n")
			calls := &executionPlanCalls{}
			r := executionPlanRuntime(t, calls, func(string) (string, error) {
				calls.baseLookups++
				return strings.Repeat("b", 40), nil
			})

			if _, err := r.Spawn(context.Background(), SpawnRequest{Home: home, ID: "task-1", Project: "demo", Harness: "claude"}); err != nil {
				t.Fatalf("Spawn() = %v, want stale provenance accepted for %s", err, class)
			}
			if calls.baseLookups != 0 {
				t.Fatalf("base lookups = %d, want no lookup for %s", calls.baseLookups, class)
			}
			if calls.worktreeGets != 1 || calls.herdrGets != 1 || calls.harnessBuilds != 1 {
				t.Fatalf("provisioning calls = %+v, want one complete provisioning path", calls)
			}
		})
	}
}

func TestSpawnLegacyTierCreatesAttemptBeforeWaitingForProjectLock(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: standard\n---\nbrief\n")
	calls := &executionPlanCalls{}
	r := executionPlanRuntime(t, calls, func(string) (string, error) { return strings.Repeat("a", 40), nil })
	releaseProject, err := state.Lock(home, "project:demo")
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			releaseProject()
		}
	}()

	spawnDone := make(chan error, 1)
	go func() {
		_, err := r.Spawn(context.Background(), SpawnRequest{Home: home, ID: "task-1", Project: "demo", Harness: "claude"})
		spawnDone <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		history, readErr := state.ReadHistory(home, "task-1")
		if readErr == nil && history.ActiveAttempt != nil {
			released = true
			releaseProject()
			if err := <-spawnDone; err != nil {
				t.Fatalf("Spawn() = %v, want success after project lock release", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("legacy Spawn did not create its provisioning Attempt before waiting for project lock")
}

func TestReopenMechanicalPlanStopsBeforeTaskMutationAndProvisioning(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: mechanical\nplanned_against: "+strings.Repeat("a", 40)+"\n---\nbrief\n")
	createTerminalExecutionTask(t, home)
	calls := &executionPlanCalls{}
	r := executionPlanRuntime(t, calls, func(string) (string, error) { return strings.Repeat("b", 40), nil })

	_, err := r.Reopen(context.Background(), ReopenRequest{Home: home, ID: "task-1", Harness: "claude"})
	assertPreconditionError(t, err)
	assertNoProvisioningSideEffects(t, home, calls)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskTerminal || len(history.Attempts) != 1 {
		t.Fatalf("history after stale reopen = %+v, want unchanged terminal task and one attempt", history)
	}
}

func TestReopenLegacyTierCreatesAttemptBeforeWaitingForProjectLock(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: standard\n---\nbrief\n")
	createTerminalExecutionTask(t, home)
	calls := &executionPlanCalls{}
	r := executionPlanRuntime(t, calls, func(string) (string, error) { return strings.Repeat("a", 40), nil })
	releaseProject, err := state.Lock(home, "project:demo")
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			releaseProject()
		}
	}()

	reopenDone := make(chan error, 1)
	go func() {
		_, err := r.Reopen(context.Background(), ReopenRequest{Home: home, ID: "task-1", Harness: "claude"})
		reopenDone <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		history, readErr := state.ReadHistory(home, "task-1")
		if readErr == nil && len(history.Attempts) == 2 && history.ActiveAttempt != nil {
			released = true
			releaseProject()
			if err := <-reopenDone; err != nil {
				t.Fatalf("Reopen() = %v, want success after project lock release", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("legacy Reopen did not create its provisioning Attempt before waiting for project lock")
}

func TestPromoteMechanicalPlanStopsBeforeTaskMutationAndProvisioning(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: mechanical\nplanned_against: "+strings.Repeat("a", 40)+"\n---\nbrief\n")
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout, Lifecycle: state.TaskOpen}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/old/worktree",
		Herdr: state.Herdr{WorkspaceID: "old-ws", TabID: "old-tab", PaneID: "old-pane"},
	})
	if err != nil {
		t.Fatal(err)
	}
	markExecutionAttemptRunning(t, home, attempt.ID)
	calls := &executionPlanCalls{}
	r := executionPlanRuntime(t, calls, func(string) (string, error) { return strings.Repeat("b", 40), nil })

	_, err = r.Promote(context.Background(), PromoteRequest{Home: home, ID: "task-1", Harness: "claude"})
	assertPreconditionError(t, err)
	if calls.worktreeGets != 0 || calls.herdrGets != 1 || calls.harnessBuilds != 0 {
		t.Fatalf("provisioning calls = %+v, want only the completed-scout Herdr probe", calls)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Kind != state.KindScout || len(history.Attempts) != 1 || history.ActiveAttempt == nil {
		t.Fatalf("history after stale promote = %+v, want unchanged scout task and attempt", history)
	}
}

func TestPromoteChecksScoutEligibilityBeforeBriefValidation(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: invalid\n---\nbrief\n")
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout, Lifecycle: state.TaskOpen}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Herdr: state.Herdr{PaneID: "pane-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	markExecutionAttemptRunning(t, home, attempt.ID)
	calls := &executionPlanCalls{}
	r := executionPlanRuntime(t, calls, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	_, err = r.Promote(context.Background(), PromoteRequest{Home: home, ID: "task-1", Harness: "claude"})
	assertPreconditionError(t, err)
	if !strings.Contains(err.Error(), "invalid execution_class") {
		t.Fatalf("error = %q, want brief validation after scout eligibility", err)
	}
	if calls.herdrGets != 1 {
		t.Fatalf("Herdr clients = %d, want eligibility probe before brief validation", calls.herdrGets)
	}
}

func TestPromoteLegacyTierCreatesAttemptBeforeWaitingForProjectLock(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: standard\n---\nbrief\n")
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout, Lifecycle: state.TaskOpen}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	markExecutionAttemptRunning(t, home, attempt.ID)
	calls := &executionPlanCalls{}
	r := executionPlanRuntime(t, calls, func(string) (string, error) { return strings.Repeat("a", 40), nil })
	releaseProject, err := state.Lock(home, "project:demo")
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			releaseProject()
		}
	}()

	promoteDone := make(chan error, 1)
	go func() {
		_, err := r.Promote(context.Background(), PromoteRequest{Home: home, ID: "task-1", Harness: "claude"})
		promoteDone <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		history, readErr := state.ReadHistory(home, "task-1")
		if readErr == nil && len(history.Attempts) == 2 && history.Task.Kind == state.KindShip {
			released = true
			releaseProject()
			if err := <-promoteDone; err != nil {
				t.Fatalf("Promote() = %v, want success after project lock release", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("legacy Promote did not create its ship Attempt before waiting for project lock")
}

func TestSpawnHoldsProjectLockFromMechanicalCheckThroughWorktree(t *testing.T) {
	planned := strings.Repeat("a", 40)
	home := executionPlanHome(t, "---\nexecution_class: mechanical\nplanned_against: "+planned+"\n---\nbrief\n")
	calls := &executionPlanCalls{}
	baseEntered := make(chan struct{})
	allowBase := make(chan struct{})
	worktreeEntered := make(chan struct{})
	allowWorktree := make(chan struct{})
	syncAcquired := make(chan error, 1)
	base := func(string) (string, error) {
		close(baseEntered)
		<-allowBase
		return planned, nil
	}
	r := executionPlanRuntime(t, calls, base)
	r.deps.worktree.get = func(string, string) (worktree.Lease, error) {
		_, err := state.TryLock(home, "project:demo")
		calls.projectLockProbe = err
		close(worktreeEntered)
		<-allowWorktree
		return worktree.Lease{Path: filepath.Join(home, "leased"), ID: "lease-1"}, nil
	}

	spawnDone := make(chan error, 1)
	go func() {
		_, err := r.Spawn(context.Background(), SpawnRequest{Home: home, ID: "task-1", Project: "demo", Harness: "claude"})
		spawnDone <- err
	}()
	select {
	case <-baseEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("mechanical base lookup was not reached")
	}
	go func() {
		release, err := state.Lock(home, "project:demo")
		if err == nil {
			release()
		}
		syncAcquired <- err
	}()
	close(allowBase)
	select {
	case <-worktreeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("worktree acquisition was not reached")
	}
	if !errors.Is(calls.projectLockProbe, state.ErrLockBusy) {
		t.Fatalf("project lock probe = %v, want busy through worktree acquisition", calls.projectLockProbe)
	}
	select {
	case err := <-syncAcquired:
		t.Fatalf("project sync acquired lock before provisioning completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(allowWorktree)
	if err := <-spawnDone; err != nil {
		t.Fatalf("Spawn() = %v, want exact mechanical plan to proceed", err)
	}
	select {
	case err := <-syncAcquired:
		if err != nil {
			t.Fatalf("project sync lock after provisioning = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("project sync did not acquire the lock after Spawn completed")
	}
}

type executionPlanCalls struct {
	baseLookups      int
	worktreeGets     int
	worktreeReturns  int
	herdrGets        int
	harnessBuilds    int
	projectLockProbe error
}

func executionPlanHome(t *testing.T, briefText string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "brief.md"), []byte(briefText), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://example.com/demo.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	return home
}

func executionPlanRuntime(t *testing.T, calls *executionPlanCalls, base func(string) (string, error)) *Runtime {
	t.Helper()
	fakeHerdr := &provisionHerdr{}
	r := testProvisionRuntime(fakeHerdr, func(lifecyclePhase) error { return nil })
	r.deps.projectBaseCommit = func(path string) (string, error) {
		calls.baseLookups++
		return base(path)
	}
	r.deps.herdr = func() herdrClient {
		calls.herdrGets++
		return fakeHerdr
	}
	r.deps.worktree.get = func(path, holder string) (worktree.Lease, error) {
		calls.worktreeGets++
		return worktree.Lease{Path: filepath.Join(path, "leased"), ID: "lease-1"}, nil
	}
	r.deps.worktree.headCommit = func(string) (string, error) { return strings.Repeat("a", 40), nil }
	r.deps.worktree.returnWorktree = func(string, bool) error {
		calls.worktreeReturns++
		return nil
	}
	r.deps.buildHarness = func(string, harness.Options) (string, error) {
		calls.harnessBuilds++
		return "launch", nil
	}
	return r
}

func createTerminalExecutionTask(t *testing.T, home string) {
	t.Helper()
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	markExecutionAttemptRunning(t, home, attempt.ID)
	if err := state.TransitionAttempt(home, attempt.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionTask(home, "task-1", state.TaskOpen, state.TaskTerminal); err != nil {
		t.Fatal(err)
	}
}

func markExecutionAttemptRunning(t *testing.T, home string, attemptID int64) {
	t.Helper()
	stamp := "2026-08-14T00:00:00Z"
	if err := state.MarkLaunchSubmitted(home, "task-1", attemptID, stamp); err != nil {
		t.Fatal(err)
	}
	if err := state.MarkLaunchConfirmed(home, "task-1", attemptID, stamp); err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attemptID); err != nil {
		t.Fatal(err)
	}
}

func assertNoProvisioningSideEffects(t *testing.T, home string, calls *executionPlanCalls) {
	t.Helper()
	if calls.worktreeGets != 0 || calls.herdrGets != 0 || calls.harnessBuilds != 0 {
		t.Fatalf("provisioning calls = %+v, want none", calls)
	}
}

func assertPreconditionError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want precondition")
	}
	var classified *Error
	if !errors.As(err, &classified) || classified.Kind != ErrorPrecondition {
		t.Fatalf("error = %v, want %q precondition", err, ErrorPrecondition)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
