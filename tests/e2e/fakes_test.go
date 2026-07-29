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
// cmd/notify.go execs to run a notify template; and cat, which the fake herdr
// scripts below use to read a pane's status file. Everything hand execs that is
// not listed here is faked per test (backendsThisSuiteFakes, e2e_test.go), so
// leaving those unreachable turns a missing fake into a loud failure instead of
// a call against the developer's real tools.
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
// exit or an error envelope. This fake always succeeds for both, mirroring
// the real success shape; the failure path is exercised for real elsewhere
// (see cmd/status_test.go's writeFakeHerdrPaneStatus for the full contract).
// Each "pane get" is appended to logPath so the test can tell "the watcher has
// polled this pane" apart from "the watcher hasn't got there yet". It is logged
// after the status read, not by writeFakeDispatch before the dispatch: a test
// that publishes a new status on seeing the poll would otherwise still be racing
// that poll's own read, and a status change swallowed into the seeding tick is a
// transition that never fires.
func writeFakeHerdrWatch(t *testing.T, dir, statusDir, logPath string) {
	t.Helper()
	body := fmt.Sprintf(`  "workspace list") echo '{"result":{"workspaces":[]}}' ;;
  "pane get")
    status=$(cat %s/"$3" 2>/dev/null || echo idle)
    echo "herdr pane get $3" >> %s
    printf '{"result":{"pane":{"pane_id":"%%s","tab_id":"t-1","workspace_id":"w-1","agent_status":"%%s"}}}\n' "$3" "$status"
    ;;`, shellSingleQuote(statusDir), shellSingleQuote(logPath))
	writeFakeDispatch(t, dir, "herdr", "", "$1 $2", body)
}

// writeFakeHerdrUnprobeablePanes writes a herdr fake that is reachable but
// answers no pane: the shape `hand watch --until-event` has to refuse to arm on,
// since a task it cannot see has no transition to ever deliver. Real herdr's
// failure is a non-zero exit with the reason on stderr (cmd/status_test.go's
// writeFakeHerdrPaneStatus documents the full contract).
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
	tmp := filepath.Join(statusDir, paneID+".tmp")
	if err := os.WriteFile(tmp, []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(statusDir, paneID)); err != nil {
		t.Fatal(err)
	}
}

// writeFakeTreehouse writes a treehouse fake that always leases worktreePath
// and no-ops on return/init, matching worktree.Get/Return and
// treehouseInitIfNeeded's invocation shapes ("get"/"return"/"init" as $1).
// "get" writes a banner line to stderr before its JSON, mirroring real
// treehouse's documented "all banners go to stderr" behavior, so a
// CombinedOutput regression at the call site fails the suite.
func writeFakeTreehouse(t *testing.T, dir, worktreePath string) {
	t.Helper()
	body := fmt.Sprintf(`  get) echo 'treehouse 0.7.4' >&2; printf '{"path":"%%s"}\n' %s ;;
  return) echo ok ;;
  init) echo ok ;;`, shellSingleQuote(worktreePath))
	writeFakeDispatch(t, dir, "treehouse", "", "$1", body)
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
