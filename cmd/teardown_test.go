package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
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

// The PR recorded on a task whose test does not exercise detection.
const teardownTestPR = "https://example.com/pr/1"

// The PR state teardown's landed-work check reads. From internal/faketool so a
// merge through the fake moves it, which is what keeps a test from asserting a
// state nothing could have produced.
func writeFakeGHPRState(t *testing.T, prState string) {
	t.Helper()
	faketool.GH{PRs: []faketool.GHPR{
		{Number: 1, URL: teardownTestPR, Branch: "task-1-branch", Repo: "owner/repo", State: prState},
	}}.Install(t, faketool.Bin(t))
}

// The two tools teardown shells out to, both from internal/faketool so each keeps the state its own
// commands change: the returned worktree's slot stops being leasable and the closed tab stops being
// listed. worktree need not exist yet, only be the path the pool will be asked for.
func writeFakeTreehouseReturn(t *testing.T, worktree string) {
	t.Helper()
	bin := faketool.Bin(t)
	log := invocationLog(worktree)
	faketool.Treehouse{Held: []string{worktree}, Log: log}.Install(t, bin)
	faketool.Herdr{Log: log, Workspaces: []faketool.HerdrWorkspace{
		{ID: "wA", Label: "hand:myproj", Tabs: []faketool.HerdrTab{{ID: "wA:tB", Label: "task-1", Pane: "wA:pB"}}},
	}}.Install(t, bin)
}

func invocationLog(worktree string) string {
	return worktree + ".invocations"
}

func readInvocations(t *testing.T, worktree string) []string {
	t.Helper()
	data, err := os.ReadFile(invocationLog(worktree))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// One PR on the task branch, in the project's own repo.
type ghFakePR struct {
	Number int
	URL    string
	State  string
}

// The two calls gate-opened-PR detection makes: `gh pr list --repo <repo> --head <branch>`
// (FindPRByBranch) then `gh pr view <url> --json state` (project.ValidatePR, then checkLandedWork).
// Several PRs exercise FindPRByBranch's preference tier (atqamz/hand#77), not its single result.
func writeFakeGHPRListAndView(t *testing.T, prs ...ghFakePR) {
	t.Helper()
	g := faketool.GH{}
	for _, pr := range prs {
		g.PRs = append(g.PRs, faketool.GHPR{
			Number: pr.Number, URL: pr.URL, State: pr.State,
			Branch: "task-1-branch", Repo: "owner/repo",
		})
	}
	g.Install(t, faketool.Bin(t))
}

// Registers a non-local-only project whose clone has a GitHub origin remote (RepoSlug reads it) and
// re-points worktree's checked-out branch to branch, so FindPRByBranch's --head argument matches it.
func setupTeardownGateProject(t *testing.T, home, worktree, branch string) {
	t.Helper()
	runGitIn(t, worktree, "checkout", "-q", "-b", branch)
	registerGateProject(t, home)
}

// setupTeardownGateProject's clone-and-register half only, for tests that need to control the
// worktree's branch checkout themselves (to leave a diverging commit on main before switching to the
// task branch, say).
func registerGateProject(t *testing.T, home string) {
	t.Helper()
	clonePath := filepath.Join(home, "projects", "myproj")
	initGitRepo(t, clonePath)
	runGitIn(t, clonePath, "remote", "add", "origin", "https://github.com/owner/repo.git")

	if err := project.Add(home, project.Project{Name: "myproj", URL: "https://github.com/owner/repo.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
}

// The fork variant: the PR lives on the upstream while the branch lives in headRepo, so a search of
// the project's own repo comes back empty and the fork filter has a headRepository to read. Without a
// fake that narrows on --repo no test can express the atqamz/hand#134 shape at all.
func writeFakeGHForkPRListAndView(t *testing.T, upstream, headRepo string, pr ghFakePR) {
	t.Helper()
	faketool.GH{PRs: []faketool.GHPR{{
		Number: pr.Number, URL: pr.URL, State: pr.State,
		Branch: "task-1-branch", Repo: upstream, HeadRepo: headRepo,
	}}}.Install(t, faketool.Bin(t))
}

// The atqamz/hand#134 regression: a fork project's gate opens its PR on the declared upstream,
// so detection that searches the project's own repo alone finds nothing and teardown refuses landed
// work as unlanded.
func TestTeardownDetectsGateOpenedPRonDeclaredUpstream(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	if err := project.SetUpstream(home, "myproj", "up/repo"); err != nil {
		t.Fatal(err)
	}
	writeFakeGHForkPRListAndView(t, "up/repo", "owner/repo",
		ghFakePR{Number: 7, URL: "https://github.com/up/repo/pull/7", State: "MERGED"})

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want teardown to detect the upstream PR and succeed", err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state still exists after teardown: %v %v", exists, err)
	}
}

// Covers the self-declared upstream: GitHub slugs are case-insensitive, so an upstream naming the
// project's own repo in different casing must not be searched as a second repo - every PR would come
// back twice and refuse as its own same-tier duplicate, the failure this detection exists to remove.
func TestTeardownDetectsPRWithUpstreamDeclaredAsOwnRepoInOtherCasing(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	if err := project.SetUpstream(home, "myproj", "Owner/Repo"); err != nil {
		t.Fatal(err)
	}
	writeFakeGHForkPRListAndView(t, "owner/repo", "owner/repo",
		ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "MERGED"})

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want the differently-cased self-upstream to be searched once", err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state still exists after teardown: %v %v", exists, err)
	}
}

