package herdr

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/launch"
)

func TestPaneRunExactSpecRefusesForeignForegroundProcess(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	faketool.Herdr{Responses: []faketool.HerdrResponse{
		herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"wA:pB","shell_pid":42,"foreground_process_group_id":42,"foreground_processes":[{"pid":42,"name":"bash","cwd":"/tmp/work"},{"pid":99,"name":"vim","cwd":"/tmp/work"}]}}}`),
		herdrResponse("pane run", ""),
	}, Log: callLog}.Install(t, faketool.Bin(t))
	spec := launch.LaunchSpec{Executable: "worker", Cwd: "/tmp/work"}
	err := NewClient().PaneRunExactSpec("wA:pB", spec)
	if err == nil || !strings.Contains(err.Error(), "foreign foreground process") || !IsProcessNotStarted(err) {
		t.Fatalf("PaneRunExactSpec() = %v, want pre-mutation refusal", err)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(calls), "pane run") {
		t.Fatalf("calls = %q, want no pane run", calls)
	}
}

func TestPaneRunExactSpecRefusesShellCwdMismatch(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	faketool.Herdr{Responses: []faketool.HerdrResponse{
		herdrResponse("pane process-info", `{"id":"cli:1","result":{"process_info":{"pane_id":"wA:pB","shell_pid":42,"foreground_process_group_id":42,"foreground_processes":[{"pid":42,"name":"bash","cwd":"/tmp/other"}]}}}`),
		herdrResponse("pane run", ""),
	}, Log: callLog}.Install(t, faketool.Bin(t))
	err := NewClient().PaneRunExactSpec("wA:pB", launch.LaunchSpec{Executable: "worker", Cwd: "/tmp/work"})
	if err == nil || !strings.Contains(err.Error(), "cwd") || !IsProcessNotStarted(err) {
		t.Fatalf("PaneRunExactSpec() = %v, want pre-mutation refusal", err)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(calls), "pane run") {
		t.Fatalf("calls = %q, want no pane run", calls)
	}
}

func TestPaneRunExactSpecAcceptsEquivalentShellCwd(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "WorkDir")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	observed := work
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
	if err := NewClient().PaneRunExactSpec("wA:pB", launch.LaunchSpec{Executable: "worker", Cwd: work}); err != nil {
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

func TestRenderExactPOSIXLaunchSpecAppliesCwdAndSortedEnvironment(t *testing.T) {
	spec := launch.LaunchSpec{
		Executable: "worker name",
		Args:       []string{"arg with spaces"},
		Env:        map[string]string{"Z_LAST": "z value", "A_FIRST": "$literal;value"},
		Cwd:        "/tmp/work tree",
	}
	got, err := renderExactLaunchSpec(shellPOSIX, spec)
	if err != nil {
		t.Fatal(err)
	}
	want := `cd -- '/tmp/work tree' && A_FIRST='$literal;value' Z_LAST='z value' 'worker name' 'arg with spaces'`
	if got != want {
		t.Fatalf("render exact POSIX = %q, want %q", got, want)
	}
}

func TestRenderExactPowerShellLaunchSpecScopesCwdAndEnvironment(t *testing.T) {
	spec := launch.LaunchSpec{
		Executable: "worker name",
		Args:       []string{"arg with 'quote'"},
		Env:        map[string]string{"TOKEN": "value'with;syntax"},
		Cwd:        `C:\\work tree`,
	}
	got, err := renderExactLaunchSpec(shellPowerShell, spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`[Environment]::GetEnvironmentVariable('TOKEN', 'Process')`,
		`[Environment]::SetEnvironmentVariable('TOKEN', 'value''with;syntax', 'Process')`,
		`Set-Location -LiteralPath 'C:\\work tree'`,
		`& 'worker name' 'arg with ''quote'''`,
		`[Environment]::SetEnvironmentVariable('TOKEN', $null, 'Process')`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("render exact PowerShell = %q, missing %q", got, want)
		}
	}
}

func TestRenderExactLaunchSpecRejectsInvalidTransportData(t *testing.T) {
	for _, spec := range []launch.LaunchSpec{
		{Executable: "", Cwd: "/tmp"},
		{Executable: "worker", Cwd: "/tmp\nother"},
		{Executable: "worker", Cwd: "/tmp", Env: map[string]string{"BAD-NAME": "value"}},
		{Executable: "worker", Cwd: "/tmp", Env: map[string]string{"TOKEN": "value\x00"}},
	} {
		if _, err := renderExactLaunchSpec(shellPOSIX, spec); err == nil {
			t.Fatalf("invalid spec unexpectedly rendered: %#v", spec)
		}
	}
}
