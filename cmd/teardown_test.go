package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

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

func writeFakeTreehouseReturn(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "treehouse"), []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(`#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"tab list")
	printf '{"id":"cli:1","result":{"tabs":[{"tab_id":"wA:tB","workspace_id":"wA"}]}}'
	;;
"tab close")
 printf '{"id":"cli:1","result":{}}'
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
}

func setupTeardownHome(t *testing.T) (home, worktree string) {
	t.Helper()
	home = t.TempDir()
	t.Chdir(home)
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
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("got err %v, want uncommitted changes", err)
	}
}

func TestTeardownShipFailsWhenPRNotMerged(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(`#!/bin/sh
printf '{"state":"OPEN"}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktree, Project: "myproj", PR: "https://example.com/pr/1"}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not merged") {
		t.Fatalf("got err %v, want not merged", err)
	}
}

func TestTeardownShipSucceedsWhenPRMerged(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(`#!/bin/sh
printf '{"state":"MERGED"}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

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
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not merged into the default branch") {
		t.Fatalf("got err %v, want branch not merged", err)
	}
}

func TestTeardownScoutFailsWhenReportMissing(t *testing.T) {
	home, worktree := setupTeardownHome(t)
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout, Worktree: worktree}); err != nil {
		t.Fatal(err)
	}

	cmd := newTeardownCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "report not found") {
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

func TestCloseTaskTabRejectsStaleTab(t *testing.T) {
	writeFakeTreehouseReturn(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(`#!/bin/sh
printf '{"id":"cli:1","result":{"tabs":[{"tab_id":"wA:other","workspace_id":"wA"}]}}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := closeTaskTab(herdr.NewClient(), "wA", "wA:missing"); err == nil {
		t.Fatal("stale tab was accepted")
	}
}
