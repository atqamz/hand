package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
)

func TestResolveMergeMethodDefaultsToSquash(t *testing.T) {
	m, err := resolveMergeMethod(false, false, false)
	if err != nil || m != "squash" {
		t.Fatalf("got (%q, %v), want (squash, nil)", m, err)
	}
}

func TestResolveMergeMethodHonorsFlags(t *testing.T) {
	if m, err := resolveMergeMethod(false, true, false); err != nil || m != "merge" {
		t.Fatalf("got (%q, %v), want (merge, nil)", m, err)
	}
	if m, err := resolveMergeMethod(false, false, true); err != nil || m != "rebase" {
		t.Fatalf("got (%q, %v), want (rebase, nil)", m, err)
	}
}

func TestResolveMergeMethodRejectsConflictingFlags(t *testing.T) {
	_, err := resolveMergeMethod(true, true, false)
	if err == nil {
		t.Fatal("want error for conflicting flags")
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestMergeRejectsLocalCombinedWithMethodFlags(t *testing.T) {
	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1", "--local", "--squash"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
	if !strings.Contains(err.Error(), "cannot be combined with --local") {
		t.Fatalf("err = %v, want --local conflict", err)
	}
}

func TestMergeRejectsConflictingMethodFlags(t *testing.T) {
	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1", "--squash", "--rebase"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
	if !strings.Contains(err.Error(), "only one of") {
		t.Fatalf("err = %v, want mutually exclusive method flags", err)
	}
}

func writeFakeGh(t *testing.T, checks string) {
	t.Helper()
	bin := faketool.Bin(t)
	faketool.GH{Responses: []faketool.GHResponse{{
		Command: "pr checks", Stdout: checks,
	}}}.Install(t, bin)
}

const mergeTestPR = "https://github.com/org/repo/pull/42"

// The PR hand merge works on, with the buckets `pr checks` reports for it. Every
// merge through the fake moves it to MERGED, which is what makes the pre-check in
// runPRMerge reachable at all.
func mergeTestGH(checks ...string) faketool.GH {
	return faketool.GH{PRs: []faketool.GHPR{{
		Number: 42,
		URL:    mergeTestPR,
		Branch: "task-1-branch",
		Repo:   "org/repo",
		Checks: checks,
	}}}
}

func TestPRChecksGreenAllPass(t *testing.T) {
	// Faithful to real gh here: an all-pass `pr checks --json bucket` prints
	// this array on stdout and exits 0.
	writeFakeGh(t, `[{"bucket":"pass"},{"bucket":"skipping"}]`)
	green, err := prChecksGreen("https://example.com/pr/1")
	if err != nil {
		t.Fatal(err)
	}
	if !green {
		t.Fatal("want green")
	}
}

func TestPRChecksGreenFailingCheck(t *testing.T) {
	// Exit 0 with a "fail" bucket instead of real gh's exit 1, same reason as
	// fakeGhChecksRed above.
	writeFakeGh(t, `[{"bucket":"pass"},{"bucket":"fail"}]`)
	green, err := prChecksGreen("https://example.com/pr/1")
	if err != nil {
		t.Fatal(err)
	}
	if green {
		t.Fatal("want not green")
	}
}

func TestMergeRefusesWhenNoPRRecorded(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1"}, state.Attempt{Lifecycle: state.AttemptRunning}); err != nil {
		t.Fatal(err)
	}

	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), "no PR recorded") {
		t.Fatalf("err = %v, want no PR recorded", err)
	}
}

// atqamz/hand#422: a gate-opened PR (atqamz/hand#69) can populate t.PR without hand having merged it,
// and the merge can equally have happened out of band. "Already merged" cannot be un-merged, so hand
// converges instead of refusing an unsatisfiable precondition.
func TestMergeConvergesOnAlreadyMergedPR(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	g := mergeTestGH()
	g.PRs[0].State = "MERGED"
	log := filepath.Join(t.TempDir(), "gh.log")
	g.Log = log
	g.Install(t, faketool.Bin(t))

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", PR: mergeTestPR}, state.Attempt{Lifecycle: state.AttemptRunning}); err != nil {
		t.Fatal(err)
	}

	cmd := newMergeCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"id: task-1\n", "result: converged\n", "pr: \"" + mergeTestPR + "\"\n"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output = %q, want field %q", out.String(), want)
		}
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MergeExecuted {
		t.Fatal("want task not marked merged by hand merge itself: hand observed this merge, it did not perform it")
	}
	if !got.MergeAnnounced {
		t.Fatal("want the observed merge recorded as announced")
	}

	invocations, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(invocations), "gh pr merge") {
		t.Fatalf("gh pr merge ran for a PR already merged:\n%s", invocations)
	}
}

