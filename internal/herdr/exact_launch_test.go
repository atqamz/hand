package herdr

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/launch"
)

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
