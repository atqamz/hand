//go:build contract

// Package contract runs the calls internal/faketool fakes against the real
// binaries, asserting the shapes internal/faketool/FIDELITY.md records. Build
// tag `contract`, and each test skips where its binary is absent.
package contract

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// Skips rather than fails, because CI installs none of these on purpose.
func requireBin(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not on PATH", name)
	}
}

type result struct {
	stdout string
	stderr string
	code   int
}

// Streams stay separate: half of what FIDELITY.md records is which stream a
// tool answers on, and combining them would assert nothing about that.
func run(t *testing.T, dir, name string, args ...string) result {
	t.Helper()
	var out, errOut strings.Builder
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Stdout = &out
	c.Stderr = &errOut
	err := c.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	res := result{stdout: out.String(), stderr: errOut.String(), code: code}
	t.Logf("%s %s -> exit %d\nstdout: %s\nstderr: %s",
		name, strings.Join(args, " "), res.code, res.stdout, res.stderr)
	return res
}

func (r result) requireCode(t *testing.T, want int) result {
	t.Helper()
	if r.code != want {
		t.Fatalf("exit %d, want %d", r.code, want)
	}
	return r
}

func (r result) requireStderrContains(t *testing.T, want string) result {
	t.Helper()
	if !strings.Contains(r.stderr, want) {
		t.Fatalf("stderr %q, want it to contain %q", r.stderr, want)
	}
	return r
}
