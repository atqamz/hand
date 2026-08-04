package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/project"
)

func runGitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, out)
	}
}

// Creates a bare-ish remote repo and a clone of it, with the clone's origin/HEAD already resolvable (as a
// normal `git clone` sets up).
func setupSyncProject(t *testing.T) (clonePath, remotePath string) {
	t.Helper()
	remotePath = filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remotePath)

	clonePath = filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-q", remotePath, clonePath).CombinedOutput(); err != nil {
		t.Fatalf("git clone failed: %v: %s", err, out)
	}
	return clonePath, remotePath
}

func TestSyncOneProjectUpToDate(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	clonePath, _ := setupSyncProject(t)
	movedClone := filepath.Join(home, "projects", "myproj")
	if err := os.Rename(clonePath, movedClone); err != nil {
		t.Fatal(err)
	}

	got, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != "up-to-date" || got.Detail != "" {
		t.Fatalf("outcome = %+v, want an up-to-date result with nothing to explain", got)
	}
}

func TestSyncOneProjectFastForwards(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	clonePath, remotePath := setupSyncProject(t)
	movedClone := filepath.Join(home, "projects", "myproj")
	if err := os.Rename(clonePath, movedClone); err != nil {
		t.Fatal(err)
	}
	clonePath = movedClone

	if err := os.WriteFile(filepath.Join(remotePath, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, remotePath, "add", "new.txt")
	runGitIn(t, remotePath, "commit", "-q", "-m", "add new file")

	got, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != "fast-forwarded" || !strings.Contains(got.Detail, "was 1 behind") {
		t.Fatalf("outcome = %+v, want fast-forwarded and how far behind it was", got)
	}
	if _, err := os.Stat(filepath.Join(clonePath, "new.txt")); err != nil {
		t.Fatalf("clone did not fast-forward: %v", err)
	}
}

func TestSyncOneProjectSkipsDirtyWorkingTree(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	clonePath, _ := setupSyncProject(t)
	movedClone := filepath.Join(home, "projects", "myproj")
	if err := os.Rename(clonePath, movedClone); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(movedClone, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != "skipped" || got.Detail != "dirty working tree" {
		t.Fatalf("outcome = %+v, want a skip naming the dirty working tree", got)
	}
}

// A clone registered before hand excluded the pool config: registration is over,
// so sync is the only thing left that can stop it reading dirty forever.
func TestSyncOneProjectExcludesAnUnexcludedPoolConfig(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	clonePath, _ := setupSyncProject(t)
	movedClone := filepath.Join(home, "projects", "myproj")
	if err := os.Rename(clonePath, movedClone); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(movedClone, "treehouse.toml"), []byte("max_trees = 16\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != "up-to-date" {
		t.Fatalf("outcome = %+v, want the clone syncable rather than dirty forever", got)
	}
	excluded, err := os.ReadFile(filepath.Join(movedClone, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(excluded), "treehouse.toml") {
		t.Fatalf("info/exclude = %q, want the pool config excluded", excluded)
	}
}

func TestSyncOneProjectSkipsNonDefaultBranch(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	clonePath, _ := setupSyncProject(t)
	movedClone := filepath.Join(home, "projects", "myproj")
	if err := os.Rename(clonePath, movedClone); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, movedClone, "checkout", "-q", "-b", "other")

	got, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != "skipped" || got.Detail != "on branch other, not main" {
		t.Fatalf("outcome = %+v, want a skip naming both branches", got)
	}
}

func TestSyncOneProjectSkipsDiverged(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	clonePath, remotePath := setupSyncProject(t)
	movedClone := filepath.Join(home, "projects", "myproj")
	if err := os.Rename(clonePath, movedClone); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(remotePath, "remote-only.txt"), []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, remotePath, "add", "remote-only.txt")
	runGitIn(t, remotePath, "commit", "-q", "-m", "remote commit")

	if err := os.WriteFile(filepath.Join(movedClone, "local-only.txt"), []byte("l"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, movedClone, "add", "local-only.txt")
	runGitIn(t, movedClone, "commit", "-q", "-m", "local commit")

	got, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != "skipped" || !strings.Contains(got.Detail, "diverged") {
		t.Fatalf("outcome = %+v, want a skip naming the divergence", got)
	}
}

func TestSyncOneProjectSkipsWhenNoOriginRemote(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	clonePath := filepath.Join(home, "projects", "myproj")
	initGitRepo(t, clonePath)

	got, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != "skipped" || got.Detail != "no origin remote" {
		t.Fatalf("outcome = %+v, want a skip naming the missing remote", got)
	}
}

func TestPruneGoneBranchesDeletesLocalBranchWithGoneUpstream(t *testing.T) {
	remotePath := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remotePath)
	runGitIn(t, remotePath, "checkout", "-q", "-b", "feature")
	runGitIn(t, remotePath, "checkout", "-q", "main")

	clonePath := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-q", remotePath, clonePath).CombinedOutput(); err != nil {
		t.Fatalf("git clone failed: %v: %s", err, out)
	}
	runGitIn(t, clonePath, "checkout", "-q", "feature")
	runGitIn(t, clonePath, "checkout", "-q", "main")

	runGitIn(t, remotePath, "branch", "-D", "feature")
	runGitIn(t, clonePath, "fetch", "origin", "--prune")

	pruneGoneBranches(clonePath)

	c := exec.Command("git", "branch", "--list", "feature")
	c.Dir = clonePath
	remaining, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(remaining)) != "" {
		t.Fatalf("expected feature branch pruned, got %q", remaining)
	}
}

func TestProjectSyncCommandFastForwardsTheClone(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	clonePath, remotePath := setupSyncProject(t)
	movedClone := filepath.Join(home, "projects", "myproj")
	if err := os.Rename(clonePath, movedClone); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "myproj", URL: remotePath, Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(remotePath, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, remotePath, "add", "new.txt")
	runGitIn(t, remotePath, "commit", "-q", "-m", "add new file")

	cmd := newProjectSyncCmd()
	cmd.SetArgs([]string{"myproj"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(movedClone, "new.txt")); err != nil {
		t.Fatalf("clone did not fast-forward: %v", err)
	}
}

func TestProjectSyncCommandRejectsUnknownProjectName(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectSyncCmd()
	cmd.SetArgs([]string{"unknown"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("got err %v, want not registered", err)
	}
}