// First of the two regression cases atqamz/hand#69 requires: a no-mistakes gate's own `pr` step
// opens a PR directly, bypassing `hand pr`, so t.PR is empty even though the PR is merged and the work
// landed. Teardown must detect it by branch and tear down without --force, not refuse until forced.
func TestTeardownDetectsAndTearsDownGateOpenedMergedPR(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	writeFakeGHPRListAndView(t, ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "MERGED"})

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

// The second regression case: a detected PR that is closed without merging is not landed work, and the
// guard must still refuse it exactly as it would a `hand pr`-recorded one.
func TestTeardownRefusesGateOpenedClosedUnmergedPR(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	writeFakeGHPRListAndView(t, ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "CLOSED"})

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

// The atqamz/hand#77 landed case: a branch carrying a closed-unmerged PR alongside a merged one
// (a duplicate opened by mistake, say) must tear down on the merged PR, not fall to an arbitrary pick
// that could land on the unmerged one instead.
func TestTeardownTearsDownWhenBranchHasMergedAndClosedUnmergedPR(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	writeFakeGHPRListAndView(t,
		ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "CLOSED"},
		ghFakePR{Number: 5, URL: "https://github.com/owner/repo/pull/5", State: "MERGED"})

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want teardown to tear down on the merged PR", err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state still exists after teardown: %v %v", exists, err)
	}
}

// atqamz/hand#77's refusal case: two merged PRs on the same branch do not resolve to a winner,
// and teardown must refuse naming both rather than guess which one to trust - the exact guess that
// could wave through unlanded work.
func TestTeardownRefusesAmbiguousBranch(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	writeFakeGHPRListAndView(t,
		ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "MERGED"},
		ghFakePR{Number: 5, URL: "https://github.com/owner/repo/pull/5", State: "MERGED"})

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	var ambiguous *ghutil.AmbiguousPRError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got %v, want an AmbiguousPRError", err)
	}
	if !strings.Contains(err.Error(), "#9") || !strings.Contains(err.Error(), "#5") {
		t.Fatalf("got %q, want both PR numbers named", err.Error())
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PR != "" {
		t.Fatalf("task.PR = %q, want no PR recorded for an ambiguous branch", got.PR)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || !exists {
		t.Fatalf("state gone after refused teardown, want it left in place: %v %v", exists, err)
	}
}

// A branch carrying both a merged PR and a still-open one refuses instead of tearing down on the
// merged PR: the open PR is live evidence the branch may carry unlanded work.
func TestTeardownRefusesMergedAndOpenPR(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	setupTeardownGateProject(t, home, worktree, "task-1-branch")
	writeFakeGHPRListAndView(t,
		ghFakePR{Number: 5, URL: "https://github.com/owner/repo/pull/5", State: "MERGED"},
		ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "OPEN"})

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	var ambiguous *ghutil.AmbiguousPRError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got %v, want an AmbiguousPRError", err)
	}
	if !strings.Contains(err.Error(), "#5") || !strings.Contains(err.Error(), "#9") {
		t.Fatalf("got %q, want both PR numbers named", err.Error())
	}

	if exists, err := state.Exists(home, "task-1"); err != nil || !exists {
		t.Fatalf("state gone after refused teardown, want it left in place: %v %v", exists, err)
	}
}

