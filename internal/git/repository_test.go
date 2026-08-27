package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultBranchUsesLocalMarkerWithoutOrigin(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	runGit(t, dir, "init", "-q", "-b", "trunk")
	runGit(t, dir, "-c", "user.name=git-test", "-c", "user.email=git-test@example.invalid", "commit", "-q", "--allow-empty", "-m", "baseline")
	runGit(t, dir, "update-ref", "refs/remotes/origin/trunk", "HEAD")
	runGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")

	got, err := DefaultBranch(dir)
	if err != nil || got != "trunk" {
		t.Fatalf("DefaultBranch() = %q, %v, want trunk", got, err)
	}
	ref, err := LocalDefaultBranchRef(dir)
	if err != nil || ref != "origin/trunk" {
		t.Fatalf("LocalDefaultBranchRef() = %q, %v, want origin/trunk", ref, err)
	}
}

func TestCommonDirReturnsTheMainRepositoryForAWorktree(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "-c", "user.name=git-test", "-c", "user.email=git-test@example.invalid", "commit", "-q", "--allow-empty", "-m", "baseline")
	worktree := filepath.Join(t.TempDir(), "worktree")
	runGit(t, repo, "worktree", "add", "-q", "-b", "feature", worktree)

	got, err := CommonDir(worktree)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(repo, ".git")
	if !SamePath(got, want) {
		t.Fatalf("CommonDir() = %q, want %q", got, want)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if len(args) > 1 && args[0] == "init" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
