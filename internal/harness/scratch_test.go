package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

const fleetID = "f0123456789ab"

func scratchEnv(tmp string) func(string) string {
	return func(k string) string {
		if k == "TMPDIR" {
			return tmp
		}
		return ""
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the scratch cleaner is Unix-only")
	}
}

func TestClaudeKeyReplacesEveryNonAlphanumeric(t *testing.T) {
	got := claudeKey("/home/atqa/.secondhand/worktrees/f0123456789ab/t308-a343")
	if want := "-home-atqa--secondhand-worktrees-f0123456789ab-t308-a343"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

func TestLeftoversNamesOnlyTheExactScratchDirectory(t *testing.T) {
	skipWindows(t)
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "home", ".secondhand", "worktrees", fleetID, "t7-a3")
	want := filepath.Join(tmp, "claude-1000", claudeKey(wt))
	mkdir(t, want)
	mkdir(t, filepath.Join(tmp, "claude-1000", claudeKey(wt)+"-1"))
	if got := Leftovers("claude", wt, 1000, scratchEnv(tmp)); !slices.Equal(got, []string{want}) {
		t.Fatalf("Leftovers = %v, want %v", got, []string{want})
	}
	if got := Leftovers("claude", wt, 1001, scratchEnv(tmp)); got != nil {
		t.Fatalf("another uid = %v", got)
	}
	other := t.TempDir()
	mkdir(t, filepath.Join(other, "claude-1000", claudeKey(wt)))
	env := func(k string) string {
		if k == "CLAUDE_CODE_TMPDIR" {
			return other
		}
		return scratchEnv(tmp)(k)
	}
	if got := Leftovers("claude", wt, 1000, env); !slices.Equal(got, []string{filepath.Join(other, "claude-1000", claudeKey(wt))}) {
		t.Fatalf("CLAUDE_CODE_TMPDIR = %v", got)
	}
}

func TestLeftoversRefusesAnythingButAnAttemptWorktree(t *testing.T) {
	skipWindows(t)
	tmp := t.TempDir()
	base := filepath.Join(tmp, "home", ".secondhand", "worktrees")
	for _, wt := range []string{
		filepath.Join(base, fleetID),
		filepath.Join(base, fleetID, "t7-a3", "sub"),
		filepath.Join(base, "fleet", "t7-a3"),
		filepath.Join(base, fleetID, "t7"),
		filepath.Join(tmp, "home", "project"),
		filepath.Join(tmp, "home", ".secondhand", "elsewhere", fleetID, "t7-a3"),
		"worktrees/" + fleetID + "/t7-a3",
	} {
		mkdir(t, filepath.Join(tmp, "claude-1000", claudeKey(wt)))
		if got := Leftovers("claude", wt, 1000, scratchEnv(tmp)); got != nil {
			t.Errorf("Leftovers(%q) = %v", wt, got)
		}
	}
	wt := filepath.Join(base, fleetID, "t7-a3")
	mkdir(t, filepath.Join(tmp, "claude-1000", claudeKey(wt)))
	for _, name := range []string{"codex", "opencode", "agy", "copilot"} {
		if got := Leftovers(name, wt, 1000, scratchEnv(tmp)); got != nil {
			t.Errorf("Leftovers(%s) = %v", name, got)
		}
	}
	if got := Leftovers("claude", wt, -1, scratchEnv(tmp)); got != nil {
		t.Errorf("no uid = %v", got)
	}
}

func TestLeftoversSkipsASymlinkAndAMissingDirectory(t *testing.T) {
	skipWindows(t)
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktrees", fleetID, "t7-a3")
	if got := Leftovers("claude", wt, 1000, scratchEnv(tmp)); got != nil {
		t.Fatalf("missing = %v", got)
	}
	mkdir(t, filepath.Join(tmp, "claude-1000"))
	if err := os.Symlink(t.TempDir(), filepath.Join(tmp, "claude-1000", claudeKey(wt))); err != nil {
		t.Fatal(err)
	}
	if got := Leftovers("claude", wt, 1000, scratchEnv(tmp)); got != nil {
		t.Fatalf("symlink = %v", got)
	}
}

func TestOrphansListsOnlyKeysWithoutAWorktreeFolder(t *testing.T) {
	skipWindows(t)
	tmp := t.TempDir()
	worktrees := filepath.Join(tmp, "home", ".secondhand", "worktrees")
	mkdir(t, filepath.Join(worktrees, fleetID, "t1-a1"))
	root := filepath.Join(tmp, "claude-1000")
	live := filepath.Join(root, claudeKey(filepath.Join(worktrees, fleetID, "t1-a1")))
	orphan := filepath.Join(root, claudeKey(filepath.Join(worktrees, fleetID, "t2-a2")))
	foreign := filepath.Join(root, claudeKey(filepath.Join(tmp, "other", "worktrees", fleetID, "t2-a2")))
	odd := filepath.Join(root, "-home-work")
	for _, d := range []string{live, orphan, foreign, odd} {
		mkdir(t, d)
	}
	file := filepath.Join(root, claudeKey(filepath.Join(worktrees, fleetID, "t3-a3")))
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Orphans("claude", worktrees, 1000, scratchEnv(tmp)); !slices.Equal(got, []string{orphan}) {
		t.Fatalf("Orphans = %v, want %v", got, []string{orphan})
	}
	if got := Orphans("codex", worktrees, 1000, scratchEnv(tmp)); got != nil {
		t.Fatalf("codex = %v", got)
	}
}
