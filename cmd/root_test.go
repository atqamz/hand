package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func exitCodeFor(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return 1
}

func TestUsageArgsTagsMismatchAsExitCode2(t *testing.T) {
	validate := usageArgs(cobra.ExactArgs(2))
	err := validate(&cobra.Command{}, []string{"onlyone"})
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestUsageArgsPassesThroughValidArgs(t *testing.T) {
	validate := usageArgs(cobra.ExactArgs(2))
	if err := validate(&cobra.Command{}, []string{"a", "b"}); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

func TestRootRejectsUnknownCommand(t *testing.T) {
	root := newRootCmd("test")
	root.SetArgs([]string{"bogus-command"})
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	found, err := root.ExecuteC()
	if err == nil {
		t.Fatal("want error for unknown command")
	}
	if found != root {
		t.Fatalf("found = %v, want root itself", found.Name())
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("err = %v, want unknown command", err)
	}
}

func TestRootRejectsBadArgCount(t *testing.T) {
	root := newRootCmd("test")
	root.SetArgs([]string{"spawn", "onlyonearg"})
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	_, err := root.ExecuteC()
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestRootRejectsUnknownFlag(t *testing.T) {
	root := newRootCmd("test")
	root.SetArgs([]string{"spawn", "--bogus", "a", "b"})
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	_, err := root.ExecuteC()
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestRootBareInvocationShowsHelpWithoutError(t *testing.T) {
	root := newRootCmd("test")
	root.SetArgs([]string{})
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(new(strings.Builder))
	if _, err := root.ExecuteC(); err != nil {
		t.Fatalf("got %v, want nil (bare invocation shows help)", err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("out = %q, want usage text", out.String())
	}
}
