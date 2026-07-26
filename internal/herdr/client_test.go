package herdr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeHerdr fakes herdr with the caller's own script body, so each test
// below picks the response shape it needs. Real herdr answers a query command
// with a JSON envelope carrying a non-null result object on stdout, answers a
// void command with empty stdout, and reports failure as an envelope error
// object that may come with any exit status - which is why call/callVoid
// (client.go) let an error envelope win whenever one is present and fall back
// to the exit status only when stdout is empty or the envelope parsed clean
// (env.Error == nil). The tests here fake each of those shapes verbatim,
// including both error-envelope-with-exit-1 and error-envelope-with-exit-0;
// other packages' herdr fakes cite this file rather than re-deriving them.
func writeFakeHerdr(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	path := filepath.Join(bin, "herdr")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestWorkspaceListParsesResult(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{"workspaces":[{"workspace_id":"wA","label":"proj","tab_count":2}]}}'`)
	c := NewClient()
	got, err := c.WorkspaceList()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].WorkspaceID != "wA" || got[0].Label != "proj" || got[0].TabCount != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestFindWorkspaceByLabelFound(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{"workspaces":[{"workspace_id":"wA","label":"proj"}]}}'`)
	c := NewClient()
	ws, found, err := c.FindWorkspaceByLabel("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !found || ws.WorkspaceID != "wA" {
		t.Fatalf("got %+v, %v", ws, found)
	}
}

func TestFindWorkspaceByLabelNotFound(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{"workspaces":[]}}'`)
	c := NewClient()
	_, found, err := c.FindWorkspaceByLabel("proj")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("expected found = false")
	}
}

func TestCallReturnsErrorOnEnvelopeError(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","error":{"code":"not_found","message":"pane missing"}}'; exit 1`)
	c := NewClient()
	if _, err := c.PaneGet("wA:pB"); err == nil || !strings.Contains(err.Error(), "pane missing") {
		t.Fatalf("got err %v, want pane missing", err)
	}
}

func TestCallFailsOnNonZeroExitWithNoStdout(t *testing.T) {
	writeFakeHerdr(t, `echo "binary crashed" >&2; exit 1`)
	c := NewClient()
	if _, err := c.PaneGet("wA:pB"); err == nil || !strings.Contains(err.Error(), "binary crashed") {
		t.Fatalf("got err %v, want binary crashed failure", err)
	}
}

func TestCallRejectsNullResult(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":null}'`)
	c := NewClient()
	if _, err := c.WorkspaceList(); err == nil || !strings.Contains(err.Error(), "null result") {
		t.Fatalf("got err %v, want null result failure", err)
	}
}

func TestCallRejectsNonObjectResult(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":[]}'`)
	c := NewClient()
	if _, err := c.PaneGet("wA:pB"); err == nil || !strings.Contains(err.Error(), "not an object") {
		t.Fatalf("got err %v, want non-object result failure", err)
	}
}

func TestPaneRunSucceedsOnEmptyStdout(t *testing.T) {
	writeFakeHerdr(t, ``)
	c := NewClient()
	if err := c.PaneRun("wA:pB", "echo hi"); err != nil {
		t.Fatal(err)
	}
}

func TestPaneRunSurfacesErrorEnvelopeEvenOnExitZero(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","error":{"code":"pane_not_found","message":"pane missing"}}'`)
	c := NewClient()
	if err := c.PaneRun("wA:pB", "echo hi"); err == nil || !strings.Contains(err.Error(), "pane missing") {
		t.Fatalf("got err %v, want pane missing", err)
	}
}

