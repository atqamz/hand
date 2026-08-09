package herdr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fakes herdr with the caller's own script body, so each test picks the response shape it needs.
// Every real shape is reproduced verbatim - a query's non-null result envelope, a void command's
// empty stdout, an error envelope at exit 1 and at exit 0 - and other packages cite these.
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

// Pins the fix for the orphan-tab bug: herdr creates a root tab and pane at cwd as a side effect
// of creating a workspace, so WorkspaceCreate must return them rather than discard them - a caller
// that then creates its own tab leaves the root tab behind as an unowned live shell.
func TestWorkspaceCreateParsesRootTabAndPane(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{"workspace":{"workspace_id":"wA","label":"proj","tab_count":1},"tab":{"tab_id":"wA:tB","workspace_id":"wA","label":"1"},"root_pane":{"pane_id":"wA:pC","tab_id":"wA:tB","agent_status":"idle"}}}'`)
	c := NewClient()
	ws, tab, pane, err := c.WorkspaceCreate("/tmp/clone", "proj")
	if err != nil {
		t.Fatal(err)
	}
	if ws.WorkspaceID != "wA" {
		t.Fatalf("got workspace %+v", ws)
	}
	if tab.TabID != "wA:tB" {
		t.Fatalf("got tab %+v", tab)
	}
	if pane.PaneID != "wA:pC" || pane.AgentStatus != StatusIdle {
		t.Fatalf("got pane %+v", pane)
	}
}

// Pins the fix for atqamz/hand#109: a pane is a child of the herdr server, so it otherwise
// inherits any CLAUDE_CODE_CHILD_SESSION, CLAUDE_CODE_SESSION_ID, or CLAUDECODE the server was
// started under, silently killing the worker's transcript. All three are blanked either way.
func TestWorkspaceCreateSanitizesInheritedHarnessMarkers(t *testing.T) {
	writeFakeHerdr(t, `
echo "$@" >> "$HERDR_CALL_LOG"
printf '{"id":"cli:1","result":{"workspace":{"workspace_id":"wA","label":"proj"},"tab":{"tab_id":"wA:tB","workspace_id":"wA"},"root_pane":{"pane_id":"wA:pC","tab_id":"wA:tB"}}}'
`)
	callLog := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("HERDR_CALL_LOG", callLog)

	c := NewClient()
	if _, _, _, err := c.WorkspaceCreate("/tmp/clone", "proj"); err != nil {
		t.Fatal(err)
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--env CLAUDE_CODE_CHILD_SESSION=", "--env CLAUDE_CODE_SESSION_ID=", "--env CLAUDECODE="} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("calls = %q, want %q", calls, want)
		}
	}
}

func TestWorkspaceCreateRejectsMissingRootTabOrPane(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{"workspace":{"workspace_id":"wA","label":"proj"}}}'`)
	c := NewClient()
	if _, _, _, err := c.WorkspaceCreate("/tmp/clone", "proj"); err == nil {
		t.Fatal("expected an error when the response omits the root tab and pane")
	}
}

// Pins the fix for atqamz/hand#74: a workspace_created result missing tab or root_pane still
// means herdr created the workspace (a protocol predating those fields), so WorkspaceCreate closes
// it before the parse error reaches the caller - nothing downstream learns the ID.
func TestWorkspaceCreateClosesWorkspaceOnPartialResponse(t *testing.T) {
	writeFakeHerdr(t, `
echo "$@" >> "$HERDR_CALL_LOG"
case "$1 $2" in
"workspace create")
	printf '{"id":"cli:1","result":{"workspace":{"workspace_id":"wA","label":"proj"}}}'
	;;
"workspace close")
	if [ "$3" != "wA" ]; then
		echo "unexpected close target: $@" >&2
		exit 1
	fi
	printf '{"id":"cli:1","result":{"type":"ok"}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	callLog := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("HERDR_CALL_LOG", callLog)

	c := NewClient()
	if _, _, _, err := c.WorkspaceCreate("/tmp/clone", "proj"); err == nil {
		t.Fatal("expected an error when the response omits the root tab and pane")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "workspace close wA") {
		t.Fatalf("calls = %q, want the workspace herdr created to be closed", calls)
	}
}

// Covers the sibling of the partial-response path: encoding/json keeps decoding past its first
// type error, so a response that types a field wrongly still yields a workspace ID herdr has
// already created and this call must still close.
func TestWorkspaceCreateClosesWorkspaceOnMalformedResponse(t *testing.T) {
	writeFakeHerdr(t, `
echo "$@" >> "$HERDR_CALL_LOG"
case "$1 $2" in
"workspace create")
	printf '{"id":"cli:1","result":{"workspace":{"workspace_id":"wA","label":"proj"},"tab":"none"}}'
	;;
"workspace close")
	if [ "$3" != "wA" ]; then
		echo "unexpected close target: $@" >&2
		exit 1
	fi
	printf '{"id":"cli:1","result":{"type":"ok"}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	callLog := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("HERDR_CALL_LOG", callLog)

	c := NewClient()
	if _, _, _, err := c.WorkspaceCreate("/tmp/clone", "proj"); err == nil {
		t.Fatal("expected an error when the response mistypes the tab field")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "workspace close wA") {
		t.Fatalf("calls = %q, want the workspace herdr created to be closed", calls)
	}
}

