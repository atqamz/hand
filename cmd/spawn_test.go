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

// fakeHerdrSpawnScript covers the herdr calls a clean spawn makes: it reports a pane herdr sees
// claude running in, painted past its startup frame and showing no first-run dialog, so
// confirmLaunch confirms the launch on its first poll. Real herdr answers query commands
// ("workspace list", "tab create", "pane get") with a JSON envelope and answers void commands
// ("pane run") with empty stdout on success (internal/herdr/client.go's call/callVoid doc
// comments); this fake echoes an envelope for "pane run" too, which callVoid also accepts, since
// this test exercises spawn's own success path, not herdr's response parsing - that parsing is
// covered at the client level by internal/herdr/client_test.go.
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
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"%s","tab_id":"wA:tB","workspace_id":"wA","agent":"claude","agent_status":"idle"}}}' "$3"
	;;
"pane read")
	printf 'Welcome to Claude Code\n> \n  ? for shortcuts\n'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`

// writeFakeTreehouseGet fakes "treehouse get" as always leasing worktreePath.
// Real treehouse writes a banner to stderr ahead of its JSON on "get"
// (internal/worktree/worktree.go's Get doc comment); this fake omits it since
// worktree.Get's own stdout-only parsing is covered where the banner matters,
// in tests/e2e/fakes_test.go's writeFakeTreehouse.
func writeFakeTreehouseGet(t *testing.T, bin, worktreePath string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '{\"path\":\"" + worktreePath + "\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "treehouse"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func setupSpawnHome(t *testing.T, worktreePath, herdrScript string) string {
	t.Helper()
	useFastLaunchPolling(t)
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
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(herdrScript), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTreehouseGet(t, bin, worktreePath)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(home)
	return home
}

func TestSpawnHappyPath(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, fakeHerdrSpawnScript)

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
	home := setupSpawnHome(t, wt, fakeHerdrSpawnScript)

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
	setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), fakeHerdrSpawnScript)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "unknown-proj"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("got err %v, want not registered", err)
	}
}

func TestSpawnRejectsAlreadyActiveTask(t *testing.T) {
	home := setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), fakeHerdrSpawnScript)
	if err := state.Write(home, state.Task{ID: "task-1"}); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "already active") {
		t.Fatalf("got err %v, want already active", err)
	}
}

func TestSpawnRejectsMissingBrief(t *testing.T) {
	home := setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), fakeHerdrSpawnScript)
	if err := os.Remove(filepath.Join(home, "data", "task-1", "brief.md")); err != nil {
		t.Fatal(err)
	}

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "brief not found") {
		t.Fatalf("got err %v, want brief not found", err)
	}
}

func TestSpawnRejectsUnrecognizedHarness(t *testing.T) {
	setupSpawnHome(t, filepath.Join(t.TempDir(), "wt"), fakeHerdrSpawnScript)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj", "--harness", "nonexistent"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not recognized") {
		t.Fatalf("got err %v, want not recognized", err)
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestSpawnDetectsWorktreeCollision(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, fakeHerdrSpawnScript)
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

// fakeHerdrLeakScript logs every invocation to $HERDR_CALL_LOG and fails "pane run" so a
// spawn always fails after tab creation. Whether "workspace list" reports an existing
// workspace is controlled by the presence of $HERDR_WS_EXISTS_FLAG, letting the same script
// drive both the created-workspace and pre-existing-workspace leak scenarios.
// "pane run" fails via bare exit 1 rather than real herdr's documented void-command
// failure shape (empty exit 0 + JSON error envelope, see callVoid's doc comment): spawn.go
// only branches on whether PaneRun returned a non-nil error, never on its shape, so this
// tests spawn's cleanup logic, not herdr's envelope parsing - that shape is covered by
// internal/herdr/client_test.go's TestPaneRunSurfacesErrorEnvelopeEvenOnExitZero.
const fakeHerdrLeakScript = `#!/bin/sh
echo "$@" >> "$HERDR_CALL_LOG"
cmd="$1 $2"
case "$cmd" in
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
	printf '{"id":"cli:1","result":{"tab":{"tab_id":"wA:tB","workspace_id":"wA","label":"task-1"},"root_pane":{"pane_id":"wA:pC","tab_id":"wA:tB","agent_status":"idle"}}}'
	;;
