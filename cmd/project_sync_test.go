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

// setupSyncProject creates a bare-ish remote repo and a clone of it, with the
// clone's origin/HEAD already resolvable (as a normal `git clone` sets up).
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

	msg, advanced, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if advanced {
		t.Fatalf("want not advanced, got %q", msg)
	}
	if !strings.Contains(msg, "up to date") {
		t.Fatalf("message = %q, want up to date", msg)
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

	msg, advanced, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if !advanced {
		t.Fatalf("want advanced, got message %q", msg)
	}
	if !strings.Contains(msg, "fast-forwarded") || !strings.Contains(msg, "was 1 behind") {
		t.Fatalf("message = %q, want fast-forwarded/was 1 behind", msg)
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

	msg, advanced, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if advanced {
		t.Fatalf("want not advanced, got %q", msg)
	}
	if !strings.Contains(msg, "dirty working tree") {
		t.Fatalf("message = %q, want dirty working tree", msg)
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

	msg, advanced, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if advanced {
		t.Fatalf("want not advanced, got %q", msg)
	}
	if !strings.Contains(msg, "on branch other, not main") {
		t.Fatalf("message = %q, want branch mismatch warning", msg)
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

	msg, advanced, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if advanced {
		t.Fatalf("want not advanced, got %q", msg)
	}
	if !strings.Contains(msg, "diverged") {
		t.Fatalf("message = %q, want diverged warning", msg)
	}
}

func TestSyncOneProjectSkipsWhenNoOriginRemote(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	clonePath := filepath.Join(home, "projects", "myproj")
	initGitRepo(t, clonePath)

	msg, advanced, err := syncOneProject(home, project.Project{Name: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if advanced {
		t.Fatalf("want not advanced, got %q", msg)
	}
	if !strings.Contains(msg, "no origin remote") {
		t.Fatalf("message = %q, want no origin remote warning", msg)
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

func TestProjectSyncCommandUpdatesDashboardWhenAdvanced(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "dashboard.md"), []byte("# Dashboard\n\n## Projects\n"), 0o644); err != nil {
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

	dashboard, err := os.ReadFile(filepath.Join(home, "data", "dashboard.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dashboard), "myproj: direct-pr") {
		t.Fatalf("dashboard = %q, want myproj entry", dashboard)
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