func TestTabRenameSendsCorrectArgs(t *testing.T) {
	writeFakeHerdr(t, `
if [ "$1 $2 $3 $4" != "tab rename wA:tB task-1" ]; then
	echo "unexpected args: $@" >&2
	exit 1
fi
printf '{"id":"cli:1","result":{"tab":{"tab_id":"wA:tB","workspace_id":"wA","label":"task-1"}}}'
`)
	c := NewClient()
	if err := c.TabRename("wA:tB", "task-1"); err != nil {
		t.Fatal(err)
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

// TabCreate's half of TestWorkspaceCreateSanitizesInheritedHarnessMarkers: a task landing in an
// already-existing workspace creates its own tab via TabCreate instead, and that path must be
// sanitized too.
func TestTabCreateSanitizesInheritedHarnessMarkers(t *testing.T) {
	writeFakeHerdr(t, `
echo "$@" >> "$HERDR_CALL_LOG"
printf '{"id":"cli:1","result":{"tab":{"tab_id":"wA:tB","workspace_id":"wA"},"root_pane":{"pane_id":"wA:pC","tab_id":"wA:tB"}}}'
`)
	callLog := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("HERDR_CALL_LOG", callLog)

	c := NewClient()
	if _, _, err := c.TabCreate("wA", "/tmp/wt", "task"); err != nil {
		t.Fatal(err)
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--env CLAUDE_CODE_CHILD_SESSION=", "--env CLAUDE_CODE_SESSION_ID=", "--env CLAUDECODE="} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("calls = %q, want %q", calls, want)
		}
	}
}

// Pins the fix for atqamz/hand#119: a tab-create result missing root_pane still means herdr
// created the tab (same as atqamz/hand#74's WorkspaceCreate case), so TabCreate closes it
// before the parse error reaches the caller - nothing downstream learns the tab ID.
func TestTabCreateClosesTabOnPartialResponse(t *testing.T) {
	writeFakeHerdr(t, `
echo "$@" >> "$HERDR_CALL_LOG"
case "$1 $2" in
"tab create")
	printf '{"id":"cli:1","result":{"tab":{"tab_id":"wA:tB","workspace_id":"wA"}}}'
	;;
"tab close")
	if [ "$3" != "wA:tB" ]; then
		echo "unexpected close target: $@" >&2
		exit 1
	fi
	printf '{"id":"cli:1","result":{"type":"ok"}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	callLog := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("HERDR_CALL_LOG", callLog)

	c := NewClient()
	if _, _, err := c.TabCreate("wA", "/tmp/wt", "task"); err == nil {
		t.Fatal("expected an error when the response omits the root pane")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "tab close wA:tB") {
		t.Fatalf("calls = %q, want the tab herdr created to be closed", calls)
	}
}

// Covers the sibling of the partial-response path: encoding/json keeps decoding past its first
// type error, so a response that types a field wrongly still yields a tab ID herdr has already
// created and this call must still close it.
func TestTabCreateClosesTabOnMalformedResponse(t *testing.T) {
	writeFakeHerdr(t, `
echo "$@" >> "$HERDR_CALL_LOG"
case "$1 $2" in
"tab create")
	printf '{"id":"cli:1","result":{"tab":{"tab_id":"wA:tB","workspace_id":"wA"},"root_pane":"none"}}'
	;;
"tab close")
	if [ "$3" != "wA:tB" ]; then
		echo "unexpected close target: $@" >&2
		exit 1
	fi
	printf '{"id":"cli:1","result":{"type":"ok"}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	callLog := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("HERDR_CALL_LOG", callLog)

	c := NewClient()
	if _, _, err := c.TabCreate("wA", "/tmp/wt", "task"); err == nil {
		t.Fatal("expected an error when the response mistypes the root pane field")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "tab close wA:tB") {
		t.Fatalf("calls = %q, want the tab herdr created to be closed", calls)
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
	if !errors.Is(err, ErrComposerBusyTimeout) {
		t.Fatalf("got err %v, want ErrComposerBusyTimeout", err)
	}
}

func TestWaitComposerEmptyPaneFailureIsNotABusyTimeout(t *testing.T) {
	// A pane that stops answering can never free, so the caller must be able to
	// tell this apart from the retryable timeout above.
	writeFakeHerdr(t, `printf '{"id":"cli:1","error":{"code":"pane_not_found","message":"no such pane"}}'
exit 1`)
	c := NewClient()
	err := c.WaitComposerEmpty("wA:pB", time.Second)
	if err == nil || errors.Is(err, ErrComposerBusyTimeout) {
		t.Fatalf("got err %v, want a non-timeout pane failure", err)
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

// Pins --source recent: a 23-row unattached pane clips the option and footer lines that identify a
// first-run dialog, and a dialog that matches nothing is confirmed as a started worker.
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

// Pins that the bare {code,message} failure body is honored even when herdr exits 0: read as pane
// text it would look like a dialog-free pane and confirm a worker no one ever observed.
func TestPaneReadRejectsErrorBodyOnExitZero(t *testing.T) {
	writeFakeHerdr(t, `printf '{"code":"pane_not_found","message":"no such pane"}'; exit 0`)
	c := NewClient()
	if _, err := c.PaneRead("wA:pB", 60); err == nil || !strings.Contains(err.Error(), "pane_not_found") {
		t.Fatalf("got err %v, want pane_not_found", err)
	}
}