func setupTeardownHome(t *testing.T) (home, worktree string) {
	t.Helper()
	home = t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	worktree = filepath.Join(t.TempDir(), "wt")
	initGitRepo(t, worktree)
	writeFakeTreehouseReturn(t, worktree)
	return home, worktree
}

// Puts the scout deliverable on disk for id.
func writeScoutReport(t *testing.T, home, id string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "data", id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", id, "report.md"), []byte("findings"), 0o644); err != nil {
		t.Fatal(err)
	}
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

// Teardown must survive a fault in its last step, which takes both halves of "retryable". Reverting
// either half fails this test: the ordering leaves nothing to retry, the idempotency leaves the retry
// dying on a tab herdr no longer lists.
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
	// state.Delete's removal order - report channel before the task row - leaves the task row untouched,
	// so there is something left to retry at all.
	if exists, err := state.Exists(home, "task-1"); err != nil || !exists {
		t.Fatalf("state gone after failed teardown, want it retryable: %v %v", exists, err)
	}

	if err := os.RemoveAll(reportPath); err != nil {
		t.Fatal(err)
	}

	// The retry re-runs the cleanup steps the first call already completed, which have to treat an
	// already-closed tab and an already-returned worktree as success.
	cmd = newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state still exists after retried teardown: %v %v", exists, err)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 || records[len(records)-1].Outcome != "merged" {
		t.Fatalf("completions = %+v, want task-1 recorded as merged", records)
	}
}

func TestTeardownRecordsCompletionBeforeStateRemoval(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	writeFakeGHPRState(t, "MERGED")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		PR: "https://example.com/pr/1", Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	before := time.Now().UTC().Truncate(time.Second)
	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("completions store has %d records, want 1: %+v", len(records), records)
	}
	got := records[0]
	torndownAt, err := time.Parse(time.RFC3339, got.TornDownAt)
	if err != nil {
		t.Fatalf("TornDownAt %q did not parse as RFC3339: %v", got.TornDownAt, err)
	}
	if torndownAt.Before(before) {
		t.Fatalf("TornDownAt %v predates teardown start %v", torndownAt, before)
	}
	got.TornDownAt = ""
	want := completion.Record{ID: "task-1", Project: "myproj", Kind: "ship", Outcome: "merged", Detail: "PR https://example.com/pr/1"}
	if got != want {
		t.Fatalf("record = %+v, want %+v", got, want)
	}
}

