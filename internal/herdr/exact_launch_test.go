package herdr

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
)

func TestPaneRunExecGuardRefusesForeignForegroundProcess(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	faketool.Herdr{Responses: []faketool.HerdrResponse{
		herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"wA:pB","shell_pid":42,"foreground_process_group_id":42,"foreground_processes":[{"pid":42,"name":"bash","cwd":"/tmp/work"},{"pid":99,"name":"vim","cwd":"/tmp/work"}]}}}`),
		herdrResponse("pane run", ""),
	}, Log: callLog}.Install(t, faketool.Bin(t))
	err := NewClient().PaneRunExecGuard("wA:pB", "/tmp/work", testGuardHand, testGuardLocator)
	if err == nil || !strings.Contains(err.Error(), "foreign foreground process") || !IsProcessNotStarted(err) {
		t.Fatalf("PaneRunExecGuard() = %v, want pre-mutation refusal", err)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(calls), "pane run") {
		t.Fatalf("calls = %q, want no pane run", calls)
	}
}

func TestPaneRunExecGuardRefusesShellCwdMismatch(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	faketool.Herdr{Responses: []faketool.HerdrResponse{
		herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"wA:pB","shell_pid":42,"foreground_process_group_id":42,"foreground_processes":[{"pid":42,"name":"bash","cwd":"/tmp/other"}]}}}`),
		herdrResponse("pane run", ""),
	}, Log: callLog}.Install(t, faketool.Bin(t))
	err := NewClient().PaneRunExecGuard("wA:pB", "/tmp/work", testGuardHand, testGuardLocator)
	if err == nil || !strings.Contains(err.Error(), "cwd") || !IsProcessNotStarted(err) {
		t.Fatalf("PaneRunExecGuard() = %v, want pre-mutation refusal", err)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(calls), "pane run") {
		t.Fatalf("calls = %q, want no pane run", calls)
	}
}

func TestPaneRunExecGuardAcceptsEquivalentShellCwd(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "WorkDir")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	var observed string
	if runtime.GOOS == "windows" {
		observed = strings.ToUpper(work)
	} else {
		link := filepath.Join(root, "link")
		if err := os.Symlink(work, link); err != nil {
			t.Fatal(err)
		}
		observed = link
	}
	if observed == work {
		t.Fatalf("equivalent cwd spelling did not differ: %q", observed)
	}
	callLog := filepath.Join(root, "calls.log")
	faketool.Herdr{Responses: []faketool.HerdrResponse{
		herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"wA:pB","shell_pid":42,"foreground_process_group_id":42,"foreground_processes":[{"pid":42,"name":"bash","cwd":`+strconv.Quote(observed)+`}]}}}`),
		herdrResponse("pane run", ""),
	}, Log: callLog}.Install(t, faketool.Bin(t))
	if err := NewClient().PaneRunExecGuard("wA:pB", work, testGuardHand, testGuardLocator); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "pane run") {
		t.Fatalf("calls = %q, want pane run", calls)
	}
}

func TestPaneRunExecGuardRefusesMissingShellCwd(t *testing.T) {
	work, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(t.TempDir(), "calls.log")
	faketool.Herdr{Responses: []faketool.HerdrResponse{
		herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"wA:pB","shell_pid":42,"foreground_process_group_id":42,"foreground_processes":[{"pid":42,"name":"bash","cwd":""}]}}}`),
		herdrResponse("pane run", ""),
	}, Log: callLog}.Install(t, faketool.Bin(t))
	err = NewClient().PaneRunExecGuard("wA:pB", work, testGuardHand, testGuardLocator)
	if err == nil || !strings.Contains(err.Error(), "missing shell cwd") || !IsProcessNotStarted(err) {
		t.Fatalf("PaneRunExecGuard() = %v, want pre-mutation refusal", err)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(calls), "pane run") {
		t.Fatalf("calls = %q, want no pane run", calls)
	}
}

func TestPaneRunExecGuardTypesOnlyTheFixedShapeInvocation(t *testing.T) {
	for _, test := range []struct{ shell, want string }{
		{"bash", "'" + testGuardHand + "' 'exec-guard' '" + testGuardLocator + "'"},
		{"pwsh", "& '" + testGuardHand + "' 'exec-guard' '" + testGuardLocator + "'"},
	} {
		callLog := filepath.Join(t.TempDir(), "calls.log")
		faketool.Herdr{Responses: []faketool.HerdrResponse{
			herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"wA:pB","shell_pid":42,"foreground_process_group_id":42,"foreground_processes":[{"pid":42,"name":"`+test.shell+`","cwd":"/tmp/work"}]}}}`),
			herdrResponse("pane run", ""),
		}, Log: callLog, LogCommands: []string{"pane run"}}.Install(t, faketool.Bin(t))
		if err := NewClient().PaneRunExecGuard("wA:pB", "/tmp/work", testGuardHand, testGuardLocator); err != nil {
			t.Fatal(err)
		}
		calls, err := os.ReadFile(callLog)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(calls)); got != "herdr pane run wA:pB "+test.want {
			t.Fatalf("%s pane run = %q, want only the guard invocation and its locator (EG-14)", test.shell, got)
		}
	}
}

func TestPaneRunExecGuardRefusesAnInexactInvocationBeforeAnyHerdrCall(t *testing.T) {
	for _, test := range []struct{ name, hand, locator string }{
		{"relative hand", "hand", testGuardLocator},
		{"relative locator", testGuardHand, filepath.Join("state", "exec-guard", "op", "handoff")},
		{"newline in locator", testGuardHand, testGuardLocator + "\nrm -rf ~"},
	} {
		callLog := filepath.Join(t.TempDir(), "calls.log")
		faketool.Herdr{Log: callLog}.Install(t, faketool.Bin(t))
		err := NewClient().PaneRunExecGuard("wA:pB", "/tmp/work", test.hand, test.locator)
		if _, statErr := os.Stat(callLog); err == nil || !IsProcessNotStarted(err) || statErr == nil {
			t.Fatalf("%s: PaneRunExecGuard() = %v with Herdr log %v, want a not-started refusal before any Herdr call (EG-7, EG-14)", test.name, err, statErr)
		}
	}
}

var (
	testGuardRoot    = filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	testGuardHand    = filepath.Join(testGuardRoot, "opt", "hand")
	testGuardLocator = filepath.Join(testGuardRoot, "fleet", "state", "exec-guard", "op", "handoff")
)
