package herdr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/launch"
)

func writeFakeHerdr(t *testing.T, responses ...faketool.HerdrResponse) {
	t.Helper()
	bin := faketool.Bin(t)
	faketool.Herdr{Responses: responses}.Install(t, bin)
}

func herdrResponse(command, stdout string) faketool.HerdrResponse {
	return faketool.HerdrResponse{Command: command, Stdout: stdout}
}

func TestWorkspaceListParsesResult(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("workspace list", "{\"id\":\"cli:1\",\"result\":{\"workspaces\":[{\"workspace_id\":\"wA\",\"label\":\"proj\",\"tab_count\":2}]}}"))
	c := NewClient()
	got, err := c.WorkspaceList()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].WorkspaceID != "wA" || got[0].Label != "proj" || got[0].TabCount != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestNamedSessionPrefixesHerdrInvocation(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{herdrResponse("workspace list", "{\"id\":\"cli:1\",\"result\":{\"workspaces\":[]}}")},
		Log:       callLog,
	}.Install(t, bin)

	if _, err := NewSessionClient("hand-f-fleet").WorkspaceList(); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "--session hand-f-fleet workspace list") {
		t.Fatalf("calls = %q, want named session before command", calls)
	}
}

func TestFindWorkspaceByLabelFound(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("workspace list", "{\"id\":\"cli:1\",\"result\":{\"workspaces\":[{\"workspace_id\":\"wA\",\"label\":\"proj\"}]}}"))
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
	writeFakeHerdr(t, herdrResponse("workspace list", "{\"id\":\"cli:1\",\"result\":{\"workspaces\":[]}}"))
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
	writeFakeHerdr(t, herdrResponse("workspace create", "{\"id\":\"cli:1\",\"result\":{\"workspace\":{\"workspace_id\":\"wA\",\"label\":\"proj\",\"tab_count\":1},\"tab\":{\"tab_id\":\"wA:tB\",\"workspace_id\":\"wA\",\"label\":\"1\"},\"root_pane\":{\"pane_id\":\"wA:pC\",\"tab_id\":\"wA:tB\",\"agent_status\":\"idle\"}}}"))
	c := NewClient()
	ws, tab, pane, err := c.WorkspaceCreate("/tmp/clone", nil, "proj")
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
	callLog := filepath.Join(t.TempDir(), "calls.log")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{{
			Command: "workspace create",
			Stdout:  "{\"id\":\"cli:1\",\"result\":{\"workspace\":{\"workspace_id\":\"wA\",\"label\":\"proj\"},\"tab\":{\"tab_id\":\"wA:tB\",\"workspace_id\":\"wA\"},\"root_pane\":{\"pane_id\":\"wA:pC\",\"tab_id\":\"wA:tB\"}}}",
		}},
		Log: callLog,
	}.Install(t, bin)

	c := NewClient()
	if _, _, _, err := c.WorkspaceCreate("/tmp/clone", nil, "proj"); err != nil {
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

func TestWorkspaceCreateCarriesSortedStructuredEnvironmentWithCallerPrecedence(t *testing.T) {
	bin := faketool.Bin(t)
	faketool.Herdr{Responses: []faketool.HerdrResponse{{
		Command: "workspace create",
		Args: []string{
			"--no-focus", "--cwd", "/tmp/clone", "--label", "proj",
			"--env", "CLAUDECODE=", "--env", "CLAUDE_CODE_CHILD_SESSION=", "--env", "CLAUDE_CODE_SESSION_ID=",
			"--env", "CUSTOM=literal", "--env", "HAND_ROLE=worker", "--env", "PATH=/managed/bin",
		},
		Stdout: "{\"id\":\"cli:1\",\"result\":{\"workspace\":{\"workspace_id\":\"wA\",\"label\":\"proj\"},\"tab\":{\"tab_id\":\"wA:tB\",\"workspace_id\":\"wA\"},\"root_pane\":{\"pane_id\":\"wA:pC\",\"tab_id\":\"wA:tB\"}}}",
	}}}.Install(t, bin)

	if _, _, _, err := NewClient().WorkspaceCreate("/tmp/clone", map[string]string{
		"CUSTOM":    "literal",
		"HAND_ROLE": "worker",
		"PATH":      "/managed/bin",
	}, "proj"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceCreateRejectsMissingRootTabOrPane(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("workspace create", "{\"id\":\"cli:1\",\"result\":{\"workspace\":{\"workspace_id\":\"wA\",\"label\":\"proj\"}}}"))
	c := NewClient()
	if _, _, _, err := c.WorkspaceCreate("/tmp/clone", nil, "proj"); err == nil {
		t.Fatal("expected an error when the response omits the root tab and pane")
	}
}

// Pins the fix for atqamz/hand#74: a workspace_created result missing tab or root_pane still
// means herdr created the workspace (a protocol predating those fields), so WorkspaceCreate closes
// it before the parse error reaches the caller - nothing downstream learns the ID.
func TestWorkspaceCreateClosesWorkspaceOnPartialResponse(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{
			{Command: "workspace create", Stdout: "{\"id\":\"cli:1\",\"result\":{\"workspace\":{\"workspace_id\":\"wA\",\"label\":\"proj\"}}}"},
			{Command: "workspace close", Stdout: "{\"id\":\"cli:1\",\"result\":{\"type\":\"ok\"}}"},
		},
		Log: callLog,
	}.Install(t, bin)

	c := NewClient()
	if _, _, _, err := c.WorkspaceCreate("/tmp/clone", nil, "proj"); err == nil {
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
	callLog := filepath.Join(t.TempDir(), "calls.log")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{
			{Command: "workspace create", Stdout: "{\"id\":\"cli:1\",\"result\":{\"workspace\":{\"workspace_id\":\"wA\",\"label\":\"proj\"},\"tab\":\"none\"}}"},
			{Command: "workspace close", Stdout: "{\"id\":\"cli:1\",\"result\":{\"type\":\"ok\"}}"},
		},
		Log: callLog,
	}.Install(t, bin)

	c := NewClient()
	if _, _, _, err := c.WorkspaceCreate("/tmp/clone", nil, "proj"); err == nil {
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
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "tab rename", Args: []string{"wA:tB", "task-1"}, Stdout: "{\"id\":\"cli:1\",\"result\":{\"tab\":{\"tab_id\":\"wA:tB\",\"workspace_id\":\"wA\",\"label\":\"task-1\"}}}"})
	c := NewClient()
	if err := c.TabRename("wA:tB", "task-1"); err != nil {
		t.Fatal(err)
	}
}

