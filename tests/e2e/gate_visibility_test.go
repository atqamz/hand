//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/state"
)

// writeFakeNoMistakes writes a fake no-mistakes that answers `status` and `runs` with fixed text.
// `status` always exits 0, which is what the real binary does for every outcome hand reads from it -
// an uninitialized repo, a non-git directory and a healthy gate all print their answer behind exit 0
// (verified against no-mistakes itself), so hand reads the text. `runs` carries its own exit code,
// since the real binary exits 1 on those same two refusals while printing the identical text.
// `init` is answered too, since `hand project add --mode no-mistakes` initializes the fresh clone's
// gate before anything can be dispatched into it.
func writeFakeNoMistakes(t *testing.T, dir, statusOut, runsOut string, runsExit int) {
	t.Helper()
	body := fmt.Sprintf(`  status) printf '%%s\n' %s ;;
  runs) printf '%%s\n' %s; exit %d ;;
  init) echo 'gate initialized' ;;`, shellSingleQuote(statusOut), shellSingleQuote(runsOut), runsExit)
	writeFakeDispatch(t, dir, "no-mistakes", "", "$1", body)
}

const gateReadyStatus = "    repo:  /home/atqa/secondhand/projects/demo\n" +
	"  remote:  git@github.com:owner/demo.git\n" +
	"    gate:  /home/atqa/.no-mistakes/repos/0b474f2021dd.git\n" +
	"  daemon:  running\n\n  no active run"

// TestStatusEmptyFleetStatesItsCount covers atqamz/secondhand#100 on the real binary: an empty
// fleet must say so, and must still surface a hold left open on a torn-down task's id rather than
// reading as nothing to see.
func TestStatusEmptyFleetStatesItsCount(t *testing.T) {
	home := newHome(t)

	empty := runHand(t, home, "status")
	if empty.code != 0 {
		t.Fatalf("status: exit %d, stderr %q", empty.code, empty.stderr)
	}
	if strings.TrimSpace(empty.stdout) != "no tasks (0)" {
		t.Fatalf("status stdout = %q, want an explicit no-tasks count and nothing else", empty.stdout)
	}

	emptyJSON := runHand(t, home, "status", "--json")
	if emptyJSON.code != 0 || !strings.Contains(emptyJSON.stdout, `"task_count": 0`) {
		t.Fatalf("status --json stdout = %q (exit %d), want an explicit task_count of 0",
			emptyJSON.stdout, emptyJSON.code)
	}

	held := runHand(t, home, "hold", "set", "torn-down-task",
		"--kind", "operator", "--reason", "two ways to do this, needs a call")
	if held.code != 0 {
		t.Fatalf("hold set: exit %d, stderr %q", held.code, held.stderr)
	}

	withHold := runHand(t, home, "status")
	if withHold.code != 0 {
		t.Fatalf("status: exit %d, stderr %q", withHold.code, withHold.stderr)
	}
	if !strings.Contains(withHold.stdout, "no tasks (0)") ||
		!strings.Contains(withHold.stdout, "torn-down-task") ||
		!strings.Contains(withHold.stdout, "two ways to do this, needs a call") {
		t.Fatalf("status stdout = %q, want the no-tasks count alongside the still-open hold", withHold.stdout)
	}
}

