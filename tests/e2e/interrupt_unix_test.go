//go:build e2e && !windows

package e2e

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

func (b *backgroundHand) interrupt(t *testing.T, timeout time.Duration) invocation {
	t.Helper()
	if err := b.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal hand watch: %v", err)
	}
	got := b.waitForExit(t, timeout, "SIGTERM interruption")
	if got.code != 8 {
		t.Fatalf("interrupted watch exit = %d, want 8 watch-interrupted (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "watch-interrupted") {
		t.Fatalf("interrupted watch stderr = %q, want the watch-interrupted kind", got.stderr)
	}
	return got
}
