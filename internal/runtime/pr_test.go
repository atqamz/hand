package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
)

const prDetectionRepo = "atqamz/detach-fixture"

// A worktree whose HEAD has been detached from branch after committing to it, the exact state a
// worker leaves behind deleting its feature branch post-merge (atqamz/hand#284).
func detachedHeadWorktreeFixture(t *testing.T, branch string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "worktree")
	faketool.InitRepo(t, path)
	runRuntimeGit(t, path, "checkout", "-q", "-b", branch)
	commitFixtureFile(t, path, "feature")
	head := gitOutput(t, path, "rev-parse", "HEAD")
	runRuntimeGit(t, path, "checkout", "-q", head)
	return path
}

func prDetectionProjectFixture(t *testing.T, home string) project.Project {
	t.Helper()
	name := "demo"
	clonePath := filepath.Join(home, "projects", name)
	if err := os.MkdirAll(clonePath, 0o755); err != nil {
		t.Fatal(err)
	}
	runRuntimeGit(t, clonePath, "init", "-q")
	runRuntimeGit(t, clonePath, "remote", "add", "origin", "https://github.com/"+prDetectionRepo+".git")
	return project.Project{Name: name}
}

func TestObservePRFindsAMergedPRByTheDurablyStoredBranchAfterDetachedHead(t *testing.T) {
	home := t.TempDir()
	worktreePath := detachedHeadWorktreeFixture(t, "topic")
	proj := prDetectionProjectFixture(t, home)
	faketool.GH{PRs: []faketool.GHPR{{
		Number: 9, URL: "https://github.com/" + prDetectionRepo + "/pull/9",
		Branch: "topic", State: "MERGED", Repo: prDetectionRepo,
	}}}.Install(t, faketool.Bin(t))

	active := state.Attempt{Worktree: worktreePath, Branch: "topic"}
	observation := DetectPRReadOnly(context.Background(), home, active, proj)
	if !observation.Found() || observation.URL != "https://github.com/"+prDetectionRepo+"/pull/9" || !observation.Merged {
		t.Fatalf("observation = %+v, want the durably stored branch to find the merged PR", observation)
	}
}

func TestDetectPRUsesTheRecordedPRAfterDetachedHead(t *testing.T) {
	worktreePath := detachedHeadWorktreeFixture(t, "topic")
	pr := "https://github.com/atqamz/detach-fixture/pull/9"
	active := state.Attempt{Worktree: worktreePath}

	detected, observation, err := DetectPR(context.Background(), t.TempDir(), state.Task{ID: "task-1", PR: pr}, active, project.Project{})
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Found() || observation.URL != pr {
		t.Fatalf("observation = %+v, want recorded PR %s", observation, pr)
	}
	if detected.PR != pr {
		t.Fatalf("detected task PR = %q, want %q", detected.PR, pr)
	}
}

func TestObservePRReportsUnknownNotAbsentOnDetachedHeadWithNoBranchHistory(t *testing.T) {
	home := t.TempDir()
	worktreePath := detachedHeadWorktreeFixture(t, "topic")
	proj := prDetectionProjectFixture(t, home)
	faketool.GH{}.Install(t, faketool.Bin(t))

	active := state.Attempt{Worktree: worktreePath}
	observation := DetectPRReadOnly(context.Background(), home, active, proj)
	if observation.Absent() {
		t.Fatalf("observation = %+v, want unknown: GitHub was never actually asked, so absent is a false claim", observation)
	}
	if !observation.Unknown() {
		t.Fatalf("observation = %+v, want unknown", observation)
	}
}

// The regression test from atqamz/hand#284: a PR whose head ref is the literal string "HEAD"
// would fool the pre-fix code (git rev-parse --abbrev-ref HEAD returns that exact sentinel on a
// detached checkout), so this asserts the caller never sends it to gh at all.
func TestObservePRNeverSearchesGitHubForTheLiteralSentinelHEAD(t *testing.T) {
	home := t.TempDir()
	worktreePath := detachedHeadWorktreeFixture(t, "topic")
	proj := prDetectionProjectFixture(t, home)
	logPath := filepath.Join(t.TempDir(), "gh.log")
	faketool.GH{
		PRs: []faketool.GHPR{{
			Number: 1, URL: "https://github.com/" + prDetectionRepo + "/pull/1",
			Branch: "HEAD", State: "MERGED", Repo: prDetectionRepo,
		}},
		Log: logPath,
	}.Install(t, faketool.Bin(t))

	active := state.Attempt{Worktree: worktreePath}
	observation := DetectPRReadOnly(context.Background(), home, active, proj)
	if observation.Found() {
		t.Fatalf("observation = %+v, want no PR found: a caller bug would search --head HEAD and wrongly match this trap PR", observation)
	}
	if !observation.Unknown() {
		t.Fatalf("observation = %+v, want unknown when no branch is determinable", observation)
	}
	log, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "--head HEAD") {
		t.Fatalf("gh invocation log = %q, want the literal sentinel HEAD never sent to gh as a branch name", log)
	}
}