"pane run")
	exit 1
	;;
"workspace close")
	printf '{"id":"cli:1","result":{"type":"ok"}}'
	;;
"tab list")
	printf '{"id":"cli:1","result":{"tabs":[{"tab_id":"wA:root","workspace_id":"wA","label":"root"},{"tab_id":"wA:tB","workspace_id":"wA","label":"task-1"}]}}'
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

func setupSpawnLeakEnv(t *testing.T, workspaceExists bool) string {
	t.Helper()
	callLog := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("HERDR_CALL_LOG", callLog)

	flag := filepath.Join(t.TempDir(), "ws-exists")
	if workspaceExists {
		if err := os.WriteFile(flag, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HERDR_WS_EXISTS_FLAG", flag)
	return callLog
}

// fakeHerdrStuckPaneScript launches fine but reports a pane sitting on a dialog nothing
// answers, the shape confirmLaunch must fail rather than record as a spawned task.
const fakeHerdrStuckPaneScript = `#!/bin/sh
echo "$@" >> "$HERDR_CALL_LOG"
cmd="$1 $2"
case "$cmd" in
"workspace list")
	printf '{"id":"cli:1","result":{"workspaces":[]}}'
	;;
"workspace create")
	printf '{"id":"cli:1","result":{"workspace":{"workspace_id":"wA","label":"myproj"}}}'
	;;
"tab create")
	printf '{"id":"cli:1","result":{"tab":{"tab_id":"wA:tB","workspace_id":"wA","label":"task-1"},"root_pane":{"pane_id":"wA:pC","tab_id":"wA:tB","agent_status":"idle"}}}'
	;;
"pane run")
	printf '{"id":"cli:1","result":{}}'
	;;
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"%s","tab_id":"wA:tB","workspace_id":"wA","agent":"claude","agent_status":"idle"}}}' "$3"
	;;
"pane read")
	printf 'Some brand new dialog\n> 1. Sure\n  2. Nope\n\nEnter to confirm\n'
	;;
"workspace close")
	printf '{"id":"cli:1","result":{"type":"ok"}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`

func TestSpawnRollsBackWhenWorkerNeverStarts(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	home := setupSpawnHome(t, wt, fakeHerdrStuckPaneScript)
	callLog := setupSpawnLeakEnv(t, false)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "confirm worker started") {
		t.Fatalf("got err %v, want the spawn to fail on launch confirmation", err)
	}

	if exists, existsErr := state.Exists(home, "task-1"); existsErr != nil || exists {
		t.Fatalf("state written for a worker that never started: exists=%v err=%v", exists, existsErr)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(calls), "workspace close wA") {
		t.Fatalf("calls = %q, want the workspace hand created to be closed", calls)
	}
}

func TestSpawnFailureClosesWorkspaceItCreated(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	setupSpawnHome(t, wt, fakeHerdrLeakScript)
	callLog := setupSpawnLeakEnv(t, false)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected spawn to fail")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "workspace close wA") {
		t.Fatalf("calls = %q, want the workspace hand created to be closed", calls)
	}
}

func TestSpawnFailureKeepsPreexistingWorkspace(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	setupSpawnHome(t, wt, fakeHerdrLeakScript)
	callLog := setupSpawnLeakEnv(t, true)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "myproj"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected spawn to fail")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "workspace close") {
		t.Fatalf("calls = %q, want the pre-existing shared workspace left open", calls)
	}
	if !strings.Contains(string(calls), "tab close wA:tB") {
		t.Fatalf("calls = %q, want the task's own tab closed", calls)
	}
}
