package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
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
	if _, err := resolveMergeMethod(true, true, false); err == nil {
		t.Fatal("want error for conflicting flags")
	}
}

func writeFakeGh(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const fakeGhChecksGreenAndMerge = `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pr checks")
	printf '[{"bucket":"pass"},{"bucket":"skipping"}]'
	;;
"pr merge")
	printf 'merged\n'
	;;
*)
	echo "unexpected gh args: $@" >&2
	exit 1
	;;
esac
`

const fakeGhChecksRed = `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pr checks")
	printf '[{"bucket":"pass"},{"bucket":"fail"}]'
	;;
*)
	echo "unexpected gh args: $@" >&2
	exit 1
	;;
esac
`

func TestPRChecksGreenAllPass(t *testing.T) {
	writeFakeGh(t, `#!/bin/sh
printf '[{"bucket":"pass"},{"bucket":"skipping"}]'
`)
	green, err := prChecksGreen("https://example.com/pr/1")
	if err != nil {
		t.Fatal(err)
	}
	if !green {
		t.Fatal("want green")
	}
}

func TestPRChecksGreenFailingCheck(t *testing.T) {
	writeFakeGh(t, `#!/bin/sh
printf '[{"bucket":"pass"},{"bucket":"fail"}]'
`)
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
	if err := state.Write(home, state.Task{ID: "task-1"}); err != nil {
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

func TestMergePRRefusesRedCI(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	writeFakeGh(t, fakeGhChecksRed)

	if err := state.Write(home, state.Task{ID: "task-1", PR: "https://github.com/org/repo/pull/42"}); err != nil {
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
	if got.Merged {
		t.Fatal("want task not marked merged")
	}
}

func TestMergePRSucceedsWhenChecksGreen(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	writeFakeGh(t, fakeGhChecksGreenAndMerge)

	if err := state.Write(home, state.Task{ID: "task-1", PR: "https://github.com/org/repo/pull/42"}); err != nil {
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
	if !got.Merged {
		t.Fatal("want task marked merged")
	}
	if got.MergedAt == "" {
		t.Fatal("want merged_at set")
	}
}

func TestMergeLocalRefusesUncommittedChanges(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	worktreePath := filepath.Join(t.TempDir(), "wt")
	initGitRepo(t, worktreePath)
	if err := os.WriteFile(filepath.Join(worktreePath, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip, Worktree: worktreePath, Project: "myproj"}); err != nil {
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

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip, Worktree: worktreePath}); err != nil {
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
	if !got.Merged {
		t.Fatal("want task marked merged")
	}
}

func TestMergeLocalRefusesWhenNotFastForwardable(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)

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

	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip, Worktree: worktreePath}); err != nil {
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
	if got.Merged {
		t.Fatal("want task not marked merged")
	}
}
