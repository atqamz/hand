package cli

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"testing"
)

func TestScratchEnvironmentReplacesEveryTempVariable(t *testing.T) {
	got := scratchEnvironment([]string{"A=1", "TMPDIR=/tmp", "TMP=/tmp", "TEMP=/tmp", "GOTMPDIR=/tmp", "CLAUDE_CODE_TMPDIR=/tmp", "B=2"}, "/scratch")
	want := []string{"A=1", "B=2", "TMPDIR=/scratch", "TMP=/scratch", "TEMP=/scratch", "GOTMPDIR=/scratch", "CLAUDE_CODE_TMPDIR=/scratch"}
	if !slices.Equal(got, want) {
		t.Fatalf("environment = %q, want %q", got, want)
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
