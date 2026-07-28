package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

func assertExitCode3(t *testing.T, err error) {
	t.Helper()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, out)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-q", "-m", "initial commit")
}

// writeFakeGHPRState fakes `gh pr view --json state`, which really answers with
// that JSON object on stdout and exit 0. Real gh writes warnings to stderr
// ahead of the JSON (internal/ghutil/pr.go's PRIsMerged doc comment); this fake
// omits them since these tests only check the parsed state, not the
// stdout/stderr split - that split is covered faithfully by
// internal/ghutil/pr_test.go's writeFakeGHPRView.
func writeFakeGHPRState(t *testing.T, prState string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '{\"state\":\"" + prState + "\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// writeFakeTreehouseReturn fakes the two tools teardown shells out to, each keyed
// on the state its own commands leave behind, since a fake that answers a
// state-changing command identically before and after that command cannot test
// anything about the state change.
//
// treehouse return keeps the returned worktree's pool slot directory and succeeds
// again on a second return of the same path, checked against the real tool; only
// a path no pool manages fails, and that failure path is covered directly by
// internal/worktree/worktree_test.go against the same fidelity note.
//
// herdr stops listing a closed tab. A stateless fake that re-lists a tab it was
// just told to close makes every retry test vacuous - it can never reach the
// already-closed case a second teardown run actually hits.
func writeFakeTreehouseReturn(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "treehouse"), []byte(`#!/bin/sh
case "$1" in
return)
	if [ -d "$2" ]; then
		echo "Worktree returned to pool."
		exit 0
	fi
	echo "worktree $2 is not managed by treehouse" >&2
	exit 1
	;;
esac
echo "unexpected treehouse args: $@" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	closed := filepath.Join(t.TempDir(), "closed")
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(`#!/bin/sh
closed='`+closed+`'
cmd="$1 $2"
case "$cmd" in
"tab list")
	if [ -e "$closed" ]; then
		printf '{"id":"cli:1","result":{"tabs":[]}}'
	else
		printf '{"id":"cli:1","result":{"tabs":[{"tab_id":"wA:tB","workspace_id":"wA"}]}}'
	fi
	;;
"tab close")
	touch "$closed"
	printf '{"id":"cli:1","result":{}}'
	;;
"workspace close")
	touch "$closed"
	printf '{"id":"cli:1","result":{}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// writeFakeGHPRListAndView fakes both gh calls a gate-opened-PR detection makes:
// `gh pr list --head <branch> --json url,state` (FindPRByBranch) and
// `gh pr view <url> --json state` (project.ValidatePR's existence check, then
// checkLandedWork's own merged check) - both answering with the same PR state,
// since these tests only ever have one PR in play.
func writeFakeGHPRListAndView(t *testing.T, url, prState string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$1 $2\" in\n" +
		"\"pr list\") printf '[{\"url\":\"" + url + "\",\"state\":\"" + prState + "\"}]' ;;\n" +
		"\"pr view\") printf '{\"state\":\"" + prState + "\"}' ;;\n" +
		"*) echo \"unexpected gh args: $@\" >&2; exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// setupTeardownGateProject registers a non-local-only project whose clone has a
// GitHub origin remote (RepoSlug reads it) and re-points worktree's checked-out
// branch to branch, so FindPRByBranch's --head argument matches it.
func setupTeardownGateProject(t *testing.T, home, worktree, branch string) {
	t.Helper()
	runGitIn(t, worktree, "checkout", "-q", "-b", branch)

	clonePath := filepath.Join(home, "projects", "myproj")
	initGitRepo(t, clonePath)
	runGitIn(t, clonePath, "remote", "add", "origin", "https://github.com/owner/repo.git")

	if err := project.Add(home, project.Project{Name: "myproj", URL: "https://github.com/owner/repo.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
}

// TestTeardownDetectsAndTearsDownGateOpenedMergedPR is the first of the two
// regression cases atqamz/secondhand#69 requires: a no-mistakes gate's own `pr`
// step opens a PR directly, bypassing `hand pr`, so t.PR is empty even though the
// PR is merged and the work is landed. Teardown must detect it by branch and
// tear down without --force, not just refuse until someone passes it.
func TestTeardownDetectsAndTearsDownGateOpenedMergedPR(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	writeFakeGHPRListAndView(t, "https://github.com/owner/repo/pull/9", "MERGED")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want teardown to detect the gate-opened merged PR and succeed", err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state still exists after teardown: %v %v", exists, err)
	}
}

// TestTeardownRefusesGateOpenedClosedUnmergedPR is the second regression case:
// a detected PR that is closed without merging is not landed work, and the guard
// must still refuse it exactly as it would a `hand pr`-recorded one.
func TestTeardownRefusesGateOpenedClosedUnmergedPR(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	writeFakeGHPRListAndView(t, "https://github.com/owner/repo/pull/9", "CLOSED")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "not merged") {
		t.Fatalf("got err %v, want not merged", err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PR != "https://github.com/owner/repo/pull/9" {
		t.Fatalf("task.PR = %q, want the detected PR recorded even though teardown refused", got.PR)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || !exists {
		t.Fatalf("state gone after refused teardown, want it left in place: %v %v", exists, err)
	}
}

func setupTeardownHome(t *testing.T) (home, worktree string) {
	t.Helper()
	home = t.TempDir()
	t.Chdir(home)
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "dashboard.md"), []byte(dashboardSkeleton), 0o644); err != nil {
		t.Fatal(err)
	}
	worktree = filepath.Join(t.TempDir(), "wt")
	initGitRepo(t, worktree)
	writeFakeTreehouseReturn(t)
	return home, worktree
}

func TestTeardownShipFailsOnUncommittedChanges(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	if err := os.WriteFile(filepath.Join(worktree, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj"}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("got err %v, want uncommitted changes", err)
	}
}

func TestTeardownShipFailsWhenPRNotMerged(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	writeFakeGHPRState(t, "OPEN")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj", PR: "https://example.com/pr/1"}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "not merged") {
		t.Fatalf("got err %v, want not merged", err)
	}
}

func TestTeardownShipSucceedsWhenPRMerged(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	writeFakeGHPRState(t, "MERGED")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		PR: "https://example.com/pr/1", Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state still exists after teardown: %v %v", exists, err)
	}
}