// The ordering in cmd/teardown.go must survive a fault in completion.Append the same way
// TestTeardownRetriesAfterReportRemovalFails covers state.Delete's report removal: state.Delete never
// runs, so the task stays retryable, and the retry leaves no duplicate record behind a failed line.
func TestTeardownCompletionAppendFailureLeavesStateIntact(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	writeFakeGHPRState(t, "MERGED")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		PR: "https://example.com/pr/1", Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	storePath := completion.Path(home)
	if err := os.MkdirAll(storePath, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "record completion") {
		t.Fatalf("got err %v, want a record completion failure", err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || !exists {
		t.Fatalf("state gone after failed teardown, want it retryable: %v %v", exists, err)
	}

	if err := os.Remove(storePath); err != nil {
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

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("completions store has %d records after the failed attempt and its retry, want 1: %+v", len(records), records)
	}
}

// Nothing retires a standing question on inference, but teardown is the
// operator deleting the task: the ID leaves the task list, so a question left
// behind here could never be retired by anything afterwards.
func TestTeardownRetiresTheTasksPendingQuestion(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	writeFakeGHPRState(t, "MERGED")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		PR: "https://example.com/pr/1", Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("needs-decision: which base branch?\n"), 0o644); err != nil {
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
	if _, err := os.Stat(state.ReportPath(home, "task-1")); !os.IsNotExist(err) {
		t.Fatalf("got report path err %v, want the question's report channel gone too", err)
	}
}

// An operator hold outliving its task row is the point of a hold. A limit hold is the
// opposite: with the row gone nothing will ever resume the worker and no watcher will
// clear the hold, so left behind it refuses hand spawn on this id forever.
func TestTeardownRetiresAMachineSetLimitHoldButNotAnOperatorsOwn(t *testing.T) {
	for _, tt := range []struct {
		kind      string
		wantAfter bool
	}{
		{kind: state.HoldKindLimit, wantAfter: false},
		{kind: state.HoldKindOperator, wantAfter: true},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			home, worktree := setupTeardownHome(t)
			writeFakeGHPRState(t, "MERGED")

			if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
				PR: "https://example.com/pr/1", Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
				t.Fatal(err)
			}
			if err := state.SetHold(home, state.Hold{ID: "task-1", Kind: tt.kind, Reason: "held"}); err != nil {
				t.Fatal(err)
			}

			cmd := newTeardownCmd()
			cmd.SetArgs([]string{"task-1"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}

			_, found, err := state.ReadHold(home, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if found != tt.wantAfter {
				t.Fatalf("hold present after teardown = %v, want %v for a %s hold", found, tt.wantAfter, tt.kind)
			}
		})
	}
}

func TestTeardownShipLocalOnlyFailsWhenBranchNotMerged(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
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

	writeFakeTreehouseReturn(t, worktree)
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
	writeScoutReport(t, home, "task-1")
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

// atqamz/hand#129: a scout spawned without --scout is recorded as a ship task and nothing can
// correct the record, so its report-and-no-PR shape hits the landed-work refusal and --force plus a
// respawn was the only way out. Teardown reads the work instead, and the permanent record says scout.
func TestTeardownAcceptsAShipRowThatDeliveredAScoutReport(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	runGitIn(t, worktree, "checkout", "-q", "-b", "task-1-branch")
	writeScoutReport(t, home, "task-1")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("completions = %+v, want one record", records)
	}
	if records[0].Kind != state.KindScout || records[0].Outcome != "done" {
		t.Fatalf("completion = %+v, want kind scout outcome done", records[0])
	}
}

// The half of atqamz/hand#129's fix that matters: the scout-deliverable path must not become "no PR
// and some file exists". This task carries a report next to a commit nobody landed, and the commit is
// what keeps the refusal. A guard that only checked for the report would throw the commit away.
func TestTeardownStillRefusesAShipTaskWhosePRWasNeverOpened(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	runGitIn(t, worktree, "checkout", "-q", "-b", "task-1-branch")
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("unlanded"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, worktree, "add", "feature.txt")
	runGitIn(t, worktree, "commit", "-q", "-m", "feature")
	writeScoutReport(t, home, "task-1")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "work may not be landed") {
		t.Fatalf("got err %v, want the landed-work refusal", err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || !exists {
		t.Fatalf("state gone after a refused teardown: %v %v", exists, err)
	}
}

// A promoted scout keeps its report on disk while its row turns into a ship, so a task that then merged
// locally has every shape the scout-deliverable path reads: report present, no PR, and a branch that was
// fast-forwarded in and so adds no commit. It landed as a merge - the inverse of atqamz/hand#78.
func TestTeardownRecordsMergedWhenALocallyMergedShipRowKeptItsScoutReport(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	clonePath := filepath.Join(home, "projects", "myproj")
	initGitRepo(t, clonePath)
	worktree := filepath.Join(t.TempDir(), "wt")
	writeFakeTreehouseReturn(t, worktree)
	runGitIn(t, clonePath, "branch", "task-1-branch")
	runGitIn(t, clonePath, "worktree", "add", "-q", worktree, "task-1-branch")
	if err := project.Add(home, project.Project{Name: "myproj", URL: "https://example.com/myproj.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	writeScoutReport(t, home, "task-1")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		MergeExecuted: true, MergeExecutedAt: "2026-08-04T00:00:00Z",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("completions = %+v, want one record", records)
	}
	if records[0].Kind != state.KindShip || records[0].Outcome != "merged" {
		t.Fatalf("completion = %+v, want kind ship outcome merged", records[0])
	}
}

// Merge evidence excludes the scout-deliverable path on its own, wherever the row came from: a task hand
// merged is not a report, so it never reaches the completion store as one. Nothing else can confirm this
// landing without a PR, so the ordinary refusal stands and the row survives for the operator to resolve.
func TestTeardownStillRefusesAMergedShipRowWithNoPRToConfirm(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	runGitIn(t, worktree, "checkout", "-q", "-b", "task-1-branch")
	writeScoutReport(t, home, "task-1")

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		MergeExecuted: true, MergeExecutedAt: "2026-08-04T00:00:00Z",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "work may not be landed") {
		t.Fatalf("got err %v, want the landed-work refusal", err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || !exists {
		t.Fatalf("state gone after a refused teardown: %v %v", exists, err)
	}
}

// The central case for atqamz/hand#78: a contribution offered to a repo this fleet does not control.
// Landing it is the upstream maintainer's decision, so the PR stays open indefinitely and the fake gh
// reports that. Teardown accepts it without --force and records delivered - a merge nobody made is the risk.
func TestTeardownAcceptsDeliveredWorkWithAnOpenPRWithoutForce(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	writeFakeGHPRState(t, "OPEN")

	pr := "https://github.com/kunchenguid/no-mistakes/pull/597"
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		PR: pr, DeliveredAt: "2026-08-03T00:00:00Z", DeliveredReason: "offered upstream, maintainer decides",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("teardown of delivered work: %v", err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state still exists after teardown: %v %v", exists, err)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("completions = %+v, want exactly one", records)
	}
	// Claiming a merge nobody made is the failure the delivered state exists to prevent.
	if records[0].Outcome != "delivered" {
		t.Fatalf("outcome = %q, want delivered and never merged", records[0].Outcome)
	}
	if !strings.Contains(records[0].Detail, pr) || !strings.Contains(records[0].Detail, "maintainer decides") {
		t.Fatalf("detail = %q, want the PR and the recorded reason", records[0].Detail)
	}
}

// A task filed as a ship whose deliverable was a report, no branch and no commit behind it - the shape
// a misfiled kind produces (atqamz/hand#129). The delivered state keys off the delivery, not off
// Kind, so this tears down cleanly without anyone having to correct the kind first.
func TestTeardownAcceptsDeliveredWorkWithNoPRRegardlessOfKind(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		DeliveredAt: "2026-08-03T00:00:00Z", DeliveredReason: "report at data/task-1/report.md, no code to land",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("teardown of delivered work with no PR: %v", err)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Outcome != "delivered" {
		t.Fatalf("completions = %+v, want one delivered record", records)
	}
	if records[0].Detail != "report at data/task-1/report.md, no code to land" {
		t.Fatalf("detail = %q, want the reason alone when no PR is recorded", records[0].Detail)
	}
}

// Delivery says the work left the fleet, not that whatever is still sitting in
// the worktree is safe to throw away. --force keeps that one meaning.
func TestTeardownStillRefusesUncommittedChangesOnDeliveredWork(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	if err := os.WriteFile(filepath.Join(worktree, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		DeliveredAt: "2026-08-03T00:00:00Z", DeliveredReason: "offered upstream"}); err != nil {
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

// Delivered and then genuinely landed: the record has to say merged, because the
// merge is the stronger fact and the delivered mark exists only to stop the record
// claiming a merge that never happened, not to hide one that did.
func TestTeardownRecordsMergedWhenDeliveredWorkActuallyLanded(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	writeFakeGHPRState(t, "MERGED")

	pr := "https://github.com/kunchenguid/no-mistakes/pull/597"
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		PR: pr, DeliveredAt: "2026-08-03T00:00:00Z", DeliveredReason: "offered upstream, maintainer decides",
		MergeExecuted: true, MergeExecutedAt: "2026-08-04T00:00:00Z",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("teardown of delivered work that landed: %v", err)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Outcome != "merged" {
		t.Fatalf("completions = %+v, want one merged record once the work actually landed", records)
	}
	if records[0].Detail != "PR "+pr {
		t.Fatalf("detail = %q, want the merged PR", records[0].Detail)
	}
}

// The watcher's observed merge counts the same as one hand executed: either way
// the work landed, so the record must not stop at delivered.
func TestTeardownRecordsMergedWhenAnObservedMergeFollowedDelivery(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	writeFakeGHPRState(t, "MERGED")

	pr := "https://github.com/kunchenguid/no-mistakes/pull/597"
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		PR: pr, DeliveredAt: "2026-08-03T00:00:00Z", DeliveredReason: "offered upstream",
		MergeAnnounced: true,
		Herdr:          state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("teardown of delivered work with an observed merge: %v", err)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Outcome != "merged" {
		t.Fatalf("completions = %+v, want one merged record for an observed merge", records)
	}
}

// A scout row claiming delivery with nothing on disk has delivered nothing, so
// the report check ahead of the delivered short-circuit still refuses it.
func TestTeardownStillRefusesDeliveredScoutWithNoReport(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout, Worktree: worktree,
		DeliveredAt: "2026-08-03T00:00:00Z", DeliveredReason: "handed over"}); err != nil {
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
	writeScoutReport(t, home, "task-1")
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindScout, Worktree: worktree,
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(t.TempDir(), "herdr-called")
	bin := t.TempDir()
	// This and the herdr fakes further down this file all return a non-null object result for "tab
	// list"/"tab close"/"workspace close", exactly what call() requires for success (client.go). All
	// three are query commands, not callVoid's void pane commands, so no exit-code split to reproduce.
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
	writeScoutReport(t, home, "task-1")

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

func writeAndCommit(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, dir, "add", name)
	runGitIn(t, dir, "commit", "-q", "-m", message)
}

// Creates task-1-branch off the worktree's current HEAD, then advances main past it with a further
// commit to readmeContent - the gate fix landing on main after the task branch forked - and leaves the
// worktree checked out on task-1-branch, still at the pre-fix commit.
func diverge(t *testing.T, worktree, readmeContent string) {
	t.Helper()
	runGitIn(t, worktree, "branch", "task-1-branch")
	writeAndCommit(t, worktree, "README.md", readmeContent, "gate fix lands on main")
	runGitIn(t, worktree, "checkout", "-q", "task-1-branch")
}

// atqamz/hand#79's safe case: the worktree's uncommitted edit to README.md reproduces
// byte-for-byte content main already carries (the no-mistakes gate's own re-edit of a file its merged
// fix already covers), so discarding it on teardown loses nothing.
func TestTeardownProceedsWhenDirtAlreadyMatchesMergedBase(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	diverge(t, worktree, "fixed")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixed"), 0o644); err != nil {
		t.Fatal(err)
	}
	registerGateProject(t, home)
	writeFakeGHPRListAndView(t, ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "MERGED"})

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want teardown to proceed past dirt that already matches the merged base", err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state still exists after teardown: %v %v", exists, err)
	}
}

// Two tasks share a workspace and the first one's teardown already closed its tab,
// which is where an unguarded rerun does real damage: one tab is left, the sole-tab
// branch fires, and the surviving task's workspace is closed under it.
func TestCloseTaskTabRerunLeavesASharedWorkspaceAlone(t *testing.T) {
	bin := faketool.Bin(t)
	log := filepath.Join(bin, "herdr.log")
	faketool.Herdr{Log: log, Workspaces: []faketool.HerdrWorkspace{
		{ID: "wA", Label: "hand:myproj", Tabs: []faketool.HerdrTab{
			{ID: "wA:t1", Label: "task-1", Pane: "wA:p1"},
			{ID: "wA:t2", Label: "task-2", Pane: "wA:p2"},
		}},
	}}.Install(t, bin)
	client := herdr.NewClient()

	if err := closeTaskTab(client, "wA", "wA:t1"); err != nil {
		t.Fatal(err)
	}
	if err := closeTaskTab(client, "wA", "wA:t1"); err != nil {
		t.Fatalf("got %v, want a rerun over an already-closed tab to succeed", err)
	}

	tabs, err := client.TabList("wA")
	if err != nil {
		t.Fatalf("got %v, want the shared workspace still open", err)
	}
	if len(tabs) != 1 || tabs[0].TabID != "wA:t2" {
		t.Fatalf("got tabs %+v, want the other task's tab untouched", tabs)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "workspace close") {
		t.Fatalf("herdr log = %q, want no workspace close: the workspace still holds task-2", data)
	}
}

// The argv of the last `treehouse return` of worktree, from the shared fakes'
// invocation log.
func readTreehouseReturnArgs(t *testing.T, worktree string) []string {
	t.Helper()
	var args []string
	for _, line := range readInvocations(t, worktree) {
		if fields := strings.Fields(line); len(fields) > 1 && fields[1] == "return" {
			args = fields[1:]
		}
	}
	if args == nil {
		t.Fatalf("treehouse return was never invoked for %s", worktree)
	}
	return args
}

// The step that follows the safe-dirt decision: treehouse will not clean a dirty worktree unprompted,
// and nothing here can answer its prompt, so a worktree teardown itself judged safe to discard has to
// be returned with --force even though the operator passed no --force flag.
func TestTeardownForcesWorktreeReturnPastSafeDirt(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	diverge(t, worktree, "fixed")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixed"), 0o644); err != nil {
		t.Fatal(err)
	}
	registerGateProject(t, home)
	writeFakeGHPRListAndView(t, ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "MERGED"})

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want teardown to force the return of a worktree it judged safe", err)
	}
	// Without it the pool slot goes back dirty, or the return aborts after the task's tab is closed.
	args := readTreehouseReturnArgs(t, worktree)
	if !slices.Contains(args, "--force") {
		t.Fatalf("treehouse return args = %v, want --force so the safe dirt is actually cleaned", args)
	}
}

