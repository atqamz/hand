//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/faketool"
	"github.com/atqamz/secondhand/internal/state"
)

// The one treehouse failure its exit status does not report: an unforced return of a dirty worktree
// prints the abort, exits 0, and leaves the slot leased (internal/faketool/FIDELITY.md).
func TestTeardownRefusesAnAbortedWorktreeReturn(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")

	worktree := filepath.Join(home, "wt-scout-1")
	initGitRepo(t, worktree)
	// A scout reaches that return with dirt in place, because its landed-work check reads the report on
	// disk and never the worktree. Taking the abort for success would delete the row naming the leased
	// slot and strand it in the pool for good.
	if err := os.WriteFile(filepath.Join(worktree, "scratch.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "data", "scout-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "scout-1", "report.md"), []byte("# report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := state.Write(home, state.Task{ID: "scout-1", Project: "demo", Kind: state.KindScout,
		Worktree: worktree, CreatedAt: now,
		Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}}); err != nil {
		t.Fatal(err)
	}

	dir := binDir(t)
	invocationLog := filepath.Join(t.TempDir(), "invocations.log")
	writeFakeTreehouse(t, dir, worktree, worktree)
	faketool.Herdr{Workspaces: []faketool.HerdrWorkspace{{ID: "ws-1", Label: "demo", Tabs: []faketool.HerdrTab{
		{ID: "tab-1", Label: "scout-1", Pane: "pane-1"},
		{ID: "tab-2", Label: "task-9", Pane: "pane-2"},
	}}}, Log: invocationLog}.Install(t, dir)

	refused := runHand(t, home, "teardown", "scout-1")
	assertInvocation(t, refused, 1, "still leased")

	if exists, err := state.Exists(home, "scout-1"); err != nil || !exists {
		t.Fatalf("state.Exists after the aborted return = %v, %v, want the row kept: it is the only record of the leased slot", exists, err)
	}
	if err := exec.Command("treehouse", "get", "--lease", "--json").Run(); err == nil {
		t.Fatal("the pool leased the slot out again, so the return did happen and this proves nothing")
	}

	// The second half: the first run already closed the task's tab, so this rerun has to treat a tab herdr
	// no longer lists as closed rather than reading the one tab left as this workspace's last and closing
	// another task's workspace.
	forced := runHand(t, home, "teardown", "scout-1", "--force")
	if forced.code != 0 {
		t.Fatalf("teardown --force: exit %d, stderr %q", forced.code, forced.stderr)
	}
	if exists, err := state.Exists(home, "scout-1"); err != nil || exists {
		t.Fatalf("state.Exists after the forced teardown = %v, %v, want the row removed", exists, err)
	}
	if err := exec.Command("treehouse", "get", "--lease", "--json").Run(); err != nil {
		t.Fatalf("pool still refuses a lease after the forced return: %v", err)
	}
	log, err := os.ReadFile(invocationLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "workspace close") {
		t.Fatalf("invocation log = %q, want the rerun to leave the workspace alone: tab-1 was already closed and tab-2 is another task's", log)
	}
}