// Converging is idempotent: a second hand merge on a task whose PR is still (and only ever) observed
// merged converges again rather than refusing or re-merging.
func TestMergeConvergesRepeatedlyOnAnAlreadyMergedPR(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	g := mergeTestGH()
	g.PRs[0].State = "MERGED"
	log := filepath.Join(t.TempDir(), "gh.log")
	g.Log = log
	g.Install(t, faketool.Bin(t))

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", PR: mergeTestPR}, state.Attempt{Lifecycle: state.AttemptRunning}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		cmd := newMergeCmd()
		cmd.SetArgs([]string{"task-1"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MergeExecuted || !got.MergeAnnounced {
		t.Fatalf("task = %+v, want converged (announced, not executed) after repeated runs", got)
	}
	invocations, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(invocations), "gh pr merge") {
		t.Fatalf("gh pr merge ran for a PR already merged:\n%s", invocations)
	}
}

// atqamz/hand#241 at the irreversible call: a PR state hand could not read is neither an open PR nor an
// absent one, so gh pr merge must not run and the refusal must not name a cause it did not observe.
func TestMergeRefusesAPRWhoseStateCouldNotBeObserved(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	log := filepath.Join(t.TempDir(), "gh.log")
	faketool.GH{Log: log, Responses: []faketool.GHResponse{
		{Command: "pr view", Stderr: ghRejectedCredential, Exit: 1},
	}}.Install(t, faketool.Bin(t))

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", PR: mergeTestPR}, state.Attempt{Lifecycle: state.AttemptRunning}); err != nil {
		t.Fatal(err)
	}

	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), "could not be observed") || strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want the observation failure and no claim that the PR is absent", err)
	}
	assertNoMergeRan(t, log)
	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MergeExecuted {
		t.Fatal("want no merge recorded for a PR hand could not observe")
	}
}

// The other half of the same guard: a PR GitHub positively says is not there still refuses, and says so.
func TestMergeRefusesAPRGitHubReportsAsAbsent(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	log := filepath.Join(t.TempDir(), "gh.log")
	faketool.GH{Log: log, Responses: []faketool.GHResponse{
		{Command: "pr view", Stderr: ghPRAbsentDiagnostic(42), Exit: 1},
	}}.Install(t, faketool.Bin(t))

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", PR: mergeTestPR}, state.Attempt{Lifecycle: state.AttemptRunning}); err != nil {
		t.Fatal(err)
	}

	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want the absent-PR refusal", err)
	}
	assertNoMergeRan(t, log)
}

func assertNoMergeRan(t *testing.T, log string) {
	t.Helper()
	invocations, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(invocations), "gh pr merge") {
		t.Fatalf("gh pr merge ran before the PR state was known:\n%s", invocations)
	}
}

func TestMergePRRefusesRedCI(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	mergeTestGH("pass", "fail").Install(t, faketool.Bin(t))

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", PR: mergeTestPR}, state.Attempt{Lifecycle: state.AttemptRunning}); err != nil {
		t.Fatal(err)
	}

	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), "not green") {
		t.Fatalf("err = %v, want not green", err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MergeExecuted {
		t.Fatal("want task not marked merged")
	}
	observation := ghutil.ObserveMergeState(context.Background(), mergeTestPR)
	if !observation.Found() {
		t.Fatalf("observation = %+v, want the fake PR observed", observation)
	}
	if observation.Merged {
		t.Fatal("want red checks to leave gh's PR unmerged")
	}
}

func TestMergePRSucceedsWhenChecksGreen(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	mergeTestGH("pass", "skipping").Install(t, faketool.Bin(t))

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", PR: mergeTestPR}, state.Attempt{Lifecycle: state.AttemptRunning}); err != nil {
		t.Fatal(err)
	}

	cmd := newMergeCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.MergeExecuted {
		t.Fatal("want task marked merged")
	}
	if got.MergeExecutedAt == "" {
		t.Fatal("want merged_at set")
	}

	observation := ghutil.ObserveMergeState(context.Background(), mergeTestPR)
	if !observation.Found() {
		t.Fatalf("observation = %+v, want the fake PR observed", observation)
	}
	if !observation.Merged {
		t.Fatal("want the PR merged on gh's side too, not only on the task row")
	}
	for _, want := range []string{"id: task-1\n", "result: merged\n", "method: squash\n", "pr: \"https://github.com/org/repo/pull/42\"\n", "merged: "} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output = %q, want field %q", out.String(), want)
		}
	}
}