func TestCallReturnsErrorOnEnvelopeError(t *testing.T) {
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane get", Stdout: "{\"id\":\"cli:1\",\"error\":{\"code\":\"not_found\",\"message\":\"pane missing\"}}", Exit: 1})
	c := NewClient()
	if _, err := c.PaneGet("wA:pB"); err == nil || !strings.Contains(err.Error(), "pane missing") {
		t.Fatalf("got err %v, want pane missing", err)
	}
}

func TestCallClassifiesStructuredNotFoundWithoutClassifyingProcessText(t *testing.T) {
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane get", Stdout: "{\"id\":\"cli:1\",\"error\":{\"code\":\"pane_not_found\",\"message\":\"pane missing\"}}", Exit: 1})
	c := NewClient()
	_, err := c.PaneGet("wA:pB")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("structured pane_not_found error = %v, want ErrNotFound", err)
	}

	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane get", Stderr: "binary reported not found\n", Exit: 1})
	_, err = c.PaneGet("wA:pB")
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("process failure was classified as ErrNotFound: %v", err)
	}
}

func TestCallFailsOnNonZeroExitWithNoStdout(t *testing.T) {
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane get", Stderr: "binary crashed\n", Exit: 1})
	c := NewClient()
	if _, err := c.PaneGet("wA:pB"); err == nil || !strings.Contains(err.Error(), "binary crashed") {
		t.Fatalf("got err %v, want binary crashed failure", err)
	}
}

func TestCallRejectsNullResult(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("workspace list", "{\"id\":\"cli:1\",\"result\":null}"))
	c := NewClient()
	if _, err := c.WorkspaceList(); err == nil || !strings.Contains(err.Error(), "null result") {
		t.Fatalf("got err %v, want null result failure", err)
	}
}

func TestCallRejectsNonObjectResult(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("pane get", "{\"id\":\"cli:1\",\"result\":[]}"))
	c := NewClient()
	if _, err := c.PaneGet("wA:pB"); err == nil || !strings.Contains(err.Error(), "not an object") {
		t.Fatalf("got err %v, want non-object result failure", err)
	}
}

func TestPaneRunSucceedsOnEmptyStdout(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("pane run", ""))
	c := NewClient()
	if err := c.paneRun("wA:pB", "echo hi"); err != nil {
		t.Fatal(err)
	}
}

