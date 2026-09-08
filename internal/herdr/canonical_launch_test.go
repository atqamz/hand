package herdr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/launch"
)

func TestRenderCanonicalPOSIXLaunchCarriesSortedLiteralEnvironment(t *testing.T) {
	spec := launch.LaunchSpec{
		Executable: "worker bin",
		Args:       []string{"--mode", "spaces and 'quotes'"},
		Env:        map[string]string{"Z_LAST": "z value", "A_FIRST": "a'value"},
		Cwd:        "/tmp/worktree",
	}
	got, err := renderCanonicalPOSIXLaunch(spec)
	if err != nil {
		t.Fatal(err)
	}
	want := "A_FIRST='a'\\''value' Z_LAST='z value' 'worker bin' '--mode' 'spaces and '\\''quotes'\\'''"
	if got != want {
		t.Fatalf("canonical POSIX launch = %q, want %q", got, want)
	}
}

func TestRenderCanonicalPowerShellLaunchCarriesSortedLiteralEnvironment(t *testing.T) {
	spec := launch.LaunchSpec{
		Executable: "worker bin",
		Args:       []string{"--mode", "spaces and 'quotes'"},
		Env:        map[string]string{"Z_LAST": "z value", "A_FIRST": "a'value"},
		Cwd:        `C:\worktree`,
	}
	got, err := renderCanonicalPowerShellLaunch(spec)
	if err != nil {
		t.Fatal(err)
	}
	want := "$env:A_FIRST='a''value'; $env:Z_LAST='z value'; & 'worker bin' '--mode' 'spaces and ''quotes'''"
	if got != want {
		t.Fatalf("canonical PowerShell launch = %q, want %q", got, want)
	}
}

func TestPaneRunCanonicalSpecRefusesNonIdleForegroundProcess(t *testing.T) {
	bin := faketool.Bin(t)
	faketool.Herdr{Responses: []faketool.HerdrResponse{
		{Command: "pane process-info", Stdout: `{"id":"cli:pane:process-info","result":{"type":"process_info","process_info":{"pane_id":"wA:p1","shell_pid":11,"foreground_process_group_id":22,"foreground_processes":[{"pid":11,"name":"bash","cwd":"/tmp/worktree"},{"pid":22,"name":"worker","cwd":"/tmp/worktree"}]}}}`},
	}}.Install(t, bin)
	client, err := NewClientAt(filepath.Join(bin, faketool.Executable("herdr")), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	err = client.PaneRunCanonicalSpec("wA:p1", launch.LaunchSpec{Executable: "worker", Cwd: "/tmp/worktree"})
	if err == nil || !strings.Contains(err.Error(), "not idle") {
		t.Fatalf("PaneRunCanonicalSpec error = %v, want non-idle refusal", err)
	}
}

func TestPaneRunCanonicalSpecRefusesShellCwdMismatchBeforeMutation(t *testing.T) {
	bin := faketool.Bin(t)
	faketool.Herdr{Responses: []faketool.HerdrResponse{
		{Command: "pane process-info", Stdout: `{"id":"cli:pane:process-info","result":{"type":"process_info","process_info":{"pane_id":"wA:p1","shell_pid":11,"foreground_process_group_id":11,"foreground_processes":[{"pid":11,"name":"bash","cwd":"/tmp/other"}]}}}`},
	}}.Install(t, bin)
	client, err := NewClientAt(filepath.Join(bin, faketool.Executable("herdr")), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	err = client.PaneRunCanonicalSpec("wA:p1", launch.LaunchSpec{Executable: "worker", Cwd: "/tmp/worktree"})
	if err == nil || !strings.Contains(err.Error(), "does not match persisted cwd") {
		t.Fatalf("PaneRunCanonicalSpec error = %v, want cwd refusal", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("cwd mismatch unexpectedly reached pane run: %v", err)
	}
}
