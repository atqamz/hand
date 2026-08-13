package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
)

func TestSpawnCleanupReportsAllErrors(t *testing.T) {
	cause := errors.New("spawn failed")
	cleanup := errors.New("cleanup failed")

	err := reportSpawnCleanup(cause, cleanup)
	if !errors.Is(err, cause) {
		t.Fatalf("error %v does not preserve cause", err)
	}
	if !errors.Is(err, cleanup) {
		t.Fatalf("error %v does not preserve cleanup failure", err)
	}
	if !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("error %v does not report cleanup failure", err)
	}
}

func defaultSpawnHerdr(agent string) faketool.Herdr {
	return faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{{
			ID: "wA", Label: "hand:myproj",
			Tabs: []faketool.HerdrTab{{ID: "wA:root", Label: "root", Pane: "wA:pRoot"}},
		}},
		TabCreates: []faketool.HerdrTab{{ID: "wA:tB", Label: "task-1", Pane: "wA:pC"}},
		PaneAgent:  agent, PaneStatus: "idle",
	}
}

func setupSpawnHome(t *testing.T, worktreePath string, herdr faketool.Herdr) string {
	t.Helper()
	useFastLaunchPolling(t)
	t.Setenv("HAND_HARNESS", harness.Claude)
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "myproj", URL: "https://example.com/myproj.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "brief.md"), []byte("do the thing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "myproj"), 0o755); err != nil {
		t.Fatal(err)
	}

	bin := faketool.Bin(t)
	callLog := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("HERDR_CALL_LOG", callLog)
	herdr.Log = callLog
	herdr.Install(t, bin)
	treehouseLog := filepath.Join(t.TempDir(), "treehouse.log")
	t.Setenv("TREEHOUSE_CALL_LOG", treehouseLog)
	faketool.Treehouse{Slots: []string{worktreePath}, Log: treehouseLog}.Install(t, bin)
	t.Chdir(home)
	mkFleetDirs(t, home)
	return home
}

func TestSpawnUsesDetectedHarnessWithoutConfiguredOverride(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, defaultSpawnHerdr("codex"))
	t.Setenv("HAND_HARNESS", harness.Codex)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	active := readTaskAttempt(t, home, "task-1")
	if active.Harness != harness.Codex {
		t.Fatalf("harness = %q, want detected %q", active.Harness, harness.Codex)
	}
}

func TestSpawnConfiguredHarnessWinsOverDetectedHarness(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, defaultSpawnHerdr("claude"))
	t.Setenv("HAND_HARNESS", harness.Codex)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "harness"), []byte(harness.Claude+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	active := readTaskAttempt(t, home, "task-1")
	if active.Harness != harness.Claude {
		t.Fatalf("harness = %q, want configured %q", active.Harness, harness.Claude)
	}
}

func TestSpawnExplicitHarnessOverridesConfiguredAndDetectedHarness(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, defaultSpawnHerdr(harness.Claude))
	t.Setenv("HAND_HARNESS", harness.Codex)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "harness"), []byte(harness.Codex+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj", "--harness", harness.Claude})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	active := readTaskAttempt(t, home, "task-1")
	if active.Harness != harness.Claude {
		t.Fatalf("harness = %q, want explicit %q", active.Harness, harness.Claude)
	}
}

func TestSpawnUnknownDetectedHarnessFailsBeforeWorktreeAcquisition(t *testing.T) {
	home := setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), defaultSpawnHerdr("claude"))
	t.Setenv("HAND_HARNESS", "unknown")
	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "current supervisor harness is unknown") {
		t.Fatalf("error = %v, want unknown-supervisor remedy", err)
	}
	if _, statErr := os.Stat(os.Getenv("TREEHOUSE_CALL_LOG")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("treehouse was invoked: %v", statErr)
	}
	if _, readErr := state.Read(home, "task-1"); !errors.Is(readErr, state.ErrTaskNotFound) {
		t.Fatalf("task state after refusal = %v", readErr)
	}
}

