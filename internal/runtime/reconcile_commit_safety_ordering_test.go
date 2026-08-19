package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

const commitSafetyPR = "https://github.com/atqamz/hand/pull/7"

type squashMergedWorktree struct {
	path       string
	pushedHead string
}

func squashMergedFixture(t *testing.T, localOnly int) squashMergedWorktree {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "worktree")
	faketool.InitRepo(t, path)
	remote := filepath.Join(root, "remote.git")
	runRuntimeGit(t, root, "init", "-q", "--bare", remote)
	runRuntimeGit(t, path, "remote", "add", "origin", remote)
	runRuntimeGit(t, path, "push", "-q", "-u", "origin", "main")
	runRuntimeGit(t, path, "checkout", "-q", "-b", "topic")
	commitFixtureFile(t, path, "feature")
	runRuntimeGit(t, path, "push", "-q", "-u", "origin", "topic")
	pushedHead := gitOutput(t, path, "rev-parse", "HEAD")
	runRuntimeGit(t, path, "checkout", "-q", "main")
	runRuntimeGit(t, path, "merge", "-q", "--squash", "topic")
	runRuntimeGit(t, path, "commit", "-q", "-m", "squash topic")
	runRuntimeGit(t, path, "push", "-q", "origin", "main")
	runRuntimeGit(t, path, "checkout", "-q", "topic")
	for i := 0; i < localOnly; i++ {
		commitFixtureFile(t, path, fmt.Sprintf("local-only-%d", i))
	}
	return squashMergedWorktree{path: path, pushedHead: pushedHead}
}

func commitFixtureFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
	runRuntimeGit(t, dir, "add", name)
	runRuntimeGit(t, dir, "commit", "-q", "-m", "add "+name)
}

func deleteHeadBranchAndPrune(t *testing.T, fixture squashMergedWorktree) {
	t.Helper()
	runRuntimeGit(t, fixture.path, "push", "-q", "origin", "--delete", "topic")
	runRuntimeGit(t, fixture.path, "fetch", "-q", "--prune", "origin")
	survivor := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/remotes/origin/topic")
	survivor.Dir = fixture.path
	if err := survivor.Run(); err == nil {
		t.Fatal("refs/remotes/origin/topic survived the prune, so the fixture never reaches the state under test")
	}
	if observed := worktree.ObserveCommitSafety(fixture.path); observed.State != worktree.CommitSafetyUnknown || observed.Probe.RemoteRefs == 0 {
		t.Fatalf("observation = %+v, want the remote-tracking comparison unable to answer while other remote-tracking refs survive", observed)
	}
}

func pruningRuntime(t *testing.T, fixture squashMergedWorktree) (*Runtime, *commitSafetyProbeCount) {
	t.Helper()
	r, counts := observingCommitSafetyRuntime(t, fixture.path, worktree.ObserveCommitSafety)
	r.deps.worktree.observeClean = worktree.ObserveCleanliness
	return r, counts
}

func assertWorktreeReturned(t *testing.T, home string, counts *commitSafetyProbeCount) {
	t.Helper()
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if counts.returns != 1 || history.Attempts[0].TeardownWorktreeState != state.TeardownResourceReleased || history.Task.RepairCode != "" {
		t.Fatalf("returns=%d attempt=%+v task=%+v, want the worktree proven durable and returned", counts.returns, history.Attempts[0], history.Task)
	}
}

func assertWorktreeWithheld(t *testing.T, home string, counts *commitSafetyProbeCount, wantRepairCode string) {
	t.Helper()
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != wantRepairCode || counts.returns != 0 {
		t.Fatalf("task=%+v returns=%d, want %q recorded and no return", history.Task, counts.returns, wantRepairCode)
	}
	if history.Attempts[0].TeardownWorktreeState != "" {
		t.Fatalf("worktree state = %q, want no cleanup recorded", history.Attempts[0].TeardownWorktreeState)
	}
}

func TestReconcileReturnsSquashMergedWorktreeAfterTheStaleRemoteTrackingRefIsPruned(t *testing.T) {
	fixture := squashMergedFixture(t, 0)
	deleteHeadBranchAndPrune(t, fixture)
	home, _ := commitSafetyTaskAt(t, fixture.path, commitSafetyPR, true)
	r, counts := pruningRuntime(t, fixture)
	r.deps.prHead = observedPRHead(fixture.pushedHead)
	reconcileConverges(t, r, home)
	assertWorktreeReturned(t, home, counts)
}

