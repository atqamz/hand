//go:build contract

package contract

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type herdrTab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

type herdrPane struct {
	PaneID string `json:"pane_id"`
	TabID  string `json:"tab_id"`
}

type herdrEnvelope struct {
	Result struct {
		Type      string `json:"type"`
		Workspace struct {
			WorkspaceID string `json:"workspace_id"`
			Label       string `json:"label"`
		} `json:"workspace"`
		Workspaces []struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"workspaces"`
		Tab      herdrTab   `json:"tab"`
		Tabs     []herdrTab `json:"tabs"`
		RootPane herdrPane  `json:"root_pane"`
		Pane     herdrPane  `json:"pane"`
	} `json:"result"`
}

func envelope(t *testing.T, res result) herdrEnvelope {
	t.Helper()
	var got herdrEnvelope
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("parse stdout %q: %v", res.stdout, err)
	}
	return got
}

// A workspace of its own, labelled so an operator finding a leaked one knows
// what left it. Cleanup closes that id and nothing else, so the fleet's own
// workspaces are never a candidate.
func newWorkspace(t *testing.T, cwd string) herdrEnvelope {
	t.Helper()
	created := envelope(t, run(t, cwd, "herdr", "workspace", "create",
		"--no-focus", "--cwd", cwd, "--label", "hand-contract-test").requireCode(t, 0))
	id := created.Result.Workspace.WorkspaceID
	if id == "" {
		t.Fatalf("workspace create returned no id: %s", created.Result.Type)
	}
	t.Cleanup(func() {
		listed := envelope(t, run(t, cwd, "herdr", "workspace", "list"))
		for _, ws := range listed.Result.Workspaces {
			if ws.WorkspaceID == id {
				run(t, cwd, "herdr", "workspace", "close", id)
				return
			}
		}
	})
	return created
}

func tabIDs(tabs []herdrTab) []string {
	ids := make([]string, len(tabs))
	for i, tab := range tabs {
		ids[i] = tab.TabID + "=" + tab.Label
	}
	return ids
}

func TestHerdrClosingTheSoleTabTakesTheWorkspaceWithIt(t *testing.T) {
	requireBin(t, "herdr")
	cwd := t.TempDir()

	created := newWorkspace(t, cwd)
	ws := created.Result.Workspace.WorkspaceID
	root, rootPane := created.Result.Tab.TabID, created.Result.RootPane.PaneID
	if created.Result.Tab.Label != "1" {
		t.Fatalf("root tab label = %q, want \"1\" - the label spawn renames", created.Result.Tab.Label)
	}

	second := envelope(t, run(t, cwd, "herdr", "tab", "create",
		"--workspace", ws, "--no-focus", "--cwd", cwd, "--label", "second").requireCode(t, 0))
	secondTab := second.Result.Tab.TabID

	listed := envelope(t, run(t, cwd, "herdr", "tab", "list", "--workspace", ws).requireCode(t, 0))
	if got := tabIDs(listed.Result.Tabs); len(got) != 2 || got[0] != root+"=1" || got[1] != secondTab+"=second" {
		t.Fatalf("tab list = %v, want the two tabs in creation order", got)
	}

	run(t, cwd, "herdr", "tab", "rename", root, "renamed").requireCode(t, 0)
	listed = envelope(t, run(t, cwd, "herdr", "tab", "list", "--workspace", ws).requireCode(t, 0))
	if listed.Result.Tabs[0].Label != "renamed" {
		t.Fatalf("tab list reports label %q after rename", listed.Result.Tabs[0].Label)
	}

	run(t, cwd, "herdr", "tab", "close", root).requireCode(t, 0)
	run(t, cwd, "herdr", "tab", "close", root).
		requireCode(t, 1).
		requireStderrContains(t, `"code":"tab_not_found"`)
	run(t, cwd, "herdr", "pane", "get", rootPane).
		requireCode(t, 1).
		requireStderrContains(t, `"code":"pane_not_found"`)

	run(t, cwd, "herdr", "tab", "close", secondTab).requireCode(t, 0)
	run(t, cwd, "herdr", "tab", "list", "--workspace", ws).
		requireCode(t, 1).
		requireStderrContains(t, `"code":"workspace_not_found"`)
	run(t, cwd, "herdr", "workspace", "close", ws).
		requireCode(t, 1).
		requireStderrContains(t, `"code":"workspace_not_found"`)

	listed = envelope(t, run(t, cwd, "herdr", "workspace", "list").requireCode(t, 0))
	for _, live := range listed.Result.Workspaces {
		if live.WorkspaceID == ws {
			t.Fatalf("workspace %s is still listed after its last tab closed", ws)
		}
	}
}

// Bare text on success, unlike every other command, so an envelope here would
// be read as pane content.
func readPane(t *testing.T, cwd, pane, source string) string {
	t.Helper()
	res := run(t, cwd, "herdr", "pane", "read", pane, "--source", source, "--lines", "300").requireCode(t, 0)
	if strings.HasPrefix(strings.TrimSpace(res.stdout), "{") {
		t.Fatalf("pane read answered an envelope: %s", res.stdout)
	}
	return res.stdout
}

func TestHerdrPaneRunIsVoidAndPaneReadIsBareText(t *testing.T) {
	requireBin(t, "herdr")
	cwd := t.TempDir()

	created := newWorkspace(t, cwd)
	ws := created.Result.Workspace.WorkspaceID
	pane := created.Result.RootPane.PaneID

	// Empty at first from either source, and empty from one while the other has
	// content, which is why confirmLaunch polls instead of reading once.
	if first := readPane(t, cwd, pane, "recent"); strings.TrimSpace(first) != "" {
		t.Logf("recent read had content before anything ran: %q", first)
	}

	ran := run(t, cwd, "herdr", "pane", "run", pane, "printf 'contract-probe\\n'").requireCode(t, 0)
	if strings.TrimSpace(ran.stdout) != "" {
		t.Fatalf("pane run wrote %q to stdout, want a void command", ran.stdout)
	}

	var last string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		last = readPane(t, cwd, pane, "recent")
		if strings.Contains(last, "contract-probe") {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !strings.Contains(last, "contract-probe") {
		t.Fatalf("recent read never showed the command run in the pane:\n%s", last)
	}

	run(t, cwd, "herdr", "workspace", "close", ws).requireCode(t, 0)
	run(t, cwd, "herdr", "pane", "run", pane, "true").
		requireCode(t, 1).
		requireStderrContains(t, `"code":"pane_not_found"`)
}

func TestHerdrPaneSendTextAndKeysAreVoidAndRejectMissingPane(t *testing.T) {
	requireBin(t, "herdr")
	cwd := t.TempDir()
	created := newWorkspace(t, cwd)
	ws := created.Result.Workspace.WorkspaceID
	pane := created.Result.RootPane.PaneID
	marker := "contract-send-probe"
	text := run(t, cwd, "herdr", "pane", "send-text", pane, "printf '"+marker+"\\n'").requireCode(t, 0)
	if strings.TrimSpace(text.stdout) != "" {
		t.Fatalf("pane send-text wrote %q to stdout, want a void command", text.stdout)
	}
	keys := run(t, cwd, "herdr", "pane", "send-keys", pane, "Enter").requireCode(t, 0)
	if strings.TrimSpace(keys.stdout) != "" {
		t.Fatalf("pane send-keys wrote %q to stdout, want a void command", keys.stdout)
	}
	var last string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		last = readPane(t, cwd, pane, "recent")
		if strings.Contains(last, marker) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !strings.Contains(last, marker) {
		t.Fatalf("recent read never showed sent text:\n%s", last)
	}
	run(t, cwd, "herdr", "pane", "send-text", pane, "true").requireCode(t, 0)
	run(t, cwd, "herdr", "pane", "send-keys", pane, "Enter").requireCode(t, 0)
	run(t, cwd, "herdr", "workspace", "close", ws).requireCode(t, 0)
	run(t, cwd, "herdr", "pane", "send-text", pane, "true").
		requireCode(t, 1).
		requireStderrContains(t, `"code":"pane_not_found"`)
}
