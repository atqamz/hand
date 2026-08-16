//go:build e2e && windows

package e2e

import (
	"testing"
	"time"
)

func (b *backgroundHand) interrupt(t *testing.T, timeout time.Duration) invocation {
	t.Helper()
	if err := b.cmd.Process.Kill(); err != nil {
		t.Fatalf("stop hand watch: %v", err)
	}
	return b.waitForExit(t, timeout, "portable Windows cleanup")
}
