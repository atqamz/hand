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

const fakeHerdrPromoteScript = `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pOld","tab_id":"wA:tOld","workspace_id":"wA","agent_status":"done"}}}'
	;;
"tab list")
	printf '{"id":"cli:1","result":{"tabs":[{"tab_id":"wA:tOld","workspace_id":"wA"},{"tab_id":"wA:tOther","workspace_id":"wA"}]}}'
	;;
"tab close")
	printf '{"id":"cli:1","result":{}}'
	;;
"workspace list")
	printf '{"id":"cli:1","result":{"workspaces":[{"workspace_id":"wA","label":"myproj","tab_count":2}]}}'
	;;
"tab create")
	printf '{"id":"cli:1","result":{"tab":{"tab_id":"wA:tNew","workspace_id":"wA","label":"task-1"},"root_pane":{"pane_id":"wA:pNew","tab_id":"wA:tNew","agent_status":"idle"}}}'
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

const fakeHerdrPromotePaneWorking = `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pOld","tab_id":"wA:tOld","workspace_id":"wA","agent_status":"working"}}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`

func setupPromoteHome(t *testing.T, oldWorktree, newWorktree, herdrScript string) string {
	t.Helper()
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("scout findings"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "brief.md"), []byte("implement the fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "myproj", URL: "https://example.com/myproj.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "myproj"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := state.Write(home, state.Task{
		ID:       "task-1",
		Project:  "myproj",
		Kind:     state.KindScout,
		Worktree: oldWorktree,
		Herdr:    state.Herdr{WorkspaceID: "wA", TabID: "wA:tOld", PaneID: "wA:pOld"},
	}); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(herdrScript), 0o755); err != nil {
		t.Fatal(err)
	}
	treehouseScript := "#!/bin/sh\nprintf '{\"path\":\"" + newWorktree + "\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "treehouse"), []byte(treehouseScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(home)
	return home
}

func TestPromoteHappyPath(t *testing.T) {
	oldWt := filepath.Join(t.TempDir(), "old-wt")
	newWt := filepath.Join(t.TempDir(), "new-wt")
	home := setupPromoteHome(t, oldWt, newWt, fakeHerdrPromoteScript)

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != state.KindShip {
		t.Fatalf("kind = %q, want ship", got.Kind)
	}
	if got.Worktree != newWt {
		t.Fatalf("worktree = %q, want %q", got.Worktree, newWt)
	}
	if got.Herdr.TabID != "wA:tNew" || got.Herdr.PaneID != "wA:pNew" {
		t.Fatalf("herdr = %+v, want new tab/pane", got.Herdr)
	}
	if got.Harness != "claude" {
		t.Fatalf("harness = %q, want claude", got.Harness)
	}
}

func TestPromoteRefusesNonScout(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip}); err != nil {
		t.Fatal(err)
	}

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

func TestPromoteRefusesMissingReport(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout}); err != nil {
		t.Fatal(err)
	}

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

func TestPromoteRefusesMissingBrief(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("findings"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout}); err != nil {
		t.Fatal(err)
	}

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

func TestPromoteRefusesUnregisteredProject(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("findings"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "brief.md"), []byte("implement the fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout, Project: "unregistered-proj"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

// fakeHerdrPromoteLeakScript mirrors fakeHerdrLeakScript for promote: it logs every call and
// fails "pane run" so the promotion always fails after the new tab exists, with
// $HERDR_WS_EXISTS_FLAG choosing between the created-workspace and pre-existing-workspace cases.
const fakeHerdrPromoteLeakScript = `#!/bin/sh
echo "$@" >> "$HERDR_CALL_LOG"
cmd="$1 $2"
case "$cmd" in
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pOld","tab_id":"wA:tOld","workspace_id":"wA","agent_status":"done"}}}'
	;;
"workspace list")
	if [ -e "$HERDR_WS_EXISTS_FLAG" ]; then
		printf '{"id":"cli:1","result":{"workspaces":[{"workspace_id":"wA","label":"myproj","tab_count":2}]}}'
	else
		printf '{"id":"cli:1","result":{"workspaces":[]}}'
	fi
	;;
"workspace create")
	printf '{"id":"cli:1","result":{"workspace":{"workspace_id":"wA","label":"myproj"}}}'
	;;
"tab create")
	printf '{"id":"cli:1","result":{"tab":{"tab_id":"wA:tNew","workspace_id":"wA","label":"task-1"},"root_pane":{"pane_id":"wA:pNew","tab_id":"wA:tNew","agent_status":"idle"}}}'
	;;
"pane run")
	exit 1
	;;
"workspace close")
	printf '{"id":"cli:1","result":{"type":"ok"}}'
	;;
"tab list")
	printf '{"id":"cli:1","result":{"tabs":[{"tab_id":"wA:tOld","workspace_id":"wA"},{"tab_id":"wA:tNew","workspace_id":"wA"}]}}'
	;;
"tab close")
	printf '{"id":"cli:1","result":{"type":"ok"}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`

func TestPromoteFailureClosesWorkspaceItCreated(t *testing.T) {
	oldWt := filepath.Join(t.TempDir(), "old-wt")
	newWt := filepath.Join(t.TempDir(), "new-wt")
	home := setupPromoteHome(t, oldWt, newWt, fakeHerdrPromoteLeakScript)
	callLog := setupSpawnLeakEnv(t, false)

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected promote to fail")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "workspace close wA") {
		t.Fatalf("calls = %q, want the workspace hand created to be closed", calls)
	}
	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != state.KindScout {
		t.Fatalf("kind = %q, want the task left as a scout", got.Kind)
	}
}

func TestPromoteFailureKeepsPreexistingWorkspace(t *testing.T) {
	oldWt := filepath.Join(t.TempDir(), "old-wt")
	newWt := filepath.Join(t.TempDir(), "new-wt")
	setupPromoteHome(t, oldWt, newWt, fakeHerdrPromoteLeakScript)
	callLog := setupSpawnLeakEnv(t, true)

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected promote to fail")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "workspace close") {
		t.Fatalf("calls = %q, want the pre-existing shared workspace left open", calls)
	}
	if !strings.Contains(string(calls), "tab close wA:tNew") {
		t.Fatalf("calls = %q, want only the new tab closed", calls)
	}
}

func TestPromoteRefusesWhenAgentStillWorking(t *testing.T) {
	oldWt := filepath.Join(t.TempDir(), "old-wt")
	newWt := filepath.Join(t.TempDir(), "new-wt")
	home := setupPromoteHome(t, oldWt, newWt, fakeHerdrPromotePaneWorking)
	_ = home

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}
