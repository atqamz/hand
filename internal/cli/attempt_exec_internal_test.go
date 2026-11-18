package cli

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestScratchEnvironmentReplacesEveryTempVariable(t *testing.T) {
	got := scratchEnvironment([]string{"A=1", "TMPDIR=/tmp", "TMP=/tmp", "TEMP=/tmp", "GOTMPDIR=/tmp", "CLAUDE_CODE_TMPDIR=/tmp", "B=2"}, "/scratch", 0)
	want := []string{"A=1", "B=2", "TMPDIR=/scratch", "TMP=/scratch", "TEMP=/scratch", "GOTMPDIR=/scratch", "CLAUDE_CODE_TMPDIR=/scratch"}
	if !slices.Equal(got, want) {
		t.Fatalf("environment = %q, want %q", got, want)
	}
}

func TestScratchSocketWarningCoversTheSunPathLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sun_path is a Unix limit")
	}
	for _, tc := range []struct {
		length int
		warn   bool
	}{{96, false}, {97, true}, {116, true}} {
		got := scratchSocketWarning(strings.Repeat("d", tc.length))
		if (got != "") != tc.warn {
			t.Errorf("%d bytes: warning = %q, want a warning: %v", tc.length, got, tc.warn)
		}
	}
}

func TestProcessExitCodeTranslatesANonzeroExit(t *testing.T) {
	name, args := "/bin/sh", []string{"-c", "exit 7"}
	if runtime.GOOS == "windows" {
		name = os.Getenv("ComSpec")
		if name == "" {
			t.Skip("no ComSpec")
		}
		args = []string{"/c", "exit 7"}
	}
	err := exec.Command(name, args...).Run()
	if code, ok := processExitCode(err); !ok || code != 7 {
		t.Fatalf("processExitCode(%v) = %d, %v; want 7, true", err, code, ok)
	}
	if code, ok := processExitCode(errors.New("not a process exit")); ok {
		t.Fatalf("processExitCode of a plain error = %d, true; want false", code)
	}
}

func TestScratchEnvironmentSetsTheCompactPercentOnlyWhenGiven(t *testing.T) {
	old := envNamesFold
	envNamesFold = false
	t.Cleanup(func() { envNamesFold = old })
	inherited := []string{"A=1", "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=90", "claude_autocompact_pct_override=80"}
	if got := scratchEnvironment(inherited, "/scratch", 0); !slices.Contains(got, "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=90") || !slices.Contains(got, "claude_autocompact_pct_override=80") {
		t.Fatalf("environment without a percent = %q, want the inherited values kept", got)
	}
	got := scratchEnvironment(inherited, "/scratch", 60)
	var found []string
	for _, kv := range got {
		if strings.EqualFold(strings.SplitN(kv, "=", 2)[0], "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE") {
			found = append(found, kv)
		}
	}
	if want := []string{"claude_autocompact_pct_override=80", "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=60"}; !slices.Equal(found, want) {
		t.Fatalf("percent entries = %q, want %q (an exact-case inherited value replaced, a differently cased one kept on a case-sensitive system)", found, want)
	}
}

func TestScratchEnvironmentReplacesTheCompactPercentInAnyCaseOnWindows(t *testing.T) {
	old := envNamesFold
	envNamesFold = true
	t.Cleanup(func() { envNamesFold = old })
	got := scratchEnvironment([]string{"Claude_Autocompact_Pct_Override=90", "A=1"}, "/scratch", 60)
	want := []string{"A=1", "TMPDIR=/scratch", "TMP=/scratch", "TEMP=/scratch", "GOTMPDIR=/scratch", "CLAUDE_CODE_TMPDIR=/scratch", "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=60"}
	if !slices.Equal(got, want) {
		t.Fatalf("environment = %q, want %q", got, want)
	}
}
