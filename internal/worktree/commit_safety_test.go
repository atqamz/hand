package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func commitFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-q", "-m", "add "+name)
}

func pushedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	worktreePath := filepath.Join(root, "worktree")
	faketool.InitRepo(t, worktreePath)
	remote := filepath.Join(root, "remote.git")
	runGit(t, root, "init", "-q", "--bare", remote)
	runGit(t, worktreePath, "remote", "add", "origin", remote)
	runGit(t, worktreePath, "push", "-q", "-u", "origin", "main")
	return worktreePath
}

func TestObserveCommitSafetyProvesDurabilityFromRemoteTrackingRefs(t *testing.T) {
	worktreePath := pushedRepo(t)
	observation := ObserveCommitSafety(worktreePath)
	if observation.State != CommitSafetyRemoteObserved {
		t.Fatalf("observation = %+v, want a pushed worktree proven durable", observation)
	}
	if observation.Probe.LocalOnly != 0 || observation.Probe.RemoteRefs == 0 || len(observation.Probe.Head) != 40 {
		t.Fatalf("probe = %+v, want the compared head and remote-tracking refs recorded", observation.Probe)
	}
}

func TestObserveCommitSafetyCountsCommitsHeldOnlyInTheWorktree(t *testing.T) {
	worktreePath := pushedRepo(t)
	commitFile(t, worktreePath, "one")
	commitFile(t, worktreePath, "two")
	observation := ObserveCommitSafety(worktreePath)
	if observation.State != CommitSafetyLocalOnly || observation.Probe.LocalOnly != 2 {
		t.Fatalf("observation = %+v, want both unpushed commits counted", observation)
	}
}

func TestObserveCommitSafetyReportsUnknownForEveryUnobservableComparison(t *testing.T) {
	t.Run("no remote configured", func(t *testing.T) {
		worktreePath := filepath.Join(t.TempDir(), "worktree")
		faketool.InitRepo(t, worktreePath)
		observation := ObserveCommitSafety(worktreePath)
		if observation.State != CommitSafetyUnknown || observation.Probe.RemoteRefs != 0 {
			t.Fatalf("observation = %+v, want unknown with no remote-tracking ref to compare against", observation)
		}
		if !strings.Contains(observation.Probe.Reason, "no remote-tracking ref") {
			t.Fatalf("reason = %q, want the absent remote-tracking refs named", observation.Probe.Reason)
		}
	})
	t.Run("pruned remote-tracking ref", func(t *testing.T) {
		worktreePath := pushedRepo(t)
		runGit(t, worktreePath, "push", "-q", "origin", "main:refs/heads/survivor")
		runGit(t, worktreePath, "fetch", "-q", "origin")
		commitFile(t, worktreePath, "after-push")
		runGit(t, worktreePath, "update-ref", "-d", "refs/remotes/origin/main")
		observation := ObserveCommitSafety(worktreePath)
		if observation.State != CommitSafetyUnknown || observation.Probe.RemoteRefs == 0 {
			t.Fatalf("observation = %+v, want unknown while other remote-tracking refs still exist", observation)
		}
		if !strings.Contains(observation.Probe.Reason, "origin/main") {
			t.Fatalf("reason = %q, want the pruned upstream named", observation.Probe.Reason)
		}
	})
	t.Run("no repository at the recorded path", func(t *testing.T) {
		observation := ObserveCommitSafety(filepath.Join(t.TempDir(), "gone"))
		if observation.State != CommitSafetyUnknown || !strings.Contains(observation.Probe.Reason, "resolve worktree HEAD") {
			t.Fatalf("observation = %+v, want unknown naming the failed head resolution", observation)
		}
	})
	t.Run("no worktree recorded", func(t *testing.T) {
		observation := ObserveCommitSafety("")
		if observation.State != CommitSafetyUnknown || observation.Probe.Command != LocalOnlyCommitCommand {
			t.Fatalf("observation = %+v, want unknown carrying the probe it would have run", observation)
		}
	})
}
