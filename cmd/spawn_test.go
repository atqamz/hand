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

func TestSpawnCleanupReportsAllErrors(t *testing.T) {
	cause := errors.New("spawn failed")
	cleanup := errors.New("cleanup failed")

	err := reportSpawnCleanup(cause, cleanup)
	if !errors.Is(err, cause) {
		t.Fatalf("error %v does not preserve cause", err)
	}
	if !errors.Is(err, cleanup) {
		t.Fatalf("error %v does not preserve cleanup failure", err)
	}
	if !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("error %v does not report cleanup failure", err)
	}
}

const fakeHerdrSpawnScript = `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"workspace list")
	printf '{"id":"cli:1","result":{"workspaces":[{"workspace_id":"wA","label":"myproj","tab_count":1}]}}'
	;;
"tab create")
	printf '{"id":"cli:1","result":{"tab":{"tab_id":"wA:tB","workspace_id":"wA","label":"task-1"},"root_pane":{"pane_id":"wA:pC","tab_id":"wA:tB","agent_status":"idle"}}}'
	;;
"pane run")
 printf '{"id":"cli:1","result":{}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`

func setupSpawnHome(t *testing.T, worktreePath string) string {
	t.Helper()
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "dashboard.md"), []byte(dashboardSkeleton), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "myproj", URL: "https://example.com/myproj.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "brief.md"), []byte("do the thing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "myproj"), 0o755); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(fakeHerdrSpawnScript), 0o755); err != nil {
		t.Fatal(err)
	}
	treehouseScript := "#!/bin/sh\nprintf '{\"path\":\"" + worktreePath + "\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "treehouse"), []byte(treehouseScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(home)
	return home
}

func TestSpawnHappyPath(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "myproj" || got.Kind != state.KindShip || got.Harness != "claude" {
		t.Fatalf("got %+v", got)
	}
	if got.Worktree != wt {
		t.Fatalf("got worktree %q, want %q", got.Worktree, wt)
	}
	if got.Herdr.WorkspaceID != "wA" || got.Herdr.TabID != "wA:tB" || got.Herdr.PaneID != "wA:pC" {
		t.Fatalf("got herdr %+v", got.Herdr)
	}
}

func TestSpawnScoutFlag(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj", "--scout"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != state.KindScout {
		t.Fatalf("got kind %q, want scout", got.Kind)
	}
}

func TestSpawnRejectsUnregisteredProject(t *testing.T) {
	setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"))

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "unknown-proj"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("got err %v, want not registered", err)
	}
}

func TestSpawnRejectsAlreadyActiveTask(t *testing.T) {
	home := setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"))
	if err := state.Write(home, state.Task{ID: "task-1"}); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("got err %v, want already active", err)
	}
}

func TestSpawnRejectsMissingBrief(t *testing.T) {
	home := setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"))
	if err := os.Remove(filepath.Join(home, "data", "task-1", "brief.md")); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "brief not found") {
		t.Fatalf("got err %v, want brief not found", err)
	}
}

func TestSpawnRejectsUnrecognizedHarness(t *testing.T) {
	setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"))

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj", "--harness", "nonexistent"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not recognized") {
		t.Fatalf("got err %v, want not recognized", err)
	}
}

func TestSpawnDetectsWorktreeCollision(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt)
	if err := state.Write(home, state.Task{ID: "other-task", Worktree: wt}); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("got err %v, want collision", err)
	}

	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state written after collision: exists=%v err=%v", exists, err)
	}
}
