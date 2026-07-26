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
	paneRun := herdrOK(t, map[string]any{})
	tabList := herdrOK(t, map[string]any{"tabs": []any{map[string]any{"tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "label": ids.Label}}})
	tabClose := herdrOK(t, map[string]any{})
	workspaceClose := herdrOK(t, map[string]any{})
	paneGet := herdrOK(t, map[string]any{"pane": map[string]any{"pane_id": ids.PaneID, "tab_id": ids.TabID, "workspace_id": ids.WorkspaceID, "agent_status": status}})

	script := fmt.Sprintf(`case "$1 $2" in
  "workspace list") echo %s ;;
  "workspace create") echo %s ;;
  "tab create") echo %s ;;
  "pane run") echo %s ;;
  "tab list") echo %s ;;
  "tab close") echo %s ;;
  "workspace close") echo %s ;;
  "pane get") echo %s ;;
  *) echo "unexpected herdr invocation: $@" >&2; exit 1 ;;
esac
`,
		shellSingleQuote(workspaceList), shellSingleQuote(workspaceCreate), shellSingleQuote(tabCreate),
		shellSingleQuote(paneRun), shellSingleQuote(tabList), shellSingleQuote(tabClose),
		shellSingleQuote(workspaceClose), shellSingleQuote(paneGet))
	writeFakeBin(t, dir, "herdr", script)
}

// writeFakeHerdrWatch writes a herdr fake for the watch scenario: workspace
// list always succeeds (satisfies watcher.Run's reachability probe), and
// "pane get <id>" reports whatever status currently sits in statusDir/<id>,
// letting the test drive independent, per-task status transitions while
// `hand watch` polls in the background just by rewriting one file per task.
func writeFakeHerdrWatch(t *testing.T, dir, statusDir string) {
	t.Helper()
	script := fmt.Sprintf(`case "$1 $2" in
  "workspace list") echo '{"result":{"workspaces":[]}}' ;;
  "pane get")
    status=$(cat %s/"$3" 2>/dev/null || echo idle)
    printf '{"result":{"pane":{"pane_id":"%%s","tab_id":"t-1","workspace_id":"w-1","agent_status":"%%s"}}}\n' "$3" "$status"
    ;;
  *) echo "unexpected herdr invocation: $@" >&2; exit 1 ;;
esac
`, shellSingleQuote(statusDir))
	writeFakeBin(t, dir, "herdr", script)
}

func setPaneStatus(t *testing.T, statusDir, paneID, status string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(statusDir, paneID), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeFakeTreehouse writes a treehouse fake that always leases worktreePath
// and no-ops on return/init, matching worktree.Get/Return and
// treehouseInitIfNeeded's invocation shapes ("get"/"return"/"init" as $1).
func writeFakeTreehouse(t *testing.T, dir, worktreePath string) {
	t.Helper()
	script := fmt.Sprintf(`case "$1" in
  get) printf '{"path":"%%s"}\n' %s ;;
  return) echo ok ;;
  init) echo ok ;;
  *) echo "unexpected treehouse invocation: $@" >&2; exit 1 ;;
esac
`, shellSingleQuote(worktreePath))
	writeFakeBin(t, dir, "treehouse", script)
}

// writeFakeGh writes a gh fake with a caller-supplied case body, mirroring
// cmd/merge_test.go's writeFakeGh: merge and PR-status scenarios each need a
// distinct dispatch (pr checks / pr merge / pr view), so the body is not
// fixed here the way herdr/treehouse's are.
func writeFakeGh(t *testing.T, dir, caseBody string) {
	t.Helper()
	writeFakeBin(t, dir, "gh", "case \"$1 $2\" in\n"+caseBody+"\n*) echo \"unexpected gh invocation: $@\" >&2; exit 1 ;;\nesac\n")
}

// redirectGitRemote makes `git clone <matchURL> <dest>` resolve to a local
// repo instead of the network, via git's url.<target>.insteadOf mechanism.
// GIT_CONFIG_GLOBAL (git >= 2.32) points git at an isolated config file for
// the rest of the test instead of the real user's ~/.gitconfig.
func redirectGitRemote(t *testing.T, matchURL, localRepoPath string) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	content := fmt.Sprintf("[url \"file://%s\"]\n\tinsteadOf = %s\n", localRepoPath, matchURL)
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}