func TestPaneProcessInfoParsesShellEvidence(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"wA:pB","shell_pid":42,"foreground_process_group_id":42,"foreground_processes":[{"pid":42,"name":"bash","argv":["bash","-l"],"argv0":"bash","cmdline":"bash -l","cwd":"/tmp/wt"}]}}}`))
	info, err := NewClient().PaneProcessInfo("wA:pB")
	if err != nil {
		t.Fatal(err)
	}
	if info.PaneID != "wA:pB" || info.ShellPID != 42 || len(info.ForegroundProcesses) != 1 || info.ForegroundProcesses[0].Name != "bash" {
		t.Fatalf("process info = %+v", info)
	}
}

func TestPaneProcessInfoRejectsEvidenceForAnotherPane(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"other-pane","shell_pid":42,"foreground_processes":[{"pid":42,"name":"bash"}]}}}`))
	if _, err := NewClient().PaneProcessInfo("wA:pB"); err == nil || !strings.Contains(err.Error(), "mismatched pane") {
		t.Fatalf("PaneProcessInfo() = %v, want mismatched pane error", err)
	}
}

func TestPaneRunSpecDetectsPOSIXShellBeforeSubmission(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{
			herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"wA:pB","shell_pid":42,"foreground_processes":[{"pid":42,"name":"bash"}]}}}`),
			herdrResponse("pane run", ""),
		},
		Log: callLog,
	}.Install(t, bin)
	spec, err := launch.NewSpec(launch.LaunchSpec{Executable: "worker name", Args: []string{"$literal", "has spaces"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewClient().PaneRunSpec("wA:pB", spec); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "pane process-info --pane wA:pB") || !strings.Contains(string(calls), `pane run wA:pB 'worker name' '$literal' 'has spaces'`) {
		t.Fatalf("calls = %q, want process evidence before quoted run", calls)
	}
}

func TestPaneRunSpecDetectsPowerShell(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{
			herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"wA:pB","shell_pid":42,"foreground_processes":[{"pid":42,"name":"pwsh.exe"}]}}}`),
			herdrResponse("pane run", ""),
		},
		Log: callLog,
	}.Install(t, bin)
	spec, err := launch.NewSpec(launch.LaunchSpec{Executable: "worker", Args: []string{"it's literal"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewClient().PaneRunSpec("wA:pB", spec); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), `pane run wA:pB & 'worker' 'it''s literal'`) {
		t.Fatalf("calls = %q, want PowerShell call operator and literal quoting", calls)
	}
}

