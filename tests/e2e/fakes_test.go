//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// binDir returns a directory prepended to PATH for the rest of the test, so
// fake binaries written there are found before any real tool of the same name.
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

// shellSingleQuote wraps s for safe embedding as a single-quoted shell literal.
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
// workspace is created, tab list reports exactly the one tab so teardown
// closes the whole workspace (mirroring closeTaskTab's sole-tab behavior).
func writeFakeHerdrStatic(t *testing.T, dir string, ids herdrIDs) {
	t.Helper()
	status := ids.PaneStatus
	if status == "" {
		status = "working"
	}

	workspaceList := herdrOK(t, map[string]any{"workspaces": []any{}})
	workspaceCreate := herdrOK(t, map[string]any{"workspace": map[string]any{"workspace_id": ids.WorkspaceID, "label": ids.Label, "tab_count": 1}})
	tabCreate := herdrOK(t, map[string]any{
		"tab":       map[string]any{"tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "label": ids.Label},
		"root_pane": map[string]any{"pane_id": ids.PaneID, "tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "agent_status": status},
	})
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
  "tab create") echo %s ;;
  "pane run") ;;
  "pane send-text") ;;
  "pane send-keys") ;;
  "pane read") printf 'Welcome to Claude Code\n> \n  ? for shortcuts\n' ;;
  "tab list") echo %s ;;
  "tab close") echo %s ;;
  "workspace close") echo %s ;;
  "pane get") echo %s ;;`,
		shellSingleQuote(workspaceList), shellSingleQuote(workspaceCreate), shellSingleQuote(tabCreate),
		shellSingleQuote(tabList), shellSingleQuote(tabClose),
		shellSingleQuote(workspaceClose), shellSingleQuote(paneGet))
	writeFakeDispatch(t, dir, "herdr", "", "$1 $2", body)
}

// writeFakeHerdrWatch writes a herdr fake for the watch scenario: workspace
// list always succeeds (satisfies watcher.Run's reachability probe), and
// "pane get <id>" reports whatever status currently sits in statusDir/<id>,
// letting the test drive independent, per-task status transitions while
// `hand watch` polls in the background just by rewriting one file per task.
// Every invocation is appended to logPath so the test can tell "the watcher
// has polled this pane" apart from "the watcher hasn't got there yet".
func writeFakeHerdrWatch(t *testing.T, dir, statusDir, logPath string) {
	t.Helper()
	body := fmt.Sprintf(`  "workspace list") echo '{"result":{"workspaces":[]}}' ;;
  "pane get")
    status=$(cat %s/"$3" 2>/dev/null || echo idle)
    printf '{"result":{"pane":{"pane_id":"%%s","tab_id":"t-1","workspace_id":"w-1","agent_status":"%%s"}}}\n' "$3" "$status"
    ;;`, shellSingleQuote(statusDir))
	writeFakeDispatch(t, dir, "herdr", logPath, "$1 $2", body)
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