// TestStatusFlagsAShippedPRThatNeverRanThroughTheGate covers atqamz/secondhand#92 through the
// operator's own sequence: spawn a ship task into a no-mistakes project, record its PR, let the
// worker report done, then read `hand status`. The gate holds no completed run for that PR, so
// both the fleet overview and the task's own detail view have to say so - and stop saying it the
// moment a completed run records that exact URL.
func TestStatusFlagsAShippedPRThatNeverRanThroughTheGate(t *testing.T) {
	prURL := "https://github.com/owner/demo/pull/7"

	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	redirectGitRemote(t, "https://github.com/owner/demo.git", remote)

	home := newHome(t)
	worktree := filepath.Join(t.TempDir(), "wt-ship-login-fix")

	dir := binDir(t)
	writeFakeTreehouse(t, dir, worktree)
	writeFakeHerdrStatic(t, dir, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo", PaneStatus: "idle"})
	writeFakeDispatch(t, dir, "gh", "", "$1 $2", `  "pr view") echo '{"state":"OPEN"}' ;;`)
	writeFakeNoMistakes(t, dir, gateReadyStatus,
		"  completed    other-branch  758d72bf  2026-08-03 04:29  https://github.com/owner/demo/pull/4", 0)

	added := runHand(t, home, "project", "add", "https://github.com/owner/demo.git", "--mode", "no-mistakes")
	if added.code != 0 {
		t.Fatalf("project add: exit %d, stderr %q", added.code, added.stderr)
	}
	writeBrief(t, home, "ship-login-fix")

	spawned := runHand(t, home, "spawn", "ship-login-fix", "demo")
	if spawned.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", spawned.code, spawned.stderr)
	}

	recorded := runHand(t, home, "pr", "ship-login-fix", prURL)
	if recorded.code != 0 {
		t.Fatalf("pr record: exit %d, stderr %q", recorded.code, recorded.stderr)
	}
	if err := os.WriteFile(state.ReportPath(home, "ship-login-fix"),
		[]byte("done: PR "+prURL+" opened, checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fleet := runHand(t, home, "status")
	if fleet.code != 0 {
		t.Fatalf("status: exit %d, stderr %q", fleet.code, fleet.stderr)
	}
	if !strings.Contains(fleet.stdout, "(gate: no run found)") {
		t.Fatalf("status stdout = %q, want the shipped PR flagged as never having run through the gate", fleet.stdout)
	}

	single := runHand(t, home, "status", "ship-login-fix")
	if single.code != 0 || !strings.Contains(single.stdout, "Gate run:    no run found") {
		t.Fatalf("status ship-login-fix stdout = %q (exit %d), want the Gate run line", single.stdout, single.code)
	}

	singleJSON := runHand(t, home, "status", "ship-login-fix", "--json")
	if singleJSON.code != 0 || !strings.Contains(singleJSON.stdout, `"gate_run_issue": "no run found"`) {
		t.Fatalf("status --json stdout = %q (exit %d), want gate_run_issue", singleJSON.stdout, singleJSON.code)
	}

	writeFakeNoMistakes(t, dir, gateReadyStatus,
		"  completed    ship-login-fix  758d72bf  2026-08-03 04:29  "+prURL, 0)

	gated := runHand(t, home, "status")
	if gated.code != 0 {
		t.Fatalf("status: exit %d, stderr %q", gated.code, gated.stderr)
	}
	if strings.Contains(gated.stdout, "(gate:") {
		t.Fatalf("status stdout = %q, want no gate marker once a completed run recorded this PR", gated.stdout)
	}
}

// TestGateCheckNamesAMissingOrNonGitClonePath covers atqamz/secondhand#97 on both operator
// surfaces. A clone path that is missing, and one that exists but is not a git repository, are
// different failures from a gate that was never initialized: `no-mistakes init` repairs neither,
// so neither may be reported as "not initialized" nor - the worse outcome - pass as ready and let
// a worker be dispatched into a project the gate cannot cover.
func TestGateCheckNamesAMissingOrNonGitClonePath(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "no-mistakes")
	writeBrief(t, home, "task-1")

	dir := binDir(t)
	writeFakeTreehouse(t, dir, filepath.Join(t.TempDir(), "unused-worktree"))
	writeFakeHerdrStatic(t, dir, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})
	writeFakeNoMistakes(t, dir, "not in a git repository", "not in a git repository", 1)

	clonePath := filepath.Join(home, "projects", "demo")

	// The clone directory does not exist at all: the chdir fails before no-mistakes ever runs, and
	// the refusal has to name that path rather than blame the binary.
	missing := runHand(t, home, "spawn", "task-1", "demo")
	if missing.code == 0 {
		t.Fatalf("spawn into a project with no clone on disk succeeded, stdout %q", missing.stdout)
	}
	if !strings.Contains(missing.stderr, clonePath) {
		t.Fatalf("spawn stderr = %q, want it to name the missing clone path %s", missing.stderr, clonePath)
	}
	if strings.Contains(missing.stderr, "binary not found or not runnable") {
		t.Fatalf("spawn stderr = %q, must not blame the no-mistakes binary for a missing clone path", missing.stderr)
	}
	if _, err := state.Read(home, "task-1"); err == nil {
		t.Fatal("a refused spawn must leave no task row behind")
	}

	// The clone directory exists but is not a git repository: no-mistakes prints that, exiting 0.
	if err := os.MkdirAll(clonePath, 0o755); err != nil {
		t.Fatal(err)
	}
	nonGit := runHand(t, home, "spawn", "task-1", "demo")
	if nonGit.code == 0 {
		t.Fatalf("spawn into a non-git clone path succeeded, stdout %q", nonGit.stdout)
	}
	if !strings.Contains(nonGit.stderr, "not a git repository") || !strings.Contains(nonGit.stderr, clonePath) {
		t.Fatalf("spawn stderr = %q, want it to name the non-git clone path %s", nonGit.stderr, clonePath)
	}
	if strings.Contains(nonGit.stderr, "no-mistakes init") {
		t.Fatalf("spawn stderr = %q, must not offer no-mistakes init as the remedy for a non-git path", nonGit.stderr)
	}

	// The same misclassification on the surface an operator watches before dispatching: unreachable,
	// never the "not initialized" bucket whose remedy would not help here.
	listed := runHand(t, home, "project", "list")
	if listed.code != 0 {
		t.Fatalf("project list: exit %d, stderr %q", listed.code, listed.stderr)
	}
	if !strings.Contains(listed.stdout, "(gate: unreachable)") || strings.Contains(listed.stdout, "(gate: not initialized)") {
		t.Fatalf("project list stdout = %q, want the non-git clone reported as unreachable", listed.stdout)
	}
}
