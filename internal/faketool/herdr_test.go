package faketool

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runHerdr(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	c := exec.Command("herdr", args...)
	var out, errOut strings.Builder
	c.Stdout = &out
	c.Stderr = &errOut
	err := c.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("run herdr %v: %v", args, err)
	}
	return out.String(), errOut.String(), code
}

func twoTabWorkspace() Herdr {
	return Herdr{Workspaces: []HerdrWorkspace{{ID: "wA", Label: "hand:proj", Tabs: []HerdrTab{
		{ID: "wA:t1", Label: "task-1", Pane: "wA:p1"},
		{ID: "wA:t2", Label: "task-2", Pane: "wA:p2"},
	}}}}
}

// The listing is the fake's whole point: a closed tab has to stop appearing in it,
// which is the answer a per-test fake gave identically before and after the close.
func TestHerdrStopsListingAClosedTab(t *testing.T) {
	twoTabWorkspace().Install(t, Bin(t))

	before, _, code := runHerdr(t, "tab", "list", "--workspace", "wA")
	if code != 0 || !strings.Contains(before, `"tab_id":"wA:t1"`) || !strings.Contains(before, `"tab_id":"wA:t2"`) {
		t.Fatalf("tab list = %q (exit %d), want both tabs", before, code)
	}

	if _, errOut, code := runHerdr(t, "tab", "close", "wA:t1"); code != 0 {
		t.Fatalf("tab close exit %d (%q), want 0", code, errOut)
	}

	after, _, code := runHerdr(t, "tab", "list", "--workspace", "wA")
	if code != 0 {
		t.Fatalf("tab list after close exit %d, want 0", code)
	}
	if strings.Contains(after, `"tab_id":"wA:t1"`) {
		t.Fatalf("tab list = %q, want the closed tab gone", after)
	}
	if !strings.Contains(after, `"tab_id":"wA:t2"`) {
		t.Fatalf("tab list = %q, want the workspace's other tab still there", after)
	}
}