// hand merge writes the row only after gh has merged, so a fault between the two leaves the PR merged
// and the row saying otherwise. The pre-check now converges rather than refusing, recovering the lost
// write instead of re-running `gh pr merge`, which is exit 0 with a warning (internal/faketool/FIDELITY.md).
func TestMergeConvergesOnAPRAnEarlierRunAlreadyMerged(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	log := filepath.Join(t.TempDir(), "gh.log")
	g := mergeTestGH()
	g.Log = log
	g.Install(t, faketool.Bin(t))

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", PR: mergeTestPR}, state.Attempt{Lifecycle: state.AttemptRunning}); err != nil {
		t.Fatal(err)
	}

	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got.MergeExecuted = false
	got.MergeExecutedAt = ""
	if err := state.Write(home, got); err != nil {
		t.Fatal(err)
	}

	rerun := newMergeCmd()
	rerun.SetArgs([]string{"task-1"})
	if err := rerun.Execute(); err != nil {
		t.Fatal(err)
	}

	converged, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !converged.MergeAnnounced {
		t.Fatal("want the rerun to converge on the observed merge rather than refuse")
	}

	invocations, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(invocations), "gh pr merge"); n != 1 {
		t.Fatalf("gh pr merge ran %d times, want 1: the rerun must not merge again\n%s", n, invocations)
	}
}

func TestMergeLocalRefusesUncommittedChanges(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	worktreePath := filepath.Join(t.TempDir(), "wt")
	initGitRepo(t, worktreePath)
	if err := os.WriteFile(filepath.Join(worktreePath, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Kind: state.KindShip, Project: "myproj"}, state.Attempt{Lifecycle: state.AttemptRunning, Worktree: worktreePath}); err != nil {
		t.Fatal(err)
	}

	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1", "--local"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

func TestMergeLocalFastForwardSucceeds(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	clonePath := filepath.Join(home, "projects", "myproj")
	initGitRepo(t, clonePath)

	worktreePath := filepath.Join(t.TempDir(), "wt")
	runGitIn(t, clonePath, "worktree", "add", "-q", worktreePath, "-b", "task-1-branch")
	if err := os.WriteFile(filepath.Join(worktreePath, "feature.txt"), []byte("feature"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, worktreePath, "add", "feature.txt")
	runGitIn(t, worktreePath, "commit", "-q", "-m", "add feature")

	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "myproj", URL: "local", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Worktree: worktreePath}); err != nil {
		t.Fatal(err)
	}

	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1", "--local"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(clonePath, "feature.txt")); err != nil {
		t.Fatalf("clone did not fast-forward: %v", err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.MergeExecuted {
		t.Fatal("want task marked merged")
	}
}

func TestMergeLocalRefusesWhenNotFastForwardable(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	clonePath := filepath.Join(home, "projects", "myproj")
	initGitRepo(t, clonePath)

	worktreePath := filepath.Join(t.TempDir(), "wt")
	runGitIn(t, clonePath, "worktree", "add", "-q", worktreePath, "-b", "task-1-branch")
	if err := os.WriteFile(filepath.Join(worktreePath, "feature.txt"), []byte("feature"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, worktreePath, "add", "feature.txt")
	runGitIn(t, worktreePath, "commit", "-q", "-m", "add feature")

	if err := os.WriteFile(filepath.Join(clonePath, "other.txt"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, clonePath, "add", "other.txt")
	runGitIn(t, clonePath, "commit", "-q", "-m", "diverge main")

	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "myproj", URL: "local", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}

	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Worktree: worktreePath}); err != nil {
		t.Fatal(err)
	}

	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1", "--local"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MergeExecuted {
		t.Fatal("want task not marked merged")
	}
}
