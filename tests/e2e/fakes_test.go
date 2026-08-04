//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/faketool"
)

// realBinsOnPath are the only real executables this suite needs to resolve:
// git, which both hand and the test helpers shell out to for real; sh, which
// internal/notify execs to run a notify template (from both cmd/notify.go and
// the watcher's in-process hook); and cat, which the fake herdr scripts below
// use to read a pane's status file. Everything hand execs that is not listed
// here is faked per test (backendsThisSuiteFakes, e2e_test.go), so leaving
// those unreachable turns a missing fake into a loud failure instead of a call
// against the developer's real tools.
var realBinsOnPath = []string{"git", "sh", "cat"}

// hermeticPath is the PATH every test runs under, built once by TestMain from
// the inherited PATH. Each needed binary is symlinked in individually rather
// than having its own directory prepended: on a real machine git commonly
// lives in the same directory as real herdr and treehouse, so exposing that
// directory would hand the suite straight back the tools it fakes.
var hermeticPath string

func buildHermeticPath(dir string) (string, error) {
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	for _, name := range realBinsOnPath {
		resolved, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("resolve %s, which this suite runs for real: %w", name, err)
		}
		if err := os.Symlink(resolved, filepath.Join(dir, name)); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// binDir returns a directory prepended to the current PATH for the rest of the
// test, so fake binaries written there are found first. Prepending keeps this
// additive - a test can call binDir twice and keep both fake dirs - and stays
// hermetic only because TestMain runs first: by then PATH is already
// hermeticPath, so there is no ambient PATH left to inherit here.
func binDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func writeFakeBin(t *testing.T, dir, name, caseBody string) {
	t.Helper()
	script := "#!/bin/sh\n" + caseBody
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// writeFakeDispatch writes a fake binary that dispatches caseBody on selector
// (the shell expression each case arm matches, e.g. "$1" or "$1 $2") and fails
// loudly on any invocation shape the test did not anticipate. When logPath is
// non-empty every invocation is appended to it as "<name> <args...>", so a
// test can assert on which calls were and were not made.
func writeFakeDispatch(t *testing.T, dir, name, logPath, selector, caseBody string) {
	t.Helper()
	script := ""
	if logPath != "" {
		script = fmt.Sprintf("echo \"%s $@\" >> %s\n", name, shellSingleQuote(logPath))
	}
	script += fmt.Sprintf("case \"%s\" in\n%s\n  *) echo \"unexpected %s invocation: $@\" >&2; exit 1 ;;\nesac\n",
		selector, caseBody, name)
	writeFakeBin(t, dir, name, script)
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// herdrIDs is the fixed set of workspace/tab/pane identifiers a static fake
// herdr hands back for a single spawn/promote + teardown lifecycle.
type herdrIDs struct {
	WorkspaceID string
	TabID       string
	PaneID      string
	Label       string
	PaneStatus  string // agent_status reported by "pane get"; defaults to "working" if empty
}

// The herdr fake for a spawn (or promote) followed by a teardown within one test:
// no workspace exists yet, so the command creates one, and the create's response
// carries the root tab and pane herdr makes alongside it - the ones the task
// renames and reuses instead of creating a second tab. A test needing a workspace
// already open declares faketool.Herdr itself.
func writeFakeHerdrStatic(t *testing.T, dir string, ids herdrIDs) {
	t.Helper()
	writeFakeHerdrStaticLogged(t, dir, "", ids)
}

// writeFakeHerdrStatic plus an invocation log, for tests that assert which herdr
// calls were made: that spawn reuses the workspace's own root tab rather than
// creating a second one, and that tearing that sole tab down closes the workspace.
func writeFakeHerdrStaticLogged(t *testing.T, dir, logPath string, ids herdrIDs) {
	t.Helper()
	// Real herdr creates a tab whenever it is asked to, but a generated fake has to
	// know the identifiers up front, so a few spares stand ready for the second and
	// later task spawned into the workspace the first one created. Running out is a
	// loud failure, never a silent reuse of the first task's tab.
	spares := make([]faketool.HerdrTab, 4)
	for i := range spares {
		spares[i] = faketool.HerdrTab{
			ID:   fmt.Sprintf("%s-%d", ids.TabID, i+2),
			Pane: fmt.Sprintf("%s-%d", ids.PaneID, i+2),
		}
	}
	faketool.Herdr{
		Creates: []faketool.HerdrWorkspace{{ID: ids.WorkspaceID, Label: ids.Label, Tabs: []faketool.HerdrTab{
			{ID: ids.TabID, Label: "1", Pane: ids.PaneID},
		}}},
		TabCreates: spares,
		PaneStatus: ids.PaneStatus,
		Log:        logPath,
	}.Install(t, dir)
}

// writeFakeHerdrWatch writes a herdr fake for the watch scenario: workspace
// list always succeeds (satisfies watcher.Run's reachability probe), and
// "pane get <id>" reports whatever status currently sits in statusDir/<id>,
// letting the test drive independent, per-task status transitions while
// `hand watch` polls in the background just by rewriting one file per task.
// Both are query commands per internal/herdr/client.go's call() doc comment:
// real success is a non-null result object on exit 0, real failure a non-zero
// exit or an error envelope. "workspace list" always succeeds, mirroring the
// real success shape. "pane get" mirrors the real failure shape too - a
// diagnostic on stderr and a non-zero exit, the same contract
// cmd/status_test.go's writeFakeHerdrPaneStatus documents - whenever the
// published status is the sentinel "unreachable", so a test can take a pane
// dark and bring it back while the watcher polls.
// Each "pane get" is logged after the status read, never before: a test waiting on
// the Nth poll before publishing would otherwise still be racing that poll's read.
// The failing branch logs too, so waiting on the Nth probe works for a dark pane
// exactly as it does for a healthy one.
//
// The reported agent comes from statusDir/<id>.agent and is empty unless a test
// calls setPaneAgent, which is what keeps a scenario that never mentions an agent
// out of every harness-capability path. "pane read" answers with the raw text in
// statusDir/<id>.text - herdr's one command whose success shape is bare text on
// stdout rather than a result envelope, per client.go's PaneRead doc comment - and
// "pane send-text"/"pane send-keys" answer with the empty stdout of a void command.
func writeFakeHerdrWatch(t *testing.T, dir, statusDir, logPath string) {
	t.Helper()
	quotedStatusDir, quotedLog := shellSingleQuote(statusDir), shellSingleQuote(logPath)
	body := fmt.Sprintf(`  "workspace list") echo '{"result":{"workspaces":[]}}' ;;
  "pane get")
    status=$(cat %s/"$3" 2>/dev/null || echo idle)
    agent=$(cat %s/"$3".agent 2>/dev/null || echo "")
    echo "herdr pane get $3" >> %s
    if [ "$status" = unreachable ]; then
      echo "herdr: pane $3 not found" >&2
      exit 1
    fi
    printf '{"result":{"pane":{"pane_id":"%%s","tab_id":"t-1","workspace_id":"w-1","agent":"%%s","agent_status":"%%s"}}}\n' "$3" "$agent" "$status"
    ;;
  "pane read")
    echo "herdr pane read $3" >> %s
    cat %s/"$3".text 2>/dev/null
    ;;
  "pane send-text") echo "herdr pane send-text $3 $4" >> %s ;;
  "pane send-keys") echo "herdr pane send-keys $3 $4" >> %s ;;`,
		quotedStatusDir, quotedStatusDir, quotedLog, quotedLog, quotedStatusDir, quotedLog, quotedLog)
	writeFakeDispatch(t, dir, "herdr", "", "$1 $2", body)
}

// writeFakeHerdrSend writes a herdr fake for the send scenario: "pane get"
// reports whatever status currently sits in statusDir/<pane-id>, so a test can
// free a busy composer while `hand send` is waiting on it, and
// "pane send-text"/"pane send-keys" answer with the empty stdout real herdr
// gives a void command (client.go's callVoid doc comment). Every invocation is
// logged with the pid of the hand process that made it, which is what lets a
// test tell two concurrent senders apart; each pane status read is logged after
// the read for the same reason writeFakeHerdrWatch does it.
func writeFakeHerdrSend(t *testing.T, dir, statusDir, logPath string) {
	t.Helper()
	quotedLog := shellSingleQuote(logPath)
	body := fmt.Sprintf(`  "pane get")
    status=$(cat %s/"$3" 2>/dev/null || echo idle)
    echo "sender=$PPID pane get $3" >> %s
    printf '{"result":{"pane":{"pane_id":"%%s","tab_id":"t-1","workspace_id":"w-1","agent":"claude","agent_status":"%%s"}}}\n' "$3" "$status"
    ;;
  "pane send-text") echo "sender=$PPID pane send-text $4" >> %s ;;
  "pane send-keys") echo "sender=$PPID pane send-keys $4" >> %s ;;`,
		shellSingleQuote(statusDir), quotedLog, quotedLog, quotedLog)
	writeFakeDispatch(t, dir, "herdr", "", "$1 $2", body)
}

func writeFakeHerdrUnprobeablePanes(t *testing.T, dir string) {
	t.Helper()
	body := `  "workspace list") echo '{"result":{"workspaces":[]}}' ;;
  "pane get") echo "herdr: pane $3 not found" >&2; exit 1 ;;`
	writeFakeDispatch(t, dir, "herdr", "", "$1 $2", body)
}

// setPaneStatus publishes a pane's status by atomic rename: the fake herdr
// cats these files from a concurrently polling `hand watch`, and a truncating
// in-place write would let it read a phantom empty status mid-update.
func setPaneStatus(t *testing.T, statusDir, paneID, status string) {
	t.Helper()
	publishPaneFile(t, statusDir, paneID, status)
}

// setPaneAgent publishes which agent the fake reports running in a pane, driving
// every harness-capability path the watcher takes.
func setPaneAgent(t *testing.T, statusDir, paneID, agent string) {
	t.Helper()
	publishPaneFile(t, statusDir, paneID+".agent", agent)
}

// setPaneText publishes the scrollback the fake answers `pane read` with.
func setPaneText(t *testing.T, statusDir, paneID, text string) {
	t.Helper()
	publishPaneFile(t, statusDir, paneID+".text", text)
}

func publishPaneFile(t *testing.T, statusDir, name, content string) {
	t.Helper()
	tmp := filepath.Join(statusDir, name+".tmp")
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(statusDir, name)); err != nil {
		t.Fatal(err)
	}
}

// A one-slot treehouse pool at worktreePath, plus any paths it leased out before
// the test began - a scout's worktree a promote hands back, say, which the real
// pool would refuse as unmanaged if it were never declared.
//
// internal/faketool holds the pool: the slot is leased exclusively the way real
// treehouse's pool lock holds it, so a test cannot build the one fixture the real
// backend never produces - two live tasks on one slot - and prove the collision
// guard against a state that never occurs.
func writeFakeTreehouse(t *testing.T, dir, worktreePath string, alreadyLeased ...string) {
	t.Helper()
	faketool.Treehouse{Slots: []string{worktreePath}, Held: alreadyLeased}.Install(t, dir)
}

// The same one-slot pool as a treehouse older than v2.1.0: it leases and frees
// the slot identically but reports no lease_id at all, which is what drives
// worktree.CheckCollision down its path-comparison fallback - the same branch a
// task row written before the lease_id column existed takes.
func writeFakeTreehouseWithoutLeaseIdentity(t *testing.T, dir, worktreePath string) {
	t.Helper()
	faketool.Treehouse{
		Slots:           []string{worktreePath},
		Banner:          "treehouse 0.7.4",
		NoLeaseIdentity: true,
	}.Install(t, dir)
}

// returnFakeWorktree frees a leased pool slot through the fake treehouse's own
// return arm. It stands in for the return `hand teardown` runs before it deletes
// the task's row: when that deletion fails, this is exactly the state left
// behind - the slot back in the pool, a row still naming it.
func returnFakeWorktree(t *testing.T, worktreePath string) {
	t.Helper()
	out, err := exec.Command("treehouse", "return", worktreePath).CombinedOutput()
	if err != nil {
		t.Fatalf("fake treehouse return %s: %v: %s", worktreePath, err, out)
	}
}

// redirectGitRemote makes `git clone <matchURL> <dest>` resolve to a local
// repo instead of the network, via git's url.<target>.insteadOf mechanism,
// appending the rule to the scratch config isolateGitConfig already points
// this test's git invocations at.
func redirectGitRemote(t *testing.T, matchURL, localRepoPath string) {
	t.Helper()
	cfg := isolateGitConfig(t)
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "[url \"file://%s\"]\n\tinsteadOf = %s\n", localRepoPath, matchURL); err != nil {
		t.Fatal(err)
	}
}