func TestPaneRunSpecFailsClosedForUnknownShell(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{
			herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"wA:pB","shell_pid":42,"foreground_processes":[{"pid":42,"name":"cmd.exe"}]}}}`),
		},
		Log: callLog,
	}.Install(t, bin)
	spec, err := launch.NewSpec(launch.LaunchSpec{Executable: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewClient().PaneRunSpec("wA:pB", spec); err == nil || !strings.Contains(err.Error(), "unsupported shell") {
		t.Fatalf("PaneRunSpec() = %v, want unsupported shell error", err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "pane run") {
		t.Fatalf("calls = %q, want no pane run after unknown shell", calls)
	}
}

func TestPaneRunSurfacesErrorEnvelopeEvenOnExitZero(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("pane run", "{\"id\":\"cli:1\",\"error\":{\"code\":\"pane_not_found\",\"message\":\"pane missing\"}}"))
	c := NewClient()
	if err := c.paneRun("wA:pB", "echo hi"); err == nil || !strings.Contains(err.Error(), "pane missing") {
		t.Fatalf("got err %v, want pane missing", err)
	}
}

func TestPaneSendTextSucceedsOnEmptyStdout(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("pane send-text", ""))
	c := NewClient()
	if err := c.PaneSendText("wA:pB", "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestPaneSendErrorsExposeOnlyStructuredPreSideEffectRejections(t *testing.T) {
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane send-text", Stderr: "{\"error\":{\"code\":\"pane_send_failed\",\"message\":\"queue full\"}}", Exit: 1})
	if err := NewClient().PaneSendText("wA:pB", "hello"); err == nil || !IsPreSideEffectRejection(err) {
		t.Fatalf("structured send-text rejection = %v, want typed pre-side-effect error", err)
	}

	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane send-keys", Stdout: "{\"error\":{\"code\":\"pane_send_failed\",\"message\":\"queue full\"}}", Exit: 1})
	if err := NewClient().PaneSendKeys("wA:pB", "Enter"); err == nil || !IsPreSideEffectRejection(err) {
		t.Fatalf("structured send-keys rejection = %v, want typed pre-side-effect error", err)
	}

	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane send-text", Stderr: "transport failed\n", Exit: 1})
	if err := NewClient().PaneSendText("wA:pB", "hello"); err == nil || IsPreSideEffectRejection(err) {
		t.Fatalf("generic send-text failure = %v, want ambiguous error", err)
	}
}

func TestPaneSendKeysDoesNotClaimMultiKeyFailureWasPreSideEffect(t *testing.T) {
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane send-keys", Stderr: "{\"error\":{\"code\":\"pane_send_failed\",\"message\":\"second key rejected\"}}", Exit: 1})
	if err := NewClient().PaneSendKeys("wA:pB", "Enter", "Tab"); err == nil || IsPreSideEffectRejection(err) {
		t.Fatalf("multi-key send failure = %v, want ambiguous failure", err)
	}
}

func TestPaneSendTextReportsWhenHerdrProcessNeverStarted(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := NewClient().PaneSendText("wA:pB", "hello")
	if err == nil || !IsProcessNotStarted(err) {
		t.Fatalf("missing herdr executable error = %v, want process-not-started evidence", err)
	}
}

func TestPaneSendFailureAfterHerdrStartsIsAmbiguous(t *testing.T) {
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane send-text", Stderr: "transport failed\n", Exit: 1})
	err := NewClient().PaneSendText("wA:pB", "hello")
	if err == nil || IsProcessNotStarted(err) || IsPreSideEffectRejection(err) {
		t.Fatalf("started process failure = %v, want ambiguous failure", err)
	}
}

func TestPaneSendKeysPassesKeys(t *testing.T) {
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane send-keys", Args: []string{"wA:pB", "Enter"}})
	c := NewClient()
	if err := c.PaneSendKeys("wA:pB", "Enter"); err != nil {
		t.Fatal(err)
	}
}

func TestPaneSendKeysSucceedsOnEmptyStdout(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("pane send-keys", ""))
	c := NewClient()
	if err := c.PaneSendKeys("wA:pB", "Enter"); err != nil {
		t.Fatal(err)
	}
}

func TestCallVoidFailsOnNonZeroExitWithNoStdout(t *testing.T) {
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane run", Stderr: "binary crashed\n", Exit: 1})
	c := NewClient()
	if err := c.paneRun("wA:pB", "echo hi"); err == nil || !strings.Contains(err.Error(), "binary crashed") {
		t.Fatalf("got err %v, want binary crashed failure", err)
	}
}

func TestTabCreateParsesTabAndRootPane(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("tab create", "{\"id\":\"cli:1\",\"result\":{\"tab\":{\"tab_id\":\"wA:tB\",\"workspace_id\":\"wA\",\"label\":\"task\"},\"root_pane\":{\"pane_id\":\"wA:pC\",\"tab_id\":\"wA:tB\",\"agent_status\":\"idle\"}}}"))
	c := NewClient()
	tab, pane, err := c.TabCreate("wA", "/tmp/wt", nil, "task")
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
	callLog := filepath.Join(t.TempDir(), "calls.log")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{{
			Command: "tab create",
			Stdout:  "{\"id\":\"cli:1\",\"result\":{\"tab\":{\"tab_id\":\"wA:tB\",\"workspace_id\":\"wA\"},\"root_pane\":{\"pane_id\":\"wA:pC\",\"tab_id\":\"wA:tB\"}}}",
		}},
		Log: callLog,
	}.Install(t, bin)

	c := NewClient()
	if _, _, err := c.TabCreate("wA", "/tmp/wt", nil, "task"); err != nil {
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
	callLog := filepath.Join(t.TempDir(), "calls.log")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{
			{Command: "tab create", Stdout: "{\"id\":\"cli:1\",\"result\":{\"tab\":{\"tab_id\":\"wA:tB\",\"workspace_id\":\"wA\"}}}"},
			{Command: "tab close", Stdout: "{\"id\":\"cli:1\",\"result\":{\"type\":\"ok\"}}"},
		},
		Log: callLog,
	}.Install(t, bin)

	c := NewClient()
	if _, _, err := c.TabCreate("wA", "/tmp/wt", nil, "task"); err == nil {
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
	callLog := filepath.Join(t.TempDir(), "calls.log")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{
			{Command: "tab create", Stdout: "{\"id\":\"cli:1\",\"result\":{\"tab\":{\"tab_id\":\"wA:tB\",\"workspace_id\":\"wA\"},\"root_pane\":\"none\"}}"},
			{Command: "tab close", Stdout: "{\"id\":\"cli:1\",\"result\":{\"type\":\"ok\"}}"},
		},
		Log: callLog,
	}.Install(t, bin)

	c := NewClient()
	if _, _, err := c.TabCreate("wA", "/tmp/wt", nil, "task"); err == nil {
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
	writeFakeHerdr(t, herdrResponse("pane get", "{\"id\":\"cli:1\",\"result\":{\"pane\":{\"pane_id\":\"wA:pB\",\"agent_status\":\"working\",\"cwd\":\"/tmp/worktree\",\"foreground_cwd\":\"/tmp/worktree/worker\"}}}"))
	c := NewClient()
	pane, err := c.PaneGet("wA:pB")
	if err != nil {
		t.Fatal(err)
	}
	if pane.AgentStatus != StatusWorking || pane.Cwd != "/tmp/worktree" || pane.ForegroundCwd != "/tmp/worktree/worker" {
		t.Fatalf("got %+v, want status and cwd evidence", pane)
	}
}

func TestWaitComposerEmptyReturnsWhenIdle(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("pane get", "{\"id\":\"cli:1\",\"result\":{\"pane\":{\"pane_id\":\"wA:pB\",\"agent_status\":\"idle\"}}}"))
	c := NewClient()
	if err := c.WaitComposerEmpty("wA:pB", time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWaitComposerEmptyAcceptsRenderedIdlePromptWhileAgentStatusIsWorking(t *testing.T) {
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane get", Stdout: "{\"id\":\"cli:1\",\"result\":{\"pane\":{\"pane_id\":\"wA:pB\",\"agent_status\":\"working\"}}}"}, faketool.HerdrResponse{Command: "pane read", Stdout: "────────────────────────\n❯\n────────────────────────\n  Opus 5 | 3 shells\n"})
	c := NewClient()
	if err := c.WaitComposerEmpty("wA:pB", time.Second); err != nil {
		t.Fatalf("got %v, want rendered idle prompt to override stale working status", err)
	}
}

func TestWaitComposerEmptyTimesOutWhileWorking(t *testing.T) {
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane get", Stdout: "{\"id\":\"cli:1\",\"result\":{\"pane\":{\"pane_id\":\"wA:pB\",\"agent_status\":\"working\"}}}"}, faketool.HerdrResponse{Command: "pane read", Stdout: "working...\nesc to interrupt (15s)\n  3 shells\n"})
	c := NewClient()
	err := c.WaitComposerEmpty("wA:pB", 100*time.Millisecond)
	if !errors.Is(err, ErrComposerBusyTimeout) {
		t.Fatalf("got err %v, want ErrComposerBusyTimeout", err)
	}
	if !strings.Contains(err.Error(), `agent_status="working"`) || !strings.Contains(err.Error(), "background_shells=3") {
		t.Fatalf("got err %v, want status and shell diagnostics", err)
	}
}

func TestWaitComposerEmptyPaneFailureIsNotABusyTimeout(t *testing.T) {
	// A pane that stops answering can never free, so the caller must be able to
	// tell this apart from the retryable timeout above.
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane get", Stdout: "{\"id\":\"cli:1\",\"error\":{\"code\":\"pane_not_found\",\"message\":\"no such pane\"}}", Exit: 1})
	c := NewClient()
	err := c.WaitComposerEmpty("wA:pB", time.Second)
	if err == nil || errors.Is(err, ErrComposerBusyTimeout) {
		t.Fatalf("got err %v, want a non-timeout pane failure", err)
	}
}

func TestWaitComposerEmptyHonorsTimeoutWhenPaneGetHangs(t *testing.T) {
	bin := faketool.Bin(t)
	faketool.Herdr{Hang: []string{"pane get"}}.Install(t, bin)
	c := NewClient()
	started := time.Now()
	err := c.WaitComposerEmpty("wA:pB", 25*time.Millisecond)
	if !errors.Is(err, ErrComposerBusyTimeout) {
		t.Fatalf("got err %v, want ErrComposerBusyTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("WaitComposerEmpty took %s after pane get hung", elapsed)
	}
}

func TestTabListParsesResult(t *testing.T) {
	writeFakeHerdr(t, herdrResponse("tab list", "{\"id\":\"cli:1\",\"result\":{\"tabs\":[{\"tab_id\":\"wA:tB\",\"workspace_id\":\"wA\"}]}}"))
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
	writeFakeHerdr(t, faketool.HerdrResponse{Command: "pane read", Args: []string{"wA:pB", "--source", "recent", "--lines", "60"}, Stdout: "Welcome to Claude Code\n"})
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
	writeFakeHerdr(t, herdrResponse("pane read", "{\"code\":\"pane_not_found\",\"message\":\"no such pane\"}"))
	c := NewClient()
	if _, err := c.PaneRead("wA:pB", 60); err == nil || !strings.Contains(err.Error(), "pane_not_found") {
		t.Fatalf("got err %v, want pane_not_found", err)
	}
}
