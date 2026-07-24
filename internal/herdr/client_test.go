package herdr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	if err := c.PaneRun("wA:pB", "echo hi"); err == nil || !strings.Contains(err.Error(), "not an object") {
		t.Fatalf("got err %v, want non-object result failure", err)
	}
}

func TestPaneRunRequiresResultEnvelope(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{}}'`)
	c := NewClient()
	if err := c.PaneRun("wA:pB", "echo hi"); err != nil {
		t.Fatal(err)
	}
}

func TestPaneSendTextRequiresResultEnvelope(t *testing.T) {
	writeFakeHerdr(t, `printf '{"id":"cli:1","result":{}}'`)
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
printf '{"id":"cli:1","result":{}}'
`)
	c := NewClient()
	if err := c.PaneSendKeys("wA:pB", "Enter"); err != nil {
		t.Fatal(err)
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
