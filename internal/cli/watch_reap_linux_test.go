//go:build linux

package cli_test

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestWatchNeverKillsAProcessRunningInAWorktree(t *testing.T) {
	fx := newAttemptFixture(t)
	wt := endAttempts(fx, 1)
	cmd := exec.Command("sleep", "300")
	cmd.Dir = wt(1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	stop := startWatch(t, fx, "--every", "20ms")
	eventually(t, func() bool { return strings.Contains(retentionEvents(t, fx), "a1: a process is running in it") })
	stop()
	mustExist(t, wt(1), true)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("the reaper killed the process: %v", err)
	}
}
