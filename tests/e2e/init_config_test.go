//go:build e2e

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Runs hand with stdin the caller chooses and a deadline, because the failure being tested for is a
// process that never returns rather than one that returns the wrong thing.
func runHandStdin(t *testing.T, home string, stdin *os.File, args ...string) invocation {
	t.Helper()
	cmd := exec.Command(handBin, args...)
	cmd.Dir = home
	cmd.Stdin = stdin
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start hand %v: %v", args, err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		code := 0
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("wait hand %v: %v", args, err)
			}
			code = exitErr.ExitCode()
		}
		t.Logf("$ hand %s\n  exit %d\n  stdout: %s\n  stderr: %s",
			strings.Join(args, " "), code, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
		return invocation{code: code, stdout: stdout.String(), stderr: stderr.String()}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("hand %v never returned with nothing to read on stdin; stdout=%q stderr=%q",
			args, stdout.String(), stderr.String())
		return invocation{}
	}
}

// An open pipe nothing ever writes to, which is the stdin a read blocks forever on. /dev/null returns EOF
// immediately, so it catches a read that fails but not one that waits.
func openPipeStdin(t *testing.T) *os.File {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	return reader
}

func devNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// Bootstrap runs in scripts and in CI, and the session hook runs on every session start, so neither may
// ever depend on something being on stdin.
func TestInitAndSessionStartNeverBlockOnStdin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stdin func(*testing.T) *os.File
	}{
		{"dev-null", devNull},
		{"unwritten-pipe", openPipeStdin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateGitConfig(t)
			home := t.TempDir()

			initialized := runHandStdin(t, home, tc.stdin(t), "init")
			if initialized.code != 0 {
				t.Fatalf("hand init: exit %d, stderr %q", initialized.code, initialized.stderr)
			}
			if !strings.Contains(initialized.stdout, "config_missing: 1") {
				t.Fatalf("init stdout = %q, want the unanswered harness reported", initialized.stdout)
			}

			session := runHandStdin(t, home, tc.stdin(t), "config")
			if session.code != 0 {
				t.Fatalf("hand config: exit %d, stderr %q", session.code, session.stderr)
			}
			if bare := runHandStdin(t, home, tc.stdin(t)); bare.code != 0 {
				t.Fatalf("bare hand: exit %d, stderr %q", bare.code, bare.stderr)
			}
		})
	}
}

// The whole first run through the built binary: bootstrap chooses nothing, the session document asks, the
// answer is persisted, and what the chosen harness cannot carry is never asked about.
func TestFirstRunConfigurationHappensAfterBootstrap(t *testing.T) {
	home := newHome(t)

	before := runHand(t, home)
	for _, want := range []string{"config_missing: 1", "harness,missing,none", "Ask the operator which harness"} {
		if !strings.Contains(before.stdout, want) {
			t.Fatalf("session document = %q, want it to contain %q", before.stdout, want)
		}
	}

	handConfigSet(t, home, "harness", "codex")
	after := runHand(t, home)
	for _, want := range []string{"config_missing: 0", "harness,configured,codex", "model,unsupported,none", "effort,unsupported,none"} {
		if !strings.Contains(after.stdout, want) {
			t.Fatalf("session document = %q, want it to contain %q", after.stdout, want)
		}
	}
	if strings.Contains(after.stdout, "Ask the operator") {
		t.Fatalf("session document = %q, want nothing left to ask about under codex", after.stdout)
	}

	refused := runHand(t, home, "config", "set", "model", "gpt-5")
	if refused.code != 2 {
		t.Fatalf("config set model under codex: exit %d, want 2", refused.code)
	}
	if _, err := os.Stat(filepath.Join(home, "config", "model.codex")); err == nil {
		t.Fatal("config/model.codex was written for a harness that takes no model flag")
	}
}

// The retired flag has to refuse rather than be ignored, so a script still passing it is told the
// configuration moved instead of appearing to have set something.
func TestInitRefusesTheRetiredSetupFlag(t *testing.T) {
	isolateGitConfig(t)
	got := runHand(t, t.TempDir(), "init", "--setup")
	if got.code != 2 {
		t.Fatalf("hand init --setup: exit %d, want 2", got.code)
	}
	if !strings.Contains(got.stderr, "unknown flag") {
		t.Fatalf("stderr = %q, want it to name the flag as unknown", got.stderr)
	}
}