func TestPaneSendTextSucceedsOnEmptyStdout(t *testing.T) {
	writeFakeHerdr(t, ``)
	c := NewClient()
	if err := c.PaneSendText("wA:pB", "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestPaneSendKeysPassesKeys(t *testing.T) {
	writeFakeHerdr(t, `
if [ "$1" != "pane" ] || [ "$2" != "send-keys" ] || [ "$3" != "wA:pB" ] || [ "$4" != "Enter" ]; then
	echo "unexpected args: $@" >&2
	exit 1
fi
`)
	c := NewClient()
	if err := c.PaneSendKeys("wA:pB", "Enter"); err != nil {
		t.Fatal(err)
	}
}

func TestPaneSendKeysSucceedsOnEmptyStdout(t *testing.T) {
	writeFakeHerdr(t, ``)
	c := NewClient()
	if err := c.PaneSendKeys("wA:pB", "Enter"); err != nil {
		t.Fatal(err)
	}
}

func TestCallVoidFailsOnNonZeroExitWithNoStdout(t *testing.T) {
	writeFakeHerdr(t, `echo "binary crashed" >&2; exit 1`)
	c := NewClient()
	if err := c.PaneRun("wA:pB", "echo hi"); err == nil || !strings.Contains(err.Error(), "binary crashed") {
		t.Fatalf("got err %v, want binary crashed failure", err)
	}
}

func TestTabCreateParsesTabAndRootPane(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{"tab":{"tab_id":"wA:tB","workspace_id":"wA","label":"task"},"root_pane":{"pane_id":"wA:pC","tab_id":"wA:tB","agent_status":"idle"}}}'`)
	c := NewClient()
	tab, pane, err := c.TabCreate("wA", "/tmp/wt", "task")
	if err != nil {
		t.Fatal(err)
	}
	if tab.TabID != "wA:tB" || pane.PaneID != "wA:pC" || pane.AgentStatus != StatusIdle {
		t.Fatalf("got tab=%+v pane=%+v", tab, pane)
	}
}

func TestPaneGetParsesAgentStatus(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"working"}}}'`)
	c := NewClient()
	pane, err := c.PaneGet("wA:pB")
	if err != nil {
		t.Fatal(err)
	}
	if pane.AgentStatus != StatusWorking {
		t.Fatalf("got %q, want working", pane.AgentStatus)
	}
}

func TestWaitComposerEmptyReturnsWhenIdle(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"idle"}}}'`)
	c := NewClient()
	if err := c.WaitComposerEmpty("wA:pB", time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWaitComposerEmptyTimesOutWhileWorking(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"working"}}}'`)
	c := NewClient()
	err := c.WaitComposerEmpty("wA:pB", 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "composer not empty") {
		t.Fatalf("got err %v, want composer not empty timeout", err)
	}
}

func TestTabListParsesResult(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{"tabs":[{"tab_id":"wA:tB","workspace_id":"wA"}]}}'`)
	c := NewClient()
	tabs, err := c.TabList("wA")
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 1 || tabs[0].TabID != "wA:tB" {
		t.Fatalf("got %+v", tabs)
	}
}

// TestPaneReadReadsRecentScrollback pins --source recent: a 23-row unattached pane clips the option
// and footer lines that identify a first-run dialog, and a dialog that matches nothing is confirmed
// as a started worker.
func TestPaneReadReadsRecentScrollback(t *testing.T) {
	writeFakeHerdr(t, `case "$*" in
*"--source recent"*) printf 'Welcome to Claude Code\n' ;;
*) echo "unexpected read args: $*" >&2; exit 1 ;;
esac`)
	c := NewClient()
	got, err := c.PaneRead("wA:pB", 60)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Welcome to Claude Code" {
		t.Fatalf("got %q, want the pane text", got)
	}
}

// TestPaneReadRejectsErrorBodyOnExitZero pins that the bare {code,message} failure body is
// honored even when herdr exits 0: read as pane text it would look like a dialog-free pane and
// confirm a worker no one ever observed.
func TestPaneReadRejectsErrorBodyOnExitZero(t *testing.T) {
	writeFakeHerdr(t, `printf '{"code":"pane_not_found","message":"no such pane"}'; exit 0`)
	c := NewClient()
	if _, err := c.PaneRead("wA:pB", 60); err == nil || !strings.Contains(err.Error(), "pane_not_found") {
		t.Fatalf("got err %v, want pane_not_found", err)
	}
}