func TestSpawnHappyPath(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, defaultSpawnHerdr("claude"))
	t.Setenv("HAND_HOME", ".")
	callLog := os.Getenv("HERDR_CALL_LOG")
	if callLog == "" {
		t.Fatal("setupSpawnHome did not configure the herdr call log")
	}

	cmd := newSpawnCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	active := readTaskAttempt(t, home, "task-1")
	if got.Project != "myproj" || got.Kind != state.KindShip || active.Harness != "claude" {
		t.Fatalf("got %+v", got)
	}
	if active.Worktree != wt {
		t.Fatalf("got worktree %q, want %q", active.Worktree, wt)
	}
	if active.Herdr.WorkspaceID != "wA" || active.Herdr.TabID != "wA:tB" || active.Herdr.PaneID != "wA:pC" {
		t.Fatalf("got herdr %+v", active.Herdr)
	}
	if active.LeaseID != "lease-1" {
		t.Fatalf("got lease id %q, want the identity treehouse handed back", active.LeaseID)
	}
	for _, want := range []string{
		"id: task-1\n",
		"result: spawned\n",
		"project: myproj\n",
		"kind: ship\n",
		"harness: claude\n",
		"worktree: " + axi.Value(wt) + "\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output = %q, want field %q", out.String(), want)
		}
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	launch := string(calls)
	env := "HAND_ROLE=worker HAND_HOME='" + home + "'"
	envAt := strings.Index(launch, env)
	if envAt < 0 {
		t.Fatalf("launch = %q, want absolute worker environment %q", launch, env)
	}
	if harnessAt := strings.Index(launch, "claude --dangerously"); harnessAt < 0 || envAt > harnessAt {
		t.Fatalf("launch = %q, want worker environment before harness executable", launch)
	}
}

func TestSpawnIgnoresSameLabelledWorkspaceHandDidNotCreate(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{
			{ID: "wHuman", Label: "myproj", Tabs: []faketool.HerdrTab{{ID: "wHuman:root", Label: "root", Pane: "wHuman:pHuman"}}},
			{ID: "wA", Label: "hand:myproj", Tabs: []faketool.HerdrTab{{ID: "wA:root", Label: "root", Pane: "wA:pRoot"}}},
		},
		TabCreates: []faketool.HerdrTab{{ID: "wA:tB", Label: "task-1", Pane: "wA:pC"}},
		PaneAgent:  "claude", PaneStatus: "idle",
	})

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	active := readTaskAttempt(t, home, "task-1")
	if active.Herdr.WorkspaceID != "wA" {
		t.Fatalf("got workspace %q, want hand's own hand:myproj workspace wA, not the same-labelled one it did not create", active.Herdr.WorkspaceID)
	}
}

func TestSpawnPersistsResolvedNotDeclaredTierValues(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, defaultSpawnHerdr("claude"))
	briefPath := filepath.Join(home, "data", "task-1", "brief.md")
	if err := os.WriteFile(briefPath, []byte("---\nmodel: brief-model\neffort: brief-effort\n---\n# Title\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj", "--model", "flag-model"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	active := readTaskAttempt(t, home, "task-1")
	if active.Model != "flag-model" {
		t.Fatalf("got model %q, want the flag to win over the brief's declared %q", active.Model, "brief-model")
	}
	if active.Effort != "brief-effort" {
		t.Fatalf("got effort %q, want the brief's declared value since no flag or config overrides it", active.Effort)
	}
}

func TestSpawnScoutFlag(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, defaultSpawnHerdr("claude"))

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj", "--scout"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != state.KindScout {
		t.Fatalf("got kind %q, want scout", got.Kind)
	}
}

func TestSpawnRejectsUnregisteredProject(t *testing.T) {
	setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), defaultSpawnHerdr("claude"))

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "unknown-proj"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("got err %v, want not registered", err)
	}
}

func TestSpawnRejectsAlreadyActiveTask(t *testing.T) {
	home := setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), defaultSpawnHerdr("claude"))
	if err := state.Write(home, state.Task{ID: "task-1"}); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "already active") {
		t.Fatalf("got err %v, want already active", err)
	}
}

// A hold survives the teardown of the task it was set on, so the id it names can
// be free while its question is still open. Reusing that id has to refuse rather
// than reattach the old question to unrelated work.
func TestSpawnRejectsHeldIDWithNoTaskRow(t *testing.T) {
	home := setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), defaultSpawnHerdr("claude"))
	if err := state.SetHold(home, state.Hold{ID: "task-1", Kind: state.HoldKindOperator, Reason: "needs a call"}); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "open hold") || !strings.Contains(err.Error(), "hand hold clear task-1") {
		t.Fatalf("got err %v, want an open-hold refusal naming the remedy", err)
	}
	if _, readErr := state.Read(home, "task-1"); !errors.Is(readErr, state.ErrTaskNotFound) {
		t.Fatalf("got %v, want no task row written for a refused spawn", readErr)
	}
}