func TestReconcileWithholdsPrunedWorktreeWithNoPullRequestEvidence(t *testing.T) {
	fixture := squashMergedFixture(t, 0)
	deleteHeadBranchAndPrune(t, fixture)
	home, _ := commitSafetyTaskAt(t, fixture.path, "", false)
	r, counts := pruningRuntime(t, fixture)
	reconcileNeedsRepair(t, r, home)
	assertWorktreeWithheld(t, home, counts, repairCodeWorktreeCommitSafetyUnknown)
}

func TestReconcileWithholdsPrunedWorktreeCarryingALocalOnlyCommit(t *testing.T) {
	fixture := squashMergedFixture(t, 1)
	deleteHeadBranchAndPrune(t, fixture)
	head := gitOutput(t, fixture.path, "rev-parse", "HEAD")
	if head == fixture.pushedHead {
		t.Fatal("the fixture head equals the pushed head, so it carries no local-only commit to lose")
	}
	home, _ := commitSafetyTaskAt(t, fixture.path, commitSafetyPR, true)
	r, counts := pruningRuntime(t, fixture)
	r.deps.prHead = observedPRHead(fixture.pushedHead)
	reconcileNeedsRepair(t, r, home)
	assertWorktreeWithheld(t, home, counts, repairCodeWorktreeCommitSafetyUnknown)
	if kept := gitOutput(t, fixture.path, "rev-parse", "HEAD"); kept != head {
		t.Fatalf("head = %s, want the local-only commit still held at %s", kept, head)
	}
}

func TestReconcileWithholdsCommitMadeAfterTheRecordedPullRequestHead(t *testing.T) {
	fixture := squashMergedFixture(t, 1)
	home, _ := commitSafetyTaskAt(t, fixture.path, commitSafetyPR, true)
	r, counts := pruningRuntime(t, fixture)
	r.deps.prHead = observedPRHead(fixture.pushedHead)
	reconcileNeedsRepair(t, r, home)
	assertWorktreeWithheld(t, home, counts, repairCodeWorktreeLocalCommits)
}

func TestReconcileWithholdsPrunedWorktreeWhenGitHubCannotBeReached(t *testing.T) {
	fixture := squashMergedFixture(t, 0)
	deleteHeadBranchAndPrune(t, fixture)
	home, _ := commitSafetyTaskAt(t, fixture.path, commitSafetyPR, true)
	r, counts := pruningRuntime(t, fixture)
	r.deps.prHead = unobservedPR("gh pr view failed: dial tcp: lookup api.github.com: no such host")
	reconcileNeedsRepair(t, r, home)
	assertWorktreeWithheld(t, home, counts, repairCodeWorktreeCommitSafetyUnknown)
}

func TestCommitSafetyAnswerSurvivesThePruneThatChangesWhichSourceProvesIt(t *testing.T) {
	fixture := squashMergedFixture(t, 0)
	if observed := worktree.ObserveCommitSafety(fixture.path); observed.State != worktree.CommitSafetyRemoteObserved {
		t.Fatalf("observation = %+v, want the stale remote-tracking ref proving durability before the prune", observed)
	}
	before, _ := commitSafetyTaskAt(t, fixture.path, commitSafetyPR, true)
	cached, cachedCounts := pruningRuntime(t, fixture)
	reconcileConverges(t, cached, before)
	assertWorktreeReturned(t, before, cachedCounts)

	deleteHeadBranchAndPrune(t, fixture)

	after, _ := commitSafetyTaskAt(t, fixture.path, commitSafetyPR, true)
	observed, observedCounts := pruningRuntime(t, fixture)
	asked := 0
	observed.deps.prHead = func(ctx context.Context, pr string) ghutil.PRObservation {
		asked++
		return observedPRHead(fixture.pushedHead)(ctx, pr)
	}
	reconcileConverges(t, observed, after)
	assertWorktreeReturned(t, after, observedCounts)
	if asked != 1 {
		t.Fatalf("pull request head observations = %d, want the pruned clone to prove durability from GitHub exactly once", asked)
	}
}