// TestTeardownRetriesAfterReportRemovalFails proves teardown survives a fault in
// its last step, which takes both halves of "retryable". state.Delete's removal
// order (report channel before task JSON) leaves the task JSON untouched, so
// there is something left to retry; and the retry then re-runs the cleanup steps
// the first call already completed, which have to treat an already-closed tab and
// an already-returned worktree as success. Reverting either half fails this test:
// the ordering leaves nothing to retry, the idempotency leaves the retry dying on
// a tab herdr no longer lists.
func TestTeardownRetriesAfterReportRemovalFails(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	writeFakeGHPRState(t, "MERGED")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		PR: "https://example.com/pr/1", Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	reportPath := state.ReportPath(home, "task-1")
	if err := os.Mkdir(reportPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportPath, "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "remove report channel") {
		t.Fatalf("got err %v, want a remove report channel failure", err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || !exists {
		t.Fatalf("state gone after failed teardown, want it retryable: %v %v", exists, err)
	}

	if err := os.RemoveAll(reportPath); err != nil {
		t.Fatal(err)
	}

	cmd = newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state still exists after retried teardown: %v %v", exists, err)
	}

	dashboardData, err := os.ReadFile(filepath.Join(home, "data", "dashboard.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dashboardData), "task-1: myproj | ship | merged | PR https://example.com/pr/1") {
		t.Fatalf("dashboard = %q, want task-1 moved to Recent Completions", dashboardData)
	}
}

// A worker that asks a question and then gives up leaves that question on the
// dashboard on purpose - no event kind clears it on inference. Teardown is not
// inference, it is the operator deleting the task, and once the Active Tasks row is
// gone nothing can ever retire the entry: the ID leaves the task list, so hand watch
// stops tracking it. Leaving it strands an unanswerable question in the supervisor's
// queue for good, and it is the leak that made the section look unbounded.
func TestTeardownRetiresTheTasksPendingQuestion(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	writeFakeGHPRState(t, "MERGED")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		PR: "https://example.com/pr/1", Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	dashPath := filepath.Join(home, "data", "dashboard.md")
	if err := dashboard.Update(dashPath, dashboard.UpdateOpts{
		AddActiveTask:      &dashboard.ActiveTask{ID: "task-1", Project: "myproj", Kind: "ship", State: "working", Age: "just now"},
		SetPendingDecision: &dashboard.PendingDecision{ID: "task-1", Text: "which base branch?"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := dashboard.Update(dashPath, dashboard.UpdateOpts{
		UpdateAgentState: &dashboard.AgentStateUpdate{ID: "task-1", State: "report-failed", Age: "5m"},
	}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dashPath)
	if err != nil {
		t.Fatal(err)
	}
	d, err := dashboard.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.PendingDecisions) != 0 {
		t.Fatalf("PendingDecisions = %+v, want the torn-down task's question gone", d.PendingDecisions)
	}
	if len(d.ActiveTasks) != 0 {
		t.Fatalf("ActiveTasks = %+v, want the row gone too", d.ActiveTasks)
	}
}

func TestTeardownShipLocalOnlyFailsWhenBranchNotMerged(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	clonePath := filepath.Join(t.TempDir(), "clone")
	initGitRepo(t, clonePath)

	worktree := filepath.Join(t.TempDir(), "wt")
	c := exec.Command("git", "clone", "-q", clonePath, worktree)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git clone failed: %v: %s", err, out)
	}
	branch := exec.Command("git", "checkout", "-q", "-b", "task-1-branch")
	branch.Dir = worktree
	if out, err := branch.CombinedOutput(); err != nil {
		t.Fatalf("git checkout failed: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "add", "feature.txt")
	commit.Dir = worktree
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v: %s", err, out)
	}
	commitCmd := exec.Command("git", "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "wip")
	commitCmd.Dir = worktree
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v: %s", err, out)
	}

	writeFakeTreehouseReturn(t)
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "myproj", URL: "https://example.com/myproj.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(clonePath, filepath.Join(home, "projects", "myproj")); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj"}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "not merged into the default branch") {
		t.Fatalf("got err %v, want branch not merged", err)
	}
}

func TestBranchIsMergedUsesOriginDefaultBranch(t *testing.T) {
	clonePath := filepath.Join(t.TempDir(), "clone")
	initGitRepo(t, clonePath)

	runGit := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, out)
		}
	}
	runGit(clonePath, "branch", "release")
	runGit(clonePath, "branch", "task-1")
	runGit(clonePath, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	runGit(clonePath, "update-ref", "refs/remotes/origin/main", "refs/heads/main")

	worktreePath := filepath.Join(t.TempDir(), "worktree")
	runGit(clonePath, "worktree", "add", "-q", worktreePath, "task-1")
	if err := os.WriteFile(filepath.Join(worktreePath, "feature.txt"), []byte("feature"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(worktreePath, "add", "feature.txt")
	runGit(worktreePath, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "feature")
	runGit(clonePath, "-c", "user.name=test", "-c", "user.email=test@example.com", "merge", "--no-ff", "-q", "task-1", "-m", "merge task")
	runGit(clonePath, "checkout", "-q", "release")

	merged, err := branchIsMerged(clonePath, worktreePath)
	if err != nil {
		t.Fatal(err)
	}
	if !merged {
		t.Fatal("task branch should be merged into origin default branch")
	}
}

func TestTeardownScoutFailsWhenReportMissing(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout, Worktree: worktree}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "report not found") {
		t.Fatalf("got err %v, want report not found", err)
	}
}

