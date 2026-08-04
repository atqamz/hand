//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

func herdrOK(t *testing.T, result any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"result": result})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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

// writeFakeHerdrStatic writes a herdr fake that always reports the same
// workspace/tab/pane identifiers, suitable for a spawn (or promote) followed
// by a teardown within one test: workspace list reports none so a fresh
// workspace is created, and workspace create's own response carries the root
// tab/pane herdr always creates alongside it - the ones the task then renames
// (via "tab rename") and reuses instead of creating a second tab. tab list
// reports exactly the one tab so teardown closes the whole workspace
// (mirroring closeTaskTab's sole-tab behavior).
func writeFakeHerdrStatic(t *testing.T, dir string, ids herdrIDs) {
	t.Helper()
	writeFakeHerdrStaticLogged(t, dir, "", ids)
}

// writeFakeHerdrStaticLogged is writeFakeHerdrStatic plus an invocation log, for tests that need
// to assert which herdr calls were actually made (e.g. that spawn reuses the workspace's own root
// tab instead of creating a second one, and that teardown of that sole tab closes the workspace).
func writeFakeHerdrStaticLogged(t *testing.T, dir, logPath string, ids herdrIDs) {
	t.Helper()
	status := ids.PaneStatus
	if status == "" {
		status = "working"
	}

	workspaceList := herdrOK(t, map[string]any{"workspaces": []any{}})
	workspaceCreate := herdrOK(t, map[string]any{
		"workspace": map[string]any{"workspace_id": ids.WorkspaceID, "label": ids.Label, "tab_count": 1},
		"tab":       map[string]any{"tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "label": "1"},
		"root_pane": map[string]any{"pane_id": ids.PaneID, "tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "agent_status": status},
	})
	tabRename := herdrOK(t, map[string]any{"tab": map[string]any{"tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "label": ids.Label}})
	tabList := herdrOK(t, map[string]any{"tabs": []any{map[string]any{"tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "label": ids.Label}}})
	tabClose := herdrOK(t, map[string]any{"type": "ok"})
	workspaceClose := herdrOK(t, map[string]any{"type": "ok"})
	paneGet := herdrOK(t, map[string]any{"pane": map[string]any{"pane_id": ids.PaneID, "tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "agent": "claude", "agent_status": status}})

	// "pane run"/"send-text"/"send-keys" are void commands: real herdr writes
	// nothing to stdout on success, unlike every query command above. "pane get" reports claude
	// running in the pane and "pane read" a startup frame with no first-run dialog on it, which
	// is what confirmLaunch's poll loop needs to confirm the launch.
	body := fmt.Sprintf(`  "workspace list") echo %s ;;
  "workspace create") echo %s ;;
  "tab rename") echo %s ;;
  "pane run") ;;
  "pane send-text") ;;
  "pane send-keys") ;;
  "pane read") printf 'Welcome to Claude Code\n> \n  ? for shortcuts\n' ;;
  "tab list") echo %s ;;
  "tab close") echo %s ;;
  "workspace close") echo %s ;;
  "pane get") echo %s ;;`,
		shellSingleQuote(workspaceList), shellSingleQuote(workspaceCreate), shellSingleQuote(tabRename),
		shellSingleQuote(tabList), shellSingleQuote(tabClose),
		shellSingleQuote(workspaceClose), shellSingleQuote(paneGet))
	writeFakeDispatch(t, dir, "herdr", logPath, "$1 $2", body)
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

// writeFakeTreehouse writes a treehouse fake managing a one-slot pool at
// worktreePath and no-oping on init, matching worktree.Get/Return and
// treehouseInitIfNeeded's invocation shapes ("get"/"return"/"init" as $1).
// "get" writes a banner line to stderr before its JSON, mirroring real
// treehouse's documented "all banners go to stderr" behavior, so a
// CombinedOutput regression at the call site fails the suite.
//
// The slot is leased exclusively, the way real treehouse's pool lock holds it: a
// "get" while it is still out fails instead of handing one path to two holders,
// and only a "return" of that path frees it again. Without that a test could
// build the one fixture the real backend cannot produce - two live tasks on one
// slot - and prove the collision guard against a state that never occurs.
// Returning any other path stays a no-op success, which is what lets promote
// hand back a scout worktree this pool never owned.
//
// Every "get" mints a fresh lease_id off a counter file, because that is the
// one thing real treehouse (v2.1.0 and up) guarantees is never reused: a pool
// slot handed back out keeps its path and gets a new identity, which is exactly
// what worktree.CheckCollision keys on. A fake reusing one identity across
// acquisitions could not tell the collision guard's two branches apart. The
// counter and the held marker live beside the fake rather than in a fresh temp
// dir so that a test reinstalling this fake - a subtest pointing it at its own
// worktree - keeps counting up instead of reissuing an identity it already
// handed out, and keeps each slot's held state separate.
func writeFakeTreehouse(t *testing.T, dir, worktreePath string) {
	t.Helper()
	writeFakeTreehousePool(t, dir, worktreePath, "treehouse 2.1.0", true)
}

// writeFakeTreehouseWithoutLeaseIdentity is the same one-slot pool as a
// treehouse older than v2.1.0: it leases and frees the slot identically but
// reports no lease_id at all, which is what drives worktree.CheckCollision down
// its path-comparison fallback - the same branch a task row written before the
// lease_id column existed takes.
func writeFakeTreehouseWithoutLeaseIdentity(t *testing.T, dir, worktreePath string) {
	t.Helper()
	writeFakeTreehousePool(t, dir, worktreePath, "treehouse 0.7.4", false)
}

func writeFakeTreehousePool(t *testing.T, dir, worktreePath, banner string, leaseIdentity bool) {
	t.Helper()
	counter := shellSingleQuote(filepath.Join(dir, ".treehouse-leases"))
	held := shellSingleQuote(filepath.Join(dir, ".treehouse-held-"+strings.ReplaceAll(strings.Trim(worktreePath, "/"), "/", "_")))
	path := shellSingleQuote(worktreePath)
	payload := `'{"path":"%s","lease_id":"lease-%s"}\n' ` + path + ` "$n"`
	if !leaseIdentity {
		payload = `'{"path":"%s"}\n' ` + path
	}
	// Truncated rather than removed to free the slot, and tested with -s rather
	// than -e: rm is not on this suite's hermetic PATH, and redirection is a shell
	// builtin.
	body := fmt.Sprintf(`  get)
    echo %[1]s >&2
    if [ -s %[2]s ]; then
      printf 'treehouse: pool slot %%s is already leased\n' %[3]s >&2
      exit 1
    fi
    n=$(cat %[4]s 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > %[4]s
    echo "$n" > %[2]s
    printf %[5]s
    ;;
  return)
    if [ "$2" = %[3]s ]; then : > %[2]s; fi
    echo ok
    ;;
  init) echo ok ;;`, shellSingleQuote(banner), held, path, counter, payload)
	writeFakeDispatch(t, dir, "treehouse", "", "$1", body)
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