func TestSpawnTerminalTaskWithHoldPointsToReopen(t *testing.T) {
	home := setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), defaultSpawnHerdr("claude"))
	if err := state.CreateTask(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip, Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	attempt, err := state.CreateAttempt(home, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionAttempt(home, attempt.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionTask(home, "task-1", state.TaskOpen, state.TaskTerminal); err != nil {
		t.Fatal(err)
	}
	if err := state.SetHold(home, state.Hold{ID: "task-1", Kind: state.HoldKindOperator, Reason: "needs a call"}); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	err = cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "hand reopen task-1") {
		t.Fatalf("got err %v, want terminal-task reopen remedy", err)
	}
	if strings.Contains(err.Error(), "open hold") {
		t.Fatalf("got err %v, want existing terminal-task refusal before hold check", err)
	}
}

func TestSpawnAcceptsIDWhoseHoldWasCleared(t *testing.T) {
	home := setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), defaultSpawnHerdr("claude"))
	if err := state.SetHold(home, state.Hold{ID: "task-1", Kind: state.HoldKindOperator, Reason: "needs a call"}); err != nil {
		t.Fatal(err)
	}
	if err := state.ClearHold(home, "task-1"); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Read(home, "task-1"); err != nil {
		t.Fatal(err)
	}
}

func TestSpawnRejectsMissingBrief(t *testing.T) {
	home := setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), defaultSpawnHerdr("claude"))
	if err := os.Remove(filepath.Join(home, "data", "task-1", "brief.md")); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "brief not found") {
		t.Fatalf("got err %v, want brief not found", err)
	}
}

func TestSpawnRejectsUnrecognizedHarness(t *testing.T) {
	setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), defaultSpawnHerdr("claude"))

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj", "--harness", "nonexistent"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not recognized") {
		t.Fatalf("got err %v, want not recognized", err)
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
	if _, statErr := os.Stat(os.Getenv("TREEHOUSE_CALL_LOG")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("treehouse was invoked for an invalid explicit harness: %v", statErr)
	}
}

// A row with no lease identity - written before the lease_id column existed -
// is still guarded by worktree path, and that guard is still wired into spawn.
func TestSpawnDetectsWorktreeCollisionAgainstARowWithNoLeaseIdentity(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, defaultSpawnHerdr("claude"))
	if err := writeTaskAttempt(t, home, state.Task{ID: "other-task"}, state.Attempt{Lifecycle: state.AttemptRunning, Worktree: wt}); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("got err %v, want collision", err)
	}

	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state written after collision: exists=%v err=%v", exists, err)
	}
}

// A row left behind by a teardown whose state.Delete failed still names the pool
// slot treehouse has since freed and handed out again. Under a lease of its own
// that is not a collision, and the spawn has to go through.
func TestSpawnAllowsAReusedWorktreePathUnderAFreshLease(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, defaultSpawnHerdr("claude"))
	if err := writeTaskAttempt(t, home, state.Task{ID: "stale-task"}, state.Attempt{Lifecycle: state.AttemptRunning, Worktree: wt, LeaseID: "lease-0"}); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got err %v, want the spawn to proceed past a stale row on the same path", err)
	}

	active := readTaskAttempt(t, home, "task-1")
	if active.LeaseID != "lease-1" {
		t.Fatalf("got lease id %q, want the identity treehouse handed back", active.LeaseID)
	}
}

func spawnLeakHerdr(workspaceExists bool) faketool.Herdr {
	herdr := faketool.Herdr{
		Responses: []faketool.HerdrResponse{{Command: "pane run", Exit: 1}},
		PaneAgent: "claude", PaneStatus: "idle",
	}
	if workspaceExists {
		herdr.Workspaces = []faketool.HerdrWorkspace{{
			ID: "wA", Label: "hand:myproj",
			Tabs: []faketool.HerdrTab{
				{ID: "wA:root", Label: "root", Pane: "wA:pRoot"},
				{ID: "wA:other", Label: "other", Pane: "wA:pOther"},
			},
		}}
		herdr.TabCreates = []faketool.HerdrTab{{ID: "wA:tB", Label: "task-1", Pane: "wA:pC"}}
		return herdr
	}
	herdr.Creates = []faketool.HerdrWorkspace{{
		ID: "wA", Label: "myproj",
		Tabs: []faketool.HerdrTab{{ID: "wA:tB", Label: "1", Pane: "wA:pC"}},
	}}
	return herdr
}