// The counterpart: --force is the safe-dirt path's own doing, not something every teardown hands
// treehouse. A clean worktree keeps the ordinary unforced return, so treehouse's own guard still
// stands between teardown and any dirt this command never inspected.
func TestTeardownReturnsCleanWorktreeUnforced(t *testing.T) {
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
	args := readTreehouseReturnArgs(t, worktree)
	if slices.Contains(args, "--force") {
		t.Fatalf("treehouse return args = %v, want no --force for a worktree that was never dirty", args)
	}
}

// Pins the ref resolution a real treehouse worktree takes: it has refs/remotes/origin/HEAD, so the
// base is origin's tip and not whatever the local default branch head points at. Here local main has
// moved past origin/main, and only reading origin/main makes the dirt safe.
func TestTeardownProceedsWhenDirtMatchesOriginDefaultBranchTip(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	runGitIn(t, worktree, "branch", "task-1-branch")
	writeAndCommit(t, worktree, "README.md", "fixed", "gate fix lands on main")
	runGitIn(t, worktree, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	runGitIn(t, worktree, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	writeAndCommit(t, worktree, "README.md", "local main moved on", "unfetched local commit")
	runGitIn(t, worktree, "checkout", "-q", "task-1-branch")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixed"), 0o644); err != nil {
		t.Fatal(err)
	}
	registerGateProject(t, home)
	writeFakeGHPRListAndView(t, ghFakePR{Number: 9, URL: "https://github.com/owner/repo/pull/9", State: "MERGED"})

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want teardown to compare against origin's default branch tip", err)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state still exists after teardown: %v %v", exists, err)
	}
}

