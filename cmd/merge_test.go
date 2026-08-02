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
	_, err := resolveMergeMethod(true, true, false)
	if err == nil {
		t.Fatal("want error for conflicting flags")
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestMergeRejectsLocalCombinedWithMethodFlags(t *testing.T) {
	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1", "--local", "--squash"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
	if !strings.Contains(err.Error(), "cannot be combined with --local") {
		t.Fatalf("err = %v, want --local conflict", err)
	}
}

func TestMergeRejectsConflictingMethodFlags(t *testing.T) {
	cmd := newMergeCmd()
	cmd.SetArgs([]string{"task-1", "--squash", "--rebase"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
	if !strings.Contains(err.Error(), "only one of") {
		t.Fatalf("err = %v, want mutually exclusive method flags", err)
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

// fakeGhChecksGreenAndMerge always exits 0. Real gh exits 1 on a fail bucket and
// 8 on pending (prChecksGreen's own doc comment in cmd/merge.go), but
// prChecksGreen deliberately never consults the exit code once the JSON parses,
// so an exit-0 fake exercises the same code path a nonzero one would; "pr merge"
// failing is untested here (see runPRMerge's "gh pr merge failed" error path).
const fakeGhChecksGreenAndMerge = `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pr view")
	printf '{"state":"OPEN"}'
	;;
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

// fakeGhChecksRed exits 0 with a "fail" bucket rather than real gh's documented
// exit 1, for the same reason as fakeGhChecksGreenAndMerge: prChecksGreen only
// looks at the exit code when the JSON fails to parse, so this still exercises
// the real "trust the JSON over the exit code" behavior the function is built on.
const fakeGhChecksRed = `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pr view")
	printf '{"state":"OPEN"}'
	;;
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
	// Faithful to real gh here: an all-pass `pr checks --json bucket` prints
	// this array on stdout and exits 0.
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
	// Exit 0 with a "fail" bucket instead of real gh's exit 1, same reason as
	// fakeGhChecksRed above.
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
	mkFleetDirs(t, home)
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

// TestMergeRefusesAlreadyMergedPR covers cmd/merge.go:90's gap noted in
// atqamz/secondhand#69: a gate-opened PR can populate t.PR without hand having
// merged it, so t.PR != "" no longer implies hand hasn't seen it land yet.
func TestMergeRefusesAlreadyMergedPR(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeGh(t, `#!/bin/sh
case "$1 $2" in
"pr view") printf '{"state":"MERGED"}' ;;
*) echo "unexpected gh args: $@" >&2; exit 1 ;;
esac
`)

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
	if !strings.Contains(err.Error(), "already merged") {
		t.Fatalf("err = %v, want already merged", err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MergeExecuted {
		t.Fatal("want task not marked merged by hand merge itself")
	}
}

func TestMergePRRefusesRedCI(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
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
	if got.MergeExecuted {
		t.Fatal("want task not marked merged")
	}
}

func TestMergePRSucceedsWhenChecksGreen(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
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
	if !got.MergeExecuted {
		t.Fatal("want task marked merged")
	}
	if got.MergeExecutedAt == "" {
		t.Fatal("want merged_at set")
	}
}

func TestMergeLocalRefusesUncommittedChanges(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
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
	mkFleetDirs(t, home)

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
	if !got.MergeExecuted {
		t.Fatal("want task marked merged")
	}
}

func TestMergeLocalRefusesWhenNotFastForwardable(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

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
	if got.MergeExecuted {
		t.Fatal("want task not marked merged")
	}
}
