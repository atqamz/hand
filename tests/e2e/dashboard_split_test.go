//go:build e2e

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/state"
)

func readDashboardRaw(t *testing.T, home string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "data", "dashboard.md"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestDashboardUpdateSplit walks spawn -> merge --local -> teardown -> promote
// -> project add -> project remove through the built binary, checking
// data/dashboard.md's raw bytes after each step against CLAUDE.md's recorded
// per-command split: spawn, teardown, project add, and project remove must
// change it; merge --local and promote must leave it byte-for-byte alone.
func TestDashboardUpdateSplit(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	writeBrief(t, home, "task-1")

	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	sharedWorktree := filepath.Join(home, "wt-shared")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-1-branch", sharedWorktree)

	dir := binDir(t)
	writeFakeTreehouse(t, dir, sharedWorktree)
	writeFakeHerdrStatic(t, dir, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo", PaneStatus: "done"})

	dashBeforeSpawn := readDashboardRaw(t, home)
	if spawned := runHand(t, home, "spawn", "task-1", "demo"); spawned.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", spawned.code, spawned.stderr)
	}
	dashAfterSpawn := readDashboardRaw(t, home)
	if bytes.Equal(dashBeforeSpawn, dashAfterSpawn) {
		t.Fatal("spawn left dashboard.md byte-identical, want it to add task-1's active row")
	}

	runGitIn(t, sharedWorktree, "commit", "--allow-empty", "-q", "-m", "wip")
	if merged := runHand(t, home, "merge", "task-1", "--local"); merged.code != 0 {
		t.Fatalf("merge --local: exit %d, stderr %q", merged.code, merged.stderr)
	}
	dashAfterLocalMerge := readDashboardRaw(t, home)
	if !bytes.Equal(dashAfterSpawn, dashAfterLocalMerge) {
		t.Fatalf("merge --local changed dashboard.md, want it untouched:\nbefore: %s\nafter:  %s", dashAfterSpawn, dashAfterLocalMerge)
	}

	if done := runHand(t, home, "teardown", "task-1"); done.code != 0 {
		t.Fatalf("teardown: exit %d, stderr %q", done.code, done.stderr)
	}
	dashAfterTeardown := readDashboardRaw(t, home)
	if bytes.Equal(dashAfterLocalMerge, dashAfterTeardown) {
		t.Fatal("teardown left dashboard.md byte-identical, want it to drop task-1's active row and record a completion")
	}

	writeBrief(t, home, "task-2")
	if err := os.MkdirAll(filepath.Join(home, "data", "task-2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-2", "report.md"), []byte("# report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{
		ID: "task-2", Project: "demo", Kind: state.KindScout,
		Worktree:  filepath.Join(home, "wt-task-2-scout"),
		Herdr:     state.Herdr{WorkspaceID: "ws-old", TabID: "tab-old", PaneID: "pane-old"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	dashBeforePromote := readDashboardRaw(t, home)
	if promoted := runHand(t, home, "promote", "task-2"); promoted.code != 0 {
		t.Fatalf("promote: exit %d, stderr %q", promoted.code, promoted.stderr)
	}
	dashAfterPromote := readDashboardRaw(t, home)
	if !bytes.Equal(dashBeforePromote, dashAfterPromote) {
		t.Fatalf("promote changed dashboard.md, want it untouched:\nbefore: %s\nafter:  %s", dashBeforePromote, dashAfterPromote)
	}

	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	redirectGitRemote(t, "https://example.com/demo2.git", remote)

	dashBeforeAdd := readDashboardRaw(t, home)
	if added := runHand(t, home, "project", "add", "https://example.com/demo2.git", "--mode", "direct-pr"); added.code != 0 {
		t.Fatalf("project add: exit %d, stderr %q", added.code, added.stderr)
	}
	dashAfterAdd := readDashboardRaw(t, home)
	if bytes.Equal(dashBeforeAdd, dashAfterAdd) {
		t.Fatal("project add left dashboard.md byte-identical, want the Projects section populated")
	}

	if removed := runHand(t, home, "project", "remove", "demo2"); removed.code != 0 {
		t.Fatalf("project remove: exit %d, stderr %q", removed.code, removed.stderr)
	}
	dashAfterRemove := readDashboardRaw(t, home)
	if bytes.Equal(dashAfterAdd, dashAfterRemove) {
		t.Fatal("project remove left dashboard.md byte-identical, want demo2 dropped from the Projects section")
	}
}