// Recorded from herdr 0.7.5: a second close is not a quiet success, it is
// tab_not_found on stderr with exit 1. Anything relying on a repeated close to
// succeed is relying on the fake, not the tool.
func TestHerdrRefusesToCloseATabTwice(t *testing.T) {
	twoTabWorkspace().Install(t, Bin(t))

	if _, _, code := runHerdr(t, "tab", "close", "wA:t1"); code != 0 {
		t.Fatal("first tab close failed")
	}
	stdout, errOut, code := runHerdr(t, "tab", "close", "wA:t1")
	if code != 1 || !strings.Contains(errOut, `"code":"tab_not_found"`) {
		t.Fatalf("second tab close = %q / %q (exit %d), want tab_not_found on stderr", stdout, errOut, code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want the error envelope on stderr alone", stdout)
	}
}

// Recorded from herdr 0.7.5: nothing refuses to close a workspace's only tab, and
// doing it takes the workspace with it - so every later call naming that workspace
// answers workspace_not_found rather than an empty tab list.
func TestHerdrClosingTheLastTabClosesItsWorkspace(t *testing.T) {
	Herdr{Workspaces: []HerdrWorkspace{
		{ID: "wA", Label: "hand:proj", Tabs: []HerdrTab{{ID: "wA:t1", Label: "task-1", Pane: "wA:p1"}}},
	}}.Install(t, Bin(t))

	if _, errOut, code := runHerdr(t, "tab", "close", "wA:t1"); code != 0 {
		t.Fatalf("closing a sole tab exit %d (%q), want 0", code, errOut)
	}
	_, errOut, code := runHerdr(t, "tab", "list", "--workspace", "wA")
	if code != 1 || !strings.Contains(errOut, `"code":"workspace_not_found"`) {
		t.Fatalf("tab list = %q (exit %d), want workspace_not_found", errOut, code)
	}
	if _, errOut, code := runHerdr(t, "pane", "get", "wA:p1"); code != 1 || !strings.Contains(errOut, `"code":"pane_not_found"`) {
		t.Fatalf("pane get = %q (exit %d), want the pane gone with its tab", errOut, code)
	}
	list, _, _ := runHerdr(t, "workspace", "list")
	if strings.Contains(list, `"workspace_id":"wA"`) {
		t.Fatalf("workspace list = %q, want the workspace gone", list)
	}
}

func TestHerdrWorkspaceCloseTakesEveryTabWithIt(t *testing.T) {
	twoTabWorkspace().Install(t, Bin(t))

	if _, errOut, code := runHerdr(t, "workspace", "close", "wA"); code != 0 {
		t.Fatalf("workspace close exit %d (%q), want 0", code, errOut)
	}
	for _, tab := range []string{"wA:t1", "wA:t2"} {
		_, errOut, code := runHerdr(t, "tab", "close", tab)
		if code != 1 || !strings.Contains(errOut, `"code":"tab_not_found"`) {
			t.Fatalf("close %s after its workspace closed = %q (exit %d), want tab_not_found", tab, errOut, code)
		}
	}
	_, errOut, code := runHerdr(t, "workspace", "close", "wA")
	if code != 1 || !strings.Contains(errOut, `"code":"workspace_not_found"`) {
		t.Fatalf("second workspace close = %q (exit %d), want workspace_not_found", errOut, code)
	}
}

// A created workspace is live from the call that created it, not from installation:
// the listing is empty first, which is what makes spawn create one at all.
func TestHerdrWorkspaceCreateAddsAWorkspaceToTheListing(t *testing.T) {
	Herdr{Creates: []HerdrWorkspace{
		{ID: "wN", Tabs: []HerdrTab{{ID: "wN:t1", Pane: "wN:p1"}}},
	}}.Install(t, Bin(t))

	if list, _, _ := runHerdr(t, "workspace", "list"); !strings.Contains(list, `"workspaces":[]`) {
		t.Fatalf("workspace list = %q, want no workspace before the create", list)
	}

	out, _, code := runHerdr(t, "workspace", "create", "--cwd", "/tmp/wt", "--label", "hand:proj")
	if code != 0 || !strings.Contains(out, `"workspace_id":"wN"`) || !strings.Contains(out, `"pane_id":"wN:p1"`) {
		t.Fatalf("workspace create = %q (exit %d), want the declared ids", out, code)
	}
	if !strings.Contains(out, `"label":"hand:proj"`) {
		t.Fatalf("workspace create = %q, want the label it was asked for", out)
	}

	list, _, _ := runHerdr(t, "workspace", "list")
	if !strings.Contains(list, `"workspace_id":"wN"`) || !strings.Contains(list, `"tab_count":1`) {
		t.Fatalf("workspace list = %q, want the created workspace and its root tab", list)
	}
	if _, _, code := runHerdr(t, "pane", "get", "wN:p1"); code != 0 {
		t.Fatalf("pane get exit %d, want the created pane reachable", code)
	}
}

func TestHerdrTabCreateAttachesToTheWorkspaceAskedFor(t *testing.T) {
	h := twoTabWorkspace()
	h.TabCreates = []HerdrTab{{ID: "wA:t3", Pane: "wA:p3"}}
	h.Install(t, Bin(t))

	out, _, code := runHerdr(t, "tab", "create", "--workspace", "wA", "--cwd", "/tmp/wt", "--label", "task-3")
	if code != 0 || !strings.Contains(out, `"tab_id":"wA:t3"`) {
		t.Fatalf("tab create = %q (exit %d), want the declared tab", out, code)
	}
	list, _, _ := runHerdr(t, "tab", "list", "--workspace", "wA")
	if !strings.Contains(list, `"tab_id":"wA:t3"`) || !strings.Contains(list, `"label":"task-3"`) {
		t.Fatalf("tab list = %q, want the created tab under its label", list)
	}

	if _, _, code := runHerdr(t, "workspace", "close", "wA"); code != 0 {
		t.Fatal("workspace close failed")
	}
	_, errOut, code := runHerdr(t, "tab", "create", "--workspace", "wA", "--cwd", "/tmp/wt", "--label", "task-4")
	if code != 1 || !strings.Contains(errOut, `"code":"workspace_not_found"`) {
		t.Fatalf("tab create on a closed workspace = %q (exit %d), want workspace_not_found", errOut, code)
	}
}

func TestHerdrPaneGetReportsItsOwningWorkspace(t *testing.T) {
	twoTabWorkspace().Install(t, Bin(t))

	out, errOut, code := runHerdr(t, "pane", "get", "wA:p1")
	if code != 0 {
		t.Fatalf("pane get = %q (exit %d), want success", errOut, code)
	}
	for _, want := range []string{`"pane_id":"wA:p1"`, `"tab_id":"wA:t1"`, `"workspace_id":"wA"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("pane get = %q, want %s", out, want)
		}
	}
}

func TestHerdrTabRenameChangesTheListedLabel(t *testing.T) {
	twoTabWorkspace().Install(t, Bin(t))

	if _, errOut, code := runHerdr(t, "tab", "rename", "wA:t1", "renamed"); code != 0 {
		t.Fatalf("tab rename exit %d (%q), want 0", code, errOut)
	}
	if list, _, _ := runHerdr(t, "tab", "list", "--workspace", "wA"); !strings.Contains(list, `"label":"renamed"`) {
		t.Fatalf("tab list = %q, want the new label", list)
	}
	if _, _, code := runHerdr(t, "tab", "close", "wA:t1"); code != 0 {
		t.Fatal("tab close failed")
	}
	_, errOut, code := runHerdr(t, "tab", "rename", "wA:t1", "again")
	if code != 1 || !strings.Contains(errOut, `"code":"tab_not_found"`) {
		t.Fatalf("rename of a closed tab = %q (exit %d), want tab_not_found", errOut, code)
	}
}

// Void commands answer with empty stdout on success, which is the shape
// herdr.Client's callVoid reads, and still refuse a pane that is gone.
func TestHerdrVoidCommandsAnswerEmptyAndStillCheckThePane(t *testing.T) {
	twoTabWorkspace().Install(t, Bin(t))

	for _, args := range [][]string{
		{"pane", "run", "wA:p1", "echo hi"},
		{"pane", "send-text", "wA:p1", "hello"},
		{"pane", "send-keys", "wA:p1", "Enter"},
	} {
		out, errOut, code := runHerdr(t, args...)
		if code != 0 || out != "" {
			t.Fatalf("%v = %q / %q (exit %d), want empty stdout on exit 0", args, out, errOut, code)
		}
	}
	if _, _, code := runHerdr(t, "tab", "close", "wA:t1"); code != 0 {
		t.Fatal("tab close failed")
	}
	_, errOut, code := runHerdr(t, "pane", "run", "wA:p1", "echo hi")
	if code != 1 || !strings.Contains(errOut, `"code":"pane_not_found"`) {
		t.Fatalf("pane run on a closed pane = %q (exit %d), want pane_not_found", errOut, code)
	}
}

func TestHerdrResponseCanModelAcceptedInputBeforeAmbiguousFailure(t *testing.T) {
	log := filepath.Join(t.TempDir(), "text.log")
	h := twoTabWorkspace()
	h.TextLog = log
	h.Responses = []HerdrResponse{{Command: "pane send-text", Stdout: "", Stderr: "connection lost\n", Exit: 1, MutateBeforeResponse: true}}
	h.Install(t, Bin(t))
	_, stderr, code := runHerdr(t, "pane", "send-text", "wA:p1", "hello")
	if code != 1 || !strings.Contains(stderr, "connection lost") {
		t.Fatalf("send-text = %q (exit %d), want ambiguous failure", stderr, code)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "hello" {
		t.Fatalf("text log = %q, want accepted text despite lost response", data)
	}
}

func TestHerdrLogRecordsEveryInvocation(t *testing.T) {
	bin := Bin(t)
	log := bin + "/herdr.log"
	h := twoTabWorkspace()
	h.Log = log
	h.Install(t, bin)

	runHerdr(t, "tab", "list", "--workspace", "wA")
	runHerdr(t, "tab", "close", "wA:t1")

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"herdr tab list --workspace wA", "herdr tab close wA:t1"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("log = %q, want %q", data, want)
		}
	}
}