func setupSpawnLeakEnv(t *testing.T, workspaceExists bool) string {
	t.Helper()
	callLog := os.Getenv("HERDR_CALL_LOG")
	if callLog == "" {
		t.Fatal("setupSpawnHome did not configure the herdr call log")
	}

	_ = workspaceExists
	return callLog
}

func TestSpawnRollsBackWhenWorkerNeverStarts(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, faketool.Herdr{
		Creates: []faketool.HerdrWorkspace{{
			ID: "wA", Label: "myproj",
			Tabs: []faketool.HerdrTab{{ID: "wA:tB", Label: "1", Pane: "wA:pC"}},
		}},
		PaneAgent: "claude", PaneStatus: "idle", PaneReadOut: "Some brand new dialog\n> 1. Sure\n  2. Nope\n\nEnter to confirm\n",
	})
	expectLaunchTimeout()
	callLog := setupSpawnLeakEnv(t, false)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "confirm worker started") {
		t.Fatalf("got err %v, want the spawn to fail on launch confirmation", err)
	}

	if exists, existsErr := state.Exists(home, "task-1"); existsErr != nil || exists {
		t.Fatalf("state written for a worker that never started: exists=%v err=%v", exists, existsErr)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(calls), "workspace close wA") {
		t.Fatalf("calls = %q, want the workspace hand created to be closed", calls)
	}
}

func TestSpawnFailureClosesWorkspaceItCreated(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	setupSpawnHome(t, wt, spawnLeakHerdr(false))
	callLog := setupSpawnLeakEnv(t, false)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected spawn to fail")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "workspace close wA") {
		t.Fatalf("calls = %q, want the workspace hand created to be closed", calls)
	}
}

func TestSpawnPartialWorkspaceCreateLeavesNoWorkspaceBehind(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, faketool.Herdr{Responses: []faketool.HerdrResponse{
		{Command: "workspace create", Stdout: `{"id":"cli:workspace:create","result":{"workspace":{"workspace_id":"wA","label":"myproj"}}}`},
		{Command: "workspace close", Stdout: `{"id":"cli:workspace:close","result":{"type":"ok"}}`},
	}})
	callLog := setupSpawnLeakEnv(t, false)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "missing workspace, tab, or root pane") {
		t.Fatalf("got err %v, want the partial workspace_created response rejected", err)
	}

	if exists, existsErr := state.Exists(home, "task-1"); existsErr != nil || exists {
		t.Fatalf("state written for a partial workspace_created response: exists=%v err=%v", exists, existsErr)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(calls), "workspace close wA") {
		t.Fatalf("calls = %q, want the workspace herdr created to be closed", calls)
	}
}

func TestSpawnTabRenameFailureClosesWorkspaceItCreated(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, faketool.Herdr{
		Creates: []faketool.HerdrWorkspace{{
			ID: "wA", Label: "myproj",
			Tabs: []faketool.HerdrTab{{ID: "wA:tB", Label: "1", Pane: "wA:pC"}},
		}},
		Responses: []faketool.HerdrResponse{{Command: "tab rename", Exit: 1}},
	})
	callLog := setupSpawnLeakEnv(t, false)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "herdr tab rename failed") {
		t.Fatalf("got err %v, want the tab rename failure surfaced", err)
	}

	if exists, existsErr := state.Exists(home, "task-1"); existsErr != nil || exists {
		t.Fatalf("state written for a failed tab rename: exists=%v err=%v", exists, existsErr)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(calls), "workspace close wA") {
		t.Fatalf("calls = %q, want the workspace hand created to be closed", calls)
	}
}

func TestSpawnFailureKeepsPreexistingWorkspace(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	setupSpawnHome(t, wt, spawnLeakHerdr(true))
	callLog := setupSpawnLeakEnv(t, true)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected spawn to fail")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "workspace close") {
		t.Fatalf("calls = %q, want the pre-existing shared workspace left open", calls)
	}
	if !strings.Contains(string(calls), "tab close wA:tB") {
		t.Fatalf("calls = %q, want the task's own tab closed", calls)
	}
}