// The index half of the safety check: an "MM" path carries a third version in the index, and a working
// copy that matches the base says nothing about it. Comparing only the file on disk would discard that
// staged content.
func TestTeardownRefusesDirtWhenStagedContentDiffersFromBase(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	diverge(t, worktree, "fixed")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("a staged third version"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, worktree, "add", "README.md")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixed"), 0o644); err != nil {
		t.Fatal(err)
	}
	registerGateProject(t, home)

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("got %v, want the uncommitted changes refusal for a differing staged blob", err)
	}
}

// The counter-proof the brief asks for: README.md exists in base under the same path (both weaker
// checks - "the file exists in the base" and "the paths match" - would pass this), but its content
// differs from the worktree's uncommitted edit, so it must still refuse.
func TestTeardownRefusesDirtWhenContentDiffersFromBase(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	diverge(t, worktree, "fixed")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("a different fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	registerGateProject(t, home)

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("got %v, want the uncommitted changes refusal", err)
	}
}

// An untracked file blocks on its own even when every tracked change is safe: there is nothing in the
// base to compare an untracked file against, so its mere presence must refuse, and name it.
func TestTeardownRefusesDirtWithUntrackedFileEvenWhenTrackedChangeMatchesBase(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	diverge(t, worktree, "fixed")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "scratch.txt"), []byte("leftover"), 0o644); err != nil {
		t.Fatal(err)
	}
	registerGateProject(t, home)

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj",
		Herdr: state.Herdr{WorkspaceID: "wA", TabID: "wA:tB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "scratch.txt") {
		t.Fatalf("got %v, want the untracked file named in the refusal", err)
	}
}

// The refusal's git status dump is bounded (atqamz/hand#65 is the same lesson for report
// rendering): the first 20 entries print, the rest collapse to a count.
func TestTeardownRefusalCapsGitStatusOutput(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("untracked-%02d.txt", i)
		if err := os.WriteFile(filepath.Join(worktree, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj"}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "untracked-00.txt") {
		t.Fatalf("got %v, want the first entry present", err)
	}
	if strings.Contains(err.Error(), "untracked-24.txt") {
		t.Fatalf("got %v, want entries past the cap omitted", err)
	}
	if !strings.Contains(err.Error(), "...and 5 more") {
		t.Fatalf("got %v, want a count of the remaining entries", err)
	}
}