func TestTeardownScoutSucceedsWhenReportPresent(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout, Worktree: worktree,
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestTeardownForceSkipsLandedWorkChecks(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	if err := os.WriteFile(filepath.Join(worktree, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state still exists after forced teardown: %v %v", exists, err)
	}
}

func TestTeardownWaitsForProjectLockBeforeClosingResources(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindScout, Worktree: worktree,
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(t.TempDir(), "herdr-called")
	bin := t.TempDir()
	// This and the herdr fakes further down this file all return a non-null
	// object result for "tab list"/"tab close"/"workspace close", which is
	// exactly what call() requires for success (client.go); these three are
	// query commands, not the void pane commands callVoid documents, so there
	// is no exit-code-vs-envelope split to reproduce here.
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte("#!/bin/sh\ntouch '"+marker+"'\nprintf '{\"id\":\"cli:1\",\"result\":{\"tabs\":[{\"tab_id\":\"wA:tB\"}]}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	releaseProject, err := state.Lock(home, "project:myproj")
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			releaseProject()
		}
	}()

	result := make(chan error, 1)
	go func() {
		cmd := newTeardownCmd()
		cmd.SetArgs([]string{"task-1", "--force"})
		result <- cmd.Execute()
	}()

	select {
	case <-result:
		t.Fatal("teardown completed while project lock was held")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("herdr invoked while project lock was held: %v", err)
	}

	releaseProject()
	released = true
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestTeardownClosesWorkspaceWhenLastTab(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(`#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"tab list")
	printf '{"id":"cli:1","result":{"tabs":[{"tab_id":"wA:tB","workspace_id":"wA"}]}}'
	;;
"workspace close")
 printf '{"id":"cli:1","result":{}}'
	;;
"tab close")
	echo "should not close a tab when it is the last one" >&2
	exit 1
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout, Worktree: worktree,
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

// A tab herdr no longer lists is this step's goal already reached, so it must not
// fail the command: teardown's later steps can fault, and the retry that follows
// finds exactly this state.
func TestCloseTaskTabTreatsAbsentTabAsClosed(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(`#!/bin/sh
case "$1 $2" in
"tab list")
 printf '{"id":"cli:1","result":{"tabs":[{"tab_id":"wA:other","workspace_id":"wA"}]}}'
 ;;
*)
 echo "unexpected herdr args: $@" >&2
 exit 1
 ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := closeTaskTab(herdr.NewClient(), "wA", "wA:missing"); err != nil {
		t.Fatalf("got %v, want an absent tab treated as already closed", err)
	}
}

func TestCloseTaskTabClosesWorkspaceForSoleTab(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(`#!/bin/sh
case "$1 $2" in
"tab list")
 printf '{"id":"cli:1","result":{"tabs":[{"tab_id":"wA:tB","workspace_id":"wA"}]}}'
 ;;
"workspace close")
 printf '{"id":"cli:1","result":{}}'
 ;;
*)
 echo "unexpected herdr args: $@" >&2
 exit 1
 ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := closeTaskTab(herdr.NewClient(), "wA", "wA:tB"); err != nil {
		t.Fatal(err)
	}
}
